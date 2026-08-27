package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

