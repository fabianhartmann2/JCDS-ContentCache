package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestProviderReusesTokenAcrossConcurrentCalls(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "client_credentials" {
			http.Error(w, "invalid grant", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token",
			"expires_in":   300,
			"token_type":   "Bearer",
		})
	}))
	defer server.Close()

	provider := NewProvider(server.Client(), server.URL, "client", "secret", time.Minute)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := provider.Token(context.Background())
			if err != nil {
				t.Errorf("Token() error = %v", err)
				return
			}
			if token != "test-token" {
				t.Errorf("Token() = %q, want test-token", token)
			}
		}()
	}
	wg.Wait()

	if got := requests.Load(); got != 1 {
		t.Fatalf("token endpoint requests = %d, want 1", got)
	}
}

func TestProviderRefreshesAfterInvalidation(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber := requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-" + string(rune('0'+requestNumber)),
			"expires_in":   300,
		})
	}))
	defer server.Close()

	provider := NewProvider(server.Client(), server.URL, "client", "secret", time.Minute)
	first, err := provider.Token(context.Background())
	if err != nil {
		t.Fatalf("first Token() error = %v", err)
	}
	provider.Invalidate()
	second, err := provider.Token(context.Background())
	if err != nil {
		t.Fatalf("second Token() error = %v", err)
	}
	if first == second {
		t.Fatalf("Token() did not refresh after Invalidate()")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("token endpoint requests = %d, want 2", got)
	}
}

func TestProviderAcceptsObservedShortLivedJamfResponseAndReusesToken(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"sanitized-test-token",
			"scope":"synthetic-scope",
			"token_type":"Bearer",
			"expires_in":59
		}`))
	}))
	defer server.Close()

	// The configured 60-second margin is longer than the observed 59-second
	// lifetime. The provider must adapt the margin instead of refreshing on
	// every API call.
	provider := NewProvider(server.Client(), server.URL, "client", "secret", 60*time.Second)
	for range 2 {
		token, err := provider.Token(context.Background())
		if err != nil {
			t.Fatalf("Token() error = %v", err)
		}
		if token != "sanitized-test-token" {
			t.Fatalf("Token() = %q, want sanitized-test-token", token)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("token endpoint requests = %d, want 1", got)
	}
}

func TestProviderMapsFailureStatusesWithoutReturningResponseBodies(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantError error
	}{
		{name: "credentials rejected", status: http.StatusUnauthorized, wantError: ErrRejected},
		{name: "forbidden", status: http.StatusForbidden, wantError: ErrRejected},
		{name: "throttled", status: http.StatusTooManyRequests, wantError: ErrThrottled},
		{name: "gateway timeout", status: http.StatusGatewayTimeout, wantError: ErrTimeout},
		{name: "server failure", status: http.StatusInternalServerError, wantError: ErrUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("private-oauth-error-marker"))
			}))
			defer server.Close()

			provider := NewProvider(server.Client(), server.URL, "client", "secret", time.Minute)
			_, err := provider.Token(context.Background())
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Token() error = %v, want %v", err, test.wantError)
			}
			if strings.Contains(err.Error(), "private-oauth-error-marker") {
				t.Fatalf("Token() error exposed the OAuth response body: %v", err)
			}
		})
	}
}

func TestProviderRedactsTokenURLFromTransportError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})}
	provider := NewProvider(
		client,
		"https://oauth.example.invalid/token?private-query-marker=redacted",
		"client",
		"secret",
		time.Minute,
	)

	_, err := provider.Token(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Token() error = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), "private-query-marker") {
		t.Fatalf("Token() error exposed the token URL: %v", err)
	}
}
