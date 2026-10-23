package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ListCarrierConnections implements StrictServerInterface.
func (h *Handlers) ListCarrierConnections(ctx context.Context, _ ListCarrierConnectionsRequestObject) (ListCarrierConnectionsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingRead); err != nil {
		return nil, err
	}
	list, err := h.svc.Connections(ctx)
	if err != nil {
		return nil, err
	}
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	out := ListCarrierConnections200JSONResponse{Carriers: h.svc.CarrierNames(), Connections: make([]CarrierConnection, len(list))}
	for i, c := range list {
		out.Connections[i] = h.toConnection(store, c)
	}
	return out, nil
}

// ConnectCarrier implements StrictServerInterface.
func (h *Handlers) ConnectCarrier(ctx context.Context, req ConnectCarrierRequestObject) (ConnectCarrierResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	c, err := h.svc.Connect(ctx, req.Body.Carrier, req.Body.ApiKey)
	if err != nil {
		return nil, err
	}
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ConnectCarrier201JSONResponse{Connection: h.toConnection(store, c.Connection), WebhookUrl: c.WebhookURL, WebhookSecret: c.WebhookSecret}, nil
}

// DisconnectCarrier implements StrictServerInterface.
func (h *Handlers) DisconnectCarrier(ctx context.Context, req DisconnectCarrierRequestObject) (DisconnectCarrierResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingManage); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.ConnectionId)
	if err != nil {
		return nil, app.ErrConnectionNotFound
	}
	if err = h.svc.Disconnect(ctx, id); err != nil {
		return nil, err
	}
	return DisconnectCarrier204Response{}, nil
}

func (h *Handlers) toConnection(store tenant.StoreID, c domain.Connection) CarrierConnection {
	out := CarrierConnection{Id: c.ID.String(), Carrier: c.Carrier, Status: CarrierConnectionStatus(c.Status), CreatedAt: c.CreatedAt,
		DisconnectedAt: c.DisconnectedAt}
	if c.Status == domain.ConnectionActive {
		u := h.svc.WebhookURL(store, c)
		out.WebhookUrl = &u
	}
	return out
}
