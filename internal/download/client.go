package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

var ErrNotFound = errors.New("upstream object not found")

type Policy struct {
	allowedHosts map[string]struct{}
	allowHTTP    bool
}

func NewPolicy(allowedHosts []string, allowHTTP bool) Policy {
	hosts := make(map[string]struct{}, len(allowedHosts))
	for _, host := range allowedHosts {
		hosts[strings.ToLower(strings.TrimSpace(host))] = struct{}{}
	}
	return Policy{allowedHosts: hosts, allowHTTP: allowHTTP}
}

func (p Policy) Validate(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse resolved download URL: %w", err)
	}
	if !parsed.IsAbs() || parsed.Hostname() == "" {
		return nil, errors.New("resolved download URL must be absolute and contain a hostname")
	}
	if parsed.User != nil {
		return nil, errors.New("resolved download URL must not contain user information")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("resolved download URL must not contain a fragment")
	}
	if parsed.Scheme != "https" && !(p.allowHTTP && parsed.Scheme == "http") {
		return nil, errors.New("resolved download URL must use HTTPS")
	}

	hostname := strings.ToLower(parsed.Hostname())
	if _, allowed := p.allowedHosts[hostname]; !allowed {
		return nil, fmt.Errorf("resolved download hostname %q is not allowed", hostname)
	}
	if !p.allowHTTP && net.ParseIP(hostname) != nil {
		return nil, errors.New("resolved download URL must not use an IP literal")
	}
	if !p.allowHTTP && parsed.Port() != "" && parsed.Port() != "443" {
		return nil, errors.New("resolved HTTPS download URL must use port 443")
	}

	return parsed, nil
}

type Client struct {
	httpClient      *http.Client
	policy          Policy
	maxPackageBytes int64
}

func NewClient(baseClient *http.Client, policy Policy, maxPackageBytes int64) *Client {
	clientCopy := *baseClient
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many upstream redirects")
		}
		_, err := policy.Validate(req.URL.String())
		return err
	}
	return &Client{
		httpClient:      &clientCopy,
		policy:          policy,
		maxPackageBytes: maxPackageBytes,
	}
}

func (c *Client) Open(ctx context.Context, rawURL string) (*http.Response, error) {
	parsed, err := c.policy.Validate(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create upstream object request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request upstream object: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024*1024))
		resp.Body.Close()
		return nil, fmt.Errorf("upstream object returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > c.maxPackageBytes {
		resp.Body.Close()
		return nil, fmt.Errorf("upstream object exceeds the configured maximum size")
	}
	return resp, nil
}

