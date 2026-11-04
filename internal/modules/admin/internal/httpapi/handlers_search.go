package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
)

// AdminSearch implements StrictServerInterface.
func (h *Handlers) AdminSearch(ctx context.Context, req AdminSearchRequestObject) (AdminSearchResponseObject, error) {
	r, err := h.svc.Search(ctx, req.Params.Q)
	if err != nil {
		return nil, err
	}
	out := AdminSearch200JSONResponse{
		Stores: summaries(r.Stores), OwnerStores: summaries(r.OwnerStores),
		Invoices: make([]AdminInvoice, len(r.Invoices)), Payments: make([]AdminPayment, len(r.Payments)),
		Agencies: make([]AdminAgency, len(r.Agencies)), Admins: make([]AdminTeamMember, len(r.Admins)),
		Failed: make([]AdminSearchResultFailed, len(r.Failed)),
	}
	if r.Jump != "" {
		out.Jump = &r.Jump
	}
	for i, inv := range r.Invoices {
		out.Invoices[i] = toInvoice(inv)
	}
	for i, p := range r.Payments {
		out.Payments[i] = toPayment(p)
	}
	for i, a := range r.Agencies {
		out.Agencies[i] = toAgency(a)
	}
	for i, m := range r.Admins {
		out.Admins[i] = toMember(m)
	}
	for i, f := range r.Failed {
		out.Failed[i] = AdminSearchResultFailed(f)
	}
	return out, nil
}

func summaries(es []tenancy.DirectoryEntry) []AdminStoreSummary {
	out := make([]AdminStoreSummary, len(es))
	for i, e := range es {
		out[i] = AdminStoreSummary{Store: toStore(e.Store), PrimaryHost: e.PrimaryHost}
	}
	return out
}

func toMember(m store.Member) AdminTeamMember {
	return AdminTeamMember{Id: m.ID.String(), Name: m.Name, Email: m.Email, Role: AdminRole(m.Role), Active: m.Active,
		Resetting: m.Resetting, CreatedAt: m.CreatedAt, LastLoginAt: m.LastLoginAt}
}
