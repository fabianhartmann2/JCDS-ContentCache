package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

type Index struct {
	mu       sync.Mutex
	path     string
	accessed map[string]time.Time
	dirty    bool
}

type indexFile struct {
	Version  int                  `json:"version"`
	Accessed map[string]time.Time `json:"accessed"`
}

func LoadIndex(path string) (*Index, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("access-index path must be absolute")
	}
	index := &Index{path: filepath.Clean(path), accessed: make(map[string]time.Time)}
	data, err := os.ReadFile(index.path)
	if errors.Is(err, os.ErrNotExist) {
		return index, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read access index: %w", err)
	}
	var persisted indexFile
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, fmt.Errorf("decode access index: %w", err)
	}
	if persisted.Version != 1 || persisted.Accessed == nil {
		return nil, errors.New("access index has an unsupported format")
	}
	for name, accessed := range persisted.Accessed {
		if store.ValidateFilename(name) == nil && !accessed.IsZero() {
			index.accessed[name] = accessed.UTC()
		}
	}
	return index, nil
}

func (i *Index) Record(filename string, at time.Time) error {
	if err := store.ValidateFilename(filename); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	at = at.UTC()
	if previous, ok := i.accessed[filename]; ok && !at.After(previous) {
		return nil
	}
	i.accessed[filename] = at
	i.dirty = true
	return nil
}

func (i *Index) snapshot() map[string]time.Time {
	i.mu.Lock()
	defer i.mu.Unlock()
	result := make(map[string]time.Time, len(i.accessed))
	for name, accessed := range i.accessed {
		result[name] = accessed
	}
	return result
}

func (i *Index) Remove(filename string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.accessed[filename]; ok {
		delete(i.accessed, filename)
		i.dirty = true
	}
}

func (i *Index) Flush() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(i.path), 0o770); err != nil {
		return fmt.Errorf("prepare access-index directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(i.path), ".access-index-*.tmp")
	if err != nil {
		return fmt.Errorf("create access-index snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure access-index snapshot: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(indexFile{Version: 1, Accessed: i.accessed}); err != nil {
		temporary.Close()
		return fmt.Errorf("encode access-index snapshot: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync access-index snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close access-index snapshot: %w", err)
	}
	if err := os.Rename(temporaryPath, i.path); err != nil {
		return fmt.Errorf("publish access-index snapshot: %w", err)
	}
	i.dirty = false
	return nil
}

type AccessEvent struct {
	Status         int     `json:"status"`
	URI            string  `json:"uri"`
	Source         string  `json:"source"`
	BytesSent      int64   `json:"bytes_sent"`
	RequestSeconds float64 `json:"request_seconds"`
	RangeKind      string  `json:"range_kind"`
	Completion     string  `json:"completion"`
}

type TelemetryEvent = AccessEvent

func ParseTelemetryEvent(message []byte) (TelemetryEvent, string, bool) {
	start := strings.IndexByte(string(message), '{')
	if start < 0 {
		return TelemetryEvent{}, "", false
	}
	var event TelemetryEvent
	if json.Unmarshal(message[start:], &event) != nil {
		return TelemetryEvent{}, "", false
	}
	parsed, err := url.ParseRequestURI(event.URI)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(!strings.HasPrefix(parsed.Path, "/packages/") && !strings.HasPrefix(parsed.Path, "/Packages/")) {
		return TelemetryEvent{}, "", false
	}
	filename, _ := packageFilename(event)
	return event, filename, true
}

func ParseAccessEvent(message []byte) (string, bool) {
	var event AccessEvent
	start := strings.IndexByte(string(message), '{')
	if start < 0 || json.Unmarshal(message[start:], &event) != nil {
		return "", false
	}
	return packageFilename(event)
}

func packageFilename(event AccessEvent) (string, bool) {
	if event.Status != 200 && event.Status != 206 {
		return "", false
	}
	parsed, err := url.ParseRequestURI(event.URI)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	prefix := "/packages/"
	if strings.HasPrefix(parsed.Path, "/Packages/") {
		prefix = "/Packages/"
	} else if !strings.HasPrefix(parsed.Path, prefix) {
		return "", false
	}
	filename, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), prefix))
	if err != nil || store.ValidateFilename(filename) != nil {
		return "", false
	}
	return filename, true
}

type SpaceFunc func(path string) (availableBytes, totalBytes int64, err error)

type Cleaner struct {
	StoreRoot      string
	AuditPath      string
	Retention      time.Duration
	TriggerPercent float64
	TargetPercent  float64
	Index          *Index
	Now            func() time.Time
	Space          SpaceFunc
}

type Result struct {
	Triggered     bool
	TargetReached bool
	RemovedFiles  int
	RemovedBytes  int64
}

type candidate struct {
	name       string
	path       string
	size       int64
	lastAccess time.Time
}

func (c *Cleaner) Run() (Result, error) {
	if c.Index == nil || c.Retention <= 0 || c.TriggerPercent < 0 || c.TargetPercent <= c.TriggerPercent || c.TargetPercent >= 100 {
		return Result{}, errors.New("invalid cleaner configuration")
	}
	space := c.Space
	if space == nil {
		space = filesystemSpace
	}
	available, total, err := space(c.StoreRoot)
	if err != nil {
		return Result{}, fmt.Errorf("inspect package-store capacity: %w", err)
	}
	if float64(available)*100/float64(total) >= c.TriggerPercent {
		return Result{}, nil
	}
	result := Result{Triggered: true}
	needed := int64(float64(total)*c.TargetPercent/100) - available
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	indexed := c.Index.snapshot()
	entries, err := os.ReadDir(c.StoreRoot)
	if err != nil {
		return result, fmt.Errorf("read package store: %w", err)
	}
	var candidates []candidate
	for _, entry := range entries {
		if store.ValidateFilename(entry.Name()) != nil {
			continue
		}
		path := filepath.Join(c.StoreRoot, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		lastAccess := info.ModTime().UTC()
		if indexedAt, ok := indexed[entry.Name()]; ok && indexedAt.After(lastAccess) {
			lastAccess = indexedAt
		}
		if now.Sub(lastAccess) >= c.Retention {
			candidates = append(candidates, candidate{entry.Name(), path, info.Size(), lastAccess})
		}
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].lastAccess.Before(candidates[right].lastAccess) })
	for _, item := range candidates {
		if result.RemovedBytes >= needed {
			break
		}
		info, err := os.Lstat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() != item.size {
			continue
		}
		if err := os.Remove(item.path); err != nil {
			return result, fmt.Errorf("remove selected package: %w", err)
		}
		c.Index.Remove(item.name)
		result.RemovedFiles++
		result.RemovedBytes += item.size
		if err := appendAudit(c.AuditPath, item, now); err != nil {
			return result, err
		}
	}
	if err := c.Index.Flush(); err != nil {
		return result, err
	}
	result.TargetReached = result.RemovedBytes >= needed
	return result, nil
}

func appendAudit(path string, item candidate, at time.Time) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open cleanup audit: %w", err)
	}
	defer file.Close()
	record := struct {
		Time       time.Time `json:"time"`
		Filename   string    `json:"filename"`
		Bytes      int64     `json:"bytes"`
		LastAccess time.Time `json:"last_access"`
		Reason     string    `json:"reason"`
	}{at, item.name, item.size, item.lastAccess, "low_disk_retention"}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		return fmt.Errorf("write cleanup audit: %w", err)
	}
	return nil
}

func filesystemSpace(path string) (int64, int64, error) {
	var statistics syscall.Statfs_t
	if err := syscall.Statfs(path, &statistics); err != nil {
		return 0, 0, err
	}
	if statistics.Bsize <= 0 || statistics.Blocks == 0 {
		return 0, 0, errors.New("filesystem returned invalid capacity")
	}
	return int64(statistics.Bavail) * statistics.Bsize, int64(statistics.Blocks) * statistics.Bsize, nil
}
