package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxTokenResponseBytes = 1024 * 1024

var (
	ErrRejected        = errors.New("OAuth credentials rejected")
	ErrThrottled       = errors.New("OAuth token endpoint throttled")
	ErrTimeout         = errors.New("OAuth token endpoint timed out")
	ErrUnavailable     = errors.New("OAuth token endpoint unavailable")
	ErrInvalidResponse = errors.New("OAuth token response invalid")
)

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
	refreshAt  time.Time
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
		if p.token != "" && p.now().Before(p.refreshAt) {
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

		token, refreshAt, err := p.fetch(ctx)

		p.mu.Lock()
		if err == nil {
			p.token = token
			p.refreshAt = refreshAt
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
	p.refreshAt = time.Time{}
	p.mu.Unlock()
}

func (p *Provider) fetch(ctx context.Context) (string, time.Time, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: create OAuth token request", ErrUnavailable)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", time.Time{}, tokenRequestFailure(err)
	}
	defer resp.Body.Close()

	if statusErr := tokenStatusFailure(resp.StatusCode); statusErr != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxTokenResponseBytes))
		return "", time.Time{}, statusErr
	}

	var payload struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
		TokenType   string      `json:"token_type"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxTokenResponseBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("%w: decode OAuth response", ErrInvalidResponse)
	}
	if payload.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("%w: OAuth response did not contain access_token", ErrInvalidResponse)
	}
	if payload.TokenType != "" && !strings.EqualFold(payload.TokenType, "Bearer") {
		return "", time.Time{}, fmt.Errorf("%w: OAuth response returned unsupported token_type", ErrInvalidResponse)
	}
	expiresIn, err := payload.ExpiresIn.Int64()
	if err != nil || expiresIn <= 0 {
		return "", time.Time{}, fmt.Errorf("%w: OAuth response contained invalid expires_in", ErrInvalidResponse)
	}

	lifetime := time.Duration(expiresIn) * time.Second
	skew := p.expirySkew
	// Jamf may issue tokens whose entire lifetime is shorter than the configured
	// safety margin. Clamp the margin to 20% of the observed lifetime so a valid
	// short-lived token is still reused while retaining an early-refresh window.
	if maximumSkew := lifetime / 5; skew > maximumSkew {
		skew = maximumSkew
	}
	if skew < 0 {
		skew = 0
	}
	return payload.AccessToken, p.now().Add(lifetime - skew), nil
}

func tokenRequestFailure(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("OAuth token request: %w", context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return ErrTimeout
	}
	return ErrUnavailable
}

func tokenStatusFailure(status int) error {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: OAuth token endpoint returned HTTP %d", ErrRejected, status)
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return fmt.Errorf("%w: OAuth token endpoint returned HTTP %d", ErrTimeout, status)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: OAuth token endpoint returned HTTP %d", ErrThrottled, status)
	case status >= http.StatusInternalServerError:
		return fmt.Errorf("%w: OAuth token endpoint returned HTTP %d", ErrUnavailable, status)
	default:
		return fmt.Errorf("OAuth token endpoint returned unexpected HTTP %d", status)
	}
}
