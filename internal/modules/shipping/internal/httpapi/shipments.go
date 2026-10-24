package httpapi

import (
	"bytes"
	"context"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/cursor"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errBadCursor = errs.Validation(errs.FieldError{Field: "cursor", Code: "invalid", Message: "invalid cursor"})

// ListShipments implements StrictServerInterface.
func (h *Handlers) ListShipments(ctx context.Context, req ListShipmentsRequestObject) (ListShipmentsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	q := domain.ShipmentQuery{Limit: deref(req.Params.Limit)}
	if req.Params.OrderId != nil {
		id, err := ulid.Parse(*req.Params.OrderId)
		if err != nil {
			return ListShipments200JSONResponse{Items: []Shipment{}}, nil // no shipment has that order
		}
		q.OrderID = &id
	}
	if req.Params.Status != nil {
		st := domain.ShipmentStatus(*req.Params.Status)
		q.Status = &st
	}
	if c := deref(req.Params.Cursor); c != "" {
		at, id, err := cursor.Decode(c)
		if err != nil {
			return nil, errBadCursor
		}
		q.After = &domain.Cursor{At: at, ID: id}
	}
	p, err := h.svc.Shipments(ctx, q)
	if err != nil {
		return nil, err
	}
	out := ListShipments200JSONResponse{Items: make([]Shipment, len(p.Items))}
	for i, s := range p.Items {
		out.Items[i] = toShipment(s)
	}
	if p.Next != nil {
		c := cursor.Encode(p.Next.At, p.Next.ID)
		out.NextCursor = &c
	}
	return out, nil
}

// CreateShipments implements StrictServerInterface.
func (h *Handlers) CreateShipments(ctx context.Context, req CreateShipmentsRequestObject) (CreateShipmentsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersFulfil); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	conn, err := ulid.Parse(req.Body.ConnectionId)
	if err != nil {
		return nil, errs.Validation(errs.FieldError{Field: "connection_id", Code: "not_found", Message: "connect this carrier first"})
	}
	ids := make([]ulid.ID, 0, len(req.Body.OrderIds))
	var bad []string
	for _, s := range req.Body.OrderIds {
		id, parseErr := ulid.Parse(s)
		if parseErr != nil {
			bad = append(bad, s)
			continue
		}
		ids = append(ids, id)
	}
	results, err := h.svc.Ship(ctx, ids, conn)
	if err != nil {
		return nil, err
	}
	out := CreateShipments200JSONResponse{Results: make([]ShipResult, 0, len(results)+len(bad))}
	for _, r := range results {
		item := ShipResult{OrderId: r.OrderID.String()}
		if r.Shipment != nil {
			s := toShipment(*r.Shipment)
			item.Shipment = &s
		} else {
			item.Error = bulkError(r.Err)
		}
		out.Results = append(out.Results, item)
	}
	for _, s := range bad {
		out.Results = append(out.Results, ShipResult{OrderId: s, Error: &BulkError{Code: "order_not_found", Detail: "order not found"}})
	}
	return out, nil
}

func bulkError(err error) *BulkError {
	var e *errs.Error
	if errors.As(err, &e) {
		return &BulkError{Code: e.Code, Detail: e.Message}
	}
	return &BulkError{Code: "internal", Detail: "the order could not be shipped"}
}

// GetShipment implements StrictServerInterface.
func (h *Handlers) GetShipment(ctx context.Context, req GetShipmentRequestObject) (GetShipmentResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.ShipmentId)
	if err != nil {
		return nil, app.ErrShipmentNotFound
	}
	s, evs, err := h.svc.Shipment(ctx, id)
	if err != nil {
		return nil, err
	}
	out := GetShipment200JSONResponse{Shipment: toShipment(s), Events: make([]ShipmentEvent, len(evs))}
	for i, e := range evs {
		out.Events[i] = ShipmentEvent{Id: e.ID.String(), Status: ShipmentStatus(e.Status), Source: ShipmentEventSource(e.Source), Note: e.Note, At: e.At}
		if e.RawState != "" {
			raw := e.RawState
			out.Events[i].CarrierState = &raw
		}
	}
	return out, nil
}

// CancelShipment implements StrictServerInterface.
func (h *Handlers) CancelShipment(ctx context.Context, req CancelShipmentRequestObject) (CancelShipmentResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersFulfil); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.ShipmentId)
	if err != nil {
		return nil, app.ErrShipmentNotFound
	}
	s, err := h.svc.Cancel(ctx, id)
	if err != nil {
		return nil, err
	}
	return CancelShipment200JSONResponse(toShipment(s)), nil
}

// GetShipmentLabel implements StrictServerInterface.
func (h *Handlers) GetShipmentLabel(ctx context.Context, req GetShipmentLabelRequestObject) (GetShipmentLabelResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.ShipmentId)
	if err != nil {
		return nil, app.ErrShipmentNotFound
	}
	pdf, err := h.svc.Label(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetShipmentLabel200ApplicationpdfResponse{Body: bytes.NewReader(pdf), ContentLength: int64(len(pdf))}, nil
}

func toShipment(s domain.Shipment) Shipment {
	out := Shipment{Id: s.ID.String(), OrderId: s.OrderID.String(), ConnectionId: s.ConnectionID.String(), Carrier: s.Carrier,
		Status: ShipmentStatus(s.Status), CodMinor: s.COD.Amount(), Currency: string(s.COD.Currency()), LabelReady: s.LabelKey != "",
		CancelRequestedAt: s.CancelRequestedAt, StatusAt: s.StatusAt, CreatedAt: s.CreatedAt, Version: s.Version}
	for _, f := range []struct {
		v   string
		dst **string
	}{{s.TrackingNumber, &out.TrackingNumber}, {s.RawState, &out.CarrierState}, {s.Failure, &out.Failure}} {
		if f.v != "" {
			v := f.v
			*f.dst = &v
		}
	}
	return out
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
