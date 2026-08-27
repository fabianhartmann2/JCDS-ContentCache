// Package httpapi exposes the internal HTTP interface used by NGINX.
package httpapi

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/config"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/pathpolicy"
)

// New returns the helper's internal HTTP handler. Package retrieval currently
// stops after validation; the Jamf adapter and streaming store-fill are the next
// vertical-slice implementation step.
func New(cfg config.Config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", readinessHandler(cfg.StoreRoot))
	mux.Handle("/packages/", packageHandler(logger))
	// Validate package paths before ServeMux can canonicalize dot segments or
	// encoded separators into a redirect.
	return securityHeaders(packagePathGuard(mux))
}

func readinessHandler(storeRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		info, err := os.Stat(storeRoot)
		if err != nil || !info.IsDir() {
			safeError(w, http.StatusServiceUnavailable, "store unavailable")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	}
}

func packageHandler(logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			safeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		if containsEncodedSeparator(r.URL.RawPath) {
			safeError(w, http.StatusBadRequest, "invalid package path")
			return
		}

		name, err := pathpolicy.PackageName(r.URL.Path)
		if err != nil {
			safeError(w, http.StatusBadRequest, "invalid package path")
			return
		}

		logger.Info("validated package miss", "package", name)
		safeError(w, http.StatusServiceUnavailable, "upstream adapter not configured")
	})
}

func containsEncodedSeparator(rawPath string) bool {
	rawPath = strings.ToLower(rawPath)
	return strings.Contains(rawPath, "%2f") || strings.Contains(rawPath, "%5c")
}

func packagePathGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/packages" || strings.HasPrefix(r.URL.Path, "/packages/") {
			if containsEncodedSeparator(r.URL.RawPath) {
				safeError(w, http.StatusBadRequest, "invalid package path")
				return
			}
			if _, err := pathpolicy.PackageName(r.URL.Path); err != nil {
				safeError(w, http.StatusBadRequest, "invalid package path")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func safeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.Error(w, message, status)
}
