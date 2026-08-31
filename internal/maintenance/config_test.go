package maintenance

import (
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	for _, name := range []string{
		"CACHE_RETENTION",
		"CACHE_CLEANUP_INTERVAL",
		"ACCESS_INDEX_FLUSH_INTERVAL",
		"CACHE_CLEANUP_TRIGGER_FREE_PERCENT",
		"CACHE_CLEANUP_TARGET_FREE_PERCENT",
	} {
		t.Setenv(name, "")
	}
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Retention != 90*24*time.Hour || config.TriggerPercent != 30 || config.TargetPercent != 35 {
		t.Fatalf("unexpected lifecycle defaults: %+v", config)
	}
}

func TestLoadConfigSupportsDynamicRetention(t *testing.T) {
	t.Setenv("CACHE_RETENTION", "45d")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("non-Go duration syntax should be rejected")
	}
	t.Setenv("CACHE_RETENTION", "1080h")
	t.Setenv("CACHE_CLEANUP_TRIGGER_FREE_PERCENT", "25.5")
	t.Setenv("CACHE_CLEANUP_TARGET_FREE_PERCENT", "31")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Retention != 45*24*time.Hour || config.TriggerPercent != 25.5 || config.TargetPercent != 31 {
		t.Fatalf("dynamic lifecycle configuration was not applied: %+v", config)
	}
}

func TestLoadConfigRejectsInvalidThresholdOrder(t *testing.T) {
	t.Setenv("CACHE_CLEANUP_TRIGGER_FREE_PERCENT", "40")
	t.Setenv("CACHE_CLEANUP_TARGET_FREE_PERCENT", "35")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("target at or below trigger should be rejected")
	}
}
