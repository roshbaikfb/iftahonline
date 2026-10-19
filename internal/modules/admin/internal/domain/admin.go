// Package domain holds the super-admin rules: who an admin is, how long
// their sessions live, and what a valid suspension reason is. Pure:
// standard library and internal/platform only.
package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrNotFound is returned by the store layer for missing rows.
var ErrNotFound = errors.New("admin: not found")

// Admin is a platform operator. Admins are not merchant users and are not
// store members; 2FA is mandatory (the seed is set at creation).
type Admin struct {
	ID           ulid.ID
	Email        string
	Name         string
	PasswordHash string
	TOTPSecret   []byte // sealed
	TOTPLastStep int64
	Active       bool
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}

// Admin sessions are short (ADR-0005 §8).
const (
	SessionIdleTimeout = 30 * time.Minute
	SessionMaxLifetime = 8 * time.Hour
	SessionTouchEvery  = time.Minute
)

// ExpiresAt is the sliding idle expiry, capped by the absolute lifetime.
func ExpiresAt(created, now time.Time) time.Time {
	idle := now.Add(SessionIdleTimeout)
	if limit := created.Add(SessionMaxLifetime); idle.After(limit) {
		return limit
	}
	return idle
}

// MinPasswordLen for admins is stricter than for merchants.
const MinPasswordLen = 14

// ValidatePassword checks an admin password.
func ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if !utf8.ValidString(pw) || n < MinPasswordLen || n > 128 {
		return errs.Validation(errs.FieldError{Field: "password", Code: "invalid", Message: "admin passwords must be 14 to 128 characters"})
	}
	return nil
}

// ValidateName trims and checks an admin's display name.
func ValidateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(name); n < 1 || n > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errs.Validation(errs.FieldError{Field: "name", Code: "invalid", Message: "name must be 1 to 100 characters"})
	}
	return name, nil
}

// ValidateReason checks why an admin changes a store. It is kept in the
// admin audit log, so it is required and bounded.
func ValidateReason(raw string) (string, error) {
	r := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(r); n < 3 || n > 500 || strings.ContainsFunc(r, func(c rune) bool { return unicode.IsControl(c) && c != '\n' }) {
		return "", errs.Validation(errs.FieldError{Field: "reason", Code: "invalid", Message: "give a reason of 3 to 500 characters"})
	}
	return r, nil
}
