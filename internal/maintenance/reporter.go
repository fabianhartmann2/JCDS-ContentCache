package maintenance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

type MetricsConfig struct {
	Enabled           bool
	ConfigError       error
	URL               *url.URL
	Interval          time.Duration
	Timeout           time.Duration
	AllowedHosts      map[string]struct{}
	AllowPrivateIPs   bool
	AuthMode          string
	HMACSecretFile    string
	InstanceName      string
	InstanceUUID      string
	InstanceFQDN      string
	InstanceVersion   string
	InstanceCommit    string
	IdentityPath      string
	InventoryMode     string
	InventoryMaxItems int
	TLSCertFile       string
	TLSWarningBefore  time.Duration
	TLSCriticalBefore time.Duration
	HealthURL         string
	MaxAttempts       int
}

func loadMetricsConfig() (MetricsConfig, error) {
	c := MetricsConfig{Enabled: strings.EqualFold(value("METRICS_WEBHOOK_ENABLED", "false"), "true")}
	if !c.Enabled {
		return c, nil
	}
	var err error
	c.Interval, err = duration("METRICS_WEBHOOK_INTERVAL", time.Minute)
	if err != nil {
		return c, err
	}
	c.Timeout, err = duration("METRICS_WEBHOOK_TIMEOUT", 10*time.Second)
	if err != nil {
		return c, err
	}
	c.TLSWarningBefore, err = duration("METRICS_TLS_WARNING_BEFORE", 30*24*time.Hour)
	if err != nil {
		return c, err
	}
	c.TLSCriticalBefore, err = duration("METRICS_TLS_CRITICAL_BEFORE", 14*24*time.Hour)
	if err != nil {
		return c, err
	}
	c.URL, err = url.Parse(strings.TrimSpace(os.Getenv("METRICS_WEBHOOK_URL")))
	if err != nil || c.URL.Scheme != "https" || c.URL.Hostname() == "" || c.URL.User != nil || c.URL.Fragment != "" {
		return c, errors.New("METRICS_WEBHOOK_URL must be an exact HTTPS URL without credentials or fragment")
	}
	c.AllowedHosts = make(map[string]struct{})
	for _, host := range strings.Split(os.Getenv("METRICS_WEBHOOK_ALLOWED_HOSTS"), ",") {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if host != "" {
			c.AllowedHosts[host] = struct{}{}
		}
	}
	if _, ok := c.AllowedHosts[strings.ToLower(strings.TrimSuffix(c.URL.Hostname(), "."))]; !ok {
		return c, errors.New("webhook hostname is not in METRICS_WEBHOOK_ALLOWED_HOSTS")
	}
	c.AllowPrivateIPs = strings.EqualFold(value("METRICS_WEBHOOK_ALLOW_PRIVATE_IPS", "false"), "true")
	c.AuthMode = strings.ToLower(value("METRICS_WEBHOOK_AUTH_MODE", "none"))
	c.HMACSecretFile = strings.TrimSpace(os.Getenv("METRICS_WEBHOOK_HMAC_SECRET_FILE"))
	if c.AuthMode != "none" && c.AuthMode != "hmac" {
		return c, errors.New("METRICS_WEBHOOK_AUTH_MODE must be none or hmac")
	}
	if c.AuthMode == "hmac" && !filepath.IsAbs(c.HMACSecretFile) {
		return c, errors.New("HMAC secret file must be absolute")
	}
	c.InstanceName = strings.TrimSpace(os.Getenv("METRICS_INSTANCE_NAME"))
	c.InstanceUUID = strings.TrimSpace(os.Getenv("METRICS_INSTANCE_UUID"))
	c.InstanceFQDN = strings.TrimSpace(os.Getenv("METRICS_INSTANCE_FQDN"))
	c.InstanceVersion = value("METRICS_INSTANCE_VERSION", "unknown")
	c.InstanceCommit = value("METRICS_INSTANCE_COMMIT", "unknown")
	c.IdentityPath = value("METRICS_INSTANCE_ID_PATH", "/srv/jamf-maintenance/instance-id")
	c.InventoryMode = strings.ToLower(value("METRICS_PACKAGE_INVENTORY", "summary"))
	if c.InventoryMode != "none" && c.InventoryMode != "summary" && c.InventoryMode != "full" {
		return c, errors.New("invalid package inventory mode")
	}
	c.InventoryMaxItems, err = positiveInt("METRICS_PACKAGE_INVENTORY_MAX_ITEMS", 5000)
	if err != nil {
		return c, err
	}
	c.TLSCertFile = strings.TrimSpace(os.Getenv("METRICS_TLS_CERT_FILE"))
	if !filepath.IsAbs(c.TLSCertFile) {
		return c, errors.New("METRICS_TLS_CERT_FILE must be absolute")
	}
	c.HealthURL = value("METRICS_HEALTH_URL", "http://nginx:8080/health/ready")
	health, err := url.Parse(c.HealthURL)
	if err != nil || health.Scheme != "http" || health.Hostname() == "" {
		return c, errors.New("METRICS_HEALTH_URL must be an internal HTTP URL")
	}
	c.MaxAttempts, err = positiveInt("METRICS_WEBHOOK_MAX_ATTEMPTS", 3)
	if err != nil || c.MaxAttempts > 5 {
		return c, errors.New("METRICS_WEBHOOK_MAX_ATTEMPTS must be between 1 and 5")
	}
	if c.Interval < 10*time.Second || c.Timeout <= 0 || c.Timeout >= c.Interval || c.TLSCriticalBefore <= 0 || c.TLSWarningBefore < c.TLSCriticalBefore {
		return c, errors.New("invalid metrics timing configuration")
	}
	return c, nil
}

func positiveInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return n, nil
}

type TrafficSnapshot struct {
	WindowSeconds         int64   `json:"window_seconds"`
	Requests              int64   `json:"requests"`
	LocalHits             int64   `json:"local_hits"`
	JCDSFills             int64   `json:"jcds_fills"`
	InflightFollowers     int64   `json:"inflight_followers"`
	HitRatio              float64 `json:"hit_ratio"`
	BytesServed           int64   `json:"bytes_served"`
	BytesDownloaded       int64   `json:"bytes_downloaded"`
	RequestSecondsTotal   float64 `json:"request_seconds_total"`
	RequestSecondsAverage float64 `json:"request_seconds_average"`
	RequestSecondsMax     float64 `json:"request_seconds_max"`
	Status2xx             int64   `json:"status_2xx"`
	Status4xx             int64   `json:"status_4xx"`
	Status5xx             int64   `json:"status_5xx"`
	RangeRequests         int64   `json:"range_requests"`
	Failures              int64   `json:"failures"`
}

type TrafficCollector struct {
	mu       sync.Mutex
	since    time.Time
	snapshot TrafficSnapshot
}

func NewTrafficCollector(now time.Time) *TrafficCollector { return &TrafficCollector{since: now.UTC()} }
func (c *TrafficCollector) Record(e TelemetryEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.snapshot
	s.Requests++
	s.BytesServed += e.BytesSent
	s.RequestSecondsTotal += e.RequestSeconds
	if e.RequestSeconds > s.RequestSecondsMax {
		s.RequestSecondsMax = e.RequestSeconds
	}
	if e.Source == "LOCAL" {
		s.LocalHits++
	}
	if e.Source == "JCDS" {
		s.JCDSFills++
		s.BytesDownloaded += e.BytesSent
	}
	if e.Source == "INFLIGHT" {
		s.InflightFollowers++
	}
	if e.RangeKind != "none" && e.RangeKind != "" {
		s.RangeRequests++
	}
	switch {
	case e.Status >= 200 && e.Status < 300:
		s.Status2xx++
	case e.Status >= 400 && e.Status < 500:
		s.Status4xx++
	case e.Status >= 500:
		s.Status5xx++
	}
	if e.Status >= 400 || e.Completion == "incomplete" {
		s.Failures++
	}
}
func (c *TrafficCollector) Take(now time.Time) TrafficSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.snapshot
	s.WindowSeconds = int64(now.Sub(c.since).Seconds())
	if s.Requests > 0 {
		s.HitRatio = float64(s.LocalHits) / float64(s.Requests)
		s.RequestSecondsAverage = s.RequestSecondsTotal / float64(s.Requests)
	}
	c.snapshot = TrafficSnapshot{}
	c.since = now.UTC()
	return s
}

type CleanupSnapshot struct {
	RetentionSeconds  int64      `json:"retention_seconds"`
	LastResult        string     `json:"last_result"`
	LastCompletedAt   *time.Time `json:"last_completed_at,omitempty"`
	RemovedFilesTotal int64      `json:"removed_files_total"`
	RemovedBytesTotal int64      `json:"removed_bytes_total"`
}
type CleanupTracker struct {
	mu    sync.Mutex
	value CleanupSnapshot
}

func NewCleanupTracker(retention time.Duration) *CleanupTracker {
	return &CleanupTracker{value: CleanupSnapshot{RetentionSeconds: int64(retention.Seconds()), LastResult: "not_required"}}
}
func (c *CleanupTracker) Record(r Result, err error, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value.LastCompletedAt = &at
	if err != nil {
		c.value.LastResult = "failed"
		return
	}
	if !r.Triggered {
		c.value.LastResult = "not_required"
	} else if r.TargetReached {
		c.value.LastResult = "target_reached"
	} else {
		c.value.LastResult = "target_not_reached"
	}
	c.value.RemovedFilesTotal += int64(r.RemovedFiles)
	c.value.RemovedBytesTotal += r.RemovedBytes
}
func (c *CleanupTracker) Snapshot() CleanupSnapshot { c.mu.Lock(); defer c.mu.Unlock(); return c.value }

type Reporter struct {
	Config                        MetricsConfig
	StoreRoot                     string
	Index                         *Index
	TriggerPercent, TargetPercent float64
	Traffic                       *TrafficCollector
	Cleanup                       *CleanupTracker
	Started                       time.Time
	Client                        *http.Client
	now                           func() time.Time
	sequence                      uint64
	previousOK                    bool
	consecutiveFailures           int
}

type packageItem struct {
	Filename     string    `json:"filename"`
	SizeBytes    int64     `json:"size_bytes"`
	LastAccessAt time.Time `json:"last_access_at"`
}
type cacheSnapshot struct {
	PackageCount       int           `json:"package_count"`
	PackageBytes       int64         `json:"package_bytes"`
	TemporaryCount     int           `json:"temporary_count"`
	TemporaryBytes     int64         `json:"temporary_bytes"`
	IndexEntries       int           `json:"index_entries"`
	UnindexedEntries   int           `json:"unindexed_entries"`
	UnsafeEntries      int           `json:"unsafe_entries"`
	InventoryMode      string        `json:"inventory_mode"`
	InventoryTotal     int           `json:"inventory_total,omitempty"`
	InventoryReturned  int           `json:"inventory_returned,omitempty"`
	InventoryTruncated bool          `json:"inventory_truncated,omitempty"`
	Packages           []packageItem `json:"packages,omitempty"`
}

func (r *Reporter) Run(ctx context.Context) {
	if !r.Config.Enabled {
		return
	}
	if r.Started.IsZero() {
		r.Started = time.Now().UTC()
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.Client == nil {
		r.Client = secureWebhookClient(r.Config)
	}
	ticker := time.NewTicker(r.Config.Interval)
	defer ticker.Stop()
	r.deliver(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.deliver(ctx)
		}
	}
}

func (r *Reporter) deliver(ctx context.Context) {
	body, eventID, err := r.snapshot()
	if err != nil {
		slog.Warn("webhook snapshot failed", "error", err)
		return
	}
	secret := []byte(nil)
	if r.Config.AuthMode == "hmac" {
		secret, err = readHMACSecret(r.Config.HMACSecretFile)
		if err != nil {
			slog.Warn("webhook authentication unavailable", "error", err)
			return
		}
	}
	for attempt := 1; attempt <= r.Config.MaxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(1<<(attempt-2)) * 250 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Config.URL.String(), bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-JCDS-Event-ID", eventID)
			if len(secret) > 0 {
				timestamp := strconv.FormatInt(r.now().Unix(), 10)
				mac := hmac.New(sha256.New, secret)
				mac.Write([]byte(timestamp))
				mac.Write([]byte("\n"))
				mac.Write(body)
				req.Header.Set("X-JCDS-Timestamp", timestamp)
				req.Header.Set("X-JCDS-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
			}
			var resp *http.Response
			resp, err = r.Client.Do(req)
			if err == nil {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					r.previousOK = true
					r.consecutiveFailures = 0
					return
				}
				err = fmt.Errorf("receiver status %d", resp.StatusCode)
			}
		}
	}
	r.previousOK = false
	r.consecutiveFailures++
	slog.Warn("webhook delivery failed", "consecutive_failures", r.consecutiveFailures, "error", err)
}

func readHMACSecret(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("HMAC secret is not a regular file")
	}
	permissions := info.Mode().Perm()
	if permissions&0o007 != 0 || permissions&0o030 != 0 {
		return nil, errors.New("HMAC secret has unsafe group or other permissions")
	}
	if permissions&0o040 != 0 {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Gid) != os.Getegid() {
			return nil, errors.New("HMAC secret group does not match the process group")
		}
	}
	secret, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	secret = bytes.TrimSpace(secret)
	if len(secret) < 32 {
		return nil, errors.New("HMAC secret must contain at least 32 bytes")
	}
	return secret, nil
}

func secureWebhookClient(c MetricsConfig) *http.Client {
	dialer := &net.Dialer{Timeout: c.Timeout}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !c.AllowPrivateIPs && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
				return nil, errors.New("webhook resolved to a disallowed address")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("webhook hostname resolved without addresses")
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: transport, Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (r *Reporter) snapshot() ([]byte, string, error) {
	now := r.now().UTC()
	id, err := ensureInstanceID(r.Config.InstanceUUID, r.Config.IdentityPath)
	if err != nil {
		return nil, "", err
	}
	eventID, err := newUUID()
	if err != nil {
		return nil, "", err
	}
	r.sequence++
	ready, status := r.checkHealth(now)
	tls := inspectCertificate(r.Config.TLSCertFile, now, r.Config.TLSWarningBefore, r.Config.TLSCriticalBefore)
	storage, err := r.storage()
	if err != nil {
		return nil, "", err
	}
	cache, err := r.inventory()
	if err != nil {
		return nil, "", err
	}
	payload := struct {
		SchemaVersion int             `json:"schema_version"`
		EventID       string          `json:"event_id"`
		Sequence      uint64          `json:"sequence"`
		ObservedAt    time.Time       `json:"observed_at"`
		Instance      any             `json:"instance"`
		Health        any             `json:"health"`
		TLS           any             `json:"tls"`
		Storage       any             `json:"storage"`
		Cache         cacheSnapshot   `json:"cache"`
		Traffic       TrafficSnapshot `json:"traffic"`
		Cleanup       CleanupSnapshot `json:"cleanup"`
		Reporter      any             `json:"reporter"`
	}{1, eventID, r.sequence, now, struct {
		Name    string `json:"name"`
		UUID    string `json:"uuid"`
		FQDN    string `json:"fqdn"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Uptime  int64  `json:"uptime_seconds"`
	}{r.Config.InstanceName, id, r.Config.InstanceFQDN, r.Config.InstanceVersion, r.Config.InstanceCommit, int64(now.Sub(r.Started).Seconds())}, struct {
		Ready         bool      `json:"ready"`
		GatewayStatus int       `json:"gateway_status"`
		CheckedAt     time.Time `json:"checked_at"`
	}{ready, status, now}, tls, storage, cache, r.Traffic.Take(now), r.Cleanup.Snapshot(), struct {
		Previous bool `json:"previous_delivery_succeeded"`
		Failures int  `json:"consecutive_failures"`
	}{r.previousOK, r.consecutiveFailures}}
	body, err := json.Marshal(payload)
	return body, eventID, err
}

func (r *Reporter) checkHealth(now time.Time) (bool, int) {
	ctx, cancel := context.WithTimeout(context.Background(), min(r.Config.Timeout, 5*time.Second))
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.Config.HealthURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, 0
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	resp.Body.Close()
	return resp.StatusCode == 200, resp.StatusCode
}
func (r *Reporter) storage() (any, error) {
	a, t, err := filesystemSpace(r.StoreRoot)
	if err != nil {
		return nil, err
	}
	free := float64(a) * 100 / float64(t)
	return struct {
		Total     int64   `json:"total_bytes"`
		Available int64   `json:"available_bytes"`
		Free      float64 `json:"free_percent"`
		Pressure  bool    `json:"pressure"`
		Trigger   float64 `json:"cleanup_trigger_percent"`
		Target    float64 `json:"cleanup_target_percent"`
	}{t, a, math.Round(free*10) / 10, free < r.TriggerPercent, r.TriggerPercent, r.TargetPercent}, nil
}
func (r *Reporter) inventory() (cacheSnapshot, error) {
	result := cacheSnapshot{InventoryMode: r.Config.InventoryMode}
	indexed := r.Index.snapshot()
	result.IndexEntries = len(indexed)
	temporaryRoot := filepath.Join(filepath.Dir(r.StoreRoot), ".temporary")
	if temporaryEntries, temporaryErr := os.ReadDir(temporaryRoot); temporaryErr == nil {
		for _, entry := range temporaryEntries {
			info, statErr := os.Lstat(filepath.Join(temporaryRoot, entry.Name()))
			if statErr == nil && info.Mode().IsRegular() {
				result.TemporaryCount++
				result.TemporaryBytes += info.Size()
			}
		}
	} else if !errors.Is(temporaryErr, os.ErrNotExist) {
		return result, temporaryErr
	}
	entries, err := os.ReadDir(r.StoreRoot)
	if err != nil {
		return result, err
	}
	for _, e := range entries {
		path := filepath.Join(r.StoreRoot, e.Name())
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() || store.ValidateFilename(e.Name()) != nil {
			result.UnsafeEntries++
			continue
		}
		result.PackageCount++
		result.PackageBytes += info.Size()
		at := info.ModTime().UTC()
		if x, ok := indexed[e.Name()]; ok {
			if x.After(at) {
				at = x
			}
		} else {
			result.UnindexedEntries++
		}
		if r.Config.InventoryMode == "full" {
			result.Packages = append(result.Packages, packageItem{e.Name(), info.Size(), at})
		}
	}
	if r.Config.InventoryMode == "full" {
		sort.Slice(result.Packages, func(i, j int) bool { return result.Packages[i].Filename < result.Packages[j].Filename })
		result.InventoryTotal = len(result.Packages)
		if len(result.Packages) > r.Config.InventoryMaxItems {
			result.Packages = result.Packages[:r.Config.InventoryMaxItems]
			result.InventoryTruncated = true
		}
		result.InventoryReturned = len(result.Packages)
	}
	return result, nil
}

type tlsSnapshot struct {
	Subject          string     `json:"subject,omitempty"`
	NotAfter         *time.Time `json:"not_after,omitempty"`
	RemainingSeconds int64      `json:"remaining_seconds"`
	RemainingDays    int64      `json:"remaining_days"`
	ExpiryStatus     string     `json:"expiry_status"`
}

func inspectCertificate(path string, now time.Time, warning, critical time.Duration) tlsSnapshot {
	result := tlsSnapshot{ExpiryStatus: "unknown"}
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return result
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return result
	}
	remaining := cert.NotAfter.Sub(now)
	notAfter := cert.NotAfter.UTC()
	result.Subject = cert.Subject.String()
	result.NotAfter = &notAfter
	result.RemainingSeconds = int64(remaining.Seconds())
	result.RemainingDays = int64(math.Floor(remaining.Hours() / 24))
	switch {
	case remaining <= 0:
		result.ExpiryStatus = "expired"
	case remaining <= critical:
		result.ExpiryStatus = "critical"
	case remaining <= warning:
		result.ExpiryStatus = "warning"
	default:
		result.ExpiryStatus = "ok"
	}
	return result
}
func ensureInstanceID(configured, path string) (string, error) {
	if configured != "" {
		if !validUUID(configured) {
			return "", errors.New("configured metrics instance UUID is invalid")
		}
		return strings.ToLower(configured), nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if !validUUID(id) {
			return "", errors.New("persisted metrics instance UUID is invalid")
		}
		return strings.ToLower(id), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o770); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}
func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
