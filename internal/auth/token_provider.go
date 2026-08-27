package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxTokenResponseBytes = 1024 * 1024

type TokenSource interface {
	Token(context.Context) (string, error)
	Invalidate()
}

type Provider struct {
	client       *http.Client
	tokenURL     string
	clientID     string
	clientSecret string
	expirySkew   time.Duration
	now          func() time.Time

	mu         sync.Mutex
	token      string
	expiresAt  time.Time
	refreshing bool
	wait       chan struct{}
}

func NewProvider(client *http.Client, tokenURL, clientID, clientSecret string, expirySkew time.Duration) *Provider {
	return &Provider{
		client:       client,
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		expirySkew:   expirySkew,
		now:          time.Now,
	}
}

func (p *Provider) Token(ctx context.Context) (string, error) {
	for {
		p.mu.Lock()
		if p.token != "" && p.now().Add(p.expirySkew).Before(p.expiresAt) {
			token := p.token
			p.mu.Unlock()
			return token, nil
		}
		if p.refreshing {
			wait := p.wait
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-wait:
				continue
			}
		}

		p.refreshing = true
		p.wait = make(chan struct{})
		p.mu.Unlock()

		token, expiresAt, err := p.fetch(ctx)

		p.mu.Lock()
		if err == nil {
			p.token = token
			p.expiresAt = expiresAt
		}
		p.refreshing = false
		close(p.wait)
		p.mu.Unlock()

		if err != nil {
			return "", err
		}
		return token, nil
	}
}

func (p *Provider) Invalidate() {
	p.mu.Lock()
	p.token = ""
	p.expiresAt = time.Time{}
	p.mu.Unlock()
}

func (p *Provider) fetch(ctx context.Context) (string, time.Time, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create OAuth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("request OAuth token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxTokenResponseBytes))
		return "", time.Time{}, fmt.Errorf("OAuth token endpoint returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
		TokenType   string      `json:"token_type"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxTokenResponseBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("decode OAuth response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("OAuth response did not contain access_token")
	}
	if payload.TokenType != "" && !strings.EqualFold(payload.TokenType, "Bearer") {
		return "", time.Time{}, fmt.Errorf("OAuth response returned unsupported token_type")
	}
	expiresIn, err := payload.ExpiresIn.Int64()
	if err != nil || expiresIn <= 0 {
		return "", time.Time{}, fmt.Errorf("OAuth response contained invalid expires_in")
	}

	return payload.AccessToken, p.now().Add(time.Duration(expiresIn) * time.Second), nil
}
