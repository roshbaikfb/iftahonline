package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/store/shippingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrOrderShipped is returned when the order already has a live shipment.
var ErrOrderShipped = errors.New("shipping store: order already has a live shipment")

// InsertShipment stores a requested shipment.
func InsertShipment(ctx context.Context, tx Tx, s domain.Shipment) (domain.Shipment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Shipment{}, err
	}
	row, err := shippingdb.New(tx).InsertShipment(ctx, shippingdb.InsertShipmentParams{ID: s.ID, StoreID: store, OrderID: s.OrderID,
		ConnectionID: s.ConnectionID, Carrier: s.Carrier, Currency: string(s.COD.Currency()), CodMinor: s.COD.Amount()})
	if postgres.IsUniqueViolation(err, "shipments_one_live_per_order") {
		return domain.Shipment{}, ErrOrderShipped
	}
	if err != nil {
		return domain.Shipment{}, fmt.Errorf("shipping store: insert shipment: %w", err)
	}
	return toShipment(row)
}

// GetShipment returns a shipment, locked for update when lock is set, or
// domain.ErrNotFound.
func GetShipment(ctx context.Context, tx Tx, id ulid.ID, lock bool) (domain.Shipment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Shipment{}, err
	}
	q := shippingdb.New(tx)
	var row shippingdb.Shipment
	if lock {
		row, err = q.GetShipmentForUpdate(ctx, shippingdb.GetShipmentForUpdateParams{StoreID: store, ID: id})
	} else {
		row, err = q.GetShipment(ctx, shippingdb.GetShipmentParams{StoreID: store, ID: id})
	}
	return oneShipment(row, err)
}

// ShipmentByTracking finds the store's shipment of carrier with a
// tracking number, or domain.ErrNotFound.
func ShipmentByTracking(ctx context.Context, tx Tx, carrierName, tracking string) (domain.Shipment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Shipment{}, err
	}
	row, err := shippingdb.New(tx).ShipmentByTracking(ctx, shippingdb.ShipmentByTrackingParams{StoreID: store, Carrier: carrierName, TrackingNumber: &tracking})
	return oneShipment(row, err)
}

// ListShipments returns one page, newest first.
func ListShipments(ctx context.Context, tx Tx, q domain.ShipmentQuery) ([]domain.Shipment, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	p := shippingdb.ListShipmentsParams{StoreID: store, BeforeAt: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC),
		BeforeID: ulid.ID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		OrderID:  q.OrderID, Lim: int32(q.Limit)} //nolint:gosec // bounded by the caller
	if q.After != nil {
		p.BeforeAt, p.BeforeID = q.After.At, q.After.ID
	}
	if q.Status != nil {
		st := string(*q.Status)
		p.Status = &st
	}
	rows, err := shippingdb.New(tx).ListShipments(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("shipping store: list shipments: %w", err)
	}
	out := make([]domain.Shipment, len(rows))
	for i, r := range rows {
		if out[i], err = toShipment(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetCreated records the carrier's tracking number.
func SetCreated(ctx context.Context, tx Tx, id ulid.ID, tracking, providerID string) (domain.Shipment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Shipment{}, err
	}
	row, err := shippingdb.New(tx).SetShipmentCreated(ctx, shippingdb.SetShipmentCreatedParams{StoreID: store, ID: id,
		TrackingNumber: &tracking, ProviderID: providerID})
	return oneShipment(row, err)
}

// SetStatus moves a shipment.
func SetStatus(ctx context.Context, tx Tx, id ulid.ID, st domain.ShipmentStatus, rawState, failure string, at time.Time) (domain.Shipment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Shipment{}, err
	}
	row, err := shippingdb.New(tx).SetShipmentStatus(ctx, shippingdb.SetShipmentStatusParams{StoreID: store, ID: id, Status: string(st),
		RawState: rawState, Failure: failure, StatusAt: at})
	return oneShipment(row, err)
}

// Touch records the carrier's raw state of an unchanged shipment.
func Touch(ctx context.Context, tx Tx, id ulid.ID, rawState string) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = shippingdb.New(tx).TouchShipment(ctx, shippingdb.TouchShipmentParams{StoreID: store, ID: id, RawState: rawState}); err != nil {
		return fmt.Errorf("shipping store: touch shipment: %w", err)
	}
	return nil
}

// SetLabel records where the shipment's label is stored.
func SetLabel(ctx context.Context, tx Tx, id ulid.ID, key string) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = shippingdb.New(tx).SetShipmentLabel(ctx, shippingdb.SetShipmentLabelParams{StoreID: store, ID: id, LabelKey: key}); err != nil {
		return fmt.Errorf("shipping store: set label: %w", err)
	}
	return nil
}

// RequestCancel records that the merchant asked to cancel.
func RequestCancel(ctx context.Context, tx Tx, id ulid.ID) (domain.Shipment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Shipment{}, err
	}
	row, err := shippingdb.New(tx).RequestShipmentCancel(ctx, shippingdb.RequestShipmentCancelParams{StoreID: store, ID: id})
	return oneShipment(row, err)
}

// InsertEvent appends to a shipment's timeline.
func InsertEvent(ctx context.Context, tx Tx, shipmentID ulid.ID, e domain.ShipmentEvent) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = shippingdb.New(tx).InsertShipmentEvent(ctx, shippingdb.InsertShipmentEventParams{ID: e.ID, StoreID: store, ShipmentID: shipmentID,
		OccurredAt: e.At, Status: string(e.Status), RawState: e.RawState, Source: e.Source, Note: e.Note}); err != nil {
		return fmt.Errorf("shipping store: insert shipment event: %w", err)
	}
	return nil
}

// Events returns a shipment's timeline, oldest first.
func Events(ctx context.Context, tx Tx, shipmentID ulid.ID) ([]domain.ShipmentEvent, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := shippingdb.New(tx).ListShipmentEvents(ctx, shippingdb.ListShipmentEventsParams{StoreID: store, ShipmentID: shipmentID})
	if err != nil {
		return nil, fmt.Errorf("shipping store: shipment events: %w", err)
	}
	out := make([]domain.ShipmentEvent, len(rows))
	for i, r := range rows {
		out[i] = domain.ShipmentEvent{ID: r.ID, At: r.OccurredAt, Status: domain.ShipmentStatus(r.Status), RawState: r.RawState, Source: r.Source, Note: r.Note}
	}
	return out, nil
}

func oneShipment(row shippingdb.Shipment, err error) (domain.Shipment, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Shipment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Shipment{}, fmt.Errorf("shipping store: shipment: %w", err)
	}
	return toShipment(row)
}

func toShipment(r shippingdb.Shipment) (domain.Shipment, error) {
	cod, err := money.New(r.CodMinor, money.Currency(r.Currency))
	if err != nil {
		return domain.Shipment{}, fmt.Errorf("shipping store: %w", err)
	}
	s := domain.Shipment{ID: r.ID, OrderID: r.OrderID, ConnectionID: r.ConnectionID, Carrier: r.Carrier, Status: domain.ShipmentStatus(r.Status),
		ProviderID: r.ProviderID, COD: cod, LabelKey: r.LabelKey, RawState: r.RawState, Failure: r.Failure,
		CancelRequestedAt: r.CancelRequestedAt, StatusAt: r.StatusAt, CreatedAt: r.CreatedAt, Version: int(r.Version)}
	if r.TrackingNumber != nil {
		s.TrackingNumber = *r.TrackingNumber
	}
	return s, nil
}
