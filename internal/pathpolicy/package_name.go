// Package pathpolicy defines the canonical client package namespace.
package pathpolicy

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	// Keep v1 deliberately narrow: one ASCII filename segment ending in .pkg.
	validPackageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+() -]{0,249}\.pkg$`)
	errInvalidName   = errors.New("invalid package name")
)

// PackageName extracts and validates a package name from a decoded URL path.
func PackageName(urlPath string) (string, error) {
	const prefix = "/packages/"
	if !strings.HasPrefix(urlPath, prefix) {
		return "", errInvalidName
	}
	name := strings.TrimPrefix(urlPath, prefix)
	if !ValidPackageName(name) {
		return "", errInvalidName
	}
	return name, nil
}

// ValidPackageName reports whether name is a single canonical v1 package
// filename. It rejects separators, traversal, controls, Unicode lookalikes and
// names that exceed the practical filesystem component limit.
func ValidPackageName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00\r\n\t") {
		return false
	}
	return validPackageName.MatchString(name)
}
