package maintenance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMetricsDisabledByDefault(t *testing.T) {
	t.Setenv("METRICS_API_ENABLED", "")
	t.Setenv("METRICS_WEBHOOK_ENABLED", "")
	config, err := loadMetricsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.APIEnabled || config.WebhookEnabled {
		t.Fatal("metrics should be disabled by default")
	}
}

func TestInvalidMetricsConfigurationFailsOpen(t *testing.T) {
	t.Setenv("METRICS_API_ENABLED", "true")
	t.Setenv("METRICS_INSTANCE_NAME", "test cache")
	t.Setenv("METRICS_TLS_CERT_FILE", "relative.pem")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Metrics.APIEnabled || config.Metrics.WebhookEnabled || config.Metrics.ConfigError == nil {
		t.Fatalf("invalid optional metrics were not disabled safely: %+v", config.Metrics)
	}
}

func TestMetricsConfigurationRequiresExactAllowedHTTPSHost(t *testing.T) {
	setSharedMetricsEnvironment(t)
	t.Setenv("METRICS_API_ENABLED", "true")
	t.Setenv("METRICS_WEBHOOK_ENABLED", "true")
	t.Setenv("METRICS_WEBHOOK_URL", "https://receiver.example/hook")
	t.Setenv("METRICS_WEBHOOK_ALLOWED_HOSTS", "different.example")
	config, err := loadMetricsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.APIEnabled || config.WebhookEnabled || config.WebhookConfigError == nil {
		t.Fatal("unapproved webhook host should disable only webhook delivery")
	}
	t.Setenv("METRICS_WEBHOOK_ALLOWED_HOSTS", "receiver.example")
	t.Setenv("METRICS_WEBHOOK_AUTH_MODE", "hmac")
	t.Setenv("METRICS_WEBHOOK_HMAC_SECRET_FILE", "/run/secrets/webhook-hmac")
	config, err = loadMetricsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.WebhookEnabled || config.WebhookConfigError != nil || config.URL.Hostname() != "receiver.example" || config.MaxAttempts != 3 {
		t.Fatalf("unexpected metrics configuration: %+v", config)
	}
}

func TestMetricsAPIWorksWithoutWebhookConfiguration(t *testing.T) {
	setSharedMetricsEnvironment(t)
	t.Setenv("METRICS_API_ENABLED", "true")
	t.Setenv("METRICS_WEBHOOK_ENABLED", "false")
	t.Setenv("METRICS_WEBHOOK_URL", "")
	t.Setenv("METRICS_WEBHOOK_ALLOWED_HOSTS", "")
	config, err := loadMetricsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.APIEnabled || config.WebhookEnabled || config.WebhookConfigError != nil || config.URL != nil {
		t.Fatalf("unexpected API-only metrics configuration: %+v", config)
	}
}

func setSharedMetricsEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("METRICS_INSTANCE_NAME", "test cache")
	t.Setenv("METRICS_TLS_CERT_FILE", "/run/tls/fullchain.pem")
}

func TestHMACSecretRequiresRestrictedRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("0123456789abcdef0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readHMACSecret(path); err == nil {
		t.Fatal("group- or world-readable secret should be rejected")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHMACSecret(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := readHMACSecret(path); err != nil {
		t.Fatalf("matching process-group read access should be accepted: %v", err)
	}
}

func TestStableInstanceIDPersistsWithRestrictedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance-id")
	first, err := ensureInstanceID("", path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureInstanceID("", path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !validUUID(first) {
		t.Fatalf("instance identity is not stable: %q %q", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("instance identity mode = %04o; want 0600", info.Mode().Perm())
	}
}

func TestTrafficCollectorAggregatesAndResetsWindow(t *testing.T) {
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	collector := NewTrafficCollector(now)
	collector.Record(TelemetryEvent{Status: 200, Source: "JCDS", BytesSent: 100, RangeKind: "none", Completion: "complete"})
	collector.Record(TelemetryEvent{Status: 200, Source: "INFLIGHT", BytesSent: 100, RangeKind: "none", Completion: "complete"})
	collector.Record(TelemetryEvent{Status: 206, Source: "LOCAL", BytesSent: 20, RangeKind: "resume", Completion: "complete"})
	snapshot := collector.Take(now.Add(time.Minute))
	if snapshot.Requests != 3 || snapshot.JCDSFills != 1 || snapshot.InflightFollowers != 1 || snapshot.LocalHits != 1 || snapshot.RangeRequests != 1 || snapshot.BytesDownloaded != 100 {
		t.Fatalf("unexpected traffic snapshot: %+v", snapshot)
	}
	if next := collector.Take(now.Add(2 * time.Minute)); next.Requests != 0 || next.WindowSeconds != 60 {
		t.Fatalf("traffic window did not reset: %+v", next)
	}
}

func TestSnapshotStoreHandlerIsOptionalAndSideEffectFree(t *testing.T) {
	store := &SnapshotStore{}

	disabled := httptest.NewRecorder()
	store.Handler(false).ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/health/metrics", nil))
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled endpoint status = %d; want 404", disabled.Code)
	}

	unavailable := httptest.NewRecorder()
	store.Handler(true).ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/health/metrics", nil))
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), "metrics_unavailable") {
		t.Fatalf("uninitialized endpoint response = %d %q", unavailable.Code, unavailable.Body.String())
	}

	body := []byte(`{"schema_version":1,"event_id":"test-event"}`)
	store.Store(body, "test-event")
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		store.Handler(true).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/metrics", nil))
		if response.Code != http.StatusOK || response.Body.String() != string(body) {
			t.Fatalf("metrics response = %d %q", response.Code, response.Body.String())
		}
		if response.Header().Get("X-JCDS-Metrics-Event-ID") != "test-event" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("missing metrics response headers: %v", response.Header())
		}
	}

	head := httptest.NewRecorder()
	store.Handler(true).ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/health/metrics", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD response = %d body=%q", head.Code, head.Body.String())
	}

	method := httptest.NewRecorder()
	store.Handler(true).ServeHTTP(method, httptest.NewRequest(http.MethodPost, "/health/metrics", nil))
	if method.Code != http.StatusMethodNotAllowed || method.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("invalid method response = %d headers=%v", method.Code, method.Header())
	}
}

func TestInventorySummaryAndFullCap(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(root), ".temporary"), 0o770); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A.pkg", "B.pkg"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), ".temporary", "partial"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "A.pkg"), filepath.Join(root, "Unsafe.pkg")); err != nil {
		t.Fatal(err)
	}
	index, err := LoadIndex(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	reporter := &Reporter{Config: MetricsConfig{InventoryMode: "full", InventoryMaxItems: 1}, StoreRoot: root, Index: index}
	snapshot, err := reporter.inventory()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PackageCount != 2 || snapshot.TemporaryCount != 1 || snapshot.UnsafeEntries != 1 || !snapshot.InventoryTruncated || snapshot.InventoryReturned != 1 || snapshot.InventoryTotal != 2 {
		t.Fatalf("unexpected inventory snapshot: %+v", snapshot)
	}
}

func TestCertificateExpiryStates(t *testing.T) {
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		remaining time.Duration
		want      string
	}{
		{"ok", 60 * 24 * time.Hour, "ok"},
		{"warning", 20 * 24 * time.Hour, "warning"},
		{"critical", 7 * 24 * time.Hour, "critical"},
		{"expired", -time.Hour, "expired"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeCertificate(t, now.Add(test.remaining))
			got := inspectCertificate(path, now, 30*24*time.Hour, 14*24*time.Hour)
			if got.ExpiryStatus != test.want {
				t.Fatalf("expiry status = %q; want %q", got.ExpiryStatus, test.want)
			}
		})
	}
	if got := inspectCertificate(filepath.Join(t.TempDir(), "missing.pem"), now, time.Hour, 30*time.Minute); got.ExpiryStatus != "unknown" {
		t.Fatalf("unreadable certificate status = %q", got.ExpiryStatus)
	}
}

func TestReporterSignsSnapshotAndRetriesBoundedly(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	secretPath := filepath.Join(t.TempDir(), "hmac.secret")
	if err := os.WriteFile(secretPath, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	var signatureOK atomic.Bool
	var receivedBody atomic.Value
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := ioReadAll(request.Body)
		receivedBody.Store(append([]byte(nil), body...))
		timestamp := request.Header.Get("X-JCDS-Timestamp")
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(timestamp))
		mac.Write([]byte("\n"))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		signatureOK.Store(hmac.Equal([]byte(want), []byte(request.Header.Get("X-JCDS-Signature"))))
		if attempts.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer health.Close()
	root := filepath.Join(t.TempDir(), "packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	index, err := LoadIndex(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(receiver.URL)
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	reporter := &Reporter{
		Config: MetricsConfig{
			APIEnabled: true, WebhookEnabled: true, URL: parsed, Interval: time.Minute, Timeout: time.Second,
			AuthMode: "hmac", HMACSecretFile: secretPath,
			InstanceUUID:  "2b1ab8d0-a064-47ca-af4e-366c53c43f10",
			InventoryMode: "summary", InventoryMaxItems: 10,
			TLSCertFile:      writeCertificate(t, now.Add(60*24*time.Hour)),
			TLSWarningBefore: 30 * 24 * time.Hour, TLSCriticalBefore: 14 * 24 * time.Hour,
			HealthURL: health.URL, MaxAttempts: 3,
		},
		StoreRoot: root, Index: index, Traffic: NewTrafficCollector(now),
		Cleanup: NewCleanupTracker(90 * 24 * time.Hour), Started: now,
		Client: receiver.Client(), Snapshots: &SnapshotStore{}, now: func() time.Time { return now },
	}
	reporter.collect(context.Background())
	if attempts.Load() != 2 || !signatureOK.Load() || !reporter.previousOK || reporter.consecutiveFailures != 0 {
		t.Fatalf("unexpected reporter result: attempts=%d signature=%v previous=%v failures=%d", attempts.Load(), signatureOK.Load(), reporter.previousOK, reporter.consecutiveFailures)
	}
	storedBody, storedEventID, ok := reporter.Snapshots.Load()
	webhookBody, _ := receivedBody.Load().([]byte)
	if !ok || storedEventID == "" || !bytes.Equal(storedBody, webhookBody) {
		t.Fatal("webhook and metrics API did not receive the exact same snapshot bytes")
	}
}

func TestAPIOnlySnapshotDoesNotRequireOrAttemptWebhook(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer health.Close()
	root := filepath.Join(t.TempDir(), "packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	index, err := LoadIndex(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	traffic := NewTrafficCollector(now)
	traffic.Record(TelemetryEvent{Status: 200, Source: "LOCAL", BytesSent: 42, Completion: "complete"})
	reporter := &Reporter{
		Config: MetricsConfig{
			APIEnabled: true, WebhookEnabled: false, Interval: time.Minute, Timeout: time.Second,
			InstanceName: "API-only cache", InstanceUUID: "2b1ab8d0-a064-47ca-af4e-366c53c43f10",
			InventoryMode: "summary", InventoryMaxItems: 10,
			TLSCertFile:      writeCertificate(t, now.Add(60*24*time.Hour)),
			TLSWarningBefore: 30 * 24 * time.Hour, TLSCriticalBefore: 14 * 24 * time.Hour,
			HealthURL: health.URL,
		},
		StoreRoot: root, Index: index, Traffic: traffic,
		Cleanup: NewCleanupTracker(90 * 24 * time.Hour), Started: now,
		Snapshots: &SnapshotStore{}, now: func() time.Time { return now.Add(time.Minute) },
	}
	reporter.collect(context.Background())
	body, _, ok := reporter.Snapshots.Load()
	if !ok {
		t.Fatal("API-only reporter did not publish a snapshot")
	}
	var payload struct {
		Traffic struct {
			Requests int64 `json:"requests"`
		} `json:"traffic"`
		Reporter struct {
			WebhookEnabled bool `json:"webhook_enabled"`
		} `json:"reporter"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Traffic.Requests != 1 || payload.Reporter.WebhookEnabled {
		t.Fatalf("unexpected API-only payload: %+v", payload)
	}
	first := append([]byte(nil), body...)
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		reporter.Snapshots.Handler(true).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/metrics", nil))
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), first) {
			t.Fatal("API read changed or regenerated the stored snapshot")
		}
	}
}

func writeCertificate(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "jcds-cache.appfruit.ch"}, NotBefore: notAfter.Add(-365 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "certificate.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func ioReadAll(body io.Reader) ([]byte, error) {
	return io.ReadAll(body)
}

func TestSnapshotExcludesSensitiveFields(t *testing.T) {
	encoded, err := json.Marshal(TrafficSnapshot{Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"client", "uri", "request_id", "token", "signed_url"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("traffic snapshot exposed %q", forbidden)
		}
	}
}
