// Package httpapi serves the tenancy tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Handlers implements StrictServerInterface.
type Handlers struct {
	svc  *app.Service
	edge domain.Edge // where custom domains point (ADR-0037)
	auth authz.Authorizer
}

var _ StrictServerInterface = (*Handlers)(nil)

// Mount registers the tenancy routes on mux. scope is the membership
// middleware: it authenticates, returns 404 to non-members, and sets the
// tenant and principal (ADR-0006). edge is where custom domains point.
func Mount(mux *http.ServeMux, svc *app.Service, edge domain.Edge, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		resp.Write(w, r, errs.BadRequest("invalid request", err))
	}
	strict := NewStrictHandlerWithOptions(&Handlers{svc: svc, edge: edge, auth: auth}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: resp.Write,
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter:       mux,
		Middlewares:      []MiddlewareFunc{MiddlewareFunc(scope)},
		ErrorHandlerFunc: badRequest,
	})
}

var errDomainNotFound = errs.NotFound("domain_not_found", "domain not found")

// domainID parses a path ID. A malformed ID is simply not found.
func domainID(s string) (ulid.ID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return ulid.ID{}, errDomainNotFound
	}
	return id, nil
}

// GetStore implements StrictServerInterface.
func (h *Handlers) GetStore(ctx context.Context, _ GetStoreRequestObject) (GetStoreResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermStoreRead); err != nil {
		return nil, err
	}
	st, err := h.svc.GetStore(ctx)
	if err != nil {
		return nil, err
	}
	return GetStore200JSONResponse(toStore(st)), nil
}

// UpdateStore implements StrictServerInterface.
func (h *Handlers) UpdateStore(ctx context.Context, req UpdateStoreRequestObject) (UpdateStoreResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermStoreUpdate); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	st, err := h.svc.UpdateSettings(ctx, domain.StoreSettings{
		Name: req.Body.Name, Locale: string(req.Body.Locale), Timezone: string(req.Body.Timezone),
	})
	if err != nil {
		return nil, err
	}
	return UpdateStore200JSONResponse(toStore(st)), nil
}

// ListDomains implements StrictServerInterface.
func (h *Handlers) ListDomains(ctx context.Context, _ ListDomainsRequestObject) (ListDomainsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermDomainsRead); err != nil {
		return nil, err
	}
	ds, err := h.svc.ListDomains(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Domain, len(ds))
	for i, d := range ds {
		items[i] = h.toDomain(d)
	}
	return ListDomains200JSONResponse{Items: items}, nil
}

// AddDomain implements StrictServerInterface.
func (h *Handlers) AddDomain(ctx context.Context, req AddDomainRequestObject) (AddDomainResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermDomainsManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	d, err := h.svc.AddCustomDomain(ctx, req.Body.Host)
	if err != nil {
		return nil, err
	}
	return AddDomain201JSONResponse(h.toDomain(d)), nil
}

// DeleteDomain implements StrictServerInterface.
func (h *Handlers) DeleteDomain(ctx context.Context, req DeleteDomainRequestObject) (DeleteDomainResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermDomainsManage); err != nil {
		return nil, err
	}
	id, err := domainID(req.DomainId)
	if err != nil {
		return nil, err
	}
	if err := h.svc.RemoveDomain(ctx, id); err != nil {
		return nil, err
	}
	return DeleteDomain204Response{}, nil
}

// VerifyDomain implements StrictServerInterface.
func (h *Handlers) VerifyDomain(ctx context.Context, req VerifyDomainRequestObject) (VerifyDomainResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermDomainsManage); err != nil {
		return nil, err
	}
	id, err := domainID(req.DomainId)
	if err != nil {
		return nil, err
	}
	d, err := h.svc.VerifyDomain(ctx, id)
	if err != nil {
		return nil, err
	}
	return VerifyDomain202JSONResponse(h.toDomain(d)), nil
}

// SetPrimaryDomain implements StrictServerInterface.
func (h *Handlers) SetPrimaryDomain(ctx context.Context, req SetPrimaryDomainRequestObject) (SetPrimaryDomainResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermDomainsManage); err != nil {
		return nil, err
	}
	id, err := domainID(req.DomainId)
	if err != nil {
		return nil, err
	}
	d, err := h.svc.SetPrimaryDomain(ctx, id)
	if err != nil {
		return nil, err
	}
	return SetPrimaryDomain200JSONResponse(h.toDomain(d)), nil
}

func toStore(s domain.Store) Store {
	return Store{
		Id: s.ID.String(), Slug: s.Slug, Name: s.Name,
		Status: StoreStatus(s.Status), Plan: StorePlan(s.Plan), Currency: StoreCurrency(s.Currency),
		Locale: StoreLocale(s.Locale), Timezone: s.Timezone,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func (h *Handlers) toDomain(d domain.Domain) Domain {
	out := Domain{
		Id: d.ID.String(), Host: d.Host, Kind: DomainKind(d.Kind), Status: DomainStatus(d.Status),
		IsPrimary: d.IsPrimary, VerifiedAt: d.VerifiedAt, LastCheckedAt: d.LastCheckedAt, CreatedAt: d.CreatedAt,
	}
	if d.FailureReason != "" {
		r := DomainFailureReason(d.FailureReason)
		out.FailureReason = &r
	}
	if d.Kind == domain.KindCustom && d.Status != domain.DomainVerified {
		name, value := d.VerificationRecord()
		out.Verification = &DomainVerification{RecordType: TXT, Name: name, Value: value}
	}
	if d.Kind == domain.KindCustom {
		r := h.edge.RoutingFor(d.Host)
		records := make([]DnsRecord, len(r.Records))
		for i, rec := range r.Records {
			records[i] = DnsRecord{Type: DnsRecordType(rec.Type), Name: rec.Name, Value: rec.Value}
		}
		out.Routing = &DomainRouting{Apex: r.Apex, Records: records}
	}
	return out
}
