// Package domain holds identity rules: what a valid email, name and
// password are, session tokens, and the role × permission matrix. Pure:
// standard library and internal/platform only (CLAUDE.md rule 3).
package domain

import (
	"errors"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrNotFound is returned by the store layer for missing rows.
var ErrNotFound = errors.New("identity: not found")

// UserStatus is an account's state.
type UserStatus string

// User statuses. Disabled users cannot sign in and lose their sessions.
const (
	UserActive   UserStatus = "active"
	UserDisabled UserStatus = "disabled"
)

// User is a person who can sign in. Users are platform-wide; their access
// to a store comes from a membership.
type User struct {
	ID              ulid.ID
	Email           string
	EmailVerifiedAt *time.Time
	Name            string
	PasswordHash    string
	Status          UserStatus
	Phone           string // verified E.164, or empty
	TOTPSecret      []byte // sealed; set during enrolment and while enabled
	TOTPEnabledAt   *time.Time
	TOTPLastStep    int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MFAEnabled reports whether sign-in needs a second factor.
func (u User) MFAEnabled() bool { return u.TOTPEnabledAt != nil }

// Password length bounds, in characters. The upper bound keeps hashing
// cost bounded.
const (
	MinPasswordLen = 10
	MaxPasswordLen = 128
)

// NormalizeEmail lower-cases and validates an address. Display names,
// comments and IP-literal domains are rejected.
func NormalizeEmail(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if len(s) < 3 || len(s) > 254 || strings.ContainsAny(s, " <>\"()[],;\\") {
		return "", false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", false
	}
	local, host, ok := strings.Cut(s, "@")
	if !ok || local == "" || len(local) > 64 || !strings.Contains(host, ".") ||
		strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", false
	}
	return s, true
}

// ValidateName trims and checks a display name.
func ValidateName(raw string) (string, *errs.FieldError) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	if n < 1 || n > 100 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return "", &errs.FieldError{Field: "name", Code: "invalid", Message: "name must be 1 to 100 characters"}
	}
	return name, nil
}

// commonPasswords are rejected outright; the list is short on purpose
// (length is the main defence) and checked case-insensitively.
func commonPasswords() []string {
	return []string{
		"1234567890", "12345678910", "0123456789", "1111111111", "0000000000",
		"password12", "password123", "password1234", "qwertyuiop", "qwerty1234",
		"iloveyou123", "abcdefghij", "1q2w3e4r5t", "asdfghjkl1", "aa12345678",
		"iftahonline", "iftahonline1", "iftahonline123",
	}
}

// ValidatePassword enforces the password policy (NIST 800-63B style:
// length over composition rules).
func ValidatePassword(password, email string) *errs.FieldError {
	n := utf8.RuneCountInString(password)
	switch {
	case !utf8.ValidString(password):
		return &errs.FieldError{Field: "password", Code: "invalid", Message: "password is not valid text"}
	case n < MinPasswordLen:
		return &errs.FieldError{Field: "password", Code: "too_short", Message: "password must be at least 10 characters"}
	case n > MaxPasswordLen:
		return &errs.FieldError{Field: "password", Code: "too_long", Message: "password must be at most 128 characters"}
	}
	lower := strings.ToLower(password)
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	first, _ := utf8.DecodeRuneInString(password)
	switch {
	case strings.Count(password, string(first)) == n:
		return &errs.FieldError{Field: "password", Code: "too_weak", Message: "password is too easy to guess"}
	case slices.Contains(commonPasswords(), lower):
		return &errs.FieldError{Field: "password", Code: "too_weak", Message: "password is too common"}
	case local != "" && strings.Contains(lower, local) && utf8.RuneCountInString(local) >= 4:
		return &errs.FieldError{Field: "password", Code: "too_weak", Message: "password must not contain your email"}
	}
	return nil
}
