// Package httpapi serves the analytics tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/store"
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

// Mount registers the analytics routes on mux. scope is the membership
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

// days turns the API's inclusive range into the service's [from, to).
func days(from, to openapi_types.Date) (time.Time, time.Time) {
	return from.Time, to.AddDate(0, 0, 1)
}

func figures(f domain.Figures) ProfitFigures {
	return ProfitFigures{Orders: f.Orders, Delivered: f.Delivered, Returned: f.Returned, Cancelled: f.Cancelled, Open: f.Open,
		CostMissing: f.CostMissing, RevenueMinor: f.Revenue, DeliveryPaidMinor: f.DeliveryPaid, ProductCostMinor: f.ProductCost,
		ShippingCostMinor: f.ShippingCost, CodFeesMinor: f.CODFees, ReturnCostMinor: f.ReturnCost, AdSpendMinor: f.AdSpend,
		ProfitMinor: f.Profit()}
}

// GetProfit implements StrictServerInterface.
func (h *Handlers) GetProfit(ctx context.Context, req GetProfitRequestObject) (GetProfitResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceRead); err != nil {
		return nil, err
	}
	by := domain.ByProduct
	if req.Params.By != nil {
		var err error
		if by, err = domain.ParseDimension(string(*req.Params.By)); err != nil {
			return nil, err
		}
	}
	from, to := days(req.Params.From, req.Params.To)
	r, err := h.svc.Profit(ctx, from, to, by)
	if err != nil {
		return nil, err
	}
	out := GetProfit200JSONResponse{Currency: ProfitReportCurrency(r.Currency), From: req.Params.From, To: req.Params.To,
		By: ProfitReportBy(by), Totals: figures(r.Totals), Days: make([]ProfitDay, len(r.Days)), Rows: make([]ProfitRow, len(r.Rows))}
	for i, d := range r.Days {
		out.Days[i] = ProfitDay{Day: openapi_types.Date{Time: d.Day}, Orders: d.Orders, RevenueMinor: d.Revenue + d.DeliveryPaid, ProfitMinor: d.Profit()}
	}
	for i, row := range r.Rows {
		out.Rows[i] = ProfitRow{Key: row.Key, Label: row.Label, Figures: figures(row.Figures)}
	}
	return out, nil
}

func settingsOut(s domain.Settings) ProfitSettings {
	return ProfitSettings{DeliveryCostMinor: s.DeliveryCost, ReturnCostMinor: s.ReturnCost, CodFeeBp: int(s.CODFeeBP)}
}

// GetProfitSettings implements StrictServerInterface.
func (h *Handlers) GetProfitSettings(ctx context.Context, _ GetProfitSettingsRequestObject) (GetProfitSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceRead); err != nil {
		return nil, err
	}
	s, err := h.svc.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return GetProfitSettings200JSONResponse(settingsOut(s)), nil
}

// UpdateProfitSettings implements StrictServerInterface.
func (h *Handlers) UpdateProfitSettings(ctx context.Context, req UpdateProfitSettingsRequestObject) (UpdateProfitSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	s, err := h.svc.SaveSettings(ctx, domain.Settings{DeliveryCost: req.Body.DeliveryCostMinor, ReturnCost: req.Body.ReturnCostMinor,
		CODFeeBP: int64(req.Body.CodFeeBp)})
	if err != nil {
		return nil, err
	}
	return UpdateProfitSettings200JSONResponse(settingsOut(s)), nil
}

func adSpendOut(a store.AdSpend) AdSpend {
	return AdSpend{Id: a.ID.String(), Day: openapi_types.Date{Time: a.Day}, Platform: AdSpendPlatform(a.Platform), Campaign: a.Campaign,
		Currency: a.Currency, AmountMinor: a.Amount, Source: AdSpendSource(a.Source), Note: a.Note, CreatedAt: a.CreatedAt}
}

// ListAdSpend implements StrictServerInterface.
func (h *Handlers) ListAdSpend(ctx context.Context, req ListAdSpendRequestObject) (ListAdSpendResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceRead); err != nil {
		return nil, err
	}
	from, to := days(req.Params.From, req.Params.To)
	list, err := h.svc.AdSpend(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := ListAdSpend200JSONResponse{Items: make([]AdSpend, len(list.Items)), Count: &list.Count, TotalMinor: &list.TotalMinor}
	for i, a := range list.Items {
		out.Items[i] = adSpendOut(a)
	}
	return out, nil
}

// CreateAdSpend implements StrictServerInterface.
func (h *Handlers) CreateAdSpend(ctx context.Context, req CreateAdSpendRequestObject) (CreateAdSpendResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	b := req.Body
	in := app.AdSpendInput{Day: b.Day.Time, Platform: string(b.Platform), AmountMinor: b.AmountMinor}
	if b.Campaign != nil {
		in.Campaign = *b.Campaign
	}
	if b.Note != nil {
		in.Note = *b.Note
	}
	a, err := h.svc.AddAdSpend(ctx, in)
	if err != nil {
		return nil, err
	}
	return CreateAdSpend201JSONResponse(adSpendOut(a)), nil
}

// DeleteAdSpend implements StrictServerInterface.
func (h *Handlers) DeleteAdSpend(ctx context.Context, req DeleteAdSpendRequestObject) (DeleteAdSpendResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceManage); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.AdSpendId)
	if err != nil {
		return nil, app.ErrNotFound
	}
	if err = h.svc.DeleteAdSpend(ctx, id); err != nil {
		return nil, err
	}
	return DeleteAdSpend204Response{}, nil
}
