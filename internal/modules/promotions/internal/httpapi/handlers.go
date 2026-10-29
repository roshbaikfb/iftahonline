// Package httpapi serves the promotions tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Handlers implements StrictServerInterface.
type Handlers struct {
	svc  *app.Service
	auth authz.Authorizer
}

var _ StrictServerInterface = (*Handlers)(nil)

var errBodyRequired = errs.BadRequest("request body is required", nil)

// Mount registers the promotions routes on mux. scope is the membership
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

// ListCoupons implements StrictServerInterface.
func (h *Handlers) ListCoupons(ctx context.Context, _ ListCouponsRequestObject) (ListCouponsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPromotionsManage); err != nil {
		return nil, err
	}
	list, err := h.svc.Coupons(ctx)
	if err != nil {
		return nil, err
	}
	out := ListCoupons200JSONResponse{Items: make([]Coupon, len(list))}
	for i, c := range list {
		out.Items[i] = toCoupon(c)
	}
	return out, nil
}

// CreateCoupon implements StrictServerInterface.
func (h *Handlers) CreateCoupon(ctx context.Context, req CreateCouponRequestObject) (CreateCouponResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPromotionsManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	c, err := h.svc.Create(ctx, fromInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return CreateCoupon201JSONResponse(toCoupon(c)), nil
}

// GetCoupon implements StrictServerInterface.
func (h *Handlers) GetCoupon(ctx context.Context, req GetCouponRequestObject) (GetCouponResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPromotionsManage); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.CouponId)
	if err != nil {
		return nil, app.ErrCouponNotFound
	}
	c, err := h.svc.Coupon(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetCoupon200JSONResponse(toCoupon(c)), nil
}

// UpdateCoupon implements StrictServerInterface.
func (h *Handlers) UpdateCoupon(ctx context.Context, req UpdateCouponRequestObject) (UpdateCouponResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPromotionsManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	id, err := ulid.Parse(req.CouponId)
	if err != nil {
		return nil, app.ErrCouponNotFound
	}
	c, err := h.svc.Update(ctx, id, fromInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return UpdateCoupon200JSONResponse(toCoupon(c)), nil
}

// DeleteCoupon implements StrictServerInterface.
func (h *Handlers) DeleteCoupon(ctx context.Context, req DeleteCouponRequestObject) (DeleteCouponResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermPromotionsManage); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.CouponId)
	if err != nil {
		return nil, app.ErrCouponNotFound
	}
	if err = h.svc.Delete(ctx, id); err != nil {
		return nil, err
	}
	return DeleteCoupon204Response{}, nil
}

func fromInput(in CouponInput) domain.Coupon {
	c := domain.Coupon{Code: in.Code, Kind: domain.Kind(in.Kind), MaxUses: in.MaxUses, MaxPerCustomer: in.MaxPerCustomer,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, Active: in.Active}
	if in.Percent != nil {
		c.Percent = *in.Percent
	}
	if in.AmountMinor != nil {
		c.AmountMinor = *in.AmountMinor
	}
	if in.MinSubtotalMinor != nil {
		c.MinSubtotalMinor = *in.MinSubtotalMinor
	}
	return c
}

func toCoupon(c domain.Coupon) Coupon {
	minSubtotal := c.MinSubtotalMinor
	out := Coupon{Id: c.ID.String(), Code: c.Code, Kind: CouponKind(c.Kind), MinSubtotalMinor: &minSubtotal, MaxUses: c.MaxUses,
		MaxPerCustomer: c.MaxPerCustomer, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Active: c.Active, Uses: c.Uses,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	switch c.Kind {
	case domain.Percent:
		p := c.Percent
		out.Percent = &p
	case domain.Fixed:
		a := c.AmountMinor
		out.AmountMinor = &a
	}
	return out
}
