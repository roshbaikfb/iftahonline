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
	// StoreInfo is what a confirmation needs about the store: its name,
	// locale and primary host.
	StoreInfo = app.StoreInfo
	// StoreSource provides it; cmd/platform adapts tenancy.
	StoreSource = app.StoreSource
	// Contacts returns the emails of the store's owner and managers;
	// cmd/platform adapts identity.
	Contacts = app.Contacts
	// LinkView is what the SMS link's page shows.
	LinkView = app.LinkView
	// ConfirmationStatus is where an order's confirmation stands.
	ConfirmationStatus = domain.ConfirmationStatus
)

// Confirmation statuses.
const (
	ConfirmationPending   = domain.Pending
	ConfirmationConfirmed = domain.Confirmed
	ConfirmationCancelled = domain.Cancelled
	ConfirmationExpired   = domain.Expired
	ConfirmationSkipped   = domain.QuotaSkipped
	ConfirmationUnreached = domain.Unreached
	ConfirmationStale     = domain.Stale
)

// ErrConfirmationNotFound means there is no confirmation (for that order, or that
// SMS link token in the store in ctx).
var ErrConfirmationNotFound = app.ErrConfirmationNotFound

// ConfirmLinks is what the storefront's SMS link page uses (the
// storefront role). Every method acts on the store in ctx.
type ConfirmLinks interface {
	// LinkConfirmation shows the order a link's token names.
	LinkConfirmation(ctx context.Context, token string) (LinkView, error)
	// AnswerLink confirms or cancels the order (actor customer:sms);
	// once settled, it shows how.
	AnswerLink(ctx context.Context, token string, answer Answer) (LinkView, error)
	// WhatsAppLink is the thank-you page's "confirm on WhatsApp" link
	// (wa.me, the platform's number, a ready text with the order's code)
	// in locale; "" when there is none to offer.
	WhatsAppLink(ctx context.Context, orderID ulid.ID, locale string) (string, error)
}

var _ ConfirmLinks = (*app.Service)(nil)

// PlanLimitsSubscriber is the event bus subscriber name of
// Module.PlanLimitHandler.
const PlanLimitsSubscriber = "messaging.plan_limits"

// ConfirmOrdersSubscriber is the event bus subscriber name of
// Module.OrderPlacedHandler: subscribe it to orders.EventOrderPlaced on
// every role (publishers need the list); only the worker's handles.
const ConfirmOrdersSubscriber = "messaging.confirm_orders"

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
