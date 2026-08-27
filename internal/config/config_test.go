package config

import (
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		ListenAddress:        ":8080",
		StoreRoot:            "/srv/jamf-store/packages",
		TempRoot:             "/srv/jamf-store/.temporary",
		TokenURL:             "https://tenant.example/api/v1/oauth/token",
		ClientID:             "client-id",
		ClientSecret:         "client-secret",
		CatalogURL:           "https://tenant.example/api/v1/jcds/files",
		ResolverURLTemplate:  "https://tenant.example/api/files/{filename}",
		DownloadURLField:     "uri",
		AllowedDownloadHosts: []string{"download.example"},
		TokenExpirySkew:      time.Minute,
		FillTimeout:          time.Hour,
		ShutdownTimeout:      time.Minute,
		MaxPackageBytes:      1024,
		MinFreeBytes:         128,
		MinFreePercent:       20,
		TempFileMaxAge:       24 * time.Hour,
	}
}

func TestValidateAcceptsCompleteConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("Validate() returned an unexpected error: %v", err)
	}
}

func TestValidateRejectsHTTPByDefault(t *testing.T) {
	cfg := validConfig()
	cfg.TokenURL = "http://tenant.example/api/v1/oauth/token"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("Validate() error = %v, want HTTPS error", err)
	}
}

func TestValidateRequiresOneResolverPlaceholder(t *testing.T) {
	cfg := validConfig()
	cfg.ResolverURLTemplate = "https://tenant.example/api/files/static.pkg"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("Validate() error = %v, want placeholder error", err)
	}
}

func TestValidateRequiresHTTPSCatalogURL(t *testing.T) {
	cfg := validConfig()
	cfg.CatalogURL = "http://tenant.example/api/v1/jcds/files"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "JAMF_CATALOG_URL must use HTTPS") {
		t.Fatalf("Validate() error = %v, want catalog HTTPS error", err)
	}
}

func TestValidateRejectsInvalidCapacityConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "negative free bytes",
			mutate: func(cfg *Config) {
				cfg.MinFreeBytes = -1
			},
			want: "MIN_FREE_BYTES",
		},
		{
			name: "free percent reaches 100",
			mutate: func(cfg *Config) {
				cfg.MinFreePercent = 100
			},
			want: "MIN_FREE_PERCENT",
		},
		{
			name: "temporary age is zero",
			mutate: func(cfg *Config) {
				cfg.TempFileMaxAge = 0
			},
			want: "TEMP_FILE_MAX_AGE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %s error", err, test.want)
			}
		})
	}
}
