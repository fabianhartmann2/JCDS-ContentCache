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

const testSHA3 = "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

func TestCatalogLookupReturnsExactMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]FileMetadata{
			{FileName: "Other.pkg", Length: 12, SHA3: testSHA3},
			{FileName: "ExampleFile.pkg", Length: 42, MD5: strings.Repeat("a", 32), Region: "test-region-1", SHA3: testSHA3},
		})
	}))
	defer server.Close()

	catalog := NewCatalogClient(server.Client(), &fakeTokens{token: "token"}, server.URL)
	got, err := catalog.Lookup(context.Background(), "ExampleFile.pkg")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got.FileName != "ExampleFile.pkg" || got.Length != 42 || got.SHA3 != testSHA3 {
		t.Fatalf("Lookup() = %+v", got)
	}
}

func TestCatalogLookupMapsMissingEntryToNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]FileMetadata{})
	}))
	defer server.Close()

	catalog := NewCatalogClient(server.Client(), &fakeTokens{token: "token"}, server.URL)
	_, err := catalog.Lookup(context.Background(), "Missing.pkg")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup() error = %v, want ErrNotFound", err)
	}
}

func TestCatalogLookupRetriesOneUnauthorizedResponse(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]FileMetadata{{FileName: "ExampleFile.pkg", Length: 42, SHA3: testSHA3}})
	}))
	defer server.Close()

	tokens := &fakeTokens{token: "token"}
	catalog := NewCatalogClient(server.Client(), tokens, server.URL)
	if _, err := catalog.Lookup(context.Background(), "ExampleFile.pkg"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("catalog requests = %d, want 2", got)
	}
	if got := tokens.invalidations.Load(); got != 1 {
		t.Fatalf("token invalidations = %d, want 1", got)
	}
}

func TestCatalogRejectsInvalidSHA3(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]FileMetadata{{FileName: "ExampleFile.pkg", Length: 42, SHA3: "not-a-sha3"}})
	}))
	defer server.Close()

	catalog := NewCatalogClient(server.Client(), &fakeTokens{token: "token"}, server.URL)
	if _, err := catalog.Lookup(context.Background(), "ExampleFile.pkg"); err == nil {
		t.Fatal("Lookup() accepted invalid SHA3 metadata")
	}
}

func TestCatalogRejectsIncompletePaginatedEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalCount": 2,
			"results": []FileMetadata{{FileName: "ExampleFile.pkg", Length: 42, SHA3: testSHA3}},
		})
	}))
	defer server.Close()

	catalog := NewCatalogClient(server.Client(), &fakeTokens{token: "token"}, server.URL)
	_, err := catalog.Lookup(context.Background(), "ExampleFile.pkg")
	if err == nil || !strings.Contains(err.Error(), "pagination parameters") {
		t.Fatalf("Lookup() error = %v, want pagination error", err)
	}
}
