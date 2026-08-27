package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

type mockService struct {
	logger      *slog.Logger
	publicBase  string
	fixtureRoot string
	clientID    string
	clientSecret string
	chunkDelay  time.Duration
	chunkSize   int
	tokenCalls  atomic.Int64
	resolveCalls atomic.Int64
	objectCalls atomic.Int64
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("mock upstream stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	listenAddress := envOrDefault("MOCK_LISTEN_ADDR", ":8081")
	chunkDelay, err := time.ParseDuration(envOrDefault("MOCK_CHUNK_DELAY", "0s"))
	if err != nil {
		return fmt.Errorf("MOCK_CHUNK_DELAY: %w", err)
	}
	chunkSize, err := strconv.Atoi(envOrDefault("MOCK_CHUNK_SIZE", "65536"))
	if err != nil || chunkSize <= 0 {
		return errors.New("MOCK_CHUNK_SIZE must be a positive integer")
	}
	service := &mockService{
		logger:       logger,
		publicBase:   strings.TrimRight(envOrDefault("MOCK_PUBLIC_BASE_URL", "http://localhost:8081"), "/"),
		fixtureRoot:  envOrDefault("MOCK_FIXTURE_ROOT", "testdata/packages"),
		clientID:     envOrDefault("MOCK_CLIENT_ID", "mock-client"),
		clientSecret: envOrDefault("MOCK_CLIENT_SECRET", "mock-secret"),
		chunkDelay:   chunkDelay,
		chunkSize:    chunkSize,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/oauth/token", service.handleToken)
	mux.HandleFunc("GET /api/v1/jcds/files/{filename}", service.handleResolve)
	mux.HandleFunc("GET /objects/{filename}", service.handleObject)
	mux.HandleFunc("HEAD /objects/{filename}", service.handleObject)
	mux.HandleFunc("GET /metrics", service.handleMetrics)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
	}
	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	serverError := make(chan error, 1)
	go func() {
		logger.Info("mock upstream listening", "address", listenAddress)
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-shutdownSignal.Done():
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownContext)
}

func (s *mockService) handleToken(w http.ResponseWriter, r *http.Request) {
	s.tokenCalls.Add(1)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !constantTimeEqual(r.Form.Get("client_id"), s.clientID) ||
		!constantTimeEqual(r.Form.Get("client_secret"), s.clientSecret) ||
		r.Form.Get("grant_type") != "client_credentials" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "mock-access-token",
		"expires_in":   300,
		"token_type":   "Bearer",
	})
}

func (s *mockService) handleResolve(w http.ResponseWriter, r *http.Request) {
	s.resolveCalls.Add(1)
	if !constantTimeEqual(r.Header.Get("Authorization"), "Bearer mock-access-token") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	filename := r.PathValue("filename")
	if err := store.ValidateFilename(filename); err != nil {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(filepath.Join(s.fixtureRoot, filename)); errors.Is(err, os.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "fixture error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"url": s.publicBase + "/objects/" + url.PathEscape(filename),
	})
}

func (s *mockService) handleObject(w http.ResponseWriter, r *http.Request) {
	s.objectCalls.Add(1)
	filename := r.PathValue("filename")
	if err := store.ValidateFilename(filename); err != nil {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	file, err := os.Open(filepath.Join(s.fixtureRoot, filename))
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "fixture error", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "fixture error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	buffer := make([]byte, s.chunkSize)
	for {
		readBytes, readErr := file.Read(buffer)
		if readBytes > 0 {
			if _, err := w.Write(buffer[:readBytes]); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			if s.chunkDelay > 0 {
				time.Sleep(s.chunkDelay)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return
		}
		if readErr != nil {
			s.logger.Error("read fixture", "filename", filename, "error", readErr)
			return
		}
	}
}

func (s *mockService) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int64{
		"token_requests":   s.tokenCalls.Load(),
		"resolve_requests": s.resolveCalls.Load(),
		"object_requests":  s.objectCalls.Load(),
	})
}

func constantTimeEqual(actual, expected string) bool {
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func envOrDefault(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

