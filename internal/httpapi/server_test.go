package httpapi

import (
	"bytes"
	"context"
	"crypto/sha3"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/auth"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/download"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/jamf"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

type staticResolver struct {
	url   string
	err   error
	calls atomic.Int64
}

func (r *staticResolver) Resolve(context.Context, string) (string, error) {
	r.calls.Add(1)
	if r.err != nil {
		return "", r.err
	}
	return r.url, nil
}

type staticMetadata struct {
	metadata jamf.FileMetadata
	err      error
	calls    atomic.Int64
}

func (m *staticMetadata) Lookup(context.Context, string) (jamf.FileMetadata, error) {
	m.calls.Add(1)
	if m.err != nil {
		return jamf.FileMetadata{}, m.err
	}
	return m.metadata, nil
}

func newTestServer(t *testing.T, objectURL string, expectedContent []byte) (*Server, string, *staticResolver, *staticMetadata) {
	t.Helper()
	root := t.TempDir()
	packageStore, err := store.New(filepath.Join(root, "packages"), filepath.Join(root, ".temporary"))
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	resolver := &staticResolver{url: objectURL}
	digest := sha3.Sum512(expectedContent)
	metadata := &staticMetadata{metadata: jamf.FileMetadata{
		FileName: "ExampleFile.pkg",
		Length:   int64(len(expectedContent)),
		SHA3:     hex.EncodeToString(digest[:]),
	}}
	policy := download.NewPolicy([]string{"127.0.0.1"}, true)
	downloader := download.NewClient(http.DefaultClient, policy, 1024*1024)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := New(logger, packageStore, metadata, resolver, downloader, 10*time.Second, 1024*1024, 0, 0)
	return server, filepath.Join(root, "packages", "ExampleFile.pkg"), resolver, metadata
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

	expectedContent := append(append([]byte{}, firstChunk...), secondChunk...)
	api, finalPath, resolver, metadata := newTestServer(t, objectServer.URL, expectedContent)
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
	waitForFile(t, finalPath)

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
	if got := metadata.calls.Load(); got != 1 {
		t.Fatalf("metadata calls = %d, want 1", got)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect final package: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("final package was not published within %s", time.Second)
		}
		time.Sleep(10 * time.Millisecond)
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

	api, _, resolver, metadata := newTestServer(t, objectServer.URL, content)
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
	if got := metadata.calls.Load(); got != 1 {
		t.Fatalf("metadata calls = %d, want 1", got)
	}
}

func TestInvalidPackagePathDoesNotCallResolver(t *testing.T) {
	api, _, resolver, metadata := newTestServer(t, "http://127.0.0.1/unused", nil)
	request := httptest.NewRequest(http.MethodGet, "/packages/..%2Fsecret.pkg", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if got := resolver.calls.Load(); got != 0 {
		t.Fatalf("resolver calls = %d, want 0", got)
	}
	if got := metadata.calls.Load(); got != 0 {
		t.Fatalf("metadata calls = %d, want 0", got)
	}
}

func TestChecksumMismatchIsNotPublished(t *testing.T) {
	content := []byte("package bytes with the wrong catalog digest")
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "43")
		_, _ = w.Write(content)
	}))
	defer objectServer.Close()

	api, finalPath, _, metadata := newTestServer(t, objectServer.URL, content)
	metadata.metadata.SHA3 = hex.EncodeToString(make([]byte, 64))
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/packages/ExampleFile.pkg")
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()

	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("checksum-mismatched package was published, stat error = %v", err)
	}
}

func TestClientDisconnectDoesNotCancelFill(t *testing.T) {
	firstChunk := []byte("first streamed chunk\n")
	secondChunk := []byte("remaining package bytes\n")
	expectedContent := append(append([]byte{}, firstChunk...), secondChunk...)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() {
		releaseOnce.Do(func() { close(release) })
	}
	var objectCalls atomic.Int64

	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		objectCalls.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(expectedContent)))
		_, _ = w.Write(firstChunk)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
		_, _ = w.Write(secondChunk)
	}))
	defer objectServer.Close()

	api, finalPath, resolver, metadata := newTestServer(t, objectServer.URL, expectedContent)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	defer unblock()

	requestContext, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, server.URL+"/packages/ExampleFile.pkg", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("GET miss error = %v", err)
	}
	firstResponseChunk := make([]byte, len(firstChunk))
	if _, err := io.ReadFull(response.Body, firstResponseChunk); err != nil {
		t.Fatalf("read first streamed chunk: %v", err)
	}
	if string(firstResponseChunk) != string(firstChunk) {
		t.Fatalf("first streamed chunk = %q, want %q", firstResponseChunk, firstChunk)
	}

	cancel()
	response.Body.Close()
	unblock()
	waitForFile(t, finalPath)

	localResponse, err := server.Client().Get(server.URL + "/packages/ExampleFile.pkg")
	if err != nil {
		t.Fatalf("GET completed package error = %v", err)
	}
	localBody, err := io.ReadAll(localResponse.Body)
	localResponse.Body.Close()
	if err != nil {
		t.Fatalf("read completed package: %v", err)
	}
	if string(localBody) != string(expectedContent) {
		t.Fatalf("completed package = %q, want %q", localBody, expectedContent)
	}
	if got := localResponse.Header.Get("X-Package-Source"); got != "LOCAL" {
		t.Fatalf("completed package source = %q, want LOCAL", got)
	}
	if got := objectCalls.Load(); got != 1 {
		t.Fatalf("object calls = %d, want 1", got)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want 1", got)
	}
	if got := metadata.calls.Load(); got != 1 {
		t.Fatalf("metadata calls = %d, want 1", got)
	}
}

func TestTruncatedUpstreamObjectIsNotPublished(t *testing.T) {
	expectedContent := []byte("complete package content")
	partialContent := expectedContent[:8]
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(expectedContent)))
		_, _ = w.Write(partialContent)
	}))
	defer objectServer.Close()

	api, finalPath, resolver, metadata := newTestServer(t, objectServer.URL, expectedContent)
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/packages/ExampleFile.pkg")
	if err != nil {
		t.Fatalf("GET miss error = %v", err)
	}
	_, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("response read error = %v, want unexpected EOF", readErr)
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("truncated package was published, stat error = %v", err)
	}
	temporaryRoot := filepath.Join(filepath.Dir(filepath.Dir(finalPath)), ".temporary")
	temporaryEntries, err := os.ReadDir(temporaryRoot)
	if err != nil {
		t.Fatalf("read temporary directory: %v", err)
	}
	if len(temporaryEntries) != 0 {
		t.Fatalf("temporary directory contains %d entries after truncated transfer", len(temporaryEntries))
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want 1", got)
	}
	if got := metadata.calls.Load(); got != 1 {
		t.Fatalf("metadata calls = %d, want 1", got)
	}
}

func TestRangeOnMissFetchesFullObjectAndLocalHitServesRange(t *testing.T) {
	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	receivedRange := make(chan string, 1)
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRange <- r.Header.Get("Range")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = w.Write(content)
	}))
	defer objectServer.Close()

	api, finalPath, resolver, metadata := newTestServer(t, objectServer.URL, content)
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	missRequest, err := http.NewRequest(http.MethodGet, server.URL+"/packages/ExampleFile.pkg", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	missRequest.Header.Set("Range", "bytes=5-9")
	missResponse, err := server.Client().Do(missRequest)
	if err != nil {
		t.Fatalf("range GET miss error = %v", err)
	}
	missBody, err := io.ReadAll(missResponse.Body)
	missResponse.Body.Close()
	if err != nil {
		t.Fatalf("read range miss response: %v", err)
	}
	if missResponse.StatusCode != http.StatusOK {
		t.Fatalf("range miss status = %d, want 200", missResponse.StatusCode)
	}
	if string(missBody) != string(content) {
		t.Fatalf("range miss body = %q, want complete object", missBody)
	}
	if got := <-receivedRange; got != "" {
		t.Fatalf("upstream Range header = %q, want empty", got)
	}
	waitForFile(t, finalPath)

	localRequest, err := http.NewRequest(http.MethodGet, server.URL+"/packages/ExampleFile.pkg", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	localRequest.Header.Set("Range", "bytes=5-9")
	localResponse, err := server.Client().Do(localRequest)
	if err != nil {
		t.Fatalf("range GET local error = %v", err)
	}
	localBody, err := io.ReadAll(localResponse.Body)
	localResponse.Body.Close()
	if err != nil {
		t.Fatalf("read local range response: %v", err)
	}
	if localResponse.StatusCode != http.StatusPartialContent {
		t.Fatalf("local range status = %d, want 206", localResponse.StatusCode)
	}
	if string(localBody) != string(content[5:10]) {
		t.Fatalf("local range body = %q, want %q", localBody, content[5:10])
	}
	wantContentRange := "bytes 5-9/" + strconv.Itoa(len(content))
	if got := localResponse.Header.Get("Content-Range"); got != wantContentRange {
		t.Fatalf("Content-Range = %q, want %q", got, wantContentRange)
	}
	if got := localResponse.Header.Get("X-Package-Source"); got != "LOCAL" {
		t.Fatalf("local range source = %q, want LOCAL", got)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want 1", got)
	}
	if got := metadata.calls.Load(); got != 1 {
		t.Fatalf("metadata calls = %d, want 1", got)
	}
}

func TestHeadMissDoesNotStartUpstreamFill(t *testing.T) {
	api, _, resolver, metadata := newTestServer(t, "http://127.0.0.1/unused", nil)
	request := httptest.NewRequest(http.MethodHead, "/packages/ExampleFile.pkg", nil)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("HEAD miss status = %d, want 503", response.Code)
	}
	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if got := resolver.calls.Load(); got != 0 {
		t.Fatalf("resolver calls = %d, want 0", got)
	}
	if got := metadata.calls.Load(); got != 0 {
		t.Fatalf("metadata calls = %d, want 0", got)
	}
}

func TestJamfCapitalizedPackagePathUsesSameStoredObject(t *testing.T) {
	api, finalPath, resolver, metadata := newTestServer(t, "http://127.0.0.1/unused", nil)
	if err := os.WriteFile(finalPath, []byte("cached package"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/Packages/ExampleFile.pkg", nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("capitalized package status = %d, want 200", response.Code)
	}
	if response.Body.String() != "cached package" {
		t.Fatalf("capitalized package body = %q", response.Body.String())
	}
	if got := response.Header().Get("X-Package-Source"); got != "LOCAL" {
		t.Fatalf("capitalized package source = %q, want LOCAL", got)
	}
	if resolver.calls.Load() != 0 || metadata.calls.Load() != 0 {
		t.Fatal("capitalized local hit unexpectedly contacted upstream")
	}
}

func TestInvalidPackagePathsAreRejectedBeforeUpstreamCalls(t *testing.T) {
	tests := []string{
		"/packages/../Secret.pkg",
		"/packages/%2e%2e%2fSecret.pkg",
		"/packages/Folder%2FExample.pkg",
		"/packages/Folder%5cExample.pkg",
		"/packages/ExampleFile.PKG",
		"/packages/.Hidden.pkg",
		"/packages/ExampleFile.pkg?url=https://invalid.example/object",
		"/packages/https:%2F%2Finvalid.example%2FExample.pkg",
	}

	for _, requestTarget := range tests {
		t.Run(requestTarget, func(t *testing.T) {
			api, _, resolver, metadata := newTestServer(t, "http://127.0.0.1/unused", nil)
			request := httptest.NewRequest(http.MethodGet, requestTarget, nil)
			response := httptest.NewRecorder()

			api.handlePackage(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			if got := resolver.calls.Load(); got != 0 {
				t.Fatalf("resolver calls = %d, want 0", got)
			}
			if got := metadata.calls.Load(); got != 0 {
				t.Fatalf("metadata calls = %d, want 0", got)
			}
		})
	}
}

func TestDependencyErrorsHaveControlledStatusesAndDiagnosticCategories(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantStatus     int
		wantBody       string
		wantRetryAfter string
		wantCategory   string
	}{
		{
			name:         "package absent",
			err:          jamf.ErrNotFound,
			wantStatus:   http.StatusNotFound,
			wantBody:     "package not found\n",
			wantCategory: "resolver_not_found",
		},
		{
			name:         "OAuth credentials rejected",
			err:          auth.ErrRejected,
			wantStatus:   http.StatusBadGateway,
			wantBody:     "package source is unavailable\n",
			wantCategory: "jamf_auth_failed",
		},
		{
			name:           "OAuth throttled",
			err:            auth.ErrThrottled,
			wantStatus:     http.StatusServiceUnavailable,
			wantBody:       "package source is temporarily unavailable\n",
			wantRetryAfter: "30",
			wantCategory:   "jamf_throttled",
		},
		{
			name:         "dependency timeout",
			err:          jamf.ErrTimeout,
			wantStatus:   http.StatusGatewayTimeout,
			wantBody:     "package retrieval timed out\n",
			wantCategory: "upstream_timeout",
		},
		{
			name:         "invalid Jamf response",
			err:          jamf.ErrInvalidResponse,
			wantStatus:   http.StatusBadGateway,
			wantBody:     "package source is unavailable\n",
			wantCategory: "jamf_response_invalid",
		},
		{
			name:         "upstream unavailable",
			err:          jamf.ErrUnavailable,
			wantStatus:   http.StatusBadGateway,
			wantBody:     "package source is unavailable\n",
			wantCategory: "upstream_unavailable",
		},
		{
			name:         "download URL rejected",
			err:          download.ErrPolicyViolation,
			wantStatus:   http.StatusBadGateway,
			wantBody:     "package retrieval failed\n",
			wantCategory: "download_url_rejected",
		},
		{
			name:         "package store capacity exhausted",
			err:          store.ErrInsufficientSpace,
			wantStatus:   http.StatusInsufficientStorage,
			wantBody:     "package store has insufficient free space\n",
			wantCategory: "store_capacity_exhausted",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api, finalPath, _, metadata := newTestServer(t, "http://127.0.0.1/unused", nil)
			metadata.err = test.err
			var logs bytes.Buffer
			api.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			request := httptest.NewRequest(http.MethodGet, "/packages/ExampleFile.pkg", nil)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if response.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.wantBody)
			}
			if got := response.Header().Get("Retry-After"); got != test.wantRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, test.wantRetryAfter)
			}
			if !strings.Contains(logs.String(), `"category":"`+test.wantCategory+`"`) {
				t.Fatalf("log = %q, want category %q", logs.String(), test.wantCategory)
			}
			if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
				t.Fatalf("dependency failure published a final package, stat error = %v", err)
			}
		})
	}
}
