package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errAgencyNotFound = errs.NotFound("agency_not_found", "agency not found")

func agencyID(raw string) (ulid.ID, error) {
	id, err := ulid.Parse(raw)
	if err != nil {
		return ulid.ID{}, errAgencyNotFound
	}
	return id, nil
}

// AdminListAgencies implements StrictServerInterface.
func (h *Handlers) AdminListAgencies(ctx context.Context, _ AdminListAgenciesRequestObject) (AdminListAgenciesResponseObject, error) {
	as, err := h.svc.Agencies(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminListAgencies200JSONResponse{Items: make([]AdminAgency, len(as))}
	for i, a := range as {
		out.Items[i] = toAgency(a)
	}
	return out, nil
}

// AdminCreateAgency implements StrictServerInterface.
func (h *Handlers) AdminCreateAgency(ctx context.Context, req AdminCreateAgencyRequestObject) (AdminCreateAgencyResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	d, err := h.svc.CreateAgency(ctx, b.Name, string(b.Currency), b.PlanCode, b.Reason)
	if err != nil {
		return nil, err
	}
	return AdminCreateAgency201JSONResponse(toAgencyDetail(d)), nil
}

// AdminGetAgency implements StrictServerInterface.
func (h *Handlers) AdminGetAgency(ctx context.Context, req AdminGetAgencyRequestObject) (AdminGetAgencyResponseObject, error) {
	id, err := agencyID(req.AgencyId)
	if err != nil {
		return nil, err
	}
	d, err := h.svc.Agency(ctx, id)
	if err != nil {
		return nil, err
	}
	return AdminGetAgency200JSONResponse(toAgencyDetail(d)), nil
}

// AdminAddAgencyStore implements StrictServerInterface.
func (h *Handlers) AdminAddAgencyStore(ctx context.Context, req AdminAddAgencyStoreRequestObject) (AdminAddAgencyStoreResponseObject, error) {
	id, err := agencyID(req.AgencyId)
	if err != nil {
		return nil, err
	}
	store, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	d, err := h.svc.AddStore(ctx, id, store, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminAddAgencyStore200JSONResponse(toAgencyDetail(d)), nil
}

// AdminRemoveAgencyStore implements StrictServerInterface.
func (h *Handlers) AdminRemoveAgencyStore(ctx context.Context, req AdminRemoveAgencyStoreRequestObject) (AdminRemoveAgencyStoreResponseObject, error) {
	id, err := agencyID(req.AgencyId)
	if err != nil {
		return nil, err
	}
	store, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	d, err := h.svc.RemoveStore(ctx, id, store, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminRemoveAgencyStore200JSONResponse(toAgencyDetail(d)), nil
}

func toAgency(a billing.Agency) AdminAgency {
	return AdminAgency{Id: a.ID.String(), Name: a.Name, Currency: AdminAgencyCurrency(a.Currency), PlanCode: a.PlanCode,
		Stores: a.Stores, CreatedAt: a.CreatedAt}
}

func toAgencyDetail(d app.AgencyDetail) AdminAgencyDetail {
	out := AdminAgencyDetail{Agency: toAgency(d.Agency), StoreIds: make([]ULID, len(d.Stores))}
	for i, s := range d.Stores {
		out.StoreIds[i] = s.String()
	}
	return out
}
