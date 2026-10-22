package web

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
)

// pageCtx is one page being rendered: the store, the shopper's language
// and where the page lives.
type pageCtx struct {
	v         *storeView
	store     string // the store's ID, for cache tags
	l         Locale
	base      string // scheme://host
	path      string // without the language prefix, e.g. /p/shirt
	jsURL     string
	font      string // preload
	title     string
	desc      string
	image     string // og:image
	jsonLD    []byte // already JSON
	tags      []string
	eagerN    int  // images still to load eagerly
	noindex   bool // pages for one shopper (cart, thank-you)
	challenge bool // the page shows a Turnstile challenge
}

// eagerImages is how many of a page's first images load eagerly, at high
// priority: what a phone shows above the fold (a hero, or a row of two
// cards) is the LCP element.
const eagerImages = 2

// eager reports whether the next image is one of the page's first.
func (p *pageCtx) eager() bool {
	if p.eagerN < eagerImages {
		p.eagerN++
		return true
	}
	return false
}

// localize prefixes a store path with the language when it is not the
// store's default one.
func (p *pageCtx) localize(l Locale, path string) string {
	if l == p.v.locale {
		return path
	}
	if path == "/" {
		return "/" + string(l) + "/"
	}
	return "/" + string(l) + path
}

// URL is a link to path in the shopper's language.
func (p *pageCtx) URL(path string) templ.SafeURL { return templ.SafeURL(p.localize(p.l, path)) } //nolint:gosec // our own paths

// abs is the absolute URL of path in l.
func (p *pageCtx) abs(l Locale, path string) string { return p.base + p.localize(l, path) }

// productPath is a product's page.
func productPath(slug string) string { return "/p/" + slug }

// categoryPath is a category's page.
func categoryPath(slug string) string { return "/c/" + slug }

// tag records a Cache-Tag for the page, so a purge of that product or
// page reaches it (ADR-0018).
func (p *pageCtx) tag(t string) { p.tags = append(p.tags, t) }

// tagListing marks a page that lists products (home, category, landing
// pages): a product created, edited or removed may change any of them,
// even one that does not show it yet.
func (p *pageCtx) tagListing() { p.tag("l-" + p.store) }

func (p *pageCtx) tagProducts(ps []catalog.StorefrontProduct) {
	for _, pr := range ps {
		p.tag("p-" + pr.ID.String())
	}
}

// cacheTags is the Cache-Tag header: the store, then the page's own tags,
// without repeats, within Cloudflare's 16 KiB limit.
func (p *pageCtx) cacheTags() string {
	seen := map[string]bool{}
	out := []string{"s-" + p.store}
	n := len(out[0])
	for _, t := range p.tags {
		if seen[t] || n+len(t)+1 > 16000 {
			continue
		}
		seen[t] = true
		out = append(out, t)
		n += len(t) + 1
	}
	return strings.Join(out, ",")
}

// ldScript is the page's structured data as a script element. JSON from
// encoding/json escapes <, > and &, so it cannot close the element.
func (p *pageCtx) ldScript() templ.Component {
	if len(p.jsonLD) == 0 {
		return templ.NopComponent
	}
	return templ.Raw(`<script type="application/ld+json">` + string(p.jsonLD) + `</script>`)
}

// styleTag is the inline stylesheet whose hash the CSP allows. It is
// built from validated theme values and our own CSS only.
func (p *pageCtx) styleTag() templ.Component { return templ.Raw("<style>" + p.v.css + "</style>") }

// image is a picture's sources: AVIF and WebP srcsets of one image.
type image struct {
	avif, webp, src string
	width, height   int
	alt             string
}

// pictureOf builds the sources of a catalog image.
func pictureOf(img catalog.StorefrontImage, fallbackAlt string) image {
	out := image{alt: img.Alt, width: img.Width, height: img.Height}
	if out.alt == "" {
		out.alt = fallbackAlt
	}
	var avif, webp []string
	for _, r := range img.Renditions {
		entry := r.URL + " " + strconv.Itoa(r.Width) + "w"
		switch r.Format {
		case "avif":
			avif = append(avif, entry)
		case "webp":
			webp = append(webp, entry)
			if out.src == "" || r.Width <= 800 {
				out.src = r.URL // the largest rendition up to 800px wide
			}
		}
	}
	out.avif, out.webp = strings.Join(avif, ", "), strings.Join(webp, ", ")
	return out
}

func firstImage(pr catalog.StorefrontProduct) (image, bool) {
	if len(pr.Images) == 0 {
		return image{}, false
	}
	return pictureOf(pr.Images[0], pr.Title), true
}

// priceView is how a variant's price reads in the shopper's language.
type priceView struct {
	price, compare string
}

func (p *pageCtx) priceOf(v catalog.StorefrontVariant) priceView {
	out := priceView{price: p.l.Price(v.Price)}
	if v.CompareAt != nil {
		out.compare = p.l.Price(*v.CompareAt)
	}
	return out
}

// marshalLD encodes structured data; a failure leaves the page without
// it rather than broken.
func marshalLD(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
