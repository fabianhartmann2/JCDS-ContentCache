package jamf

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/auth"
)

const maxCatalogResponseBytes = 64 * 1024 * 1024

// FileMetadata is the integrity metadata returned by the JCDS file-list API.
// SHA3 is expected to contain a hexadecimal SHA3-512 digest.
type FileMetadata struct {
	FileName string `json:"fileName"`
	Length   int64  `json:"length"`
	MD5      string `json:"md5"`
	Region   string `json:"region"`
	SHA3     string `json:"sha3"`
}

type MetadataSource interface {
	Lookup(context.Context, string) (FileMetadata, error)
}

type CatalogClient struct {
	httpClient *http.Client
	tokens     auth.TokenSource
	catalogURL string
}

func NewCatalogClient(httpClient *http.Client, tokens auth.TokenSource, catalogURL string) *CatalogClient {
	return &CatalogClient{
		httpClient: httpClient,
		tokens:     tokens,
		catalogURL: catalogURL,
	}
}

func (c *CatalogClient) Lookup(ctx context.Context, filename string) (FileMetadata, error) {
	metadata, err := c.lookupOnce(ctx, filename)
	if !errors.Is(err, ErrUnauthorized) {
		return metadata, err
	}

	c.tokens.Invalidate()
	metadata, err = c.lookupOnce(ctx, filename)
	if err != nil {
		return FileMetadata{}, fmt.Errorf("retry Jamf catalog lookup: %w", err)
	}
	return metadata, nil
}

func (c *CatalogClient) lookupOnce(ctx context.Context, filename string) (FileMetadata, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return FileMetadata{}, fmt.Errorf("obtain Jamf OAuth token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.catalogURL, nil)
	if err != nil {
		return FileMetadata{}, fmt.Errorf("%w: create Jamf catalog request", ErrUnavailable)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FileMetadata{}, requestFailure("Jamf package catalog", err)
	}
	defer resp.Body.Close()

	if statusErr := apiStatusFailure("Jamf catalog", resp.StatusCode); statusErr != nil {
		drainLimited(resp.Body, maxCatalogResponseBytes)
		return FileMetadata{}, statusErr
	}

	entries, err := decodeCatalog(resp.Body)
	if err != nil {
		return FileMetadata{}, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	var match *FileMetadata
	for index := range entries {
		if entries[index].FileName != filename {
			continue
		}
		if match != nil {
			return FileMetadata{}, fmt.Errorf("%w: Jamf catalog contains duplicate entries for %q", ErrInvalidResponse, filename)
		}
		match = &entries[index]
	}
	if match == nil {
		return FileMetadata{}, ErrNotFound
	}
	if err := validateMetadata(*match); err != nil {
		return FileMetadata{}, fmt.Errorf("%w: invalid Jamf metadata for %q: %v", ErrInvalidResponse, filename, err)
	}
	return *match, nil
}

func decodeCatalog(body io.Reader) ([]FileMetadata, error) {
	limited := io.LimitReader(body, maxCatalogResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read Jamf catalog response: %w", err)
	}
	if len(payload) > maxCatalogResponseBytes {
		return nil, errors.New("Jamf catalog response exceeded the configured safety limit")
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil, errors.New("Jamf catalog response was empty")
	}

	if payload[0] == '[' {
		var entries []FileMetadata
		if err := json.Unmarshal(payload, &entries); err != nil {
			return nil, fmt.Errorf("decode Jamf catalog response: %w", err)
		}
		return entries, nil
	}

	if payload[0] == '{' {
		var envelope struct {
			TotalCount int            `json:"totalCount"`
			Results    []FileMetadata `json:"results"`
			Files      []FileMetadata `json:"files"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("decode Jamf catalog envelope: %w", err)
		}
		entries := envelope.Results
		if entries == nil {
			entries = envelope.Files
		}
		if entries == nil {
			return nil, errors.New("Jamf catalog envelope did not contain results or files")
		}
		if envelope.TotalCount > len(entries) {
			return nil, fmt.Errorf(
				"Jamf catalog response is paginated (%d of %d entries); pagination parameters must be configured before use",
				len(entries),
				envelope.TotalCount,
			)
		}
		return entries, nil
	}

	return nil, errors.New("Jamf catalog response must be a JSON array or object")
}

func validateMetadata(metadata FileMetadata) error {
	if strings.TrimSpace(metadata.FileName) == "" {
		return errors.New("fileName must not be empty")
	}
	if metadata.Length < 0 {
		return errors.New("length must not be negative")
	}
	if err := validateHexDigest("sha3", metadata.SHA3, 64); err != nil {
		return err
	}
	if metadata.MD5 != "" {
		if err := validateHexDigest("md5", metadata.MD5, 16); err != nil {
			return err
		}
	}
	return nil
}

func validateHexDigest(name, value string, byteLength int) error {
	if len(value) != byteLength*2 {
		return fmt.Errorf("%s must contain exactly %d hexadecimal characters", name, byteLength*2)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != byteLength {
		return fmt.Errorf("%s must be valid hexadecimal", name)
	}
	return nil
}

func drainLimited(reader io.Reader, limit int64) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, limit))
}
