// Package httpapi serves the customers tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/domain"
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

var (
	errBodyRequired = errs.BadRequest("request body is required", nil)
	errBadCursor    = errs.Validation(errs.FieldError{Field: "cursor", Code: "invalid", Message: "invalid cursor"})
)

// Mount registers the customers routes on mux. scope is the membership
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

// ListCustomers implements StrictServerInterface.
func (h *Handlers) ListCustomers(ctx context.Context, req ListCustomersRequestObject) (ListCustomersResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCustomersRead); err != nil {
		return nil, err
	}
	q := domain.Query{Text: deref(req.Params.Q), Blocked: deref(req.Params.Blocked), Limit: deref(req.Params.Limit)}
	if c := deref(req.Params.Cursor); c != "" {
		after, err := ulid.Parse(c)
		if err != nil {
			return nil, errBadCursor
		}
		q.After = &after
	}
	p, err := h.svc.List(ctx, q)
	if err != nil {
		return nil, err
	}
	out := ListCustomers200JSONResponse{Items: make([]Customer, len(p.Items))}
	for i, c := range p.Items {
		out.Items[i] = toCustomer(c)
	}
	if p.Next != nil {
		next := p.Next.String()
		out.NextCursor = &next
	}
	return out, nil
}

// BlockPhone implements StrictServerInterface.
func (h *Handlers) BlockPhone(ctx context.Context, req BlockPhoneRequestObject) (BlockPhoneResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCustomersManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	c, err := h.svc.BlockPhone(ctx, req.Body.Phone, deref(req.Body.Reason))
	if err != nil {
		return nil, err
	}
	return BlockPhone200JSONResponse(toCustomer(c)), nil
}

// GetCustomer implements StrictServerInterface.
func (h *Handlers) GetCustomer(ctx context.Context, req GetCustomerRequestObject) (GetCustomerResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCustomersRead); err != nil {
		return nil, err
	}
	id, err := customerID(req.CustomerId)
	if err != nil {
		return nil, err
	}
	c, err := h.svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetCustomer200JSONResponse(toCustomer(c)), nil
}

// BlockCustomer implements StrictServerInterface.
func (h *Handlers) BlockCustomer(ctx context.Context, req BlockCustomerRequestObject) (BlockCustomerResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCustomersManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	id, err := customerID(req.CustomerId)
	if err != nil {
		return nil, err
	}
	c, err := h.svc.Block(ctx, id, deref(req.Body.Reason))
	if err != nil {
		return nil, err
	}
	return BlockCustomer200JSONResponse(toCustomer(c)), nil
}

// UnblockCustomer implements StrictServerInterface.
func (h *Handlers) UnblockCustomer(ctx context.Context, req UnblockCustomerRequestObject) (UnblockCustomerResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCustomersManage); err != nil {
		return nil, err
	}
	id, err := customerID(req.CustomerId)
	if err != nil {
		return nil, err
	}
	c, err := h.svc.Unblock(ctx, id)
	if err != nil {
		return nil, err
	}
	return UnblockCustomer200JSONResponse(toCustomer(c)), nil
}

// customerID parses a path ID; a malformed one is simply not found.
func customerID(s string) (ulid.ID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return ulid.ID{}, app.ErrCustomerNotFound
	}
	return id, nil
}

// toCustomer is the merchant's view, with the full phone: it is their
// customer, and they call them.
func toCustomer(c domain.Customer) Customer {
	return Customer{
		Id: c.ID.String(), Phone: c.Phone.Unmasked(), Name: c.Name, OrdersCount: c.OrdersCount, LastOrderAt: c.LastOrderAt,
		Blocked: c.Blocked(), BlockedAt: c.BlockedAt, BlockReason: c.BlockReason, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
