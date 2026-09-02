package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

var ErrNotFound = errors.New("upstream object not found")

var (
	ErrPolicyViolation = errors.New("upstream object URL rejected")
	ErrTimeout         = errors.New("upstream object request timed out")
	ErrUnavailable     = errors.New("upstream object unavailable")
)

type Policy struct {
	allowedHosts map[string]struct{}
	allowHTTP    bool
	resolver     hostnameResolver
}

type hostnameResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

func NewPolicy(allowedHosts []string, allowHTTP bool) Policy {
	hosts := make(map[string]struct{}, len(allowedHosts))
	for _, host := range allowedHosts {
		hosts[strings.ToLower(strings.TrimSpace(host))] = struct{}{}
	}
	return Policy{allowedHosts: hosts, allowHTTP: allowHTTP, resolver: net.DefaultResolver}
}

func newPolicyWithResolver(allowedHosts []string, allowHTTP bool, resolver hostnameResolver) Policy {
	policy := NewPolicy(allowedHosts, allowHTTP)
	policy.resolver = resolver
	return policy
}

func (p Policy) Validate(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, policyViolation("URL could not be parsed")
	}
	if !parsed.IsAbs() || parsed.Hostname() == "" {
		return nil, policyViolation("URL must be absolute and contain a hostname")
	}
	if parsed.User != nil {
		return nil, policyViolation("URL must not contain user information")
	}
	if parsed.Fragment != "" {
		return nil, policyViolation("URL must not contain a fragment")
	}
	if parsed.Scheme != "https" && !(p.allowHTTP && parsed.Scheme == "http") {
		return nil, policyViolation("URL must use HTTPS")
	}

	hostname := strings.ToLower(parsed.Hostname())
	if _, allowed := p.allowedHosts[hostname]; !allowed {
		return nil, policyViolation("hostname is not allowed")
	}
	if !p.allowHTTP && net.ParseIP(hostname) != nil {
		return nil, policyViolation("URL must not use an IP literal")
	}
	if !p.allowHTTP && parsed.Port() != "" && parsed.Port() != "443" {
		return nil, policyViolation("HTTPS URL must use port 443")
	}

	return parsed, nil
}

func (p Policy) validateResolvedDestination(ctx context.Context, parsed *url.URL) error {
	// Explicit HTTP support exists only for the credential-free local mock
	// stack, where loopback and container-private addresses are expected.
	if p.allowHTTP {
		return nil
	}
	addresses, err := p.resolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("%w: destination hostname could not be resolved", ErrUnavailable)
	}
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
			return policyViolation("hostname resolves to a non-public address")
		}
	}
	return nil
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
			return policyViolation("too many redirects")
		}
		parsed, err := policy.Validate(req.URL.String())
		if err != nil {
			return err
		}
		return policy.validateResolvedDestination(req.Context(), parsed)
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
	if err := c.policy.validateResolvedDestination(ctx, parsed); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, policyViolation("request could not be created")
	}
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		var networkError net.Error
		switch {
		case errors.Is(err, ErrPolicyViolation):
			return nil, policyViolation("redirect target is not allowed")
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("upstream object request: %w", context.Canceled)
		case errors.Is(err, context.DeadlineExceeded):
			return nil, ErrTimeout
		case errors.As(err, &networkError) && networkError.Timeout():
			return nil, ErrTimeout
		default:
			return nil, ErrUnavailable
		}
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024*1024))
		resp.Body.Close()
		return nil, ErrTimeout
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024*1024))
		resp.Body.Close()
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
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

func policyViolation(reason string) error {
	return fmt.Errorf("%w: %s", ErrPolicyViolation, reason)
}
