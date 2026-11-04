package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
)

// AdminListAlerts implements StrictServerInterface.
func (h *Handlers) AdminListAlerts(ctx context.Context, _ AdminListAlertsRequestObject) (AdminListAlertsResponseObject, error) {
	p, err := h.svc.Alerts(ctx)
	if err != nil {
		return nil, err
	}
	return AdminListAlerts200JSONResponse{Open: toAlerts(p.Open), Resolved: toAlerts(p.Resolved), Email: p.Email}, nil
}

func toAlerts(vs []app.AlertView) []AdminAlert {
	out := make([]AdminAlert, len(vs))
	for i, v := range vs {
		out[i] = AdminAlert{Id: v.ID.String(), Kind: AdminAlertKind(v.Kind), Severity: AdminAlertSeverity(v.Severity), Value: v.Value,
			Title: v.Title, Body: v.Body, Path: v.Path, OpenedAt: v.OpenedAt, LastSeenAt: v.LastSeenAt, ResolvedAt: v.ResolvedAt}
	}
	return out
}

// AdminSetAlertEmail implements StrictServerInterface.
func (h *Handlers) AdminSetAlertEmail(ctx context.Context, req AdminSetAlertEmailRequestObject) (AdminSetAlertEmailResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.SetAlertEmail(ctx, req.Body.Enabled); err != nil {
		return nil, err
	}
	return AdminSetAlertEmail204Response{}, nil
}
