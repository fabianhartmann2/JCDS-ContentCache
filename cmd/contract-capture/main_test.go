package main

import (
	"encoding/json"
	"strings"
	"testing"
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
