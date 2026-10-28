package app

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// PlatformConnection is the connection ID the platform's own accounts'
// receipts carry: there is no store connection behind them (ADR-0038).
func PlatformConnection() ulid.ID { return ulid.ID{15: 1} }

// Reply is a customer's answer, for the module that sent the message
// (6c: the order confirmation).
type Reply struct {
	MessageID ulid.ID
	Kind      domain.Kind
	OrderID   *ulid.ID
	Answer    domain.Reply
	At        time.Time
}

// ReplyHandler acts on a customer's first answer to a message, in the
// transaction that records it.
type ReplyHandler interface {
	HandleReplyTx(ctx context.Context, tx pgx.Tx, r Reply) error
}

// SetReplyHandler supplies the handler (at startup, before jobs run).
func (s *Service) SetReplyHandler(h ReplyHandler) { s.replies = h }

// PlatformProvider is a channel's side of the webhooks inbox: one URL for
// the platform's account, every store's events.
type PlatformProvider struct {
	svc *Service
	ch  channel.Channel
}

var _ webhooks.PlatformProvider = PlatformProvider{}

// PlatformProviders returns one provider per configured channel.
func (s *Service) PlatformProviders() []webhooks.PlatformProvider {
	var out []webhooks.PlatformProvider
	for _, ch := range s.channels.all() {
		out = append(out, PlatformProvider{svc: s, ch: ch})
	}
	return out
}

// Name implements webhooks.PlatformProvider.
func (p PlatformProvider) Name() string { return p.ch.Name() }

// VerifyPlatform implements webhooks.PlatformProvider: the channel checks
// the signature, and each event is placed in its store by our reference
// (a status's) or our signed button payload (a reply's). Customers'
// texts are not placed yet (6c); events that cannot be placed are left
// out and counted in the log.
func (p PlatformProvider) VerifyPlatform(ctx context.Context, req webhooks.Request) ([]webhooks.Part, error) {
	events, err := p.ch.VerifyWebhook(channel.Webhook{Header: req.Header, Query: req.Query, Body: req.Body})
	switch {
	case errors.Is(err, channel.ErrForged):
		return nil, webhooks.ErrForged
	case errors.Is(err, channel.ErrBadWebhook):
		p.svc.log.WarnContext(ctx, "messaging: unreadable webhook dropped", "channel", p.ch.Name(), "err", err)
		return nil, nil
	case err != nil:
		return nil, err
	}
	parts := make([]webhooks.Part, 0, len(events))
	unplaced := 0
	for _, e := range events {
		var ref domain.Ref
		ok := false
		switch e.Kind {
		case channel.StatusEvent:
			ref, ok = domain.ParseRef(e.Ref)
		case channel.ButtonEvent:
			ref, _, ok = p.svc.buttons.ParsePayload(e.Payload)
		}
		if !ok {
			unplaced++
			continue
		}
		e.From, e.Text = "", "" // only texts carry them, and texts are not placed
		body, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		parts = append(parts, webhooks.Part{Store: ref.Store, ConnectionID: PlatformConnection(), EventID: e.ID, Body: body})
	}
	if unplaced > 0 {
		p.svc.log.InfoContext(ctx, "messaging: webhook events not placed", "channel", p.ch.Name(), "count", unplaced)
	}
	return parts, nil
}

// ChallengePlatform implements webhooks.PlatformProvider: Meta's URL
// handshake against WHATSAPP_VERIFY_TOKEN. Other channels have none.
func (p PlatformProvider) ChallengePlatform(_ context.Context, query url.Values) (string, error) {
	if p.ch != p.svc.channels.WhatsApp || p.svc.verify == "" || query.Get("hub.mode") != "subscribe" ||
		!hmac.Equal([]byte(query.Get("hub.verify_token")), []byte(p.svc.verify)) {
		return "", webhooks.ErrForged
	}
	return query.Get("hub.challenge"), nil
}

// ProcessWebhookArgs processes an accepted channel event.
type ProcessWebhookArgs struct {
	jobs.TenantArgs
	ReceiptID ulid.ID `json:"receipt_id"`
	Provider  string  `json:"provider"`
}

// Kind implements river.JobArgs.
func (ProcessWebhookArgs) Kind() string { return "messaging.process_webhook" }

// AcceptTx implements webhooks.PlatformProvider.
func (p PlatformProvider) AcceptTx(ctx context.Context, tx pgx.Tx, r webhooks.Receipt) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	_, err = p.svc.jobs.InsertTx(ctx, tx, ProcessWebhookArgs{TenantArgs: t, ReceiptID: r.ID, Provider: p.ch.Name()}, nil)
	return err
}

// errEarly: a status arrived before the send job recorded the message
// as sent; the job waits and tries again.
var errEarly = errors.New("messaging: status arrived before the send was recorded")

// IsEarly reports whether a webhook job should wait for its message's
// send to be recorded.
func IsEarly(err error) bool { return errors.Is(err, errEarly) }

// ProcessWebhook runs ProcessWebhookArgs. Statuses move the message
// forward only; a status of an earlier attempt (before an SMS fallback)
// is stale; a channel that can be asked (Twilio) is asked, the webhook
// being a hint (ADR-0024). WhatsApp cannot be asked: its signature and,
// for replies, ours are the evidence (ADR-0039).
func (s *Service) ProcessWebhook(ctx context.Context, receiptID ulid.ID, provider string) error {
	_, _, body, err := s.inbox.Get(ctx, receiptID)
	if errors.Is(err, webhooks.ErrReceiptNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var e channel.Event
	if body == nil || json.Unmarshal(body, &e) != nil {
		return s.mark(ctx, receiptID, "unreadable", nil)
	}
	switch e.Kind {
	case channel.StatusEvent:
		return s.applyStatus(ctx, receiptID, provider, e)
	case channel.ButtonEvent:
		return s.applyReply(ctx, receiptID, e)
	}
	return s.mark(ctx, receiptID, "ignored", nil)
}

// mark records a receipt's outcome, after change (in the same
// transaction) when there is one.
func (s *Service) mark(ctx context.Context, receiptID ulid.ID, outcome string, change func(tx pgx.Tx) (string, error)) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if change != nil {
			o, err := change(tx)
			if err != nil {
				return err
			}
			outcome = o
		}
		_, err := s.inbox.MarkProcessedTx(ctx, tx, receiptID, outcome)
		return err
	})
}

func toStatus(st channel.Status) domain.Status {
	switch st {
	case channel.StatusSent:
		return domain.Sent
	case channel.StatusDelivered:
		return domain.Delivered
	case channel.StatusRead:
		return domain.Read
	case channel.StatusFailed:
		return domain.Failed
	}
	return ""
}

func (s *Service) applyStatus(ctx context.Context, receiptID ulid.ID, provider string, e channel.Event) error {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	ref, ok := domain.ParseRef(e.Ref)
	if !ok || ref.Store != storeID {
		return s.mark(ctx, receiptID, "unknown_message", nil)
	}
	m, err := s.Get(ctx, ref.Message)
	if errors.Is(err, domain.ErrNotFound) {
		return s.mark(ctx, receiptID, "unknown_message", nil)
	}
	if err != nil {
		return err
	}
	if m.Status == domain.Queued && m.Provider == "" && !m.Fallback {
		return errEarly
	}
	ch, ok := s.channels.byProvider(provider)
	if !ok {
		return s.mark(ctx, receiptID, "unknown_channel", nil)
	}
	st, code := e.Status, e.ErrorCode
	if f, ok := ch.(channel.Fetcher); ok && m.Provider == provider && m.ProviderID == e.ProviderID {
		if st, code, err = f.Fetch(ctx, e.ProviderID); err != nil {
			return err
		}
	}
	return s.mark(ctx, receiptID, "", func(tx pgx.Tx) (string, error) {
		cur, err := store.Get(ctx, tx, m.ID, true)
		if err != nil {
			return "", err
		}
		if cur.Provider != provider || cur.ProviderID != e.ProviderID {
			return "stale", nil
		}
		next := toStatus(st)
		var changed bool
		switch {
		case next == "":
			return "in_flight", nil
		case next == domain.Failed && cur.Channel == domain.WhatsApp && ch.Undeliverable(code) && s.channels.SMS != nil:
			if changed, err = s.fallbackTx(ctx, tx, &cur, code); err != nil {
				return "", err
			}
			if changed {
				return "fallback", store.Update(ctx, tx, cur)
			}
		case next == domain.Failed:
			if changed, err = s.failTx(ctx, tx, &cur, code); err != nil {
				return "", err
			}
			if changed {
				return "failed", store.Update(ctx, tx, cur)
			}
		case cur.Status.Advances(next):
			cur.Status = next
			return string(next), store.Update(ctx, tx, cur)
		}
		return "unchanged", nil
	})
}

func (s *Service) applyReply(ctx context.Context, receiptID ulid.ID, e channel.Event) error {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	ref, answer, ok := s.buttons.ParsePayload(e.Payload)
	if !ok || ref.Store != storeID {
		return s.mark(ctx, receiptID, "forged_payload", nil)
	}
	at := e.At
	if at.IsZero() {
		at = s.now().UTC()
	}
	return s.mark(ctx, receiptID, "", func(tx pgx.Tx) (string, error) {
		m, err := store.Get(ctx, tx, ref.Message, true)
		if errors.Is(err, domain.ErrNotFound) {
			return "unknown_message", nil
		}
		if err != nil {
			return "", err
		}
		first, err := store.RecordReply(ctx, tx, m.ID, answer, at)
		if err != nil || !first {
			return "repeated_reply", err
		}
		// A tap means the message was read.
		if m.Status.Advances(domain.Read) {
			m.Status = domain.Read
			if err = store.Update(ctx, tx, m); err != nil {
				return "", err
			}
		}
		if s.replies != nil {
			if err = s.replies.HandleReplyTx(ctx, tx, Reply{MessageID: m.ID, Kind: m.Kind, OrderID: m.OrderID, Answer: answer, At: at}); err != nil {
				return "", err
			}
		}
		return "reply_" + string(answer), nil
	})
}

// AddWorkers registers the messaging jobs' workers.
func (s *Service) AddWorkers(w *river.Workers) error {
	return errors.Join(
		river.AddWorkerSafely(w, &sendWorker{svc: s}),
		river.AddWorkerSafely(w, &webhookWorker{svc: s}),
	)
}

type sendWorker struct {
	river.WorkerDefaults[SendMessageArgs]
	svc *Service
}

func (w *sendWorker) Work(ctx context.Context, job *river.Job[SendMessageArgs]) error {
	return w.svc.SendMessage(ctx, job.Args.MessageID, job.Attempt >= job.MaxAttempts)
}

// earlyWait is how long a status that beat its send waits.
const earlyWait = 15 * time.Second

type webhookWorker struct {
	river.WorkerDefaults[ProcessWebhookArgs]
	svc *Service
}

func (w *webhookWorker) Work(ctx context.Context, job *river.Job[ProcessWebhookArgs]) error {
	err := w.svc.ProcessWebhook(ctx, job.Args.ReceiptID, job.Args.Provider)
	if IsEarly(err) {
		return river.JobSnooze(earlyWait)
	}
	return err
}
