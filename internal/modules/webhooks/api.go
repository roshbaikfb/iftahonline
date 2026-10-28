// Package webhooks receives inbound webhooks from carriers, payment
// gateways and messaging channels (ADR-0024, ADR-0038): it verifies each
// delivery through its provider, stores the raw body, and lets each
// provider event through once, to a processing job of the provider's
// module. This file is the module's
// only exported surface (ADR-0001); see docs/modules/webhooks.md.
package webhooks

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs and the provider contract.
type (
	// Provider is what a carrier's or gateway's module implements and
	// registers: verify a delivery, then enqueue its processing job.
	Provider = app.Provider
	// Request is a delivery as received.
	Request = app.Request
	// Verified is what Verify extracts: the provider's event id.
	Verified = domain.Verified
	// Receipt is an accepted delivery.
	Receipt = domain.Receipt
	// Limits are the request budgets per client IP and per connection.
	Limits = app.Limits
	// Challenger is a Provider that also answers a GET URL handshake
	// (Meta's hub.challenge) for its connections.
	Challenger = app.Challenger
	// PlatformProvider is a provider's side for an account the platform
	// owns and shares between stores (one WhatsApp number, ADR-0038).
	PlatformProvider = app.PlatformProvider
	// Part is one store's event out of a platform-owned account's
	// delivery.
	Part = domain.Part
)

// MaxBody is the largest delivery accepted.
const MaxBody = domain.MaxBody

// Errors a Provider's Verify returns. Match with errors.Is.
var (
	ErrForged            = domain.ErrForged
	ErrUnknownConnection = domain.ErrUnknownConnection
	// ErrUnknownStore: a platform delivery's part names no store; the
	// part is dropped.
	ErrUnknownStore = domain.ErrUnknownStore
	// ErrReceiptNotFound is Get's error for a missing receipt.
	ErrReceiptNotFound = app.ErrReceiptAbsent
)

// Inbox is what processing jobs use. Every method acts on the store in
// ctx.
type Inbox interface {
	// Get returns an accepted receipt and its raw body (nil once the body
	// expired, after about 90 days).
	Get(ctx context.Context, id ulid.ID) (r Receipt, contentType string, body []byte, err error)
	// MarkProcessedTx records what processing did (a snake_case outcome
	// such as "paid" or "stale"), in the transaction that did it. Only
	// the first outcome is kept; it reports whether this call recorded it.
	MarkProcessedTx(ctx context.Context, tx pgx.Tx, id ulid.ID, outcome string) (bool, error)
}

var _ Inbox = (*app.Service)(nil)

// URL is where a provider sends a store connection's webhooks (the
// receiveWebhook route), under the platform's public origin.
func URL(publicURL, provider string, store tenant.StoreID, connection ulid.ID) string {
	return strings.TrimRight(publicURL, "/") + "/v1/webhooks/" + provider + "/" + store.String() + "/" + connection.String()
}

// PlatformURL is where a provider sends the webhooks of its
// platform-owned account (the receivePlatformWebhook route).
func PlatformURL(publicURL, provider string) string {
	return strings.TrimRight(publicURL, "/") + "/v1/webhooks/" + provider
}
