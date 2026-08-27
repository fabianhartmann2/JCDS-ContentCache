// Command contract-capture validates the live Jamf success contracts while
// emitting only a deliberately minimized, sanitized schema report.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/jamf"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

const maxCaptureResponseBytes = 32 * 1024 * 1024

type captureReport struct {
	OAuth    oauthReport    `json:"oauth"`
	Catalog  catalogReport  `json:"catalog"`
	Resolver resolverReport `json:"resolver"`
}

type oauthReport struct {
	Fields           map[string]string `json:"fields"`
	TokenType        string            `json:"token_type"`
	ExpiresInSeconds int64             `json:"expires_in_seconds"`
}

type catalogReport struct {
	ExactFilenameMatch bool `json:"exact_filename_match"`
	LengthPresent      bool `json:"length_present"`
	MD5HexCharacters   int  `json:"md5_hex_characters"`
	RegionPresent      bool `json:"region_present"`
	SHA3HexCharacters  int  `json:"sha3_hex_characters"`
}

type resolverReport struct {
	URLField            string `json:"url_field"`
	Scheme              string `json:"scheme"`
	HostnameFingerprint string `json:"hostname_fingerprint"`
	PathPresent         bool   `json:"path_present"`
	SignedQueryPresent  bool   `json:"signed_query_present"`
	QueryParameterCount int    `json:"query_parameter_count"`
}

type fixedTokenSource string

func (s fixedTokenSource) Token(context.Context) (string, error) {
	return string(s), nil
}

func (fixedTokenSource) Invalidate() {}

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

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	accessToken, oauth, err := captureOAuth(ctx, client, tokenURL, clientID, clientSecret)
	if err != nil {
		return err
	}
	tokens := fixedTokenSource(accessToken)
	metadataClient := jamf.NewCatalogClient(client, tokens, catalogURL)
	metadata, err := metadataClient.Lookup(ctx, packageName)
	if err != nil {
		return errors.New("catalog contract could not be validated; no dependency body or URL was emitted")
	}
	resolver := jamf.NewClient(client, tokens, resolverTemplate, "uri")
	resolvedURL, err := resolver.Resolve(ctx, packageName)
	if err != nil {
		return errors.New("resolver contract could not be validated; no dependency body or URL was emitted")
	}
	resolverSummary, err := summarizeResolvedURL(resolvedURL)
	if err != nil {
		return err
	}

	report := captureReport{
		OAuth: oauth,
		Catalog: catalogReport{
			ExactFilenameMatch: metadata.FileName == packageName,
			LengthPresent:      metadata.Length >= 0,
			MD5HexCharacters:   len(metadata.MD5),
			RegionPresent:      metadata.Region != "",
			SHA3HexCharacters:  len(metadata.SHA3),
		},
		Resolver: resolverSummary,
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
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxCaptureResponseBytes))
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

func summarizeResolvedURL(rawURL string) (resolverReport, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return resolverReport{}, errors.New("resolver returned an invalid URL; value was not emitted")
	}
	hostDigest := sha256.Sum256([]byte(strings.ToLower(parsed.Hostname())))
	return resolverReport{
		URLField:            "uri",
		Scheme:              parsed.Scheme,
		HostnameFingerprint: "sha256:" + hex.EncodeToString(hostDigest[:8]),
		PathPresent:         parsed.EscapedPath() != "",
		SignedQueryPresent:  parsed.RawQuery != "",
		QueryParameterCount: len(parsed.Query()),
	}, nil
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
