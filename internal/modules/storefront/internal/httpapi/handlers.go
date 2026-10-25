// Package httpapi serves the storefront tag of api/openapi.yaml: the
// merchant's theme and pages. Handlers authorize first, take the store
// only from the context (set by the membership middleware, never from
// the path), and return errs errors that the problem package renders.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// URLSigner builds delivery URLs of an image master (media.Signer).
type URLSigner interface {
	URLs(objectKey string) map[string]map[string]string
}

// Handlers implements StrictServerInterface.
type Handlers struct {
	svc      *app.Service
	sections *sections.Registry
	urls     URLSigner
	auth     authz.Authorizer
}

var _ StrictServerInterface = (*Handlers)(nil)

// Mount registers the storefront routes on mux. scope is the membership
// middleware: it authenticates, returns 404 to non-members, and sets the
// tenant and principal (ADR-0006).
func Mount(mux *http.ServeMux, svc *app.Service, reg *sections.Registry, urls URLSigner, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		resp.Write(w, r, errs.BadRequest("invalid request", err))
	}
	strict := NewStrictHandlerWithOptions(&Handlers{svc: svc, sections: reg, urls: urls, auth: auth}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: resp.Write,
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter:       mux,
		Middlewares:      []MiddlewareFunc{MiddlewareFunc(scope)},
		ErrorHandlerFunc: badRequest,
	})
}

var errBodyRequired = errs.BadRequest("request body is required", nil)

func (h *Handlers) authorize(ctx context.Context) error {
	return h.auth.Authorize(ctx, authz.PermStorefrontManage)
}

// GetStorefrontTheme implements StrictServerInterface.
func (h *Handlers) GetStorefrontTheme(ctx context.Context, _ GetStorefrontThemeRequestObject) (GetStorefrontThemeResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	t, err := h.svc.GetTheme(ctx)
	if err != nil {
		return nil, err
	}
	return GetStorefrontTheme200JSONResponse(toTheme(t)), nil
}

// UpdateStorefrontTheme implements StrictServerInterface.
func (h *Handlers) UpdateStorefrontTheme(ctx context.Context, req UpdateStorefrontThemeRequestObject) (UpdateStorefrontThemeResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	in := req.Body.Settings
	settings := domain.ThemeSettings{
		Colors: domain.Colors{Primary: in.Colors.Primary, Accent: in.Colors.Accent, Background: in.Colors.Background, Text: in.Colors.Text},
		Font:   domain.Font(in.Font), Radius: domain.Radius(in.Radius),
		Announcement: deref(in.Announcement), About: deref(in.About), LowStock: in.LowStock,
	}
	if in.Contact != nil {
		settings.Contact = domain.Contact{WhatsApp: deref(in.Contact.Whatsapp), Email: deref(in.Contact.Email)}
	}
	t, err := h.svc.UpdateTheme(ctx, settings, req.Body.Version)
	if err != nil {
		return nil, err
	}
	return UpdateStorefrontTheme200JSONResponse(toTheme(t)), nil
}

// ListSectionTypes implements StrictServerInterface.
func (h *Handlers) ListSectionTypes(ctx context.Context, _ ListSectionTypesRequestObject) (ListSectionTypesResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	ts := h.sections.Schemas()
	out := ListSectionTypes200JSONResponse{Items: make([]SectionTypeInfo, len(ts)), Definitions: h.sections.Definitions()}
	for i, t := range ts {
		out.Items[i] = SectionTypeInfo{Type: SectionType(t.Type), Schema: t.Schema}
	}
	return out, nil
}

// GetHomePage implements StrictServerInterface.
func (h *Handlers) GetHomePage(ctx context.Context, _ GetHomePageRequestObject) (GetHomePageResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	p, err := h.svc.GetHome(ctx)
	if err != nil {
		return nil, err
	}
	return GetHomePage200JSONResponse(toPage(p)), nil
}

// SaveHomePage implements StrictServerInterface.
func (h *Handlers) SaveHomePage(ctx context.Context, req SaveHomePageRequestObject) (SaveHomePageResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	p, err := h.svc.SaveHome(ctx, pageInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return SaveHomePage200JSONResponse(toPage(p)), nil
}

// ListLandingPages implements StrictServerInterface.
func (h *Handlers) ListLandingPages(ctx context.Context, _ ListLandingPagesRequestObject) (ListLandingPagesResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	ps, err := h.svc.ListLandingPages(ctx)
	if err != nil {
		return nil, err
	}
	return ListLandingPages200JSONResponse(toPageList(ps)), nil
}

// CreateLandingPage implements StrictServerInterface.
func (h *Handlers) CreateLandingPage(ctx context.Context, req CreateLandingPageRequestObject) (CreateLandingPageResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	p, err := h.svc.CreateLandingPage(ctx, pageInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return CreateLandingPage201JSONResponse(toPage(p)), nil
}

// GetLandingPage implements StrictServerInterface.
func (h *Handlers) GetLandingPage(ctx context.Context, req GetLandingPageRequestObject) (GetLandingPageResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	id, err := pageID(req.PageId)
	if err != nil {
		return nil, err
	}
	p, err := h.svc.GetLandingPage(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetLandingPage200JSONResponse(toPage(p)), nil
}

// UpdateLandingPage implements StrictServerInterface.
func (h *Handlers) UpdateLandingPage(ctx context.Context, req UpdateLandingPageRequestObject) (UpdateLandingPageResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	id, err := pageID(req.PageId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	p, err := h.svc.UpdateLandingPage(ctx, id, pageInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return UpdateLandingPage200JSONResponse(toPage(p)), nil
}

// DeleteLandingPage implements StrictServerInterface.
func (h *Handlers) DeleteLandingPage(ctx context.Context, req DeleteLandingPageRequestObject) (DeleteLandingPageResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	id, err := pageID(req.PageId)
	if err != nil {
		return nil, err
	}
	if err = h.svc.DeleteLandingPage(ctx, id); err != nil {
		return nil, err
	}
	return DeleteLandingPage204Response{}, nil
}

// ListPolicies implements StrictServerInterface.
func (h *Handlers) ListPolicies(ctx context.Context, _ ListPoliciesRequestObject) (ListPoliciesResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	ps, err := h.svc.ListPolicies(ctx)
	if err != nil {
		return nil, err
	}
	return ListPolicies200JSONResponse(toPageList(ps)), nil
}

// GetPolicy implements StrictServerInterface.
func (h *Handlers) GetPolicy(ctx context.Context, req GetPolicyRequestObject) (GetPolicyResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	p, err := h.svc.GetPolicy(ctx, string(req.Policy))
	if err != nil {
		return nil, err
	}
	return GetPolicy200JSONResponse(toPage(p)), nil
}

// SavePolicy implements StrictServerInterface.
func (h *Handlers) SavePolicy(ctx context.Context, req SavePolicyRequestObject) (SavePolicyResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	p, err := h.svc.SavePolicy(ctx, string(req.Policy), pageInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return SavePolicy200JSONResponse(toPage(p)), nil
}

// GetStoreLogo implements StrictServerInterface.
func (h *Handlers) GetStoreLogo(ctx context.Context, _ GetStoreLogoRequestObject) (GetStoreLogoResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	l, err := h.svc.GetLogo(ctx)
	if err != nil {
		return nil, err
	}
	var out GetStoreLogo200JSONResponse
	if l.Current != nil {
		a := h.toLogoAsset(*l.Current)
		out.Current = &a
	}
	if l.Latest != nil {
		a := h.toLogoAsset(*l.Latest)
		out.Latest = &a
	}
	return out, nil
}

// DeleteStoreLogo implements StrictServerInterface.
func (h *Handlers) DeleteStoreLogo(ctx context.Context, _ DeleteStoreLogoRequestObject) (DeleteStoreLogoResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if err := h.svc.DeleteLogo(ctx); err != nil {
		return nil, err
	}
	return DeleteStoreLogo204Response{}, nil
}

// CreateLogoUpload implements StrictServerInterface.
func (h *Handlers) CreateLogoUpload(ctx context.Context, req CreateLogoUploadRequestObject) (CreateLogoUploadResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	a, up, err := h.svc.CreateLogoUpload(ctx, string(req.Body.ContentType))
	if err != nil {
		return nil, err
	}
	return CreateLogoUpload201JSONResponse{
		Asset:  h.toLogoAsset(a),
		Upload: PresignedUpload{Url: up.URL, Fields: up.Fields, ExpiresAt: up.ExpiresAt, MaxBytes: up.MaxBytes},
	}, nil
}

// CompleteLogoUpload implements StrictServerInterface.
func (h *Handlers) CompleteLogoUpload(ctx context.Context, req CompleteLogoUploadRequestObject) (CompleteLogoUploadResponseObject, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.AssetId)
	if err != nil {
		return nil, app.ErrAssetNotFound
	}
	a, err := h.svc.CompleteLogoUpload(ctx, id)
	if err != nil {
		return nil, err
	}
	return CompleteLogoUpload202JSONResponse(h.toLogoAsset(a)), nil
}

func (h *Handlers) toLogoAsset(a domain.Asset) LogoAsset {
	out := LogoAsset{Id: a.ID.String(), Status: LogoAssetStatus(a.Status), CreatedAt: timePtr(a.CreatedAt)}
	if a.FailureReason != "" {
		out.FailureReason = &a.FailureReason
	}
	if a.Status == domain.AssetReady {
		w, ht := a.Width, a.Height
		out.Width, out.Height = &w, &ht
		u := h.urls.URLs(a.ObjectKey)
		f := func(preset string) ImageFormats {
			return ImageFormats{Avif: u[preset]["avif"], Webp: u[preset]["webp"]}
		}
		out.Urls = &ImageURLs{Thumb: f("thumb"), Small: f("small"), Medium: f("medium"), Large: f("large")}
	}
	return out
}

// pageID parses a path ID. A malformed ID is simply not found.
func pageID(s string) (ulid.ID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return ulid.ID{}, app.ErrPageNotFound
	}
	return id, nil
}

func pageInput(b PageInput) domain.PageInput {
	in := domain.PageInput{
		Title: deref(b.Title), Slug: deref(b.Slug), Description: deref(b.Description), Body: deref(b.Body),
	}
	if b.Status != nil {
		in.Status = string(*b.Status)
	}
	if b.Version != nil {
		in.Version = *b.Version
	}
	if b.Sections != nil {
		in.Sections = make([]domain.Section, len(*b.Sections))
		for i, s := range *b.Sections {
			sec := domain.Section{ID: deref(s.Id), Type: domain.SectionType(s.Type)}
			if s.Settings != nil {
				sec.Settings = *s.Settings
			}
			in.Sections[i] = sec
		}
	}
	return in
}

func toTheme(t domain.Theme) Theme {
	out := Theme{Version: t.Version, UpdatedAt: t.UpdatedAt}
	c := t.Settings.Colors
	out.Settings.Colors.Primary, out.Settings.Colors.Accent = c.Primary, c.Accent
	out.Settings.Colors.Background, out.Settings.Colors.Text = c.Background, c.Text
	out.Settings.Font = ThemeSettingsFont(t.Settings.Font)
	out.Settings.Radius = ThemeSettingsRadius(t.Settings.Radius)
	out.Settings.Announcement, out.Settings.About = opt(t.Settings.Announcement), opt(t.Settings.About)
	low := t.Settings.LowStockThreshold()
	out.Settings.LowStock = &low
	if c := t.Settings.Contact; c != (domain.Contact{}) {
		out.Settings.Contact = &struct {
			Email    *string `json:"email,omitempty"`
			Whatsapp *string `json:"whatsapp,omitempty"`
		}{Email: opt(c.Email), Whatsapp: opt(c.WhatsApp)}
	}
	return out
}

func toPage(p domain.Page) Page {
	out := Page{
		Kind: PageKind(p.Kind), Slug: p.Slug, Title: p.Title, Description: p.Description, Body: p.Body,
		Status: PageStatus(p.Status), Version: p.Version, Sections: make([]Section, len(p.Sections)),
	}
	if !p.ID.IsZero() {
		id := p.ID.String()
		out.Id = &id
	}
	out.CreatedAt, out.UpdatedAt = timePtr(p.CreatedAt), timePtr(p.UpdatedAt)
	for i, s := range p.Sections {
		id, settings := s.ID, json.RawMessage(s.Settings)
		out.Sections[i] = Section{Id: &id, Type: SectionType(s.Type), Settings: &settings}
	}
	return out
}

func toPageList(ps []domain.Page) PageList {
	out := PageList{Items: make([]Page, len(ps))}
	for i, p := range ps {
		out.Items[i] = toPage(p)
	}
	return out
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// opt is s, or absent when empty.
func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
