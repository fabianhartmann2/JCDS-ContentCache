package download

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type staticHostnameResolver struct {
	addresses []netip.Addr
	err       error
}

func (r staticHostnameResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.addresses, r.err
}

func TestPolicyRejectsUnlistedHost(t *testing.T) {
	policy := NewPolicy([]string{"download.example"}, false)
	if _, err := policy.Validate("https://unexpected.example/object"); err == nil {
		t.Fatal("Validate() accepted an unlisted hostname")
	}
}

func TestPolicyAcceptsExactHTTPSHost(t *testing.T) {
	policy := NewPolicy([]string{"download.example"}, false)
	if _, err := policy.Validate("https://download.example/object?signature=redacted"); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPolicyDoesNotAllowSubdomainSuffixMatch(t *testing.T) {
	policy := NewPolicy([]string{"download.example"}, false)
	if _, err := policy.Validate("https://download.example.attacker.invalid/object"); err == nil {
		t.Fatal("Validate() accepted a suffix-based hostname match")
	}
}

func TestClientRejectsPrivateAndLinkLocalDNSDestinationsBeforeRequest(t *testing.T) {
	tests := []struct {
		name      string
		addresses []netip.Addr
	}{
		{name: "private IPv4", addresses: []netip.Addr{netip.MustParseAddr("10.0.0.10")}},
		{name: "loopback IPv4", addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{name: "link-local IPv4", addresses: []netip.Addr{netip.MustParseAddr("169.254.10.20")}},
		{name: "private IPv6", addresses: []netip.Addr{netip.MustParseAddr("fd00::10")}},
		{name: "mixed public and private", addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.0.0.10")}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var transportCalls atomic.Int64
			baseClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				transportCalls.Add(1)
				return nil, errors.New("must not be reached")
			})}
			policy := newPolicyWithResolver(
				[]string{"download.example"},
				false,
				staticHostnameResolver{addresses: test.addresses},
			)
			client := NewClient(baseClient, policy, 1024)
			_, err := client.Open(context.Background(), "https://download.example/object?signature=redacted")
			if !errors.Is(err, ErrPolicyViolation) {
				t.Fatalf("Open() error = %v, want ErrPolicyViolation", err)
			}
			if got := transportCalls.Load(); got != 0 {
				t.Fatalf("transport calls = %d, want 0", got)
			}
		})
	}
}

func TestClientAllowsPublicDNSDestination(t *testing.T) {
	baseClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader("package")),
			ContentLength: 7,
			Header:        make(http.Header),
		}, nil
	})}
	policy := newPolicyWithResolver(
		[]string{"download.example"},
		false,
		staticHostnameResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}},
	)
	client := NewClient(baseClient, policy, 1024)
	response, err := client.Open(context.Background(), "https://download.example/object?signature=redacted")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	response.Body.Close()
}

func TestClientRevalidatesAndFollowsAllowedRedirect(t *testing.T) {
	content := []byte("redirected package content")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("redirected object request Authorization = %q, want empty", got)
		}
		if got := r.Header.Get("Accept"); got != "application/octet-stream" {
			t.Errorf("redirected object request Accept = %q", got)
		}
		_, _ = w.Write(content)
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/package", http.StatusFound)
	}))
	defer source.Close()

	client := NewClient(http.DefaultClient, NewPolicy([]string{"127.0.0.1"}, true), 1024)
	response, err := client.Open(context.Background(), source.URL+"/temporary-object")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != string(content) {
		t.Fatalf("response body = %q, want %q", body, content)
	}
}

func TestClientRejectsRedirectToUnlistedHostBeforeRequest(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		_, _ = w.Write([]byte("must not be reached"))
	}))
	defer target.Close()
	targetURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetURL+"/package", http.StatusFound)
	}))
	defer source.Close()

	client := NewClient(http.DefaultClient, NewPolicy([]string{"127.0.0.1"}, true), 1024)
	_, err := client.Open(context.Background(), source.URL+"/temporary-object")
	if err == nil {
		t.Fatal("Open() followed a redirect to an unlisted hostname")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("Open() error = %q, want an allowlist rejection", err)
	}
	if got := targetCalls.Load(); got != 0 {
		t.Fatalf("redirect target calls = %d, want 0", got)
	}
}

func TestPolicyParseErrorDoesNotExposeSignedQuery(t *testing.T) {
	policy := NewPolicy([]string{"download.example"}, false)
	_, err := policy.Validate("https://download.example/%zz?signed-query-marker=private-query-marker")
	if !errors.Is(err, ErrPolicyViolation) {
		t.Fatalf("Validate() error = %v, want ErrPolicyViolation", err)
	}
	if strings.Contains(err.Error(), "private-query-marker") {
		t.Fatalf("Validate() error exposed the signed query: %v", err)
	}
}

func TestClientRedactsSignedURLFromTransportError(t *testing.T) {
	baseClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})}
	policy := newPolicyWithResolver(
		[]string{"download.example"},
		false,
		staticHostnameResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}},
	)
	client := NewClient(baseClient, policy, 1024)
	_, err := client.Open(
		context.Background(),
		"https://download.example/object?signed-query-marker=private-query-marker",
	)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open() error = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), "private-query-marker") {
		t.Fatalf("Open() error exposed the signed URL: %v", err)
	}
}
