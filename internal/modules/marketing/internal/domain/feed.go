package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/platform/event"
)

// Feed is a product feed's format (ADR-0044): the same RSS 2.0 file in
// each provider's dialect.
type Feed string

// Feeds.
const (
	FeedGoogle Feed = "google"
	FeedMeta   Feed = "meta"
)

// Feeds lists every feed.
func Feeds() []Feed { return []Feed{FeedGoogle, FeedMeta} }

// Valid reports whether f is a known feed.
func (f Feed) Valid() bool { return f == FeedGoogle || f == FeedMeta }

// FeedPath is where the store's storefront serves feed f with key.
func FeedPath(f Feed, key string) string { return "/feeds/" + string(f) + ".xml?key=" + key }

// FeedKeyLen is the length of a feed key: 160 random bits in lower-case
// base32.
const FeedKeyLen = 32

// NewFeedKey returns a new random feed key.
func NewFeedKey() (string, error) {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("marketing: feed key: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.EncodeToString(b[:])), nil
}

// SameKey compares a key from a URL with the store's in constant time;
// an empty key never matches.
func SameKey(got, want string) bool {
	return want != "" && len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// EventFeedChanged is the type of FeedChanged.
const EventFeedChanged = "marketing.feed.changed"

// FeedChanged is published when a store turns its feeds on or off or
// replaces their key: the edge's copies under the old key must go.
type FeedChanged struct {
	event.Meta
}
