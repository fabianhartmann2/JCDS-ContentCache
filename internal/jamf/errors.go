package jamf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

var (
	ErrNotFound           = errors.New("package not found")
	ErrUnauthorized       = errors.New("Jamf API unauthorized")
	ErrForbidden          = errors.New("Jamf API forbidden")
	ErrThrottled          = errors.New("Jamf API throttled")
	ErrTimeout            = errors.New("Jamf API timed out")
	ErrUnavailable        = errors.New("Jamf API unavailable")
	ErrInvalidResponse    = errors.New("Jamf API response invalid")
	ErrUnexpectedResponse = errors.New("Jamf API response unexpected")
)

func requestFailure(operation string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s: %w", operation, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %s", ErrTimeout, operation)
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return fmt.Errorf("%w: %s", ErrTimeout, operation)
	}
	return fmt.Errorf("%w: %s request failed", ErrUnavailable, operation)
}

func apiStatusFailure(operation string, status int) error {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		return nil
	case status == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s returned HTTP %d", ErrUnauthorized, operation, status)
	case status == http.StatusForbidden:
		return fmt.Errorf("%w: %s returned HTTP %d", ErrForbidden, operation, status)
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return fmt.Errorf("%w: %s returned HTTP %d", ErrTimeout, operation, status)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s returned HTTP %d", ErrThrottled, operation, status)
	case status >= http.StatusInternalServerError:
		return fmt.Errorf("%w: %s returned HTTP %d", ErrUnavailable, operation, status)
	default:
		return fmt.Errorf("%w: %s returned HTTP %d", ErrUnexpectedResponse, operation, status)
	}
}
