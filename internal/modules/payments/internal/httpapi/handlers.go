// Package httpapi serves the payments tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/cursor"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Handlers implements StrictServerInterface.
type Handlers struct {
	svc  *app.Service
	auth authz.Authorizer
}

var _ StrictServerInterface = (*Handlers)(nil)

var (
	errBodyRequired = errs.BadRequest("request body is required", nil)
	errBadCursor    = errs.Validation(errs.FieldError{Field: "cursor", Code: "invalid", Message: "invalid cursor"})
)

// Mount registers the payments routes on mux. scope is the membership
// middleware: it authenticates, returns 404 to non-members, and sets the
// tenant and principal (ADR-0006).
func Mount(mux *http.ServeMux, svc *app.Service, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		resp.Write(w, r, errs.BadRequest("invalid request", err))
	}
	strict := NewStrictHandlerWithOptions(&Handlers{svc: svc, auth: auth}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: resp.Write,
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter:       mux,
		Middlewares:      []MiddlewareFunc{MiddlewareFunc(scope)},
		ErrorHandlerFunc: badRequest,
	})
}

// ListPaymentConnections implements StrictServerInterface.
func (h *Handlers) ListPaymentConnections(ctx context.Context, _ ListPaymentConnectionsRequestObject) (ListPaymentConnectionsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPaymentsManage); err != nil {
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
	out := ListPaymentConnections200JSONResponse{Gateways: h.svc.GatewayNames(), Connections: make([]PaymentConnection, len(list))}
	for i, c := range list {
		out.Connections[i] = h.toConnection(store, c)
	}
	return out, nil
}

// ConnectPaymentGateway implements StrictServerInterface.
func (h *Handlers) ConnectPaymentGateway(ctx context.Context, req ConnectPaymentGatewayRequestObject) (ConnectPaymentGatewayResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPaymentsManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	b := req.Body
	c, err := h.svc.Connect(ctx, app.ConnectInput{Gateway: b.Gateway, SecretKey: deref(b.SecretKey), PublicKey: deref(b.PublicKey),
		WebhookSecret: b.WebhookSecret, APIKey: b.ApiKey, IntegrationIDs: deref(b.IntegrationIds)})
	if err != nil {
		return nil, err
	}
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ConnectPaymentGateway201JSONResponse(h.toConnection(store, c)), nil
}

// DisconnectPaymentGateway implements StrictServerInterface.
func (h *Handlers) DisconnectPaymentGateway(ctx context.Context, req DisconnectPaymentGatewayRequestObject) (DisconnectPaymentGatewayResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPaymentsManage); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.ConnectionId)
	if err != nil {
		return nil, app.ErrConnectionNotFound
	}
	if err = h.svc.Disconnect(ctx, id); err != nil {
		return nil, err
	}
	return DisconnectPaymentGateway204Response{}, nil
}

// ListPayments implements StrictServerInterface.
func (h *Handlers) ListPayments(ctx context.Context, req ListPaymentsRequestObject) (ListPaymentsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	if req.Params.OrderId != nil {
		id, err := ulid.Parse(*req.Params.OrderId)
		if err != nil {
			return ListPayments200JSONResponse{Items: []Payment{}}, nil // no payment has that order
		}
		list, err := h.svc.OrderPayments(ctx, id)
		if err != nil {
			return nil, err
		}
		return ListPayments200JSONResponse{Items: toPayments(list)}, nil
	}
	var after *domain.Payment
	if c := deref(req.Params.Cursor); c != "" {
		at, id, err := cursor.Decode(c)
		if err != nil {
			return nil, errBadCursor
		}
		after = &domain.Payment{ID: id, CreatedAt: at}
	}
	p, err := h.svc.Payments(ctx, after, deref(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	out := ListPayments200JSONResponse{Items: toPayments(p.Items)}
	if p.Next != nil {
		c := cursor.Encode(p.Next.CreatedAt, p.Next.ID)
		out.NextCursor = &c
	}
	return out, nil
}

// GetPayment implements StrictServerInterface.
func (h *Handlers) GetPayment(ctx context.Context, req GetPaymentRequestObject) (GetPaymentResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.PaymentId)
	if err != nil {
		return nil, app.ErrPaymentNotFound
	}
	p, refunds, err := h.svc.Payment(ctx, id)
	if err != nil {
		return nil, err
	}
	out := GetPayment200JSONResponse{Payment: toPayment(p), Refunds: make([]PaymentRefund, len(refunds))}
	for i, r := range refunds {
		out.Refunds[i] = toRefund(r)
	}
	return out, nil
}

// RefundPayment implements StrictServerInterface.
func (h *Handlers) RefundPayment(ctx context.Context, req RefundPaymentRequestObject) (RefundPaymentResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPaymentsRefund); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	id, err := ulid.Parse(req.PaymentId)
	if err != nil {
		return nil, app.ErrPaymentNotFound
	}
	r, err := h.svc.Refund(ctx, id, req.Body.AmountMinor, deref(req.Body.Reason))
	if err != nil {
		return nil, err
	}
	return RefundPayment202JSONResponse(toRefund(r)), nil
}

func (h *Handlers) toConnection(store tenant.StoreID, c domain.Connection) PaymentConnection {
	out := PaymentConnection{Id: c.ID.String(), Gateway: c.Gateway, Status: PaymentConnectionStatus(c.Status), CreatedAt: c.CreatedAt,
		DisconnectedAt: c.DisconnectedAt}
	if c.Status == domain.ConnectionActive {
		u := h.svc.WebhookURL(store, c)
		out.WebhookUrl = &u
	}
	return out
}

func toPayments(list []domain.Payment) []Payment {
	out := make([]Payment, len(list))
	for i, p := range list {
		out[i] = toPayment(p)
	}
	return out
}

func toPayment(p domain.Payment) Payment {
	return Payment{Id: p.ID.String(), OrderId: p.OrderID.String(), ConnectionId: p.ConnectionID.String(), Gateway: p.Gateway,
		Status: PaymentStatus(p.Status), AmountMinor: p.Amount.Amount(), RefundedMinor: p.Refunded.Amount(),
		Currency: string(p.Amount.Currency()), TransactionId: opt(p.TransactionID), Failure: opt(p.Failure), StatusAt: p.StatusAt,
		CreatedAt: p.CreatedAt}
}

func toRefund(r domain.Refund) PaymentRefund {
	return PaymentRefund{Id: r.ID.String(), PaymentId: r.PaymentID.String(), AmountMinor: r.Amount.Amount(),
		Currency: string(r.Amount.Currency()), Status: PaymentRefundStatus(r.Status), Reason: r.Reason, Failure: opt(r.Failure),
		CreatedAt: r.CreatedAt}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
