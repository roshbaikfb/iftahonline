package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Limits and timing.
const (
	MaxShipBatch        = 100
	DefaultShipmentPage = 50
	MaxShipmentPage     = 200
	// PollEvery is how often an active shipment is tracked when no
	// webhook says it moved: the fallback when webhooks are lost.
	PollEvery = 6 * time.Hour
	// MaxTrackFor ends the poll of a shipment the carrier never finishes
	// (created and never picked up, say): two months covers any real
	// delivery and its returns; the carrier's webhooks still move it.
	MaxTrackFor = 60 * 24 * time.Hour
)

// Shipment errors.
var (
	ErrShipmentNotFound  = errs.NotFound("shipment_not_found", "shipment not found")
	ErrNotCancellable    = errs.Conflict("shipment_not_cancellable", "this shipment can no longer be cancelled")
	ErrLabelNotReady     = errs.Conflict("label_not_ready", "the carrier has not issued the label yet; try again in a minute")
	errBatchSize         = errs.Validation(errs.FieldError{Field: "order_ids", Code: "count", Message: fmt.Sprintf("ship 1 to %d orders at once", MaxShipBatch)})
	errPageSize          = errs.Validation(errs.FieldError{Field: "limit", Code: "range", Message: fmt.Sprintf("limit must be 1 to %d", MaxShipmentPage)})
	errOrderNotFound     = errs.NotFound("order_not_found", "order not found")
	errOrderNotReady     = errs.Conflict("order_not_ready", "only orders ready to ship can be handed to a carrier")
	errOrderShipped      = errs.Conflict("order_already_shipped", "this order already has a shipment")
	errConnectionMissing = errs.Validation(errs.FieldError{Field: "connection_id", Code: "not_found", Message: "connect this carrier first"})
)

// Errors an Orders adapter returns.
var (
	// ErrOrderUnknown: no such order in the store.
	ErrOrderUnknown = errors.New("shipping: order not found")
	// ErrOrderMoveRefused: the order's state machine refuses the move
	// (the merchant cancelled it, say).
	ErrOrderMoveRefused = errors.New("shipping: the order cannot make this move")
)

// OrderInfo is what shipping needs of an order.
type OrderInfo struct {
	ID          ulid.ID
	Status      string // the orders module's status
	Name        string
	Phone       string // E.164
	Governorate string
	City        string
	Address     string
	Notes       string
	COD         money.Money // what the courier collects
	Items       int
	Description string // what is inside, for the courier
}

// Orders is what shipping uses of the orders module; cmd/platform adapts
// orders.Fulfilment to it (shipping cannot import orders, which prices
// deliveries through shipping).
type Orders interface {
	Order(ctx context.Context, id ulid.ID) (OrderInfo, error)
	// OrderTx is Order in the caller's transaction (the order locked until
	// it ends): inside a transaction, Order would take a second connection.
	OrderTx(ctx context.Context, tx pgx.Tx, id ulid.ID) (OrderInfo, error)
	MoveTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to, actor, note string) (bool, error)
}

// ShipResult is one order's outcome in Ship.
type ShipResult struct {
	OrderID  ulid.ID
	Shipment *domain.Shipment
	Err      error
}

// Ship hands orders to a connected carrier: each order ready to ship
// gets a requested shipment and its create job, in one transaction per
// order. The carrier is called by the job, not while the merchant waits.
func (s *Service) Ship(ctx context.Context, orderIDs []ulid.ID, connectionID ulid.ID) ([]ShipResult, error) {
	if len(orderIDs) == 0 || len(orderIDs) > MaxShipBatch {
		return nil, errBatchSize
	}
	conn, err := s.activeConnection(ctx, connectionID)
	if errors.Is(err, ErrConnectionNotFound) {
		return nil, errConnectionMissing
	}
	if err != nil {
		return nil, err
	}
	out := make([]ShipResult, 0, len(orderIDs))
	seen := map[ulid.ID]bool{}
	for _, id := range orderIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		sh, shipErr := s.shipOne(ctx, id, conn.Connection)
		if shipErr != nil && !isUserError(shipErr) {
			return nil, shipErr
		}
		r := ShipResult{OrderID: id, Err: shipErr}
		if shipErr == nil {
			r.Shipment = &sh
		}
		out = append(out, r)
	}
	return out, nil
}

func isUserError(err error) bool {
	k := errs.KindOf(err)
	return k == errs.KindNotFound || k == errs.KindConflict || k == errs.KindValidation
}

func (s *Service) shipOne(ctx context.Context, orderID ulid.ID, conn domain.Connection) (domain.Shipment, error) {
	o, err := s.orders.Order(ctx, orderID)
	switch {
	case errors.Is(err, ErrOrderUnknown):
		return domain.Shipment{}, errOrderNotFound
	case err != nil:
		return domain.Shipment{}, err
	case o.Status != domain.OrderReadyToShip:
		return domain.Shipment{}, errOrderNotReady
	}
	sh := domain.Shipment{ID: s.ids.New(), OrderID: orderID, ConnectionID: conn.ID, Carrier: conn.Carrier, COD: o.COD}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if sh, txErr = store.InsertShipment(ctx, tx, sh); txErr != nil {
			return txErr
		}
		if txErr = s.timelineTx(ctx, tx, sh.ID, domain.ShipmentRequested, "", domain.SourceMerchant, ""); txErr != nil {
			return txErr
		}
		if txErr = s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return CreateShipmentArgs{TenantArgs: t, ShipmentID: sh.ID} }, nil); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "shipment.requested", TargetType: "shipment", TargetID: sh.ID.String(),
			Metadata: map[string]any{"order_id": orderID.String(), "carrier": conn.Carrier}})
	})
	if errors.Is(err, store.ErrOrderShipped) {
		return domain.Shipment{}, errOrderShipped
	}
	return sh, err
}

// Shipment returns a shipment with its timeline.
func (s *Service) Shipment(ctx context.Context, id ulid.ID) (domain.Shipment, []domain.ShipmentEvent, error) {
	var sh domain.Shipment
	var evs []domain.ShipmentEvent
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if sh, txErr = store.GetShipment(ctx, tx, id, false); txErr != nil {
			return txErr
		}
		evs, txErr = store.Events(ctx, tx, id)
		return txErr
	})
	return sh, evs, shipmentNotFound(err)
}

// Shipments returns a page of the store's shipments, newest first.
func (s *Service) Shipments(ctx context.Context, q domain.ShipmentQuery) (domain.ShipmentPage, error) {
	if q.Limit == 0 {
		q.Limit = DefaultShipmentPage
	}
	if q.Limit < 1 || q.Limit > MaxShipmentPage {
		return domain.ShipmentPage{}, errPageSize
	}
	want := q.Limit
	q.Limit++
	var items []domain.Shipment
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		items, txErr = store.ListShipments(ctx, tx, q)
		return txErr
	})
	if err != nil {
		return domain.ShipmentPage{}, err
	}
	p := domain.ShipmentPage{Items: items}
	if len(items) > want {
		p.Items = items[:want]
		last := p.Items[want-1]
		p.Next = &domain.Cursor{At: last.CreatedAt, ID: last.ID}
	}
	return p, nil
}

// Cancel asks to cancel a shipment. Before the carrier knows it, it is
// cancelled at once; afterwards a job asks the carrier, which refuses
// once the parcel is picked up.
func (s *Service) Cancel(ctx context.Context, id ulid.ID) (domain.Shipment, error) {
	var sh domain.Shipment
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if sh, txErr = store.GetShipment(ctx, tx, id, true); txErr != nil {
			return txErr
		}
		switch {
		case sh.Status.Final():
			return ErrNotCancellable
		case sh.Status == domain.ShipmentRequested:
			if sh, txErr = store.SetStatus(ctx, tx, id, domain.ShipmentCancelled, "", "", s.clock()); txErr != nil {
				return txErr
			}
			txErr = s.timelineTx(ctx, tx, id, domain.ShipmentCancelled, "", domain.SourceMerchant, "cancelled before the carrier created it")
		default:
			if sh, txErr = store.RequestCancel(ctx, tx, id); txErr != nil {
				return txErr
			}
			txErr = s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return CancelShipmentArgs{TenantArgs: t, ShipmentID: id} }, nil)
		}
		if txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "shipment.cancel_requested", TargetType: "shipment", TargetID: id.String()})
	})
	return sh, shipmentNotFound(err)
}

// Label returns a shipment's label (PDF), once the carrier issued it.
func (s *Service) Label(ctx context.Context, id ulid.ID) ([]byte, error) {
	sh, _, err := s.Shipment(ctx, id)
	if err != nil {
		return nil, err
	}
	if sh.LabelKey == "" {
		return nil, ErrLabelNotReady
	}
	return s.objects.Get(ctx, sh.LabelKey, maxLabel)
}

const maxLabel = 4 << 20

// applyTracking records what the carrier says of a shipment, in tx, and
// moves its order along: the one place shipment states change after
// creation (webhooks and polls both land here). It reports whether the
// shipment changed. A final shipment, an unknown carrier state and the
// state it already has change nothing.
func (s *Service) applyTracking(ctx context.Context, tx pgx.Tx, id ulid.ID, tr carrier.Tracking, source string) (bool, error) {
	sh, err := store.GetShipment(ctx, tx, id, true)
	if err != nil {
		return false, err
	}
	next, known := fromCarrier(tr.Status)
	if sh.Status.Final() || !known || next == sh.Status {
		return false, store.Touch(ctx, tx, id, text.Clip(tr.RawState, 300))
	}
	if sh, err = store.SetStatus(ctx, tx, id, next, text.Clip(tr.RawState, 300), "", s.clock()); err != nil {
		return false, err
	}
	note := ""
	if moved, moveErr := s.moveOrderTx(ctx, tx, sh); moveErr != nil {
		return false, moveErr
	} else if !moved {
		note = "the order was not moved (its status does not follow the parcel)"
	}
	if err = s.timelineTx(ctx, tx, id, next, text.Clip(tr.RawState, 300), source, note); err != nil {
		return false, err
	}
	m, err := event.MetaFor(ctx, s.ids, domain.EventShipmentStatusChanged, 1)
	if err != nil {
		return false, err
	}
	return true, s.events.PublishTx(ctx, tx, domain.ShipmentStatusChanged{Meta: m, ShipmentID: sh.ID, OrderID: sh.OrderID, Status: next})
}

// moveOrderTx walks the order to where its shipment is (nothing to do when
// it is there already). It reports false when the order refused a move,
// and stops there.
func (s *Service) moveOrderTx(ctx context.Context, tx pgx.Tx, sh domain.Shipment) (bool, error) {
	o, err := s.orders.OrderTx(ctx, tx, sh.OrderID)
	if errors.Is(err, ErrOrderUnknown) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, to := range domain.OrderPath(o.Status, sh.Status) {
		_, err = s.orders.MoveTx(ctx, tx, sh.OrderID, to, "carrier:"+sh.Carrier, "tracking "+sh.TrackingNumber)
		if errors.Is(err, ErrOrderMoveRefused) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// fromCarrier maps a carrier status onto a shipment status.
func fromCarrier(st carrier.Status) (domain.ShipmentStatus, bool) {
	switch st {
	case carrier.StatusCreated:
		return domain.ShipmentCreated, true
	case carrier.StatusInTransit:
		return domain.ShipmentInTransit, true
	case carrier.StatusOutForDelivery:
		return domain.ShipmentOutForDelivery, true
	case carrier.StatusDelivered:
		return domain.ShipmentDelivered, true
	case carrier.StatusFailedAttempt:
		return domain.ShipmentFailedAttempt, true
	case carrier.StatusReturned:
		return domain.ShipmentReturned, true
	case carrier.StatusCancelled:
		return domain.ShipmentCancelled, true
	case carrier.StatusUnknown:
	}
	return "", false
}

func (s *Service) timelineTx(ctx context.Context, tx pgx.Tx, id ulid.ID, st domain.ShipmentStatus, raw, source, note string) error {
	return store.InsertEvent(ctx, tx, id, domain.ShipmentEvent{ID: s.ids.New(), At: s.clock(), Status: st, RawState: raw, Source: source, Note: note})
}

func (s *Service) enqueueTx(ctx context.Context, tx pgx.Tx, mk func(jobs.TenantArgs) river.JobArgs, opts *river.InsertOpts) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	args := mk(t)
	if _, err = s.jobs.InsertTx(ctx, tx, args, opts); err != nil {
		return fmt.Errorf("shipping: enqueue %s: %w", args.Kind(), err)
	}
	return nil
}

func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

func shipmentNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return ErrShipmentNotFound
	}
	return err
}
