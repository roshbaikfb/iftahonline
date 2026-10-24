package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// CreateShipmentArgs asks the carrier to create a requested shipment.
type CreateShipmentArgs struct {
	jobs.TenantArgs
	ShipmentID ulid.ID `json:"shipment_id"`
}

// Kind implements river.JobArgs.
func (CreateShipmentArgs) Kind() string { return "shipping.create_shipment" }

// FetchLabelArgs stores a shipment's label.
type FetchLabelArgs struct {
	jobs.TenantArgs
	ShipmentID ulid.ID `json:"shipment_id"`
}

// Kind implements river.JobArgs.
func (FetchLabelArgs) Kind() string { return "shipping.fetch_label" }

// CancelShipmentArgs asks the carrier to cancel a shipment.
type CancelShipmentArgs struct {
	jobs.TenantArgs
	ShipmentID ulid.ID `json:"shipment_id"`
}

// Kind implements river.JobArgs.
func (CancelShipmentArgs) Kind() string { return "shipping.cancel_shipment" }

// TrackShipmentArgs polls an active shipment every PollEvery until it is
// final: the fallback when webhooks are lost. One job per shipment,
// snoozing between polls.
type TrackShipmentArgs struct {
	jobs.TenantArgs
	ShipmentID ulid.ID `json:"shipment_id"`
}

// Kind implements river.JobArgs.
func (TrackShipmentArgs) Kind() string { return "shipping.track_shipment" }

// ProcessWebhookArgs processes an accepted carrier webhook.
type ProcessWebhookArgs struct {
	jobs.TenantArgs
	ReceiptID ulid.ID `json:"receipt_id"`
}

// Kind implements river.JobArgs.
func (ProcessWebhookArgs) Kind() string { return "shipping.process_webhook" }

// CreateShipment runs CreateShipmentArgs: it creates the shipment at the
// carrier (idempotently, by the shipment's ID), then records the tracking
// number and enqueues the label fetch and the poll. A refusal by the
// carrier fails the shipment; anything else is retried.
func (s *Service) CreateShipment(ctx context.Context, id ulid.ID) error {
	sh, err := s.shipmentNow(ctx, id)
	if err != nil || sh.Status != domain.ShipmentRequested {
		return ignoreGone(err)
	}
	adapter, creds, err := s.Credentials(ctx, sh.ConnectionID)
	if errors.Is(err, ErrConnectionNotFound) {
		return s.failShipment(ctx, id, "the carrier was disconnected before the shipment was created")
	}
	if err != nil {
		return err
	}
	o, err := s.orders.Order(ctx, sh.OrderID)
	if errors.Is(err, ErrOrderUnknown) {
		return s.failShipment(ctx, id, "the order no longer exists")
	}
	if err != nil {
		return err
	}
	created, err := adapter.CreateShipment(ctx, creds, carrier.ShipmentRequest{
		Reference: sh.ID.String(), Receiver: carrier.Receiver{Name: o.Name, Phone: o.Phone},
		Address: carrier.Address{Governorate: o.Governorate, City: o.City, Line: o.Address},
		COD:     sh.COD, Items: o.Items, Description: o.Description, Notes: o.Notes,
	})
	switch {
	case errors.Is(err, carrier.ErrRejected), errors.Is(err, carrier.ErrAuth):
		return s.failShipment(ctx, id, err.Error())
	case err != nil:
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, txErr := store.GetShipment(ctx, tx, id, true)
		if txErr != nil {
			return txErr
		}
		if cur.Status != domain.ShipmentRequested {
			return nil // cancelled while the carrier was creating it: the cancel job below never ran for it
		}
		if _, txErr = store.SetCreated(ctx, tx, id, created.TrackingNumber, created.ProviderID); txErr != nil {
			return txErr
		}
		if txErr = s.timelineTx(ctx, tx, id, domain.ShipmentCreated, "", domain.SourceAPI, "tracking "+created.TrackingNumber); txErr != nil {
			return txErr
		}
		if txErr = s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return FetchLabelArgs{TenantArgs: t, ShipmentID: id} }, nil); txErr != nil {
			return txErr
		}
		if txErr = s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return TrackShipmentArgs{TenantArgs: t, ShipmentID: id} },
			&river.InsertOpts{ScheduledAt: s.clock().Add(PollEvery)}); txErr != nil {
			return txErr
		}
		if cur.CancelRequestedAt != nil {
			return s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return CancelShipmentArgs{TenantArgs: t, ShipmentID: id} }, nil)
		}
		return nil
	})
}

func (s *Service) failShipment(ctx context.Context, id ulid.ID, why string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := store.SetStatus(ctx, tx, id, domain.ShipmentFailed, "", text.Clip(why, 300), s.clock()); err != nil {
			return err
		}
		return s.timelineTx(ctx, tx, id, domain.ShipmentFailed, "", domain.SourceAPI, text.Clip(why, 300))
	})
}

// FetchLabel runs FetchLabelArgs.
func (s *Service) FetchLabel(ctx context.Context, id ulid.ID) error {
	sh, err := s.shipmentNow(ctx, id)
	if err != nil || !sh.Status.Tracked() || sh.LabelKey != "" {
		return ignoreGone(err)
	}
	adapter, creds, err := s.Credentials(ctx, sh.ConnectionID)
	if errors.Is(err, ErrConnectionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	pdf, err := adapter.Label(ctx, creds, sh.TrackingNumber)
	if errors.Is(err, carrier.ErrNotFound) || errors.Is(err, carrier.ErrAuth) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	key := storeID.ObjectPrefix() + "shipping/labels/" + id.String() + ".pdf"
	if err = s.objects.Put(ctx, key, "application/pdf", pdf); err != nil {
		return fmt.Errorf("shipping: store label: %w", err)
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return store.SetLabel(ctx, tx, id, key) })
}

// CancelShipment runs CancelShipmentArgs.
func (s *Service) CancelShipment(ctx context.Context, id ulid.ID) error {
	sh, err := s.shipmentNow(ctx, id)
	if err != nil || !sh.Status.Tracked() || sh.Status.Final() {
		return ignoreGone(err)
	}
	adapter, creds, err := s.Credentials(ctx, sh.ConnectionID)
	if errors.Is(err, ErrConnectionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	err = adapter.Cancel(ctx, creds, sh.TrackingNumber)
	if errors.Is(err, carrier.ErrNotCancellable) {
		return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
			return s.timelineTx(ctx, tx, id, sh.Status, sh.RawState, domain.SourceAPI, "the carrier refused to cancel: the parcel is already on its way")
		})
	}
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		_, txErr := s.applyTracking(ctx, tx, id, carrier.Tracking{Status: carrier.StatusCancelled, RawState: "cancelled"}, domain.SourceAPI)
		return txErr
	})
}

// TrackShipment runs TrackShipmentArgs once. It reports whether to poll
// again (after PollEvery).
func (s *Service) TrackShipment(ctx context.Context, id ulid.ID) (again bool, err error) {
	sh, err := s.shipmentNow(ctx, id)
	if err != nil || !sh.Status.Tracked() || sh.Status.Final() {
		return false, ignoreGone(err)
	}
	adapter, creds, err := s.Credentials(ctx, sh.ConnectionID)
	if errors.Is(err, ErrConnectionNotFound) {
		return false, nil // disconnected: nothing to poll with
	}
	if err != nil {
		return true, err
	}
	tr, err := adapter.Track(ctx, creds, sh.TrackingNumber)
	switch {
	case errors.Is(err, carrier.ErrNotFound), errors.Is(err, carrier.ErrAuth):
		return false, nil
	case err != nil:
		return true, err
	}
	var final bool
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, txErr := s.applyTracking(ctx, tx, id, tr, domain.SourcePoll); txErr != nil {
			return txErr
		}
		cur, txErr := store.GetShipment(ctx, tx, id, false)
		final = cur.Status.Final()
		return txErr
	})
	return !final, err
}

// ProcessWebhook runs ProcessWebhookArgs: the receipt's body names a
// shipment, which is tracked at the carrier before anything changes
// (ADR-0024); the receipt's outcome is recorded in the same transaction.
func (s *Service) ProcessWebhook(ctx context.Context, receiptID ulid.ID) error {
	r, _, body, err := s.inbox.Get(ctx, receiptID)
	if errors.Is(err, webhooks.ErrReceiptNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	adapter, ok := s.carriers[r.Provider]
	if !ok {
		return river.JobCancel(fmt.Errorf("shipping: no carrier %q", r.Provider))
	}
	ev, err := adapter.ParseWebhook(body)
	if err != nil {
		return s.markReceipt(ctx, receiptID, "unreadable")
	}
	var sh domain.Shipment
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		sh, txErr = store.ShipmentByTracking(ctx, tx, r.Provider, ev.TrackingNumber)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return s.markReceipt(ctx, receiptID, "unknown_shipment")
	}
	if err != nil {
		return err
	}
	_, creds, err := s.Credentials(ctx, sh.ConnectionID)
	if errors.Is(err, ErrConnectionNotFound) {
		return s.markReceipt(ctx, receiptID, "disconnected")
	}
	if err != nil {
		return err
	}
	tr, err := adapter.Track(ctx, creds, ev.TrackingNumber)
	if errors.Is(err, carrier.ErrNotFound) {
		return s.markReceipt(ctx, receiptID, "unknown_shipment")
	}
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		changed, txErr := s.applyTracking(ctx, tx, sh.ID, tr, domain.SourceWebhook)
		if txErr != nil {
			return txErr
		}
		outcome := "unchanged"
		if changed {
			outcome = "updated"
		}
		_, txErr = s.inbox.MarkProcessedTx(ctx, tx, receiptID, outcome)
		return txErr
	})
}

func (s *Service) markReceipt(ctx context.Context, id ulid.ID, outcome string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		_, err := s.inbox.MarkProcessedTx(ctx, tx, id, outcome)
		return err
	})
}

func (s *Service) shipmentNow(ctx context.Context, id ulid.ID) (domain.Shipment, error) {
	var sh domain.Shipment
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		sh, txErr = store.GetShipment(ctx, tx, id, false)
		return txErr
	})
	return sh, err
}

// ignoreGone ends a job whose shipment no longer exists.
func ignoreGone(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}

// WebhookProvider is a carrier's side of the webhooks inbox: it checks
// the connection's secret, reads the event id, and enqueues the
// processing job with the receipt.
type WebhookProvider struct {
	svc     *Service
	carrier carrier.Carrier
}

var _ webhooks.Provider = WebhookProvider{}

// WebhookProviders returns one provider per carrier.
func (s *Service) WebhookProviders() []webhooks.Provider {
	out := make([]webhooks.Provider, 0, len(s.carriers))
	for _, name := range s.carriers.Names() {
		out = append(out, WebhookProvider{svc: s, carrier: s.carriers[name]})
	}
	return out
}

// SecretHeader carries a connection's webhook secret.
const SecretHeader = "X-Webhook-Secret" //nolint:gosec // a header name, not a credential

// Name implements webhooks.Provider.
func (p WebhookProvider) Name() string { return p.carrier.Name() }

// Verify implements webhooks.Provider. The secret comes in SecretHeader,
// or in the "secret" query parameter for carriers that allow no headers.
func (p WebhookProvider) Verify(ctx context.Context, conn ulid.ID, req webhooks.Request) (webhooks.Verified, error) {
	secret := req.Header.Get(SecretHeader)
	if secret == "" {
		secret = req.Query.Get("secret")
	}
	switch err := p.svc.VerifyWebhook(ctx, p.carrier.Name(), conn, secret); {
	case errors.Is(err, ErrConnectionNotFound):
		return webhooks.Verified{}, webhooks.ErrUnknownConnection
	case errors.Is(err, ErrWebhookForged):
		return webhooks.Verified{}, webhooks.ErrForged
	case err != nil:
		return webhooks.Verified{}, err
	}
	ev, err := p.carrier.ParseWebhook(req.Body)
	if err != nil {
		return webhooks.Verified{}, nil // no event id: the inbox refuses it (400)
	}
	return webhooks.Verified{EventID: ev.EventID}, nil
}

// AcceptTx implements webhooks.Provider.
func (p WebhookProvider) AcceptTx(ctx context.Context, tx pgx.Tx, r webhooks.Receipt) error {
	return p.svc.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ProcessWebhookArgs{TenantArgs: t, ReceiptID: r.ID} }, nil)
}
