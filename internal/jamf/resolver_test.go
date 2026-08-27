package jamf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type fakeTokens struct {
	token         string
	invalidations atomic.Int64
}

func TestResolveMapsObservedNotFoundResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"httpStatus": http.StatusNotFound,
			"errors":     []any{},
		})
	}))
	defer server.Close()

	resolver := NewClient(server.Client(), &fakeTokens{token: "token"}, server.URL+"/{filename}", "uri")
	_, err := resolver.Resolve(context.Background(), "Missing.pkg")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrNotFound", err)
	}
}

func (f *fakeTokens) Token(context.Context) (string, error) {
	return f.token, nil
}

func (f *fakeTokens) Invalidate() {
	f.invalidations.Add(1)
}

func TestResolveExtractsNestedURLField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"downloadUrl": "https://download.example/object"},
		})
	}))
	defer server.Close()

	resolver := NewClient(server.Client(), &fakeTokens{token: "token"}, server.URL+"/{filename}", "data.downloadUrl")
	got, err := resolver.Resolve(context.Background(), "ExampleFile.pkg")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "https://download.example/object" {
		t.Fatalf("Resolve() = %q", got)
	}
}

func TestResolveRetriesOneUnauthorizedResponse(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"uri": "https://download.example/object"})
	}))
	defer server.Close()

	tokens := &fakeTokens{token: "token"}
	resolver := NewClient(server.Client(), tokens, server.URL+"/{filename}", "uri")
	if _, err := resolver.Resolve(context.Background(), "ExampleFile.pkg"); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("resolver requests = %d, want 2", got)
	}
	if got := tokens.invalidations.Load(); got != 1 {
		t.Fatalf("token invalidations = %d, want 1", got)
	}
}

func TestResolveMapsDependencyStatusesWithoutReturningResponseBodies(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		wantError    error
		wantRequests int64
	}{
		{name: "unauthorized after retry", status: http.StatusUnauthorized, wantError: ErrUnauthorized, wantRequests: 2},
		{name: "forbidden", status: http.StatusForbidden, wantError: ErrForbidden, wantRequests: 1},
		{name: "throttled", status: http.StatusTooManyRequests, wantError: ErrThrottled, wantRequests: 1},
		{name: "gateway timeout", status: http.StatusGatewayTimeout, wantError: ErrTimeout, wantRequests: 1},
		{name: "server failure", status: http.StatusInternalServerError, wantError: ErrUnavailable, wantRequests: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("private-resolver-error-marker"))
			}))
			defer server.Close()

			tokens := &fakeTokens{token: "token"}
			resolver := NewClient(server.Client(), tokens, server.URL+"/{filename}", "uri")
			_, err := resolver.Resolve(context.Background(), "ExampleFile.pkg")
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.wantError)
			}
			if strings.Contains(err.Error(), "private-resolver-error-marker") {
				t.Fatalf("Resolve() error exposed the resolver response body: %v", err)
			}
			if got := requests.Load(); got != test.wantRequests {
				t.Fatalf("resolver requests = %d, want %d", got, test.wantRequests)
			}
		})
	}
}

func TestResolveRedactsRequestURLFromTransportError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})}
	resolver := NewClient(
		client,
		&fakeTokens{token: "token"},
		"https://tenant.example.invalid/api/{filename}?private-query-marker=redacted",
		"uri",
	)

	_, err := resolver.Resolve(context.Background(), "ExampleFile.pkg")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), "private-query-marker") {
		t.Fatalf("Resolve() error exposed the resolver URL: %v", err)
	}
}
