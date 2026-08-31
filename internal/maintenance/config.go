package maintenance

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	UDPListen       string
	HTTPListen      string
	StoreRoot       string
	IndexPath       string
	AuditPath       string
	Retention       time.Duration
	CleanupInterval time.Duration
	FlushInterval   time.Duration
	TriggerPercent  float64
	TargetPercent   float64
	Metrics         MetricsConfig
}

func LoadConfig() (Config, error) {
	c := Config{
		UDPListen:  value("MAINTENANCE_UDP_LISTEN", ":5514"),
		HTTPListen: value("MAINTENANCE_HTTP_LISTEN", ":8082"),
		StoreRoot:  value("MAINTENANCE_STORE_ROOT", "/srv/jamf-store/packages"),
		IndexPath:  value("MAINTENANCE_INDEX_PATH", "/srv/jamf-maintenance/access-index.json"),
		AuditPath:  value("MAINTENANCE_AUDIT_PATH", "/srv/jamf-maintenance/cleanup-audit.jsonl"),
	}
	var err error
	if c.Retention, err = duration("CACHE_RETENTION", 90*24*time.Hour); err != nil {
		return Config{}, err
	}
	if c.CleanupInterval, err = duration("CACHE_CLEANUP_INTERVAL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if c.FlushInterval, err = duration("ACCESS_INDEX_FLUSH_INTERVAL", 30*time.Second); err != nil {
		return Config{}, err
	}
	if c.TriggerPercent, err = number("CACHE_CLEANUP_TRIGGER_FREE_PERCENT", 30); err != nil {
		return Config{}, err
	}
	if c.TargetPercent, err = number("CACHE_CLEANUP_TARGET_FREE_PERCENT", 35); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(c.UDPListen) == "" || strings.TrimSpace(c.HTTPListen) == "" || c.Retention <= 0 || c.CleanupInterval <= 0 || c.FlushInterval <= 0 || c.TriggerPercent < 0 || c.TargetPercent <= c.TriggerPercent || c.TargetPercent >= 100 {
		return Config{}, errors.New("invalid cache-maintainer configuration")
	}
	c.Metrics, err = loadMetricsConfig()
	if err != nil {
		// Reporting is deliberately fail-open: invalid optional monitoring must
		// never prevent cleanup, health endpoints or package delivery.
		c.Metrics.ConfigError = err
		c.Metrics.Enabled = false
	}
	return c, nil
}

func value(name, fallback string) string {
	if current := strings.TrimSpace(os.Getenv(name)); current != "" {
		return current
	}
	return fallback
}

func duration(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func number(name string, fallback float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}
