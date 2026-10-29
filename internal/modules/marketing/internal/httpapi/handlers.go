// Package httpapi serves the marketing tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
)

// Handlers implements StrictServerInterface.
type Handlers struct {
	svc  *app.Service
	auth authz.Authorizer
}

var _ StrictServerInterface = (*Handlers)(nil)

var errBodyRequired = errs.BadRequest("request body is required", nil)

// Mount registers the marketing routes on mux. scope is the membership
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

func toPixel(p domain.Pixel) Pixel {
	return Pixel{Provider: PixelProviderName(p.Provider), PixelId: p.PixelID, Label: p.Label, TestCode: p.TestCode,
		Enabled: p.Enabled, HasServerToken: p.HasServerToken, Version: p.Version, UpdatedAt: p.UpdatedAt}
}

func or(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ListPixels implements StrictServerInterface.
func (h *Handlers) ListPixels(ctx context.Context, _ ListPixelsRequestObject) (ListPixelsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMarketingManage); err != nil {
		return nil, err
	}
	pixels, err := h.svc.Pixels(ctx)
	if err != nil {
		return nil, err
	}
	out := ListPixels200JSONResponse{Items: make([]Pixel, len(pixels))}
	for i, p := range pixels {
		out.Items[i] = toPixel(p)
	}
	for _, s := range domain.Specs() {
		out.Providers = append(out.Providers, PixelProvider{Provider: PixelProviderName(s.Provider), Label: s.Label != nil,
			TestCode: s.TestCode, Server: s.Server})
	}
	return out, nil
}

// SavePixel implements StrictServerInterface.
func (h *Handlers) SavePixel(ctx context.Context, req SavePixelRequestObject) (SavePixelResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMarketingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	b := req.Body
	p, err := h.svc.SavePixel(ctx, domain.Provider(req.Provider), domain.PixelInput{PixelID: b.PixelId, Label: or(b.Label),
		TestCode: or(b.TestCode), Enabled: b.Enabled, ServerToken: b.ServerToken, Version: b.Version})
	if err != nil {
		return nil, err
	}
	return SavePixel200JSONResponse(toPixel(p)), nil
}

// DeletePixel implements StrictServerInterface.
func (h *Handlers) DeletePixel(ctx context.Context, req DeletePixelRequestObject) (DeletePixelResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMarketingManage); err != nil {
		return nil, err
	}
	if err := h.svc.DeletePixel(ctx, domain.Provider(req.Provider)); err != nil {
		return nil, err
	}
	return DeletePixel204Response{}, nil
}

// GetMarketingSettings implements StrictServerInterface.
func (h *Handlers) GetMarketingSettings(ctx context.Context, _ GetMarketingSettingsRequestObject) (GetMarketingSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMarketingManage); err != nil {
		return nil, err
	}
	s, err := h.svc.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return GetMarketingSettings200JSONResponse{Consent: MarketingSettingsConsent(s.Consent), Version: s.Version}, nil
}

// SaveMarketingSettings implements StrictServerInterface.
func (h *Handlers) SaveMarketingSettings(ctx context.Context, req SaveMarketingSettingsRequestObject) (SaveMarketingSettingsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMarketingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	s, err := h.svc.SaveSettings(ctx, domain.Settings{Consent: domain.Consent(req.Body.Consent), Version: req.Body.Version})
	if err != nil {
		return nil, err
	}
	return SaveMarketingSettings200JSONResponse{Consent: MarketingSettingsConsent(s.Consent), Version: s.Version}, nil
}

func toFeed(key string, ok bool) ProductFeed {
	if !ok {
		return ProductFeed{}
	}
	google, meta := domain.FeedPath(domain.FeedGoogle, key), domain.FeedPath(domain.FeedMeta, key)
	return ProductFeed{Enabled: true, Key: &key, GooglePath: &google, MetaPath: &meta}
}

// GetProductFeed implements StrictServerInterface.
func (h *Handlers) GetProductFeed(ctx context.Context, _ GetProductFeedRequestObject) (GetProductFeedResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMarketingManage); err != nil {
		return nil, err
	}
	key, ok, err := h.svc.FeedKey(ctx)
	if err != nil {
		return nil, err
	}
	return GetProductFeed200JSONResponse(toFeed(key, ok)), nil
}

// RenewProductFeedKey implements StrictServerInterface.
func (h *Handlers) RenewProductFeedKey(ctx context.Context, _ RenewProductFeedKeyRequestObject) (RenewProductFeedKeyResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMarketingManage); err != nil {
		return nil, err
	}
	key, err := h.svc.RenewFeedKey(ctx)
	if err != nil {
		return nil, err
	}
	return RenewProductFeedKey200JSONResponse(toFeed(key, true)), nil
}

// DisableProductFeed implements StrictServerInterface.
func (h *Handlers) DisableProductFeed(ctx context.Context, _ DisableProductFeedRequestObject) (DisableProductFeedResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMarketingManage); err != nil {
		return nil, err
	}
	if err := h.svc.DisableFeeds(ctx); err != nil {
		return nil, err
	}
	return DisableProductFeed204Response{}, nil
}
