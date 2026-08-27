package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/download"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

type staticResolver struct {
	url   string
	calls atomic.Int64
}

func (r *staticResolver) Resolve(context.Context, string) (string, error) {
	r.calls.Add(1)
	return r.url, nil
}

func newTestServer(t *testing.T, objectURL string) (*Server, string, *staticResolver) {
	t.Helper()
	root := t.TempDir()
	packageStore, err := store.New(filepath.Join(root, "packages"), filepath.Join(root, ".temporary"))
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	resolver := &staticResolver{url: objectURL}
	policy := download.NewPolicy([]string{"127.0.0.1"}, true)
	downloader := download.NewClient(http.DefaultClient, policy, 1024*1024)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := New(logger, packageStore, resolver, downloader, 10*time.Second, 1024*1024)
	return server, filepath.Join(root, "packages", "ExampleFile.pkg"), resolver
}

func TestMissStreamsThenPublishesAndBecomesLocalHit(t *testing.T) {
	firstChunk := []byte("first chunk\n")
	secondChunk := []byte("second chunk\n")
	release := make(chan struct{})
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "25")
		_, _ = w.Write(firstChunk)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
		_, _ = w.Write(secondChunk)
	}))
	defer objectServer.Close()

	api, finalPath, resolver := newTestServer(t, objectServer.URL)
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/packages/ExampleFile.pkg")
	if err != nil {
		t.Fatalf("GET miss error = %v", err)
	}
	buffer := make([]byte, len(firstChunk))
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatalf("read first streamed chunk: %v", err)
	}
	if string(buffer) != string(firstChunk) {
		t.Fatalf("first streamed chunk = %q, want %q", buffer, firstChunk)
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("final package existed before upstream completion, error = %v", err)
	}
	close(release)
	remaining, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read remaining response: %v", err)
	}
	if string(remaining) != string(secondChunk) {
		t.Fatalf("remaining response = %q, want %q", remaining, secondChunk)
	}
	if response.Header.Get("X-Package-Source") != "JCDS" {
		t.Fatalf("first X-Package-Source = %q", response.Header.Get("X-Package-Source"))
	}
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("final package was not published: %v", err)
	}

	secondResponse, err := server.Client().Get(server.URL + "/packages/ExampleFile.pkg")
	if err != nil {
		t.Fatalf("GET local hit error = %v", err)
	}
	_, _ = io.Copy(io.Discard, secondResponse.Body)
	secondResponse.Body.Close()
	if secondResponse.Header.Get("X-Package-Source") != "LOCAL" {
		t.Fatalf("second X-Package-Source = %q", secondResponse.Header.Get("X-Package-Source"))
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want 1", got)
	}
}

func TestConcurrentMissesCauseOneUpstreamTransfer(t *testing.T) {
	content := []byte("concurrent package content")
	var objectCalls atomic.Int64
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		objectCalls.Add(1)
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write(content)
	}))
	defer objectServer.Close()

	api, _, resolver := newTestServer(t, objectServer.URL)
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			response, err := server.Client().Get(server.URL + "/packages/ExampleFile.pkg")
			if err != nil {
				t.Errorf("GET error = %v", err)
				return
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Errorf("ReadAll() error = %v", err)
				return
			}
			if string(body) != string(content) {
				t.Errorf("response body = %q, want %q", body, content)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := objectCalls.Load(); got != 1 {
		t.Fatalf("upstream object calls = %d, want 1", got)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want 1", got)
	}
}

func TestInvalidPackagePathDoesNotCallResolver(t *testing.T) {
	api, _, resolver := newTestServer(t, "http://127.0.0.1/unused")
	request := httptest.NewRequest(http.MethodGet, "/packages/..%2Fsecret.pkg", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if got := resolver.calls.Load(); got != 0 {
		t.Fatalf("resolver calls = %d, want 0", got)
	}
}

