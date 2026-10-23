// Package httpapi serves the shipping tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
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
	errBodyRequired    = errs.BadRequest("request body is required", nil)
	errVersionRequired = errs.Validation(errs.FieldError{Field: "version", Code: "required", Message: "send the version you read"})
)

// Mount registers the shipping routes on mux. scope is the membership
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

// GetShipping implements StrictServerInterface.
func (h *Handlers) GetShipping(ctx context.Context, _ GetShippingRequestObject) (GetShippingResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingRead); err != nil {
		return nil, err
	}
	cfg, err := h.svc.Config(ctx)
	if err != nil {
		return nil, err
	}
	out := GetShipping200JSONResponse{Currency: string(cfg.Currency), Settings: toSettings(cfg.Settings), Zones: make([]ShippingZone, len(cfg.Zones))}
	for i, z := range cfg.Zones {
		out.Zones[i] = toZone(z)
	}
	return out, nil
}

// SaveShippingSettings implements StrictServerInterface.
func (h *Handlers) SaveShippingSettings(ctx context.Context, req SaveShippingSettingsRequestObject) (SaveShippingSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	s, err := h.svc.SaveSettings(ctx, domain.Settings{CODFee: fromFee(req.Body.CodFee), FreeOverMinor: req.Body.FreeOverMinor}, req.Body.Version)
	if err != nil {
		return nil, err
	}
	return SaveShippingSettings200JSONResponse(toSettings(s)), nil
}

// CreateShippingZone implements StrictServerInterface.
func (h *Handlers) CreateShippingZone(ctx context.Context, req CreateShippingZoneRequestObject) (CreateShippingZoneResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	z, err := h.svc.CreateZone(ctx, fromZoneInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return CreateShippingZone201JSONResponse(toZone(z)), nil
}

// UpdateShippingZone implements StrictServerInterface.
func (h *Handlers) UpdateShippingZone(ctx context.Context, req UpdateShippingZoneRequestObject) (UpdateShippingZoneResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	if req.Body.Version == nil {
		return nil, errVersionRequired
	}
	id, err := ulid.Parse(req.ZoneId)
	if err != nil {
		return nil, app.ErrZoneNotFound
	}
	z, err := h.svc.UpdateZone(ctx, id, fromZoneInput(*req.Body), *req.Body.Version)
	if err != nil {
		return nil, err
	}
	return UpdateShippingZone200JSONResponse(toZone(z)), nil
}

// DeleteShippingZone implements StrictServerInterface.
func (h *Handlers) DeleteShippingZone(ctx context.Context, req DeleteShippingZoneRequestObject) (DeleteShippingZoneResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingManage); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.ZoneId)
	if err != nil {
		return nil, app.ErrZoneNotFound
	}
	if err = h.svc.DeleteZone(ctx, id); err != nil {
		return nil, err
	}
	return DeleteShippingZone204Response{}, nil
}

// QuoteShipping implements StrictServerInterface.
func (h *Handlers) QuoteShipping(ctx context.Context, req QuoteShippingRequestObject) (QuoteShippingResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermShippingRead); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	city := ""
	if req.Body.City != nil {
		city = *req.Body.City
	}
	q, err := h.svc.QuoteMinor(ctx, req.Body.Governorate, city, req.Body.SubtotalMinor)
	if err != nil {
		return nil, err
	}
	out := QuoteShipping200JSONResponse{Deliverable: q.Deliverable, ShippingMinor: q.Shipping.Amount(), FreeShipping: q.FreeShipping,
		CodFeeMinor: q.CODFee.Amount(), Currency: string(q.Shipping.Currency())}
	if q.ZoneID != nil {
		id := q.ZoneID.String()
		out.ZoneId = &id
	}
	return out, nil
}

func fromFee(f CODFee) domain.CODFee {
	return domain.CODFee{FixedMinor: f.FixedMinor, BP: int64(f.Bp), MinMinor: f.MinMinor, MaxMinor: f.MaxMinor}
}

func toFee(f domain.CODFee) CODFee {
	return CODFee{FixedMinor: f.FixedMinor, Bp: int(f.BP), MinMinor: f.MinMinor, MaxMinor: f.MaxMinor}
}

func fromZoneInput(in ShippingZoneInput) domain.ZoneInput {
	z := domain.ZoneInput{Name: in.Name, Areas: in.Areas, RateMinor: in.RateMinor, FreeOverMinor: in.FreeOverMinor}
	if in.CodFee != nil {
		f := fromFee(*in.CodFee)
		z.CODFee = &f
	}
	if in.Cities != nil {
		for _, c := range *in.Cities {
			z.Cities = append(z.Cities, domain.CityInput{Name: c.Name, RateMinor: c.RateMinor})
		}
	}
	return z
}

func toSettings(s domain.Settings) ShippingSettings {
	return ShippingSettings{CodFee: toFee(s.CODFee), FreeOverMinor: s.FreeOverMinor, Version: s.Version}
}

func toZone(z domain.Zone) ShippingZone {
	out := ShippingZone{Id: z.ID.String(), Name: z.Name, Areas: z.Areas, RateMinor: z.RateMinor, FreeOverMinor: z.FreeOverMinor,
		Cities: make([]ShippingCity, len(z.Cities)), Position: z.Position, Version: z.Version}
	if out.Areas == nil {
		out.Areas = []string{}
	}
	if z.CODFee != nil {
		f := toFee(*z.CODFee)
		out.CodFee = &f
	}
	for i, c := range z.Cities {
		out.Cities[i] = ShippingCity{Name: c.Name, RateMinor: c.RateMinor}
	}
	return out
}
