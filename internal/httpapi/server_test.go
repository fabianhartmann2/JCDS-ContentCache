package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/config"
)

func TestHealthEndpoints(t *testing.T) {
	storeRoot := t.TempDir()
	handler := New(config.Config{StoreRoot: storeRoot}, discardLogger())

	for _, path := range []string{"/livez", "/readyz"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
		}
	}
}

func TestPackageValidation(t *testing.T) {
	handler := New(config.Config{StoreRoot: t.TempDir()}, discardLogger())

	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{name: "valid miss", method: http.MethodGet, target: "/packages/ExampleFile.pkg", wantStatus: http.StatusServiceUnavailable},
		{name: "traversal", method: http.MethodGet, target: "/packages/../secret.pkg", wantStatus: http.StatusBadRequest},
		{name: "encoded separator", method: http.MethodGet, target: "/packages/folder%2Fsecret.pkg", wantStatus: http.StatusBadRequest},
		{name: "wrong extension", method: http.MethodGet, target: "/packages/example.dmg", wantStatus: http.StatusBadRequest},
		{name: "wrong method", method: http.MethodPost, target: "/packages/ExampleFile.pkg", wantStatus: http.StatusMethodNotAllowed},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
