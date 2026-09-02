package main

import (
	"errors"
	"net/http"
	"testing"
)

func TestRejectServiceRedirect(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://redirected.example/api", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if err := rejectServiceRedirect(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("rejectServiceRedirect() error = %v, want http.ErrUseLastResponse", err)
	}
}
