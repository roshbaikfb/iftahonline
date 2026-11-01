// Package httpapi serves the orders tag of api/openapi.yaml. Handlers
// authorize first (transitions check each move's own permission in the
// service), take the store only from the context (set by the membership
// middleware, never from the path), and return errs errors that the
// problem package renders.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
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

// Mount registers the orders routes on mux. scope is the membership
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

// CountOrders implements StrictServerInterface.
func (h *Handlers) CountOrders(ctx context.Context, _ CountOrdersRequestObject) (CountOrdersResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	c, err := h.svc.Counts(ctx)
	if err != nil {
		return nil, err
	}
	return CountOrders200JSONResponse{Pending: c[domain.StatusPending], Confirmed: c[domain.StatusConfirmed], ReadyToShip: c[domain.StatusReadyToShip],
		Shipped: c[domain.StatusShipped], Delivered: c[domain.StatusDelivered], Cancelled: c[domain.StatusCancelled],
		FailedDelivery: c[domain.StatusFailedDelivery], Returned: c[domain.StatusReturned]}, nil
}

// GetOrderStats implements StrictServerInterface.
func (h *Handlers) GetOrderStats(ctx context.Context, req GetOrderStatsRequestObject) (GetOrderStatsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	r := domain.RangeToday
	if req.Params.Range != nil {
		var ok bool
		if r, ok = domain.ParseStatsRange(string(*req.Params.Range)); !ok {
			return nil, errs.Validation(errs.FieldError{Field: "range", Code: "invalid", Message: "range must be today, 7d or 30d"})
		}
	}
	st, err := h.svc.Stats(ctx, r)
	if err != nil {
		return nil, err
	}
	tally := func(t domain.Tally) OrderTally { return OrderTally{Orders: t.Orders, TotalMinor: t.TotalMinor} }
	out := GetOrderStats200JSONResponse{
		Range: OrderStatsRange(st.Range), Currency: st.Currency, From: st.Windows.From, To: st.Windows.To,
		Sales: tally(st.Sales), CodOrders: st.COD, CardOrders: st.Card, Previous: tally(st.Previous),
		Cancelled: tally(st.Cancelled), Returned: tally(st.Returned), OldestPendingAt: st.OldestPending,
	}
	out.Daily = make([]struct {
		Date       openapi_types.Date `json:"date"`
		Orders     int                `json:"orders"`
		SalesMinor int64              `json:"sales_minor"`
	}, len(st.Daily))
	for i, d := range st.Daily {
		day, err := time.Parse(time.DateOnly, d.Date)
		if err != nil {
			return nil, fmt.Errorf("orders: day %q: %w", d.Date, err)
		}
		out.Daily[i].Date, out.Daily[i].Orders, out.Daily[i].SalesMinor = openapi_types.Date{Time: day}, d.Orders, d.SalesMinor
	}
	return out, nil
}

// ListOrders implements StrictServerInterface.
func (h *Handlers) ListOrders(ctx context.Context, req ListOrdersRequestObject) (ListOrdersResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	p := req.Params
	q, err := query(p.Status, p.From, p.To, p.Governorate)
	if err != nil {
		return nil, err
	}
	q.Phone, q.Limit = deref(p.Phone), deref(p.Limit)
	if p.CustomerId != nil {
		id, parseErr := ulid.Parse(*p.CustomerId)
		if parseErr != nil {
			return nil, errs.Validation(errs.FieldError{Field: "customer_id", Code: "invalid", Message: "invalid customer_id"})
		}
		q.CustomerID = &id
	}
	if c := deref(p.Cursor); c != "" {
		before, parseErr := ulid.Parse(c)
		if parseErr != nil {
			return nil, errBadCursor
		}
		q.Before = &before
	}
	page, err := h.svc.List(ctx, q)
	if err != nil {
		return nil, err
	}
	out := ListOrders200JSONResponse{Items: make([]Order, len(page.Items))}
	for i, o := range page.Items {
		out.Items[i] = toOrder(o)
	}
	if page.Next != nil {
		next := page.Next.String()
		out.NextCursor = &next
	}
	return out, nil
}

// CreateOrder implements StrictServerInterface.
func (h *Handlers) CreateOrder(ctx context.Context, req CreateOrderRequestObject) (CreateOrderResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	b := req.Body
	in := domain.NewOrder{IdempotencyKey: deref(req.Params.IdempotencyKey), Source: domain.SourceMerchant,
		Name: b.Name, Phone: b.Phone, Governorate: b.Governorate, City: b.City, Address: b.Address, Notes: deref(b.Notes),
		CouponCode: deref(b.CouponCode)}
	for i, l := range b.Lines {
		id, err := ulid.Parse(l.VariantId)
		if err != nil {
			return nil, errs.Validation(errs.FieldError{Field: fmt.Sprintf("lines[%d].variant_id", i), Code: "invalid", Message: "invalid variant_id"})
		}
		in.Lines = append(in.Lines, domain.LineInput{VariantID: id, Qty: l.Qty})
	}
	p, err := h.svc.PlaceOrder(ctx, in)
	if err != nil {
		return nil, err
	}
	detail, err := h.svc.GetDetail(ctx, p.Order.ID)
	if err != nil {
		return nil, err
	}
	d := OrderDetail{Order: toOrder(detail.Order), Lines: toLines(detail.Order.Lines), Events: toEvents(detail.Events)}
	if p.Replayed || p.Duplicate {
		replayed := "true"
		return CreateOrder200JSONResponse{Body: d, Headers: CreateOrder200ResponseHeaders{IdempotentReplayed: &replayed}}, nil
	}
	return CreateOrder201JSONResponse(d), nil
}

// GetOrder implements StrictServerInterface.
func (h *Handlers) GetOrder(ctx context.Context, req GetOrderRequestObject) (GetOrderResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersRead); err != nil {
		return nil, err
	}
	id, err := orderID(req.OrderId)
	if err != nil {
		return nil, err
	}
	d, err := h.svc.GetDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetOrder200JSONResponse{Order: toOrder(d.Order), Lines: toLines(d.Order.Lines), Events: toEvents(d.Events)}, nil
}

// TransitionOrder implements StrictServerInterface. The service checks
// the move's own permission.
func (h *Handlers) TransitionOrder(ctx context.Context, req TransitionOrderRequestObject) (TransitionOrderResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	id, err := orderID(req.OrderId)
	if err != nil {
		return nil, err
	}
	to, err := domain.ParseStatus(string(req.Body.Status))
	if err != nil {
		return nil, err
	}
	o, err := h.svc.TransitionWithReason(ctx, id, to, deref(req.Body.Note), string(deref(req.Body.Reason)), deref(req.Body.Version))
	if err != nil {
		return nil, err
	}
	return TransitionOrder200JSONResponse(toOrder(o)), nil
}

// ReleaseOrder implements StrictServerInterface.
func (h *Handlers) ReleaseOrder(ctx context.Context, req ReleaseOrderRequestObject) (ReleaseOrderResponseObject, error) {
	id, err := orderID(req.OrderId)
	if err != nil {
		return nil, err
	}
	o, err := h.svc.ReleaseHold(ctx, id)
	if err != nil {
		return nil, err
	}
	return ReleaseOrder200JSONResponse(toOrder(o)), nil
}

// BulkTransitionOrders implements StrictServerInterface.
func (h *Handlers) BulkTransitionOrders(ctx context.Context, req BulkTransitionOrdersRequestObject) (BulkTransitionOrdersResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	to, err := domain.ParseStatus(string(req.Body.Status))
	if err != nil {
		return nil, err
	}
	ids := make([]ulid.ID, len(req.Body.OrderIds))
	for i, s := range req.Body.OrderIds {
		if ids[i], err = ulid.Parse(s); err != nil {
			return nil, errs.Validation(errs.FieldError{Field: fmt.Sprintf("order_ids[%d]", i), Code: "invalid", Message: "invalid order id"})
		}
	}
	res, err := h.svc.BulkTransition(ctx, ids, to, deref(req.Body.Note), string(deref(req.Body.Reason)))
	if err != nil {
		return nil, err
	}
	out := BulkTransitionOrders200JSONResponse{}
	out.Results = make([]BulkResult, len(res))
	for i, r := range res {
		item := BulkResult{OrderId: r.ID.String(), Ok: r.Err == nil}
		if r.Err == nil {
			o := toOrder(r.Order)
			item.Order = &o
		} else {
			item.Error = &BulkError{Code: errorCode(r.Err), Detail: errorDetail(r.Err)}
		}
		out.Results[i] = item
	}
	return out, nil
}

// ExportOrders implements StrictServerInterface. The file streams through
// a pipe: Export has already validated and audited, so later failures
// only cut the file short.
func (h *Handlers) ExportOrders(ctx context.Context, req ExportOrdersRequestObject) (ExportOrdersResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermOrdersExport); err != nil {
		return nil, err
	}
	p := req.Params
	q, err := query(p.Status, p.From, p.To, p.Governorate)
	if err != nil {
		return nil, err
	}
	ex, err := h.svc.Export(ctx, q)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		_, werr := ex.WriteTo(pw)
		pw.CloseWithError(werr)
	}()
	name := fmt.Sprintf(`attachment; filename="orders-%s.csv"`, time.Now().UTC().Format("2006-01-02"))
	return ExportOrders200TextcsvResponse{Body: pr, Headers: ExportOrders200ResponseHeaders{ContentDisposition: &name}}, nil
}

func query(status *OrderStatus, from, to *time.Time, gov *string) (domain.Query, error) {
	q := domain.Query{From: from, To: to, Governorate: deref(gov)}
	if status != nil {
		s, err := domain.ParseStatus(string(*status))
		if err != nil {
			return domain.Query{}, err
		}
		q.Status = &s
	}
	return q, nil
}

// orderID parses a path ID; a malformed one is simply not found.
func orderID(s string) (ulid.ID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return ulid.ID{}, app.ErrOrderNotFound
	}
	return id, nil
}

// errorCode and errorDetail describe one bulk failure as a problem would.
func errorCode(err error) string {
	var e *errs.Error
	if errors.As(err, &e) && e.Code != "" {
		return e.Code
	}
	return "internal"
}

func errorDetail(err error) string {
	var e *errs.Error
	if errors.As(err, &e) && e.Kind != errs.KindInternal {
		return e.Message
	}
	return "the order could not be moved"
}

// toAttribution is nil for an order no campaign brought.
func toAttribution(a domain.Attribution) *OrderAttribution {
	if a.IsZero() {
		return nil
	}
	opt := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	return &OrderAttribution{Source: opt(a.Source), Medium: opt(a.Medium), Campaign: opt(a.Campaign), Content: opt(a.Content), Term: opt(a.Term)}
}

func toOrder(o domain.Order) Order {
	c := o.Contact
	var code *string
	if o.CouponCode != "" {
		code = &o.CouponCode
	}
	discount := int64(0)
	if o.Discount.Currency() != "" {
		discount = o.Discount.Amount()
	}
	var reason *OrderOutcomeReason
	if o.OutcomeReason != domain.ReasonNone {
		r := OrderOutcomeReason(o.OutcomeReason)
		reason = &r
	}
	var action *OrderRiskAction
	if o.RiskAction != domain.RiskNone && o.RiskAction != domain.RiskPrepay { // a prepay shows as the deposit (ADR-0067)
		a := OrderRiskAction(o.RiskAction)
		action = &a
	}
	var band *RiskBand
	if o.RiskBand != "" {
		b := RiskBand(o.RiskBand)
		band = &b
	}
	var depositStatus *OrderDepositStatus
	if o.DepositStatus != "" {
		ds := OrderDepositStatus(o.DepositStatus)
		depositStatus = &ds
	}
	deposit := o.Deposit.Amount()
	return Order{
		OutcomeReason: reason, RiskAction: action, RiskBand: band, DepositMinor: &deposit, DepositStatus: depositStatus,
		DiscountMinor: &discount, CouponCode: code, Attribution: toAttribution(o.Attribution),
		Id: o.ID.String(), Code: o.Code(), Status: OrderStatus(o.Status), Version: o.Version, Source: OrderSource(o.Source),
		CustomerId: o.CustomerID.String(), Name: c.Name, Phone: c.Phone.Unmasked(), Governorate: c.Governorate, City: c.City,
		Address: c.Address, Notes: c.Notes, Currency: string(o.Total.Currency()), SubtotalMinor: o.Subtotal.Amount(),
		ShippingMinor: o.Shipping.Amount(), CodFeeMinor: o.CODFee.Amount(), TotalMinor: o.Total.Amount(), ItemCount: o.ItemCount,
		PaymentMethod: OrderPaymentMethod(o.PaymentMethod), PaymentStatus: OrderPaymentStatus(o.PaymentStatus),
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, StatusChangedAt: o.StatusChangedAt,
	}
}

func toLines(ls []domain.Line) []OrderLine {
	out := make([]OrderLine, len(ls))
	for i, l := range ls {
		out[i] = OrderLine{VariantId: l.VariantID.String(), ProductId: l.ProductID.String(), Title: l.Title, VariantTitle: l.VariantTitle,
			Sku: l.SKU, Qty: l.Qty, UnitPriceMinor: l.UnitPrice.Amount(), TotalMinor: l.Total.Amount()}
	}
	return out
}

func toEvents(es []domain.Event) []OrderEvent {
	out := make([]OrderEvent, len(es))
	for i, e := range es {
		out[i] = OrderEvent{Id: e.ID.String(), To: OrderStatus(e.To), Note: e.Note, At: e.At}
		if e.From != "" {
			from := OrderStatus(e.From)
			out[i].From = &from
		}
		if e.ActorUserID != nil {
			a := e.ActorUserID.String()
			out[i].ActorUserId = &a
		}
		if e.Actor != "" {
			a := e.Actor
			out[i].Actor = &a
		}
	}
	return out
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
