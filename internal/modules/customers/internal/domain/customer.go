// Package domain holds the customers module's rules: what a customer is
// (a phone number in one store), and how names, block reasons and
// lookups are cleaned. It imports only the standard library and
// internal/platform (CLAUDE.md rule 3).
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Limits.
const (
	MaxNameLen    = 120 // runes
	MaxReasonLen  = 300 // runes
	MaxQueryLen   = 64  // runes
	DefaultLimit  = 25
	MaxListLimit  = 100
	minNameSearch = 2 // runes; a one-letter name prefix matches half the store
)

// ErrNotFound is returned by the store when no row matches.
var ErrNotFound = errors.New("customers: not found")

// Customer is one phone number's history with one store.
type Customer struct {
	ID          ulid.ID
	Phone       phone.E164
	Name        string // the latest name given with an order
	OrdersCount int
	LastOrderAt *time.Time
	BlockedAt   *time.Time // set while the customer is blocked
	BlockReason string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Blocked reports whether the store refuses this customer's orders.
func (c Customer) Blocked() bool { return c.BlockedAt != nil }

// CleanName cleans a name (platform/text) and bounds its length. An empty name is allowed (a phone
// blocked before it ever ordered).
func CleanName(s string) (string, error) {
	return text.Clean(s, "name", 0, MaxNameLen)
}

// CleanReason is CleanName for a block reason.
func CleanReason(s string) (string, error) {
	return text.Clean(s, "reason", 0, MaxReasonLen)
}

// NameKey is the name's search form, stored next to it.
func NameKey(name string) string { return arabic.NormalizeSearch(name) }

// Query selects a page of customers.
type Query struct {
	// Text is a phone number (any accepted format) or the start of a word
	// of the name.
	Text    string
	Blocked bool // only blocked customers
	After   *ulid.ID
	Limit   int
}

// Lookup is a Query resolved to what the store filters on.
type Lookup struct {
	Phone      *phone.E164
	NamePrefix string // search form, LIKE-escaped; empty for none
	Blocked    bool
	After      *ulid.ID
	Limit      int
}

// ResolveQuery validates q. Text that parses as a phone in region is a
// phone lookup; any other text of at least two letters is a name prefix.
func ResolveQuery(q Query, region phone.Region) (Lookup, error) {
	l := Lookup{Blocked: q.Blocked, After: q.After, Limit: q.Limit}
	switch {
	case l.Limit == 0:
		l.Limit = DefaultLimit
	case l.Limit < 1 || l.Limit > MaxListLimit:
		return Lookup{}, errs.Validation(errs.FieldError{Field: "limit", Code: "range", Message: fmt.Sprintf("limit must be between 1 and %d", MaxListLimit)})
	}
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" {
		return l, nil
	}
	if utf8.RuneCountInString(q.Text) > MaxQueryLen || !utf8.ValidString(q.Text) {
		return Lookup{}, errs.Validation(errs.FieldError{Field: "q", Code: "invalid", Message: fmt.Sprintf("q must be at most %d characters", MaxQueryLen)})
	}
	if p, err := phone.Parse(q.Text, region); err == nil {
		l.Phone = &p
		return l, nil
	}
	key := NameKey(q.Text)
	if utf8.RuneCountInString(key) < minNameSearch {
		return Lookup{}, errs.Validation(errs.FieldError{Field: "q", Code: "too_short", Message: "search with a phone number or at least 2 letters of a name"})
	}
	l.NamePrefix = escapeLike(key)
	return l, nil
}

// escapeLike makes s match literally in a LIKE pattern (default escape
// character: backslash).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
