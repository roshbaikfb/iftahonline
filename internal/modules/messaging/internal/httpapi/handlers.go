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
