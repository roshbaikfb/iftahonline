package domain

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrInvalidHost means a Host header or domain name is not a usable DNS
// name (IP literals included).
var ErrInvalidHost = errors.New("tenancy: invalid host")

// NormalizeHost turns a Host header into the lookup key: lower-case ASCII
// (IDNA/punycode), no port, no trailing dot. IP literals are rejected; a
// store is only ever reached by name.
//
// It is on every storefront request, so the common case (an ASCII host
// that is already lower-case) does not allocate.
func NormalizeHost(raw string) (string, error) {
	h := raw
	if strings.HasPrefix(h, "[") {
		return "", ErrInvalidHost // IPv6 literal
	}
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		if port := h[i+1:]; port == "" || len(port) > 5 || !allDigits(port) {
			return "", ErrInvalidHost
		}
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 253 {
		return "", ErrInvalidHost
	}

	ascii, upper := true, false
	for i := 0; i < len(h); i++ {
		switch c := h[i]; {
		case c >= 0x80:
			ascii = false
		case c >= 'A' && c <= 'Z':
			upper = true
		}
	}
	switch {
	case !ascii:
		a, err := idna.Lookup.ToASCII(h)
		if err != nil {
			return "", ErrInvalidHost
		}
		h = a
	case upper:
		h = strings.ToLower(h)
	}
	if !validLabels(h) {
		return "", ErrInvalidHost
	}
	return h, nil
}

// validLabels checks LDH labels of 1..63 bytes, at most 253 in total, and
// a last label that is not all digits (so dotted-decimal IPv4 and its odd
// variants never pass as names).
func validLabels(h string) bool {
	if len(h) > 253 {
		return false
	}
	lastDigits := true
	start := 0
	for i := 0; i <= len(h); i++ {
		if i < len(h) && h[i] != '.' {
			continue
		}
		label := h[start:i]
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		lastDigits = true
		for j := 0; j < len(label); j++ {
			c := label[j]
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c == '-':
				lastDigits = false
			default:
				return false
			}
		}
		start = i + 1
	}
	// IPv6 literals never get here (':' is not an LDH character).
	return !lastDigits
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Slugs become <slug>.<base domain>, so they follow DNS label rules, and a
// few are reserved for the platform itself.
func reservedSlugs() []string {
	return []string{
		"www", "api", "admin", "app", "dashboard", "panel", "merchant", "merchants",
		"store", "stores", "shop", "shops", "mail", "email", "smtp", "imap", "pop",
		"ftp", "ns1", "ns2", "dns", "mx", "cdn", "static", "assets", "img", "images",
		"media", "files", "upload", "uploads", "help", "support", "docs", "status",
		"blog", "news", "billing", "pay", "payments", "checkout", "account", "accounts",
		"login", "signup", "auth", "oauth", "sso", "id", "edge", "origin", "internal",
		"test", "staging", "dev", "demo", "iftah", "iftahonline",
	}
}

func checkSlug(s string) (errs.FieldError, bool) {
	if len(s) < 3 || len(s) > 63 || strings.Contains(s, ".") || !validLabels(s+".x") || strings.HasPrefix(s, "xn--") || strings.Contains(s, "--") {
		return errs.FieldError{Field: "slug", Code: "invalid",
			Message: "slug must be 3 to 63 lower-case letters, digits or single hyphens, not starting or ending with a hyphen"}, false
	}
	if slices.Contains(reservedSlugs(), s) {
		return errs.FieldError{Field: "slug", Code: "reserved", Message: "this slug is reserved"}, false
	}
	return errs.FieldError{}, true
}

// CheckSlug normalizes a slug as a new store's is, and says why it
// cannot be used: "invalid", "reserved", or "" when it may be (if no
// store has it).
func CheckSlug(raw string) (slug, reason string) {
	slug = strings.ToLower(strings.TrimSpace(raw))
	if fe, ok := checkSlug(slug); !ok {
		return slug, fe.Code
	}
	return slug, ""
}

// SlugAvailability is the sign-up form's answer for an address.
type SlugAvailability struct {
	Slug      string
	Available bool
	Reason    string // invalid, reserved, taken
}

// SubdomainHost is the platform host for a slug.
func SubdomainHost(slug, baseDomain string) string { return slug + "." + baseDomain }

// HostKind tells platform subdomains from merchant-owned domains.
type HostKind string

// Domain kinds.
const (
	KindSubdomain HostKind = "subdomain"
	KindCustom    HostKind = "custom"
)

// HostStatus is a domain's verification state. Only verified domains
// route traffic.
type HostStatus string

// Domain statuses.
const (
	DomainPending  HostStatus = "pending"
	DomainVerified HostStatus = "verified"
	DomainFailed   HostStatus = "failed"
)

// Verification failure reasons, shown to the merchant.
const (
	ReasonTXTNotFound = "txt_record_not_found"
	ReasonDNSError    = "dns_error"
	ReasonTaken       = "domain_taken"
	ReasonTimedOut    = "verification_timed_out"
)

// MaxCustomDomains bounds custom domains per store.
const MaxCustomDomains = 5

// Domain is a host that routes to a store.
type Domain struct {
	ID                ulid.ID
	StoreID           tenant.StoreID
	Host              string
	Kind              HostKind
	Status            HostStatus
	IsPrimary         bool
	VerificationToken string
	FailureReason     string
	CheckAttempts     int
	VerifiedAt        *time.Time
	LastCheckedAt     *time.Time
	CreatedAt         time.Time
}

// VerificationRecordPrefix is prepended to a custom domain for its TXT
// ownership record.
const VerificationRecordPrefix = "_iftah-verify."

// VerificationRecord is the TXT record the merchant must publish.
func (d Domain) VerificationRecord() (name, value string) {
	return VerificationRecordPrefix + d.Host, "iftah-verify=" + d.VerificationToken
}

// TXTProvesOwnership reports whether one of the TXT values at the
// verification record name carries this domain's token.
func (d Domain) TXTProvesOwnership(values []string) bool {
	_, want := d.VerificationRecord()
	for _, v := range values {
		if strings.TrimSpace(v) == want {
			return true
		}
	}
	return false
}

// ValidateCustomDomain normalizes a merchant-supplied domain and rejects
// names under the platform's base domain (those are ours to assign) and
// single-label names.
func ValidateCustomDomain(raw, baseDomain string) (string, error) {
	invalid := errs.Validation(errs.FieldError{Field: "host", Code: "invalid", Message: "enter a domain name such as shop.example.com"})
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, ":/[]@ ") {
		return "", invalid // no ports, schemes, paths or user info
	}
	h, err := NormalizeHost(raw)
	if err != nil || !strings.Contains(h, ".") {
		return "", invalid
	}
	if h == baseDomain || strings.HasSuffix(h, "."+baseDomain) {
		return "", errs.Validation(errs.FieldError{Field: "host", Code: "reserved", Message: "domains under the platform domain cannot be added"})
	}
	return h, nil
}

// NewVerificationToken returns a random, DNS-safe token.
func NewVerificationToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("tenancy: token: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

// Resolution is what a host lookup finds: the store and its status.
type Resolution struct {
	StoreID tenant.StoreID
	Status  StoreStatus
}
