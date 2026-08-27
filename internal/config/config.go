package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const resolverFilenamePlaceholder = "{filename}"

type Config struct {
	ListenAddress        string
	StoreRoot            string
	TempRoot             string
	TokenURL             string
	ClientID             string
	ClientSecret         string
	CatalogURL           string
	ResolverURLTemplate  string
	DownloadURLField     string
	AllowedDownloadHosts []string
	AllowHTTP            bool
	TokenExpirySkew      time.Duration
	FillTimeout          time.Duration
	ShutdownTimeout      time.Duration
	MaxPackageBytes      int64
	MinFreeBytes         int64
	MinFreePercent       float64
	TempFileMaxAge       time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:       envOrDefault("LISTEN_ADDR", ":8080"),
		StoreRoot:           envOrDefault("STORE_ROOT", "/srv/jamf-store/packages"),
		TempRoot:            envOrDefault("TEMP_ROOT", "/srv/jamf-store/.temporary"),
		TokenURL:            strings.TrimSpace(os.Getenv("JAMF_TOKEN_URL")),
		ClientID:            strings.TrimSpace(os.Getenv("JAMF_CLIENT_ID")),
		ClientSecret:        os.Getenv("JAMF_CLIENT_SECRET"),
		CatalogURL:          strings.TrimSpace(os.Getenv("JAMF_CATALOG_URL")),
		ResolverURLTemplate: strings.TrimSpace(os.Getenv("JAMF_RESOLVER_URL_TEMPLATE")),
		DownloadURLField:    envOrDefault("JAMF_DOWNLOAD_URL_FIELD", "uri"),
	}

	var err error
	if cfg.AllowHTTP, err = parseBool("JCDS_ALLOW_HTTP", false); err != nil {
		return Config{}, err
	}
	if cfg.TokenExpirySkew, err = parseDuration("TOKEN_EXPIRY_SKEW", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.FillTimeout, err = parseDuration("FILL_TIMEOUT", 2*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = parseDuration("SHUTDOWN_TIMEOUT", 2*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.MaxPackageBytes, err = parseInt64("MAX_PACKAGE_BYTES", 25*1024*1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.MinFreeBytes, err = parseInt64("MIN_FREE_BYTES", 10*1024*1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.MinFreePercent, err = parseFloat64("MIN_FREE_PERCENT", 20); err != nil {
		return Config{}, err
	}
	if cfg.TempFileMaxAge, err = parseDuration("TEMP_FILE_MAX_AGE", 24*time.Hour); err != nil {
		return Config{}, err
	}

	cfg.AllowedDownloadHosts = splitCSV(os.Getenv("JCDS_ALLOWED_HOSTS"))
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	var validationErrors []error

	if strings.TrimSpace(c.ListenAddress) == "" {
		validationErrors = append(validationErrors, errors.New("LISTEN_ADDR must not be empty"))
	}
	if strings.TrimSpace(c.StoreRoot) == "" {
		validationErrors = append(validationErrors, errors.New("STORE_ROOT must not be empty"))
	}
	if strings.TrimSpace(c.TempRoot) == "" {
		validationErrors = append(validationErrors, errors.New("TEMP_ROOT must not be empty"))
	}
	if c.StoreRoot == c.TempRoot {
		validationErrors = append(validationErrors, errors.New("STORE_ROOT and TEMP_ROOT must be different directories"))
	}
	if c.ClientID == "" {
		validationErrors = append(validationErrors, errors.New("JAMF_CLIENT_ID is required"))
	}
	if c.ClientSecret == "" {
		validationErrors = append(validationErrors, errors.New("JAMF_CLIENT_SECRET is required"))
	}
	if err := validateServiceURL("JAMF_TOKEN_URL", c.TokenURL, c.AllowHTTP); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := validateServiceURL("JAMF_CATALOG_URL", c.CatalogURL, c.AllowHTTP); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if strings.Count(c.ResolverURLTemplate, resolverFilenamePlaceholder) != 1 {
		validationErrors = append(validationErrors, fmt.Errorf("JAMF_RESOLVER_URL_TEMPLATE must contain %s exactly once", resolverFilenamePlaceholder))
	} else {
		probeURL := strings.Replace(c.ResolverURLTemplate, resolverFilenamePlaceholder, "ExampleFile.pkg", 1)
		if err := validateServiceURL("JAMF_RESOLVER_URL_TEMPLATE", probeURL, c.AllowHTTP); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	if strings.TrimSpace(c.DownloadURLField) == "" {
		validationErrors = append(validationErrors, errors.New("JAMF_DOWNLOAD_URL_FIELD must not be empty"))
	}
	if len(c.AllowedDownloadHosts) == 0 {
		validationErrors = append(validationErrors, errors.New("JCDS_ALLOWED_HOSTS must contain at least one exact hostname"))
	}
	for _, host := range c.AllowedDownloadHosts {
		if host == "" || strings.ContainsAny(host, "/?#@") {
			validationErrors = append(validationErrors, fmt.Errorf("invalid JCDS_ALLOWED_HOSTS entry %q", host))
		}
	}
	if c.TokenExpirySkew < 0 {
		validationErrors = append(validationErrors, errors.New("TOKEN_EXPIRY_SKEW must not be negative"))
	}
	if c.FillTimeout <= 0 {
		validationErrors = append(validationErrors, errors.New("FILL_TIMEOUT must be greater than zero"))
	}
	if c.ShutdownTimeout <= 0 {
		validationErrors = append(validationErrors, errors.New("SHUTDOWN_TIMEOUT must be greater than zero"))
	}
	if c.MaxPackageBytes <= 0 {
		validationErrors = append(validationErrors, errors.New("MAX_PACKAGE_BYTES must be greater than zero"))
	}
	if c.MinFreeBytes < 0 {
		validationErrors = append(validationErrors, errors.New("MIN_FREE_BYTES must not be negative"))
	}
	if c.MinFreePercent < 0 || c.MinFreePercent >= 100 {
		validationErrors = append(validationErrors, errors.New("MIN_FREE_PERCENT must be at least zero and less than 100"))
	}
	if c.TempFileMaxAge <= 0 {
		validationErrors = append(validationErrors, errors.New("TEMP_FILE_MAX_AGE must be greater than zero"))
	}

	return errors.Join(validationErrors...)
}

func envOrDefault(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func parseBool(name string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func parseDuration(name string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func parseInt64(name string, fallback int64) (int64, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func parseFloat64(name string, fallback float64) (float64, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func splitCSV(raw string) []string {
	seen := make(map[string]struct{})
	var values []string
	for _, part := range strings.Split(raw, ",") {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func validateServiceURL(name, rawURL string, allowHTTP bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	if !parsed.IsAbs() || parsed.Hostname() == "" {
		return fmt.Errorf("%s must be an absolute URL with a hostname", name)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not contain user information", name)
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return fmt.Errorf("%s must use HTTPS", name)
	}
	return nil
}
