// Package config loads and validates non-secret service configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultListenAddress  = ":8080"
	defaultStoreRoot      = "/srv/jamf-store"
	defaultShutdownPeriod = 15 * time.Second
)

// Config contains the configuration needed by the initial helper skeleton.
// Jamf credentials will be added through a dedicated secret-loading boundary.
type Config struct {
	ListenAddress  string
	StoreRoot      string
	ShutdownPeriod time.Duration
}

// Load reads configuration from the process environment and applies safe
// development defaults. It deliberately contains no default credentials.
func Load() (Config, error) {
	cfg := Config{
		ListenAddress:  valueOrDefault("CACHE_HELPER_LISTEN_ADDRESS", defaultListenAddress),
		StoreRoot:      valueOrDefault("CACHE_STORE_ROOT", defaultStoreRoot),
		ShutdownPeriod: defaultShutdownPeriod,
	}

	if _, _, err := net.SplitHostPort(cfg.ListenAddress); err != nil {
		return Config{}, fmt.Errorf("CACHE_HELPER_LISTEN_ADDRESS: %w", err)
	}

	if !filepath.IsAbs(cfg.StoreRoot) {
		return Config{}, fmt.Errorf("CACHE_STORE_ROOT must be an absolute path")
	}
	cfg.StoreRoot = filepath.Clean(cfg.StoreRoot)
	if cfg.StoreRoot == string(filepath.Separator) {
		return Config{}, fmt.Errorf("CACHE_STORE_ROOT must not be the filesystem root")
	}

	return cfg, nil
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
