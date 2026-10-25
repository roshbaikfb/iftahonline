package web

import (
	"strings"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
)

func orString(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func boolAttr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func imageOf(pr catalog.StorefrontProduct) *image {
	img, ok := firstImage(pr)
	if !ok {
		return nil
	}
	return &img
}

// stars draws a 1-5 rating.
func stars(n int) string {
	n = min(max(n, 0), 5)
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

// optionsLabel names the variant picker by the product's options.
func optionsLabel(l Locale, pr catalog.StorefrontProduct) string {
	names := make([]string, len(pr.Options))
	for i, o := range pr.Options {
		names[i] = o.Name
	}
	return orString(strings.Join(names, " / "), l.T(MsgOptions))
}

// variantLabel reads "M / Red — 150 ج.م", plus "sold out" when it is.
func variantLabel(l Locale, pr catalog.StorefrontProduct, v catalog.StorefrontVariant) string {
	label := orString(strings.Join(v.OptionValues, " / "), pr.Title) + " — " + l.Price(v.Price)
	if !v.Available {
		label += " (" + l.T(MsgSoldOut) + ")"
	}
	return label
}

// policyTitle is a policy's title, or its default name in the shopper's
// language.
func (p *pageCtx) policyTitle(title, slug string) string {
	return orString(title, p.l.PolicyTitle(slug))
}

// link is a merchant's button target. The schema allows only store paths
// and https URLs; anything else (stored before a rule change, say) goes
// nowhere rather than somewhere unsafe.
func (p *pageCtx) link(target string) templ.SafeURL {
	switch {
	case strings.HasPrefix(target, "https://"):
		return templ.SafeURL(target) //nolint:gosec // https only, validated on save
	case strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") && !strings.HasPrefix(target, "/\\"):
		return p.URL(target)
	case strings.HasPrefix(target, "#s-"):
		return templ.SafeURL(target) //nolint:gosec // a section on this page, validated on save
	}
	return "#"
}

// salePercent is how much cheaper v is than its compare-at price, rounded
// down so the badge never overstates it; 0 when it is not on sale.
func salePercent(v catalog.StorefrontVariant) int {
	if v.CompareAt == nil || v.CompareAt.Currency() != v.Price.Currency() {
		return 0
	}
	was, now := v.CompareAt.Amount(), v.Price.Amount()
	if was <= 0 || now >= was {
		return 0
	}
	return int((was - now) * 100 / was)
}

// waLink opens a chat with an E.164 number (validated on save).
func waLink(e164 string) templ.SafeURL {
	return templ.SafeURL("https://wa.me/" + strings.TrimPrefix(e164, "+")) //nolint:gosec // digits only
}

// mailto writes to a validated address.
func mailto(addr string) templ.SafeURL {
	return templ.SafeURL("mailto:" + addr) //nolint:gosec // validated on save
}

// initial is the first letter of a name, for a tile without a picture.
func initial(name string) string {
	for _, r := range name {
		return string(r)
	}
	return ""
}

// active reports whether path is the page being shown.
func (p *pageCtx) active(path string) bool { return p.path == path }

// badgeSVG is the icon of a trust badge: our own static markup.
func badgeSVG(icon string) string {
	const open = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">`
	paths := map[string]string{
		"shipping": `<path d="M3 7h11v9H3zM14 10h4l3 3v3h-7"/><circle cx="7" cy="17.5" r="1.5"/><circle cx="17" cy="17.5" r="1.5"/>`,
		"cod":      `<rect x="3" y="6" width="18" height="12" rx="2"/><circle cx="12" cy="12" r="2.5"/><path d="M6 9v.01M18 15v.01"/>`,
		"returns":  `<path d="M4 12a8 8 0 1 0 3-6.2"/><path d="M4 4v4h4"/>`,
		"secure":   `<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>`,
		"support":  `<path d="M4 13v-1a8 8 0 0 1 16 0v1"/><rect x="3" y="13" width="4" height="6" rx="1"/><rect x="17" y="13" width="4" height="6" rx="1"/>`,
		"quality":  `<circle cx="12" cy="9" r="5"/><path d="M9 13.5 8 21l4-2 4 2-1-7.5"/>`,
	}
	p, ok := paths[icon]
	if !ok {
		return ""
	}
	return open + p + `</svg>`
}

// iconSVG is a line icon of the interface: our own static markup.
func iconSVG(name string) string {
	const open = `<svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">`
	paths := map[string]string{
		"menu":     `<path d="M4 7h16M4 12h16M4 17h16"/>`,
		"bag":      `<path d="M6 7h12l-1 13H7L6 7z"/><path d="M9 7a3 3 0 0 1 6 0"/>`,
		"chat":     `<path d="M21 12a9 9 0 0 1-13.5 7.8L3 21l1.2-4.4A9 9 0 1 1 21 12z"/>`,
		"back":     `<path d="m9 6 6 6-6 6"/>`,
		"share":    `<circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="m8.6 13.5 6.8 4M15.4 6.5l-6.8 4"/>`,
		"shipping": `<path d="M3 7h11v10H3zM14 10h4l3 3v4h-7"/><circle cx="7" cy="18" r="1.8"/><circle cx="17" cy="18" r="1.8"/>`,
		"card":     `<rect x="3" y="6" width="18" height="12" rx="2"/><path d="M3 10h18"/>`,
		"returns":  `<path d="M3 12a9 9 0 0 1 15.5-6.3L21 8M21 3v5h-5M21 12a9 9 0 0 1-15.5 6.3L3 16M3 21v-5h5"/>`,
	}
	p, ok := paths[name]
	if !ok {
		return ""
	}
	return open + p + `</svg>`
}

func joinOptions(vs []string) string { return strings.Join(vs, " / ") }
