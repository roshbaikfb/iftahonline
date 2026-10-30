// Package app holds the identity use cases: sign-up, login, sessions,
// store membership resolution and the audit log.
package app

import (
	"context"
	"net/netip"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// RequestMeta is what the HTTP layer knows about the caller's device.
type RequestMeta struct {
	UserAgent string
	IP        netip.Addr
	DeviceID  string // __Host-did cookie or X-Device-ID header; "" if unknown
}

type metaKey struct{}

// WithRequestMeta returns ctx carrying m.
func WithRequestMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

func metaFrom(ctx context.Context) RequestMeta {
	if m, ok := ctx.Value(metaKey{}).(RequestMeta); ok {
		return m
	}
	return RequestMeta{}
}

// Identity is the signed-in user on routes outside a store.
type Identity struct {
	UserID    ulid.ID
	SessionID ulid.ID
	// Impersonation is set on an admin's session as a store's owner
	// (ADR-0055).
	Impersonation *Impersonation
}

type identityKey struct{}

// WithIdentity returns ctx carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the signed-in user, if any.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok && !id.UserID.IsZero()
}
