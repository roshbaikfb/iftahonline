// Package errs defines transport-agnostic error kinds that domain and app
// code return. internal/platform/problem maps them to HTTP in one place
// (CLAUDE.md rule 10). This package must not import net/http so that
// domain packages can use it.
package errs

import (
	"errors"
	"strings"
	"time"
)

// Kind classifies an error for the caller.
type Kind int

// Kinds. KindInternal is the zero value: anything unclassified is internal.
const (
	KindInternal        Kind = iota
	KindBadRequest           // malformed input (bad JSON, wrong type)
	KindValidation           // well-formed input that breaks a rule; see Fields
	KindUnauthenticated      // no or invalid credentials
	KindForbidden            // authenticated but not allowed
	KindNotFound             // missing, or belongs to another tenant
	KindConflict             // state conflict (duplicate, stale version)
	KindRateLimited          // too many requests; see RetryAfter
	KindUnavailable          // dependency down or overloaded; safe to retry
)

// String returns a stable slug, used in problem type URIs.
func (k Kind) String() string {
	switch k {
	case KindBadRequest:
		return "bad-request"
	case KindValidation:
		return "validation"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindForbidden:
		return "forbidden"
	case KindNotFound:
		return "not-found"
	case KindConflict:
		return "conflict"
	case KindRateLimited:
		return "rate-limited"
	case KindUnavailable:
		return "unavailable"
	default:
		return "internal"
	}
}

// FieldError describes one invalid input field.
type FieldError struct {
	Field   string `json:"field"`   // JSON pointer-ish path, e.g. "items[2].quantity"
	Code    string `json:"code"`    // machine code, e.g. "required", "too_long"
	Message string `json:"message"` // human message, safe to show
}

// Error is a classified error. Message is shown to clients; Cause is not.
type Error struct {
	Kind       Kind
	Code       string // stable machine code, e.g. "product_not_found"
	Message    string // safe to show to the client
	Fields     []FieldError
	RetryAfter time.Duration // for KindRateLimited / KindUnavailable
	Cause      error         // internal detail; logged, never shown
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.String())
	if e.Code != "" {
		b.WriteString(" (" + e.Code + ")")
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.Cause != nil {
		b.WriteString(": " + e.Cause.Error())
	}
	return b.String()
}

// Unwrap exposes the cause to errors.Is/As.
func (e *Error) Unwrap() error { return e.Cause }

// KindOf returns the Kind of the first *Error in err's chain, or
// KindInternal.
func KindOf(err error) Kind {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Kind
	}
	return KindInternal
}

// Is reports whether err's chain contains an *Error of kind k.
func Is(err error, k Kind) bool {
	return err != nil && KindOf(err) == k
}

// NotFound reports a missing resource. Use it for other tenants'
// resources too, so their existence is never revealed.
func NotFound(code, msg string) error {
	return &Error{Kind: KindNotFound, Code: code, Message: msg}
}

// Conflict reports a state conflict.
func Conflict(code, msg string) error {
	return &Error{Kind: KindConflict, Code: code, Message: msg}
}

// BadRequest reports malformed input; cause is kept for logs.
func BadRequest(msg string, cause error) error {
	return &Error{Kind: KindBadRequest, Code: "bad_request", Message: msg, Cause: cause}
}

// Validation reports rule violations on specific fields.
func Validation(fields ...FieldError) error {
	return &Error{Kind: KindValidation, Code: "validation_failed", Message: "one or more fields are invalid", Fields: fields}
}

// Unauthenticated reports missing or invalid credentials.
func Unauthenticated(msg string) error {
	return &Error{Kind: KindUnauthenticated, Code: "unauthenticated", Message: msg}
}

// Forbidden reports an authorization failure.
func Forbidden(msg string) error {
	return &Error{Kind: KindForbidden, Code: "forbidden", Message: msg}
}

// RateLimited reports throttling.
func RateLimited(retryAfter time.Duration) error {
	return &Error{Kind: KindRateLimited, Code: "rate_limited", Message: "too many requests", RetryAfter: retryAfter}
}

// Unavailable reports a transient dependency failure; cause is kept for logs.
func Unavailable(retryAfter time.Duration, cause error) error {
	return &Error{Kind: KindUnavailable, Code: "unavailable", Message: "service temporarily unavailable", RetryAfter: retryAfter, Cause: cause}
}
