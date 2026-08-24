package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

const maxAPIErrorMessageRunes = 256

const tenantAccessTokenInvalidCode = 99991663

// APIError is the stable, redacted error boundary for a Feishu OpenAPI call.
// It intentionally excludes credentials and raw response bodies.
type APIError struct {
	Operation  string
	Code       int
	HTTPStatus int
	Message    string

	cause error
}

func (e *APIError) Error() string {
	if e == nil {
		return "feishu OpenAPI request failed"
	}
	operation := strings.TrimSpace(e.Operation)
	if operation == "" {
		operation = "request"
	}
	message := sanitizeAPIMessage(e.Message)
	switch {
	case e.Code != 0 && e.HTTPStatus != 0 && message != "":
		return fmt.Sprintf("feishu OpenAPI %s failed: status=%d code=%d message=%s", operation, e.HTTPStatus, e.Code, message)
	case e.Code != 0 && message != "":
		return fmt.Sprintf("feishu OpenAPI %s failed: code=%d message=%s", operation, e.Code, message)
	case e.HTTPStatus != 0 && message != "":
		return fmt.Sprintf("feishu OpenAPI %s failed: status=%d message=%s", operation, e.HTTPStatus, message)
	case e.Code != 0:
		return fmt.Sprintf("feishu OpenAPI %s failed: code=%d", operation, e.Code)
	case e.HTTPStatus != 0:
		return fmt.Sprintf("feishu OpenAPI %s failed: status=%d", operation, e.HTTPStatus)
	case message != "":
		return fmt.Sprintf("feishu OpenAPI %s failed: %s", operation, message)
	default:
		return fmt.Sprintf("feishu OpenAPI %s request failed", operation)
	}
}

func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Permanent reports whether replaying the same OpenAPI operation is expected
// to fail again. Unknown transport outcomes are retryable; ordinary business
// validation and authorization failures are permanent.
func (e *APIError) Permanent() bool {
	if e == nil {
		return true
	}
	return !retryableAPIStatus(e.HTTPStatus, e.Code)
}

// IsRetryable classifies errors for the in-process delivery scheduler. Local
// validation errors are permanent; context, network, throttling, and server
// failures are retryable.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return !apiErr.Permanent()
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func retryableAPIStatus(httpStatus, code int) bool {
	switch code {
	case 99991400, 99991401, 230002, tenantAccessTokenInvalidCode:
		return true
	}
	if httpStatus == 408 || httpStatus == 429 || httpStatus >= 500 {
		return true
	}
	// A request error without an HTTP response has an unknown remote outcome.
	return httpStatus == 0 && code == 0
}

func sanitizeAPIMessage(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	value := []rune(message)
	if len(value) > maxAPIErrorMessageRunes {
		value = value[:maxAPIErrorMessageRunes]
		message = string(value) + "…"
	}
	return message
}
