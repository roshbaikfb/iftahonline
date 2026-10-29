package domain

import (
	"net/netip"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Attribution is the campaign that brought an order (ADR-0043): the UTM
// parameters of the shopper's visit. Every field is optional; the
// merchant sees them on the order.
type Attribution struct {
	Source, Medium, Campaign, Content, Term string
}

// IsZero reports whether no field is set.
func (a Attribution) IsZero() bool { return a == Attribution{} }

// Signals are what ad providers' server APIs match a conversion with
// (ADR-0043): their cookies and click IDs, the shopper's IP address and
// user agent. They are kept one month, and never shown.
type Signals struct {
	FBP, FBC  string // Meta's _fbp and _fbc
	TTP       string // TikTok's _ttp
	TTCLID    string // TikTok's click ID
	ScCID     string // Snapchat's click ID
	GCLID     string // Google's click ID
	IP        string
	UserAgent string
}

// IsZero reports whether no signal is set.
func (s Signals) IsZero() bool { return s == Signals{} }

// Limits on what is kept: anything longer is cut, never refused (an
// order is never lost over marketing data).
const (
	MaxUTMLen       = 100
	MaxSignalLen    = 255
	MaxUserAgentLen = 512
)

// soft is s on one line, without control characters, at most n runes; ""
// when it is not text.
func soft(s string, n int) string {
	out, err := text.Clean(s, "", 0, len(s)+1)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(text.Clip(out, n)) // a cut may end on a space
}

// token is s if it is one printable ASCII word of at most n bytes (the
// providers' IDs are); "" otherwise.
func token(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return ""
	}
	for i := range len(s) {
		if s[i] <= ' ' || s[i] > '~' {
			return ""
		}
	}
	return s
}

// Clean keeps what an order may store of an attribution.
func (a Attribution) Clean() Attribution {
	return Attribution{Source: soft(a.Source, MaxUTMLen), Medium: soft(a.Medium, MaxUTMLen), Campaign: soft(a.Campaign, MaxUTMLen),
		Content: soft(a.Content, MaxUTMLen), Term: soft(a.Term, MaxUTMLen)}
}

// Clean keeps what an order may store of its signals: the providers' IDs
// as printable tokens, a parseable IP address, the user agent cut short.
func (s Signals) Clean() Signals {
	out := Signals{FBP: token(s.FBP, MaxSignalLen), FBC: token(s.FBC, MaxSignalLen), TTP: token(s.TTP, MaxSignalLen),
		TTCLID: token(s.TTCLID, MaxSignalLen), ScCID: token(s.ScCID, MaxSignalLen), GCLID: token(s.GCLID, MaxSignalLen),
		UserAgent: soft(s.UserAgent, MaxUserAgentLen)}
	if ip, err := netip.ParseAddr(strings.TrimSpace(s.IP)); err == nil {
		out.IP = ip.Unmap().WithZone("").String() // a zone means nothing off the local link
	}
	return out
}
