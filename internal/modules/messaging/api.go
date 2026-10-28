// Package messaging sends customer messages on each store's behalf over
// WhatsApp, with SMS as the fallback (ADR-0038, ADR-0039): every message
// costs a quota unit (entitlements), is logged with its delivery status,
// and goes out from the platform's own WhatsApp number and SMS account.
// This file is the module's only exported surface (ADR-0001); see
// docs/modules/messaging.md.
package messaging

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// Kind is what a message is for.
	Kind = domain.Kind
	// Vars are the values a message's texts fill in.
	Vars = domain.Vars
	// Outgoing is a message to queue.
	Outgoing = app.Outgoing
	// Queued is what EnqueueTx did, with the store's quota usage after it.
	Queued = app.Queued
	// Status is where a message is.
	Status = domain.Status
	// Message is a message as the log keeps it.
	Message = domain.Message
	// Reply is a customer's answer by button.
	Reply = app.Reply
	// Answer is the button a customer tapped.
	Answer = domain.Reply
	// ReplyHandler acts on a customer's first answer, in the transaction
	// that records it.
	ReplyHandler = app.ReplyHandler
)

// Kinds.
const (
	OrderConfirmation = domain.OrderConfirmation
)

// Statuses.
const (
	StatusQueued    = domain.Queued
	StatusSent      = domain.Sent
	StatusDelivered = domain.Delivered
	StatusRead      = domain.Read
	StatusFailed    = domain.Failed
	StatusSkipped   = domain.Skipped
)

// Answers.
const (
	Confirm = domain.Confirm
	Cancel  = domain.Cancel
)

// Locales.
const (
	Arabic  = domain.Arabic
	English = domain.English
)

// Errors. Match with errors.Is.
var (
	ErrNotFound = domain.ErrNotFound
	// ErrInvalid: an unknown kind or locale, or no recipient.
	ErrInvalid = domain.ErrInvalid
)

// Sender is what modules that message customers use. Every method acts
// on the store in ctx.
type Sender interface {
	// EnqueueTx queues a message in the caller's tenant transaction: its
	// quota unit, log entry and send job commit with the caller's state
	// change. Past the quota it is logged as skipped (Queued.Status) and
	// not sent; that is no error. Warn the merchant when
	// Queued.Usage.CrossedBy(1) is a threshold, or the status is skipped.
	EnqueueTx(ctx context.Context, tx pgx.Tx, o Outgoing) (Queued, error)
	// Get returns a logged message.
	Get(ctx context.Context, id ulid.ID) (Message, error)
}

var _ Sender = (*app.Service)(nil)
