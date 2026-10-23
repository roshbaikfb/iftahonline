// Package domain holds the rules of inbound webhooks (ADR-0024): what a
// verified delivery yields, and the receipt that makes a provider event
// count once.
package domain

import (
	"errors"
	"regexp"
	"time"
	"unicode"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Limits and retention.
const (
	// MaxBody is the largest body accepted; providers send small JSON.
	MaxBody = 64 << 10
	// ReceiptRetention is how long a provider event id is remembered.
	// Providers retry for hours to a few days; 90 days also covers
	// support questions about a delivery.
	ReceiptRetention = 90 * 24 * time.Hour
	// MaxEventID bounds a provider event id.
	MaxEventID = 200
)

// Errors a Provider returns from Verify. Match with errors.Is.
var (
	// ErrForged means the signature (or secret header) does not verify.
	ErrForged = errors.New("webhooks: signature does not verify")
	// ErrUnknownConnection means the store has no such connection for
	// the provider (or it is disconnected).
	ErrUnknownConnection = errors.New("webhooks: no such connection")
	// ErrNotFound is a missing receipt.
	ErrNotFound = errors.New("webhooks: not found")
)

var (
	providerRE = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)
	outcomeRE  = regexp.MustCompile(`^[a-z_]{1,64}$`)
)

// Verified is what a provider extracts from a delivery whose signature
// verifies: the provider's own id of the event, the once-only key.
type Verified struct {
	EventID string
}

// Receipt is an accepted delivery.
type Receipt struct {
	ID           ulid.ID
	Provider     string
	ConnectionID ulid.ID
	EventID      string
	ReceivedAt   time.Time
	ProcessedAt  *time.Time
	Outcome      string
}

// ReceivedAtOf is the receipt time a receipt ID encodes; the body's
// partition key.
func ReceivedAtOf(id ulid.ID) time.Time { return id.Time().UTC() }

// ValidProvider reports whether name is a provider name.
func ValidProvider(name string) bool { return providerRE.MatchString(name) }

// ValidOutcome reports whether o is an outcome: snake_case, 1-64.
func ValidOutcome(o string) bool { return outcomeRE.MatchString(o) }

// ValidEventID reports whether id can be stored as a provider event id:
// 1-200 printable characters, no spaces or controls.
func ValidEventID(id string) bool {
	if id == "" || len(id) > MaxEventID {
		return false
	}
	for _, r := range id {
		if r == unicode.ReplacementChar || !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
