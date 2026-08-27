package download

import "testing"

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
