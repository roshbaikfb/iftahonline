// Package app holds the messaging use cases (ADR-0039): queue a message
// with its quota unit in the caller's transaction, send it (WhatsApp,
// then SMS when WhatsApp cannot reach the customer), and apply the
// statuses and button replies its channels report by webhook.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Channels are the platform's configured channels; either may be nil.
type Channels struct {
	// WhatsApp sends template messages with buttons.
	WhatsApp channel.Channel
	// SMS sends text messages: the fallback, and the only channel when
	// WhatsApp is not configured.
	SMS channel.Channel
}

func (c Channels) all() []channel.Channel {
	var out []channel.Channel
	for _, ch := range []channel.Channel{c.WhatsApp, c.SMS} {
		if ch != nil {
			out = append(out, ch)
		}
	}
	return out
}

// byProvider is the configured channel named name.
func (c Channels) byProvider(name string) (channel.Channel, bool) {
	for _, ch := range c.all() {
		if ch.Name() == name {
			return ch, true
		}
	}
	return nil, false
}

// Service implements the use cases.
type Service struct {
	db       postgres.Beginner
	ids      *ulid.Generator
	keyring  *secretbox.Keyring
	quota    entitlements.Entitlements
	jobs     jobs.Inserter
	channels Channels
	buttons  domain.ButtonKey
	inbox    webhooks.Inbox
	verify   string
	replies  ReplyHandler
	audit    audit.Recorder
	conf     Confirmations
	log      *slog.Logger
	now      func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB       postgres.Beginner
	IDs      *ulid.Generator
	Keyring  *secretbox.Keyring
	Quota    entitlements.Entitlements
	Jobs     jobs.Inserter
	Channels Channels
	// Buttons signs quick-reply payloads.
	Buttons domain.ButtonKey
	// Inbox reads accepted webhooks (the worker role; nil in the api).
	Inbox webhooks.Inbox
	// VerifyToken answers WhatsApp's webhook URL handshake.
	VerifyToken string
	// Audit records merchant actions (identity's audit log); nil
	// discards.
	Audit audit.Recorder
	// Confirmations are the order confirmation's collaborators (the
	// worker role).
	Confirmations Confirmations
	Log           *slog.Logger
	// Now is the clock (tests); nil is time.Now.
	Now func() time.Time
}

// NewService builds the service.
func NewService(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	s := &Service{db: d.DB, ids: d.IDs, keyring: d.Keyring, quota: d.Quota, jobs: d.Jobs, channels: d.Channels, buttons: d.Buttons,
		inbox: d.Inbox, verify: d.VerifyToken, audit: d.Audit, conf: d.Confirmations, log: d.Log, now: now}
	s.replies = s // the order confirmation takes customers' answers
	return s
}

// Outgoing is a message to queue.
type Outgoing struct {
	Kind    domain.Kind
	Locale  string
	OrderID *ulid.ID
	To      phone.E164
	Vars    domain.Vars
}

// Queued is what EnqueueTx did: the message's ID and status (queued, or
// skipped_quota), and the store's quota usage after it.
type Queued struct {
	ID     ulid.ID
	Status domain.Status
	Usage  entitlements.Usage
}

// errNoChannel: neither WhatsApp nor SMS is configured.
const errNoChannel = "no_channel"

func aad(store tenant.StoreID, id ulid.ID) []byte {
	return []byte("messaging:" + store.String() + ":" + id.String())
}

// EnqueueTx queues a message for the store in ctx, in the caller's
// transaction: its quota unit, its log entry and its send job commit
// together. Past the quota the message is logged as skipped_quota and
// not sent; that is not an error (the caller's state change goes on) and
// Queued.Usage says so. Without any channel it is logged as failed.
func (s *Service) EnqueueTx(ctx context.Context, tx pgx.Tx, o Outgoing) (Queued, error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return Queued{}, err
	}
	if !o.Kind.Valid() || !domain.ValidLocale(o.Locale) || o.To.IsZero() {
		return Queued{}, fmt.Errorf("%w: kind %q, locale %q", domain.ErrInvalid, o.Kind, o.Locale)
	}
	id := s.ids.New()
	m := domain.Message{ID: id, Kind: o.Kind, Locale: o.Locale, OrderID: o.OrderID, ToMasked: o.To.Masked(), CreatedAt: domain.CreatedAtOf(id)}
	if len(s.channels.all()) == 0 {
		m.Status, m.ErrorCode = domain.Failed, errNoChannel
		return Queued{ID: id, Status: m.Status}, store.Insert(ctx, tx, m)
	}
	usage, err := s.quota.ConsumeTx(ctx, tx, entitlements.Messages, 1)
	if errors.Is(err, entitlements.ErrQuotaExceeded) {
		m.Status = domain.Skipped
		return Queued{ID: id, Status: m.Status, Usage: usage}, store.Insert(ctx, tx, m)
	}
	if err != nil {
		return Queued{}, err
	}
	content, err := json.Marshal(domain.Content{To: o.To.Unmasked(), Vars: o.Vars})
	if err != nil {
		return Queued{}, fmt.Errorf("messaging: content: %w", err)
	}
	if m.Content, err = s.keyring.Seal(content, aad(storeID, id)); err != nil {
		return Queued{}, fmt.Errorf("messaging: seal: %w", err)
	}
	m.Status, m.QuotaPeriod = domain.Queued, &usage.Period
	if err = store.Insert(ctx, tx, m); err != nil {
		return Queued{}, err
	}
	if err = s.enqueueSendTx(ctx, tx, id, false); err != nil {
		return Queued{}, err
	}
	return Queued{ID: id, Status: m.Status, Usage: usage}, nil
}

// Get returns a message of the store in ctx (its content stays sealed).
func (s *Service) Get(ctx context.Context, id ulid.ID) (domain.Message, error) {
	var m domain.Message
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		m, txErr = store.Get(ctx, tx, id, false)
		return txErr
	})
	return m, err
}

// SendMessageArgs sends a queued message. Fallback is set for the SMS
// attempt after WhatsApp could not reach the customer, so it is a job of
// its own.
type SendMessageArgs struct {
	jobs.TenantArgs
	MessageID ulid.ID `json:"message_id"`
	Fallback  bool    `json:"fallback,omitempty"`
}

// Kind implements river.JobArgs.
func (SendMessageArgs) Kind() string { return "messaging.send_message" }

// sendAttempts bounds a send's tries on transient errors (River's
// backoff spreads them over about an hour); after them it fails.
const sendAttempts = 8

func (s *Service) enqueueSendTx(ctx context.Context, tx pgx.Tx, id ulid.ID, fallback bool) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	if _, err = s.jobs.InsertTx(ctx, tx, SendMessageArgs{TenantArgs: t, MessageID: id, Fallback: fallback},
		&river.InsertOpts{MaxAttempts: sendAttempts, UniqueOpts: river.UniqueOpts{ByArgs: true}}); err != nil {
		return fmt.Errorf("messaging: enqueue send: %w", err)
	}
	return nil
}
