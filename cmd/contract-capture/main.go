// Command contract-capture validates live Jamf success contracts while
// emitting only a deliberately minimized, sanitized report.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/jamf"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

const maxCaptureResponseBytes = 32 * 1024 * 1024

var errUnsafeProbeDestination = errors.New("object probe destination rejected")

type captureReport struct {
	OAuth    oauthReport    `json:"oauth"`
	Catalog  catalogReport  `json:"catalog"`
	Resolver resolverReport `json:"resolver"`
	Object   objectReport   `json:"object_probe"`
}

type oauthReport struct {
	Fields           map[string]string `json:"fields"`
	TokenType        string            `json:"token_type"`
	ExpiresInSeconds int64             `json:"expires_in_seconds"`
}

type catalogReport struct {
	TopLevelShape       string `json:"top_level_shape"`
	FileCount           int    `json:"file_count"`
	V1PackageCount      int    `json:"v1_package_count"`
	V1TotalBytes        int64  `json:"v1_total_bytes"`
	V1LargestBytes      int64  `json:"v1_largest_bytes"`
	ExactFilenameMatch  bool   `json:"exact_filename_match"`
	LengthPresent       bool   `json:"length_present"`
	MD5HexCharacters    int    `json:"md5_hex_characters"`
	RegionPresent       bool   `json:"region_present"`
	SHA3HexCharacters   int    `json:"sha3_hex_characters"`
}

type resolverReport struct {
	URLField            string `json:"url_field"`
	Scheme              string `json:"scheme"`
	HostnameFingerprint string `json:"hostname_fingerprint"`
	PathPresent         bool   `json:"path_present"`
	SignedQueryPresent  bool   `json:"signed_query_present"`
	QueryParameterCount int    `json:"query_parameter_count"`
}

type objectReport struct {
	Head            probeReport `json:"head"`
	SingleByteRange probeReport `json:"single_byte_range"`
}

type probeReport struct {
	Outcome                     string   `json:"outcome"`
	StatusCode                  int      `json:"status_code,omitempty"`
	RedirectCount               int      `json:"redirect_count"`
	HostnameFingerprints        []string `json:"hostname_fingerprints,omitempty"`
	FinalHostnameChanged        bool     `json:"final_hostname_changed"`
	ContentLengthPresent        bool     `json:"content_length_present"`
	ContentLengthMatchesCatalog *bool    `json:"content_length_matches_catalog,omitempty"`
	ContentTypePresent          bool     `json:"content_type_present"`
	AcceptRangesBytes           bool     `json:"accept_ranges_bytes"`
	ETagPresent                 bool     `json:"etag_present"`
	LastModifiedPresent         bool     `json:"last_modified_present"`
	ContentRangePresent         bool     `json:"content_range_present"`
	RangeHonored                *bool    `json:"range_honored,omitempty"`
}

type catalogEntry struct {
	FileName string `json:"fileName"`
	Length   *int64 `json:"length"`
	MD5      string `json:"md5"`
	Region   string `json:"region"`
	SHA3     string `json:"sha3"`
}

type fixedTokenSource string

func (s fixedTokenSource) Token(context.Context) (string, error) {
	return string(s), nil
}

func (fixedTokenSource) Invalidate() {}

type destinationValidator func(context.Context, *url.URL) error

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "contract capture failed:", err)
		os.Exit(1)
	}
}

func run(output io.Writer) error {
	tokenURL, err := requiredEnvironment("JAMF_TOKEN_URL")
	if err != nil {
		return err
	}
	clientID, err := requiredEnvironment("JAMF_CLIENT_ID")
	if err != nil {
		return err
	}
	clientSecret, err := requiredEnvironment("JAMF_CLIENT_SECRET")
	if err != nil {
		return err
	}
	catalogURL, err := requiredEnvironment("JAMF_CATALOG_URL")
	if err != nil {
		return err
	}
	resolverTemplate, err := requiredEnvironment("JAMF_RESOLVER_URL_TEMPLATE")
	if err != nil {
		return err
	}
	packageName, err := requiredEnvironment("CAPTURE_PACKAGE_NAME")
	if err != nil {
		return err
	}
	if err := store.ValidateFilename(packageName); err != nil {
		return errors.New("CAPTURE_PACKAGE_NAME is not a valid flat .pkg filename")
	}
	for name, rawURL := range map[string]string{
		"JAMF_TOKEN_URL":            tokenURL,
		"JAMF_CATALOG_URL":          catalogURL,
		"JAMF_RESOLVER_URL_TEMPLATE": strings.Replace(resolverTemplate, "{filename}", "Synthetic.pkg", 1),
	} {
		if err := validateHTTPSServiceURL(name, rawURL); err != nil {
			return err
		}
	}
	if strings.Count(resolverTemplate, "{filename}") != 1 {
		return errors.New("JAMF_RESOLVER_URL_TEMPLATE must contain {filename} exactly once")
	}

	serviceClient := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	accessToken, oauth, err := captureOAuth(ctx, serviceClient, tokenURL, clientID, clientSecret)
	if err != nil {
		return err
	}
	expectedLength, catalog, err := captureCatalog(ctx, serviceClient, catalogURL, accessToken, packageName)
	if err != nil {
		return err
	}
	resolver := jamf.NewClient(serviceClient, fixedTokenSource(accessToken), resolverTemplate, "uri")
	resolvedURL, err := resolver.Resolve(ctx, packageName)
	if err != nil {
		return errors.New("resolver contract could not be validated; no dependency body or URL was emitted")
	}
	resolverSummary, err := summarizeResolvedURL(resolvedURL)
	if err != nil {
		return err
	}

	report := captureReport{
		OAuth:    oauth,
		Catalog:  catalog,
		Resolver: resolverSummary,
		Object:   captureObject(ctx, resolvedURL, expectedLength, validatePublicHTTPSDestination),
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func captureOAuth(ctx context.Context, client *http.Client, tokenURL, clientID, clientSecret string) (string, oauthReport, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "client_credentials")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", oauthReport{}, errors.New("OAuth request could not be created")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return "", oauthReport{}, errors.New("OAuth endpoint could not be reached; no request URL was emitted")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		drain(response.Body)
		return "", oauthReport{}, fmt.Errorf("OAuth endpoint returned HTTP %d; response body was discarded", response.StatusCode)
	}

	var payload map[string]any
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxCaptureResponseBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", oauthReport{}, errors.New("OAuth success body was not valid JSON")
	}
	accessToken, ok := payload["access_token"].(string)
	if !ok || accessToken == "" {
		return "", oauthReport{}, errors.New("OAuth success body did not contain a string access_token")
	}
	expiresNumber, ok := payload["expires_in"].(json.Number)
	if !ok {
		return "", oauthReport{}, errors.New("OAuth success body did not contain numeric expires_in")
	}
	expiresIn, err := expiresNumber.Int64()
	if err != nil || expiresIn <= 0 {
		return "", oauthReport{}, errors.New("OAuth success body contained invalid expires_in")
	}
	tokenType, _ := payload["token_type"].(string)
	if !strings.EqualFold(tokenType, "Bearer") {
		return "", oauthReport{}, errors.New("OAuth success body did not contain Bearer token_type")
	}

	fields := make(map[string]string, len(payload))
	for name, value := range payload {
		fields[name] = jsonType(value)
	}
	return accessToken, oauthReport{
		Fields:           fields,
		TokenType:        tokenType,
		ExpiresInSeconds: expiresIn,
	}, nil
}

func captureCatalog(ctx context.Context, client *http.Client, catalogURL, accessToken, packageName string) (int64, catalogReport, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return 0, catalogReport{}, errors.New("catalog request could not be created")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return 0, catalogReport{}, errors.New("catalog endpoint could not be reached; no request URL was emitted")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		drain(response.Body)
		return 0, catalogReport{}, fmt.Errorf("catalog endpoint returned HTTP %d; response body was discarded", response.StatusCode)
	}

	var entries []catalogEntry
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxCaptureResponseBytes))
	if err := decoder.Decode(&entries); err != nil {
		return 0, catalogReport{}, errors.New("catalog success body was not the expected top-level JSON array")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return 0, catalogReport{}, errors.New("catalog success body contained trailing JSON data")
	}

	var selected *catalogEntry
	report := catalogReport{TopLevelShape: "array", FileCount: len(entries)}
	for index := range entries {
		entry := &entries[index]
		if entry.Length == nil || *entry.Length < 0 {
			return 0, catalogReport{}, errors.New("catalog contained missing or invalid length metadata")
		}
		if store.ValidateFilename(entry.FileName) == nil {
			if report.V1TotalBytes > math.MaxInt64-*entry.Length {
				return 0, catalogReport{}, errors.New("catalog package-size total exceeded the supported range")
			}
			report.V1PackageCount++
			report.V1TotalBytes += *entry.Length
			if *entry.Length > report.V1LargestBytes {
				report.V1LargestBytes = *entry.Length
			}
		}
		if entry.FileName != packageName {
			continue
		}
		if selected != nil {
			return 0, catalogReport{}, errors.New("catalog contained duplicate entries for the selected filename")
		}
		selected = entry
	}
	if selected == nil {
		return 0, catalogReport{}, errors.New("selected package was not found in the catalog")
	}
	if len(selected.SHA3) != 128 {
		return 0, catalogReport{}, errors.New("selected catalog entry did not contain a SHA3-512-length digest")
	}
	if _, err := hex.DecodeString(selected.SHA3); err != nil {
		return 0, catalogReport{}, errors.New("selected catalog entry contained a non-hexadecimal SHA3 digest")
	}

	report.ExactFilenameMatch = true
	report.LengthPresent = selected.Length != nil
	report.MD5HexCharacters = len(selected.MD5)
	report.RegionPresent = selected.Region != ""
	report.SHA3HexCharacters = len(selected.SHA3)
	return *selected.Length, report, nil
}

func summarizeResolvedURL(rawURL string) (resolverReport, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return resolverReport{}, errors.New("resolver returned an invalid URL; value was not emitted")
	}
	return resolverReport{
		URLField:            "uri",
		Scheme:              parsed.Scheme,
		HostnameFingerprint: hostnameFingerprint(parsed.Hostname()),
		PathPresent:         parsed.EscapedPath() != "",
		SignedQueryPresent:  parsed.RawQuery != "",
		QueryParameterCount: len(parsed.Query()),
	}, nil
}

func captureObject(ctx context.Context, rawURL string, expectedLength int64, validate destinationValidator) objectReport {
	return objectReport{
		Head:            probeObject(ctx, rawURL, http.MethodHead, false, expectedLength, validate),
		SingleByteRange: probeObject(ctx, rawURL, http.MethodGet, true, expectedLength, validate),
	}
}

func probeObject(ctx context.Context, rawURL, method string, rangeProbe bool, expectedLength int64, validate destinationValidator) probeReport {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return probeReport{Outcome: "url_rejected"}
	}
	if err := validate(ctx, parsed); err != nil {
		return probeReport{Outcome: "url_rejected"}
	}

	fingerprints := []string{hostnameFingerprint(parsed.Hostname())}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 20 * time.Second
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errUnsafeProbeDestination
			}
			if err := validate(req.Context(), req.URL); err != nil {
				return err
			}
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Referer")
			fingerprints = append(fingerprints, hostnameFingerprint(req.URL.Hostname()))
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), nil)
	if err != nil {
		return probeReport{Outcome: "request_failed"}
	}
	req.Header.Set("Accept", "application/octet-stream")
	if rangeProbe {
		req.Header.Set("Range", "bytes=0-0")
	}
	response, err := client.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, errUnsafeProbeDestination):
			return probeReport{Outcome: "url_rejected"}
		case errors.Is(err, context.DeadlineExceeded):
			return probeReport{Outcome: "timeout"}
		default:
			return probeReport{Outcome: "request_failed"}
		}
	}
	defer response.Body.Close()

	report := probeReport{
		Outcome:              "completed",
		StatusCode:           response.StatusCode,
		RedirectCount:        len(fingerprints) - 1,
		HostnameFingerprints: fingerprints,
		FinalHostnameChanged: fingerprints[len(fingerprints)-1] != fingerprints[0],
		ContentLengthPresent: response.ContentLength >= 0,
		ContentTypePresent:   response.Header.Get("Content-Type") != "",
		AcceptRangesBytes:    strings.EqualFold(strings.TrimSpace(response.Header.Get("Accept-Ranges")), "bytes"),
		ETagPresent:          response.Header.Get("ETag") != "",
		LastModifiedPresent:  response.Header.Get("Last-Modified") != "",
		ContentRangePresent:  response.Header.Get("Content-Range") != "",
	}
	if rangeProbe {
		rangeHonored := response.StatusCode == http.StatusPartialContent
		report.RangeHonored = &rangeHonored
	} else if response.ContentLength >= 0 {
		matches := response.ContentLength == expectedLength
		report.ContentLengthMatchesCatalog = &matches
	}
	return report
}

func validatePublicHTTPSDestination(ctx context.Context, parsed *url.URL) error {
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return errUnsafeProbeDestination
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return errUnsafeProbeDestination
	}
	if net.ParseIP(parsed.Hostname()) != nil {
		return errUnsafeProbeDestination
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return errUnsafeProbeDestination
	}
	for _, address := range addresses {
		if !isPublicAddress(address) {
			return errUnsafeProbeDestination
		}
	}
	return nil
}

func isPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() &&
		address.IsGlobalUnicast() &&
		!address.IsPrivate() &&
		!address.IsLoopback() &&
		!address.IsLinkLocalUnicast()
}

func hostnameFingerprint(hostname string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(hostname)))
	return "sha256:" + hex.EncodeToString(digest[:8])
}

func jsonType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

func requiredEnvironment(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func validateHTTPSServiceURL(name, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return fmt.Errorf("%s must be an absolute HTTPS URL", name)
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s must be an HTTPS URL without credentials or fragments", name)
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return fmt.Errorf("%s must use the default HTTPS port", name)
	}
	return nil
}

func drain(reader io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, maxCaptureResponseBytes))
}
