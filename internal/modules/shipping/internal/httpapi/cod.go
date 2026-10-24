package httpapi

import (
	"context"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/cursor"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// defaultReportSpan is the report's period when none is given.
const defaultReportSpan = 30 * 24 * time.Hour

// GetCODReport implements StrictServerInterface.
func (h *Handlers) GetCODReport(ctx context.Context, req GetCODReportRequestObject) (GetCODReportResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceRead); err != nil {
		return nil, err
	}
	to := time.Now().UTC()
	if req.Params.To != nil {
		to = *req.Params.To
	}
	from := to.Add(-defaultReportSpan)
	if req.Params.From != nil {
		from = *req.Params.From
	}
	r, err := h.svc.Report(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := GetCODReport200JSONResponse{From: r.From, To: r.To, Carriers: make([]CODCarrier, len(r.Carriers)),
		UnmatchedLines: r.UnmatchedLines, UnmatchedMinor: r.UnmatchedAmount.Amount()}
	for i, c := range r.Carriers {
		out.Carriers[i] = CODCarrier{ConnectionId: c.ConnectionID.String(), Carrier: c.Carrier, Currency: string(c.Currency),
			Delivered: c.Delivered, ExpectedMinor: c.Expected.Amount(), SettledMinor: c.Settled.Amount(), HeldMinor: c.Held.Amount(),
			Held07Minor: c.Held0to7.Amount(), Held814Minor: c.Held8to14.Amount(), Held15PlusMinor: c.Held15Plus.Amount(),
			OverSettled: c.OverSettled}
	}
	return out, nil
}

// ListHeldCOD implements StrictServerInterface.
func (h *Handlers) ListHeldCOD(ctx context.Context, req ListHeldCODRequestObject) (ListHeldCODResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceRead); err != nil {
		return nil, err
	}
	var after *app.Cursor
	if c := deref(req.Params.Cursor); c != "" {
		at, id, err := cursor.Decode(c)
		if err != nil {
			return nil, errBadCursor
		}
		after = &domain.Cursor{At: at, ID: id}
	}
	p, err := h.svc.Held(ctx, after, deref(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	out := ListHeldCOD200JSONResponse{Items: make([]HeldCOD, len(p.Items))}
	for i, hp := range p.Items {
		out.Items[i] = HeldCOD{ShipmentId: hp.ShipmentID.String(), OrderId: hp.OrderID.String(), Carrier: hp.Carrier, TrackingNumber: hp.TrackingNumber,
			Currency: string(hp.Expected.Currency()), ExpectedMinor: hp.Expected.Amount(), SettledMinor: hp.Settled.Amount(),
			HeldMinor: max(hp.Expected.Amount()-hp.Settled.Amount(), 0), DeliveredAt: hp.DeliveredAt}
	}
	if p.Next != nil {
		c := cursor.Encode(p.Next.At, p.Next.ID)
		out.NextCursor = &c
	}
	return out, nil
}

// ImportCODStatement implements StrictServerInterface.
func (h *Handlers) ImportCODStatement(ctx context.Context, req ImportCODStatementRequestObject) (ImportCODStatementResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermFinanceManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	conn, err := ulid.Parse(req.Params.ConnectionId)
	if err != nil {
		return nil, app.ErrConnectionNotFound
	}
	got, err := h.svc.ImportStatement(ctx, conn, req.Body)
	if err != nil {
		return nil, err
	}
	out := ImportCODStatement200JSONResponse{Lines: got.Lines, Imported: got.Added.Imported, Duplicates: got.Added.Duplicates,
		Matched: got.Added.Matched}
	out.Errors = make([]struct {
		Line    int    `json:"line"`
		Message string `json:"message"`
	}, len(got.Errors))
	for i, e := range got.Errors {
		out.Errors[i].Line, out.Errors[i].Message = e.Line, e.Message
	}
	return out, nil
}
