// Package httpapi serves the messaging tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/cursor"
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

// Mount registers the messaging routes on mux. scope is the membership
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

// ListMessages implements StrictServerInterface.
func (h *Handlers) ListMessages(ctx context.Context, req ListMessagesRequestObject) (ListMessagesResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingRead); err != nil {
		return nil, err
	}
	if req.Params.OrderId != nil {
		id, err := ulid.Parse(*req.Params.OrderId)
		if err != nil {
			return ListMessages200JSONResponse{Items: []CustomerMessage{}}, nil // no message has that order
		}
		list, err := h.svc.OrderMessages(ctx, id)
		if err != nil {
			return nil, err
		}
		return ListMessages200JSONResponse{Items: toMessages(list)}, nil
	}
	var after *domain.Message
	if c := deref(req.Params.Cursor); c != "" {
		at, id, err := cursor.Decode(c)
		if err != nil {
			return nil, errBadCursor
		}
		after = &domain.Message{ID: id, CreatedAt: at}
	}
	var status domain.Status
	if req.Params.Status != nil {
		status = domain.Status(*req.Params.Status)
	}
	p, err := h.svc.Messages(ctx, after, status, deref(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	out := ListMessages200JSONResponse{Items: toMessages(p.Items)}
	if p.Next != nil {
		c := cursor.Encode(p.Next.CreatedAt, p.Next.ID)
		out.NextCursor = &c
	}
	return out, nil
}

// GetMessageUsage implements StrictServerInterface.
func (h *Handlers) GetMessageUsage(ctx context.Context, _ GetMessageUsageRequestObject) (GetMessageUsageResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingRead); err != nil {
		return nil, err
	}
	u, err := h.svc.Usage(ctx)
	if err != nil {
		return nil, err
	}
	out := GetMessageUsage200JSONResponse{Used: u.Used, Limit: u.Limit, Remaining: u.Remaining(), Level: Ok}
	out.Period.Time = u.Period
	switch u.Level() {
	case entitlements.Nearly:
		out.Level = Nearly
	case entitlements.Reached:
		out.Level = Reached
	}
	return out, nil
}

// ListMessageTexts implements StrictServerInterface.
func (h *Handlers) ListMessageTexts(ctx context.Context, _ ListMessageTextsRequestObject) (ListMessageTextsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingRead); err != nil {
		return nil, err
	}
	list, err := h.svc.Texts(ctx)
	if err != nil {
		return nil, err
	}
	out := ListMessageTexts200JSONResponse{Items: make([]MessageText, len(list))}
	for i, t := range list {
		out.Items[i] = toText(t)
	}
	return out, nil
}

// SaveMessageText implements StrictServerInterface.
func (h *Handlers) SaveMessageText(ctx context.Context, req SaveMessageTextRequestObject) (SaveMessageTextResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	t, err := h.svc.SaveText(ctx, domain.Kind(req.Kind), req.Locale, req.Body.SmsBody)
	if err != nil {
		return nil, err
	}
	return SaveMessageText200JSONResponse(toText(t)), nil
}

// ResetMessageText implements StrictServerInterface.
func (h *Handlers) ResetMessageText(ctx context.Context, req ResetMessageTextRequestObject) (ResetMessageTextResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingManage); err != nil {
		return nil, err
	}
	t, err := h.svc.ResetText(ctx, domain.Kind(req.Kind), req.Locale)
	if err != nil {
		return nil, err
	}
	return ResetMessageText200JSONResponse(toText(t)), nil
}

func toMessages(list []domain.Message) []CustomerMessage {
	out := make([]CustomerMessage, len(list))
	for i, m := range list {
		c := CustomerMessage{Id: m.ID.String(), Kind: MessageKind(m.Kind), Locale: CustomerMessageLocale(m.Locale), Status: MessageStatus(m.Status),
			To: m.ToMasked, Fallback: m.Fallback, RepliedAt: m.RepliedAt, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
		if m.OrderID != nil {
			id := m.OrderID.String()
			c.OrderId = &id
		}
		if m.Channel != "" {
			ch := CustomerMessageChannel(m.Channel)
			c.Channel = &ch
		}
		if m.ErrorCode != "" {
			code := m.ErrorCode
			c.ErrorCode = &code
		}
		if m.Reply != domain.NoReply {
			r := CustomerMessageReply(m.Reply)
			c.Reply = &r
		}
		out[i] = c
	}
	return out
}

func toText(t domain.Text) MessageText {
	return MessageText{Kind: MessageKind(t.Kind), Locale: MessageTextLocale(t.Locale), SmsBody: t.SMSBody, Custom: t.Custom, UpdatedAt: t.UpdatedAt}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// GetMessagingSettings implements StrictServerInterface.
func (h *Handlers) GetMessagingSettings(ctx context.Context, _ GetMessagingSettingsRequestObject) (GetMessagingSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingRead); err != nil {
		return nil, err
	}
	s, err := h.svc.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return GetMessagingSettings200JSONResponse{ConfirmCod: s.ConfirmCOD, AutoCancelHours: s.AutoCancelHours}, nil
}

// UpdateMessagingSettings implements StrictServerInterface.
func (h *Handlers) UpdateMessagingSettings(ctx context.Context, req UpdateMessagingSettingsRequestObject) (UpdateMessagingSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	s, err := h.svc.SaveSettings(ctx, domain.Settings{ConfirmCOD: req.Body.ConfirmCod, AutoCancelHours: req.Body.AutoCancelHours})
	if err != nil {
		return nil, err
	}
	return UpdateMessagingSettings200JSONResponse{ConfirmCod: s.ConfirmCOD, AutoCancelHours: s.AutoCancelHours}, nil
}

// GetOrderConfirmation implements StrictServerInterface.
func (h *Handlers) GetOrderConfirmation(ctx context.Context, req GetOrderConfirmationRequestObject) (GetOrderConfirmationResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingRead); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.OrderId)
	if err != nil {
		return nil, app.ErrConfirmationNotFound
	}
	c, err := h.svc.Confirmation(ctx, id)
	if err != nil {
		return nil, err
	}
	out := GetOrderConfirmation200JSONResponse{OrderId: c.OrderID.String(), Status: OrderConfirmationStatus(c.Status), ExpiresAt: c.ExpiresAt,
		AnsweredAt: c.AnsweredAt, CreatedAt: c.CreatedAt}
	if c.MessageID != nil {
		m := c.MessageID.String()
		out.MessageId = &m
	}
	if c.AnsweredBy != "" {
		by := c.AnsweredBy
		out.AnsweredBy = &by
	}
	return out, nil
}

// ListNotifications implements StrictServerInterface. The cursor is the
// last notice's ID.
func (h *Handlers) ListNotifications(ctx context.Context, req ListNotificationsRequestObject) (ListNotificationsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingRead); err != nil {
		return nil, err
	}
	var after *ulid.ID
	if c := deref(req.Params.Cursor); c != "" {
		id, err := ulid.Parse(c)
		if err != nil {
			return nil, errBadCursor
		}
		after = &id
	}
	p, err := h.svc.Notifications(ctx, after, deref(req.Params.UnreadOnly), deref(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	out := ListNotifications200JSONResponse{Items: make([]Notification, len(p.Items)), Unread: p.Unread}
	for i, n := range p.Items {
		item := Notification{Id: n.ID.String(), Kind: NotificationKind(n.Kind), Read: n.ReadAt != nil, CreatedAt: n.CreatedAt}
		if n.Data.OrderID != "" {
			item.OrderId, item.OrderRef = &n.Data.OrderID, &n.Data.OrderRef
		}
		if n.Data.Period != "" {
			item.Period, item.Used, item.Limit = &n.Data.Period, &n.Data.Used, &n.Data.Limit
		}
		out.Items[i] = item
	}
	if p.Next != nil {
		c := p.Next.ID.String()
		out.NextCursor = &c
	}
	return out, nil
}

// MarkNotificationsRead implements StrictServerInterface.
func (h *Handlers) MarkNotificationsRead(ctx context.Context, req MarkNotificationsReadRequestObject) (MarkNotificationsReadResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMessagingRead); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	ids := make([]ulid.ID, 0, len(req.Body.Ids))
	for _, s := range req.Body.Ids {
		if id, err := ulid.Parse(s); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 && len(req.Body.Ids) > 0 {
		return MarkNotificationsRead200JSONResponse{Marked: 0}, nil // nothing of ours
	}
	n, err := h.svc.MarkNotificationsRead(ctx, ids)
	if err != nil {
		return nil, err
	}
	return MarkNotificationsRead200JSONResponse{Marked: n}, nil
}
