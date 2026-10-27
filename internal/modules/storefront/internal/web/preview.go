package web

import (
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
)

// servePreview renders the page with the store's drafts when token is a
// live preview link (ADR-0036): the draft theme and home page, a banner
// that says so, links that keep the token, and a response no cache keeps
// and no search engine indexes. An unknown or expired token shows the
// published shop, as if it were absent (false: the caller carries on).
func (s *Shop) servePreview(w http.ResponseWriter, r *http.Request, p *pageCtx, token, rest string) bool {
	ctx := r.Context()
	d, ok, err := s.content.PreviewDraft(ctx, token)
	if err != nil {
		s.unavailable(w, r, err)
		return true
	}
	if !ok {
		return false
	}
	if d.Theme != nil {
		p.v = s.withTheme(p.v, *d.Theme)
		p.font = s.assets.fontPreload(p.v.theme, p.l)
	}
	p.preview, p.homeDraft, p.noindex = token, d.Home, true
	// The banner's rules ride with the page's own style, so shoppers'
	// pages carry none of them.
	p.css = previewCSS
	res, err := s.route(ctx, p, rest, parseQuery(rest, r.URL.Query()))
	if err != nil {
		s.unavailable(w, r, err)
		return true
	}
	if res.redirect != "" {
		http.Redirect(w, r, res.redirect, http.StatusFound)
		return true
	}
	s.write(w, r, res.cached)
	return true
}

// withTheme is the store's view with another theme: a copy, so the
// cached view the shoppers get is untouched.
func (s *Shop) withTheme(v *storeView, t domain.ThemeSettings) *storeView {
	cp := *v
	cp.theme = t
	cp.css = s.assets.themeCSS(t)
	cp.styleHash = styleHash(cp.css)
	return &cp
}

const previewCSS = ".preview-bar{position:sticky;top:0;z-index:50;padding:.5rem 1rem;background:#fef3c7;color:#78350f;text-align:center;font-size:.875rem;font-weight:700}"
