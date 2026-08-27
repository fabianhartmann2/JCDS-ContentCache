package jamf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/auth"
)

const maxResolverResponseBytes = 1024 * 1024

var (
	ErrNotFound     = errors.New("package not found")
	ErrUnauthorized = errors.New("Jamf API unauthorized")
	ErrThrottled    = errors.New("Jamf API throttled")
)

type Resolver interface {
	Resolve(context.Context, string) (string, error)
}

type Client struct {
	httpClient  *http.Client
	tokens      auth.TokenSource
	urlTemplate string
	urlField    string
}

func NewClient(httpClient *http.Client, tokens auth.TokenSource, urlTemplate, urlField string) *Client {
	return &Client{
		httpClient:  httpClient,
		tokens:      tokens,
		urlTemplate: urlTemplate,
		urlField:    urlField,
	}
}

func (c *Client) Resolve(ctx context.Context, filename string) (string, error) {
	downloadURL, err := c.resolveOnce(ctx, filename)
	if !errors.Is(err, ErrUnauthorized) {
		return downloadURL, err
	}

	c.tokens.Invalidate()
	downloadURL, err = c.resolveOnce(ctx, filename)
	if err != nil {
		return "", fmt.Errorf("retry Jamf package resolution: %w", err)
	}
	return downloadURL, nil
}

func (c *Client) resolveOnce(ctx context.Context, filename string) (string, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("obtain Jamf OAuth token: %w", err)
	}

	resolverURL := strings.Replace(c.urlTemplate, "{filename}", url.PathEscape(filename), 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolverURL, nil)
	if err != nil {
		return "", fmt.Errorf("create Jamf resolver request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request Jamf package resolution: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResolverResponseBytes))
		return "", ErrUnauthorized
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResolverResponseBytes))
		return "", ErrNotFound
	case http.StatusTooManyRequests:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResolverResponseBytes))
		return "", ErrThrottled
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResolverResponseBytes))
		return "", fmt.Errorf("Jamf resolver returned HTTP %d", resp.StatusCode)
	}

	var payload map[string]any
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxResolverResponseBytes))
	if err := decoder.Decode(&payload); err != nil {
		return "", fmt.Errorf("decode Jamf resolver response: %w", err)
	}

	downloadURL, err := stringField(payload, c.urlField)
	if err != nil {
		return "", err
	}
	return downloadURL, nil
}

func stringField(payload map[string]any, fieldPath string) (string, error) {
	var current any = payload
	for _, part := range strings.Split(fieldPath, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return "", fmt.Errorf("Jamf resolver field %q is not a string", fieldPath)
		}
		current, ok = object[part]
		if !ok {
			return "", fmt.Errorf("Jamf resolver response did not contain field %q", fieldPath)
		}
	}
	value, ok := current.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("Jamf resolver field %q is not a non-empty string", fieldPath)
	}
	return value, nil
}
