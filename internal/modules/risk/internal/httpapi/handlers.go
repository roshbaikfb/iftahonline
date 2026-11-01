// Package httpapi serves the risk tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders. They answer with bands and the
// store's own history only (ADR-0065).
package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
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

// Mount registers the risk routes on mux. scope is the membership
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

// GetOrderRisk implements StrictServerInterface.
func (h *Handlers) GetOrderRisk(ctx context.Context, req GetOrderRiskRequestObject) (GetOrderRiskResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.OrderId)
	if err != nil {
		return nil, app.ErrOrderNotFound
	}
	r, err := h.svc.ForOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	hist := r.History
	out := GetOrderRisk200JSONResponse{Band: RiskBand(r.Band), History: RiskHistory{
		Orders: hist.Orders, Delivered: hist.Outcomes[domain.Delivered], Returned: hist.Outcomes[domain.Returned],
		Refused: hist.Outcomes[domain.Refused], NoAnswer: hist.Outcomes[domain.NoAnswer], Unconfirmed: hist.Outcomes[domain.Unconfirmed],
	}}
	if !hist.LastAt.IsZero() {
		out.History.LastOutcomeAt = &hist.LastAt
	}
	return out, nil
}

// ListRiskBands implements StrictServerInterface.
func (h *Handlers) ListRiskBands(ctx context.Context, req ListRiskBandsRequestObject) (ListRiskBandsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	ids := make([]ulid.ID, len(req.Body.OrderIds))
	seen := make(map[ulid.ID]bool, len(ids))
	for i, s := range req.Body.OrderIds {
		id, err := ulid.Parse(s)
		if err != nil {
			return nil, errs.Validation(errs.FieldError{Field: fmt.Sprintf("order_ids[%d]", i), Code: "invalid", Message: "invalid order id"})
		}
		if seen[id] {
			return nil, errs.Validation(errs.FieldError{Field: "order_ids", Code: "duplicate", Message: "each order may appear once"})
		}
		seen[id] = true
		ids[i] = id
	}
	bands, err := h.svc.Bands(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := ListRiskBands200JSONResponse{}
	out.Bands = make([]struct {
		Band    RiskBand `json:"band"`
		OrderId ULID     `json:"order_id"`
	}, 0, len(bands))
	for _, id := range ids { // the request's order, for stable answers
		if b, ok := bands[id]; ok {
			out.Bands = append(out.Bands, struct {
				Band    RiskBand `json:"band"`
				OrderId ULID     `json:"order_id"`
			}{Band: RiskBand(b), OrderId: id.String()})
		}
	}
	return out, nil
}

// GetRiskSettings implements StrictServerInterface.
func (h *Handlers) GetRiskSettings(ctx context.Context, _ GetRiskSettingsRequestObject) (GetRiskSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	v, err := h.svc.Rules(ctx)
	if err != nil {
		return nil, err
	}
	return GetRiskSettings200JSONResponse(toSettings(v)), nil
}

// UpdateRiskSettings implements StrictServerInterface.
func (h *Handlers) UpdateRiskSettings(ctx context.Context, req UpdateRiskSettingsRequestObject) (UpdateRiskSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	v, err := h.svc.SaveRules(ctx, domain.Rules{HoldFrom: domain.Band(req.Body.HoldFrom), VerifyFrom: domain.Band(req.Body.VerifyFrom)})
	if err != nil {
		return nil, err
	}
	return UpdateRiskSettings200JSONResponse(toSettings(v)), nil
}

func toSettings(v app.RulesView) RiskSettings {
	return RiskSettings{HoldFrom: RiskRule(v.Rules.HoldFrom), VerifyFrom: RiskRule(v.Rules.VerifyFrom), Available: v.Available}
}
