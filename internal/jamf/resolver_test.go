package jamf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type fakeTokens struct {
	token         string
	invalidations atomic.Int64
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
