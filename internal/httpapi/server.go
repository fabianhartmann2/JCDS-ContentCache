package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/download"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/jamf"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

const packagePathPrefix = "/packages/"

type objectSource interface {
	Open(context.Context, string) (*http.Response, error)
}

type Server struct {
	logger          *slog.Logger
	store           *store.Store
	flights         *store.Flights
	resolver        jamf.Resolver
	downloader      objectSource
	fillTimeout     time.Duration
	maxPackageBytes int64
}

func New(
	logger *slog.Logger,
	packageStore *store.Store,
	resolver jamf.Resolver,
	downloader objectSource,
	fillTimeout time.Duration,
	maxPackageBytes int64,
) *Server {
	return &Server{
		logger:          logger,
		store:           packageStore,
		flights:         store.NewFlights(),
		resolver:        resolver,
		downloader:      downloader,
		fillTimeout:     fillTimeout,
		maxPackageBytes: maxPackageBytes,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", s.handleLiveness)
	mux.HandleFunc("/health/ready", s.handleReadiness)
	mux.HandleFunc(packagePathPrefix, s.handlePackage)
	return mux
}

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, "{\"status\":\"ok\"}\n")
	}
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	if err := s.store.Ready(); err != nil {
		http.Error(w, "package store is not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, "{\"status\":\"ready\"}\n")
	}
}

func (s *Server) handlePackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	filename, err := packageFilename(r)
	if err != nil {
		http.Error(w, "invalid package path", http.StatusBadRequest)
		return
	}

	if err := s.serveLocal(w, r, filename); err == nil {
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.logger.Error("local package lookup failed", "filename", filename, "error", err)
		http.Error(w, "local package store failure", http.StatusInternalServerError)
		return
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "package is not stored locally; perform a full GET", http.StatusServiceUnavailable)
		return
	}

	handle, leader := s.flights.Join(filename)
	if !leader {
		if err := handle.Wait(r.Context()); err != nil {
			s.writeError(w, filename, err)
			return
		}
		if err := s.serveLocal(w, r, filename); err != nil {
			s.writeError(w, filename, fmt.Errorf("open package after coordinated fill: %w", err))
		}
		return
	}

	fillContext, cancel := context.WithTimeout(context.Background(), s.fillTimeout)
	defer cancel()
	tracked := &trackingResponseWriter{ResponseWriter: w}
	err = s.fillAndStream(fillContext, tracked, filename)
	handle.Finish(err)
	if err != nil && !tracked.started {
		s.writeError(w, filename, err)
	} else if err != nil {
		s.logger.Error("package fill failed after streaming started", "filename", filename, "error", err)
	}
}

func (s *Server) fillAndStream(ctx context.Context, w *trackingResponseWriter, filename string) error {
	downloadURL, err := s.resolver.Resolve(ctx, filename)
	if err != nil {
		return err
	}
	upstream, err := s.downloader.Open(ctx, downloadURL)
	if err != nil {
		return err
	}
	defer upstream.Body.Close()

	pending, err := s.store.Begin(filename)
	if err != nil {
		return err
	}
	defer pending.Abort()

	copySafeHeaders(w.Header(), upstream.Header, filename)
	if upstream.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(upstream.ContentLength, 10))
	}
	w.Header().Set("X-Package-Source", "JCDS")
	w.WriteHeader(http.StatusOK)

	buffer := make([]byte, 64*1024)
	var downloaded int64
	clientConnected := true
	for {
		readBytes, readErr := upstream.Body.Read(buffer)
		if readBytes > 0 {
			downloaded += int64(readBytes)
			if downloaded > s.maxPackageBytes {
				return errors.New("upstream object exceeded the configured maximum size")
			}
			if _, err := pending.Write(buffer[:readBytes]); err != nil {
				return fmt.Errorf("write temporary package: %w", err)
			}
			if clientConnected {
				if _, err := w.Write(buffer[:readBytes]); err != nil {
					clientConnected = false
					s.logger.Info("client disconnected; package fill continues", "filename", filename)
				} else {
					w.Flush()
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read upstream object: %w", readErr)
		}
	}

	if err := pending.Commit(upstream.ContentLength); err != nil {
		return err
	}
	s.logger.Info("package published", "filename", filename, "bytes", downloaded)
	return nil
}

func (s *Server) serveLocal(w http.ResponseWriter, r *http.Request, filename string) error {
	file, info, err := s.store.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Package-Source", "LOCAL")
	http.ServeContent(w, r, filename, info.ModTime(), file)
	return nil
}

func (s *Server) writeError(w http.ResponseWriter, filename string, err error) {
	status := http.StatusBadGateway
	message := "package retrieval failed"

	switch {
	case errors.Is(err, jamf.ErrNotFound), errors.Is(err, download.ErrNotFound):
		status = http.StatusNotFound
		message = "package not found"
	case errors.Is(err, jamf.ErrThrottled):
		status = http.StatusServiceUnavailable
		message = "package source is temporarily unavailable"
		w.Header().Set("Retry-After", "30")
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		message = "package retrieval timed out"
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
		message = "package request was canceled"
	}

	s.logger.Error("package request failed", "filename", filename, "status", status, "error", err)
	http.Error(w, message, status)
}

func packageFilename(r *http.Request) (string, error) {
	if r.URL.RawQuery != "" {
		return "", errors.New("query parameters are not accepted")
	}
	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, packagePathPrefix) {
		return "", errors.New("path is outside the package namespace")
	}
	escapedFilename := strings.TrimPrefix(escapedPath, packagePathPrefix)
	if escapedFilename == "" || strings.Contains(escapedFilename, "/") {
		return "", errors.New("package path must contain exactly one filename segment")
	}
	lowerEscaped := strings.ToLower(escapedFilename)
	if strings.Contains(lowerEscaped, "%2f") || strings.Contains(lowerEscaped, "%5c") {
		return "", errors.New("encoded path separators are not accepted")
	}
	filename, err := url.PathUnescape(escapedFilename)
	if err != nil {
		return "", errors.New("package filename contains malformed escaping")
	}
	if err := store.ValidateFilename(filename); err != nil {
		return "", err
	}
	return filename, nil
}

func copySafeHeaders(destination, source http.Header, filename string) {
	contentType := source.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	destination.Set("Content-Type", contentType)
	destination.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	for _, header := range []string{"ETag", "Last-Modified"} {
		if value := source.Get(header); value != "" {
			destination.Set(header, value)
		}
	}
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

type trackingResponseWriter struct {
	http.ResponseWriter
	started bool
}

func (w *trackingResponseWriter) WriteHeader(statusCode int) {
	if !w.started {
		w.started = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *trackingResponseWriter) Write(data []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *trackingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

