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
		ResolverURLTemplate:  "https://tenant.example/api/files/{filename}",
		DownloadURLField:     "uri",
		AllowedDownloadHosts: []string{"download.example"},
		TokenExpirySkew:      time.Minute,
		FillTimeout:          time.Hour,
		ShutdownTimeout:      time.Minute,
		MaxPackageBytes:      1024,
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
