package config

import (
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("CACHE_HELPER_LISTEN_ADDRESS", "")
	t.Setenv("CACHE_STORE_ROOT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddress != defaultListenAddress {
		t.Fatalf("ListenAddress = %q, want %q", cfg.ListenAddress, defaultListenAddress)
	}
	if cfg.StoreRoot != defaultStoreRoot {
		t.Fatalf("StoreRoot = %q, want %q", cfg.StoreRoot, defaultStoreRoot)
	}
}

func TestLoadRejectsRelativeStoreRoot(t *testing.T) {
	t.Setenv("CACHE_STORE_ROOT", filepath.Join("var", "cache"))

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want relative-path validation error")
	}
}

func TestLoadRejectsInvalidListenAddress(t *testing.T) {
	t.Setenv("CACHE_HELPER_LISTEN_ADDRESS", "8080")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want listen-address validation error")
	}
}
