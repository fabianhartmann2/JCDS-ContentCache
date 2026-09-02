package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSummarizeResolvedURLDoesNotExposeHostnamePathOrSignedValues(t *testing.T) {
	rawURL := "https://private-distribution.example/private-object?Signature=private-signature&Expires=123"
	report, err := summarizeResolvedURL(rawURL)
	if err != nil {
		t.Fatalf("summarizeResolvedURL() error = %v", err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, sensitive := range []string{"private-distribution", "private-object", "private-signature"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("sanitized report exposed %q: %s", sensitive, encoded)
		}
	}
	if report.HostnameFingerprint == "" || !report.SignedQueryPresent || report.QueryParameterCount != 2 {
		t.Fatalf("unexpected resolver report: %#v", report)
	}
}

func TestCaptureCatalogReportsOnlyAggregateSizingAndSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer synthetic-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"fileName":"Selected.pkg","length":5,"md5":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","region":"test-region","sha3":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{"fileName":"Second.pkg","length":10,"md5":"cccccccccccccccccccccccccccccccc","region":"test-region","sha3":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
			{"fileName":"Ignored.dmg","length":100,"md5":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","region":"test-region","sha3":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}
		]`))
	}))
	defer server.Close()

	expectedLength, report, err := captureCatalog(
		context.Background(),
		server.Client(),
		server.URL,
		"synthetic-token",
		"Selected.pkg",
	)
	if err != nil {
		t.Fatalf("captureCatalog() error = %v", err)
	}
	if expectedLength != 5 {
		t.Fatalf("expected length = %d, want 5", expectedLength)
	}
	if report.FileCount != 3 || report.V1PackageCount != 2 || report.V1TotalBytes != 15 || report.V1LargestBytes != 10 {
		t.Fatalf("unexpected aggregate report: %#v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, sensitive := range []string{"Selected.pkg", "Second.pkg", "test-region", "aaaaaaaa"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("catalog report exposed %q: %s", sensitive, encoded)
		}
	}
}

func TestCaptureObjectReportsRedirectHeadAndSingleByteRangeWithoutURLValues(t *testing.T) {
	content := []byte("synthetic package content")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Referer"); got != "" {
			t.Errorf("Referer = %q, want empty", got)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"synthetic-etag"`)
		w.Header().Set("Last-Modified", time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC).Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/octet-stream")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "25")
			w.WriteHeader(http.StatusOK)
			return
		}
		if got := r.Header.Get("Range"); got != "bytes=0-0" {
			t.Errorf("Range = %q, want bytes=0-0", got)
		}
		w.Header().Set("Content-Length", "1")
		w.Header().Set("Content-Range", "bytes 0-0/25")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(content[:1])
	}))
	defer target.Close()
	targetURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetURL+"/private-object?Signature=private-signature", http.StatusFound)
	}))
	defer source.Close()

	allowTestDestination := func(context.Context, *url.URL) error { return nil }
	report := captureObject(context.Background(), source.URL+"/signed-entry", int64(len(content)), allowTestDestination)
	if report.Head.Outcome != "completed" || report.Head.StatusCode != http.StatusOK || report.Head.RedirectCount != 1 {
		t.Fatalf("unexpected HEAD report: %#v", report.Head)
	}
	if report.Head.ContentLengthMatchesCatalog == nil || !*report.Head.ContentLengthMatchesCatalog {
		t.Fatalf("HEAD content length did not match catalog: %#v", report.Head)
	}
	if report.SingleByteRange.RangeHonored == nil || !*report.SingleByteRange.RangeHonored {
		t.Fatalf("range was not reported as honored: %#v", report.SingleByteRange)
	}
	if !report.SingleByteRange.ContentRangePresent || report.SingleByteRange.RedirectCount != 1 {
		t.Fatalf("unexpected range report: %#v", report.SingleByteRange)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, sensitive := range []string{"localhost", "private-object", "private-signature", "synthetic-etag"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("object report exposed %q: %s", sensitive, encoded)
		}
	}
}

func TestPublicAddressClassification(t *testing.T) {
	tests := []struct {
		address string
		public  bool
	}{
		{address: "1.1.1.1", public: true},
		{address: "2606:4700:4700::1111", public: true},
		{address: "10.0.0.1", public: false},
		{address: "127.0.0.1", public: false},
		{address: "169.254.1.1", public: false},
		{address: "fd00::1", public: false},
		{address: "fe80::1", public: false},
	}
	for _, test := range tests {
		if got := isPublicAddress(netip.MustParseAddr(test.address)); got != test.public {
			t.Fatalf("isPublicAddress(%s) = %v, want %v", test.address, got, test.public)
		}
	}
}

func TestValidateHTTPSServiceURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{name: "standard HTTPS", rawURL: "https://tenant.example/api/v1/jcds/files"},
		{name: "explicit default port", rawURL: "https://tenant.example:443/api/v1/jcds/files"},
		{name: "plain HTTP", rawURL: "http://tenant.example/api/v1/jcds/files", wantErr: true},
		{name: "embedded credentials", rawURL: "https://user:secret@tenant.example/api/v1/jcds/files", wantErr: true},
		{name: "non-default port", rawURL: "https://tenant.example:8443/api/v1/jcds/files", wantErr: true},
		{name: "fragment", rawURL: "https://tenant.example/api/v1/jcds/files#private", wantErr: true},
		{name: "relative", rawURL: "/api/v1/jcds/files", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateHTTPSServiceURL("SYNTHETIC_URL", test.rawURL)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateHTTPSServiceURL() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestJSONTypeReportsSchemaWithoutValues(t *testing.T) {
	tests := map[string]any{
		"string":  "private-value",
		"number":  json.Number("59"),
		"boolean": true,
		"array":   []any{"private-value"},
		"object":  map[string]any{"private": "value"},
		"null":    nil,
	}
	for want, value := range tests {
		if got := jsonType(value); got != want {
			t.Fatalf("jsonType(%T) = %q, want %q", value, got, want)
		}
	}
}
