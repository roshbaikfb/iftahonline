package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Ref names a message in what we hand a channel, so the channel's
// webhooks find it, and its store, again. It is all a webhook needs to be
// placed: the platform's number serves every store (ADR-0038).
type Ref struct {
	Store   tenant.StoreID
	Message ulid.ID
}

const refPrefix = "m1."

// String is the reference as sent: "m1.<store>.<message>" (56
// characters), WhatsApp's biz_opaque_callback_data and Twilio's callback
// query.
func (r Ref) String() string { return refPrefix + r.Store.String() + "." + r.Message.String() }

// ParseRef reads a reference.
func ParseRef(s string) (Ref, bool) {
	rest, ok := strings.CutPrefix(s, refPrefix)
	if !ok {
		return Ref{}, false
	}
	storeS, msgS, ok := strings.Cut(rest, ".")
	if !ok {
		return Ref{}, false
	}
	store, err := tenant.ParseStoreID(storeS)
	if err != nil || store.IsZero() {
		return Ref{}, false
	}
	msg, err := ulid.Parse(msgS)
	if err != nil || msg.IsZero() {
		return Ref{}, false
	}
	return Ref{Store: store, Message: msg}, true
}

// ButtonKey signs button payloads (derived from a platform secret).
type ButtonKey []byte

const buttonPrefix = "b1."

// buttonMAC is the payload's signature: HMAC-SHA256 truncated to 128
// bits, base64url.
func (k ButtonKey) mac(body string) string {
	m := hmac.New(sha256.New, k)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

// Payload is a quick-reply button's payload:
// "b1.<store>.<message>.<c|x>.<mac>" (83 characters). WhatsApp returns it
// when the customer taps the button. Meta signs the webhook; ours makes
// a payload we did not issue useless even so.
func (k ButtonKey) Payload(r Ref, reply Reply) string {
	code := "c"
	if reply == Cancel {
		code = "x"
	}
	body := buttonPrefix + r.Store.String() + "." + r.Message.String() + "." + code
	return body + "." + k.mac(body)
}

// ParsePayload reads and checks a button payload.
func (k ButtonKey) ParsePayload(s string) (Ref, Reply, bool) {
	cut := strings.LastIndexByte(s, '.')
	if cut < 0 || !strings.HasPrefix(s, buttonPrefix) {
		return Ref{}, NoReply, false
	}
	body, sig := s[:cut], s[cut+1:]
	if !hmac.Equal([]byte(sig), []byte(k.mac(body))) {
		return Ref{}, NoReply, false
	}
	rest := strings.TrimPrefix(body, buttonPrefix)
	refPart, code, ok := cutLast(rest, '.')
	if !ok {
		return Ref{}, NoReply, false
	}
	r, ok := ParseRef(refPrefix + refPart)
	if !ok {
		return Ref{}, NoReply, false
	}
	switch code {
	case "c":
		return r, Confirm, true
	case "x":
		return r, Cancel, true
	}
	return Ref{}, NoReply, false
}

func cutLast(s string, sep byte) (before, after string, ok bool) {
	i := strings.LastIndexByte(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}
