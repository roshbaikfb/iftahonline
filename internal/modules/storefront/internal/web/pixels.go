package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/platform/csp"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Pixels are the store's ad pixels (marketing.Pixels, ADR-0043).
type Pixels interface {
	Browser(ctx context.Context) (marketing.Browser, error)
}

// Browser events a page reports to the store's pixels. The provider
// names are px.js's to map.
const (
	EventViewContent      = "ViewContent"
	EventAddToCart        = "AddToCart"
	EventInitiateCheckout = "InitiateCheckout"
	EventPurchase         = "Purchase"
)

// PurchaseEventID is the event_id of an order's Purchase: the browser
// and the server send the same one, so each provider counts it once.
func PurchaseEventID(order string) string { return "purchase:" + order }

// pxPixel is one provider's tag, as px.js reads it.
type pxPixel struct {
	Kind  string `json:"k"`
	ID    string `json:"id"`
	Label string `json:"l,omitempty"`
}

// pxEvent is a conversion a page reports. Value is in minor units of
// Currency (Units digits); ID is the event_id the server also sends.
type pxEvent struct {
	Name     string   `json:"n"`
	ID       string   `json:"id,omitempty"`
	Value    int64    `json:"v"`
	Units    int      `json:"u"`
	Currency string   `json:"c"`
	Items    []string `json:"ids,omitempty"`
	Ref      string   `json:"ref,omitempty"` // the order, for Purchase
}

// pxConfig is what px.js reads from the page.
type pxConfig struct {
	Pixels []pxPixel `json:"p"`
	Ask    bool      `json:"ask"` // ask for consent before loading any tag
	Events []pxEvent `json:"ev,omitempty"`
}

// pixelsOf reads the store's pixels for its view. A failure leaves the
// store without pixels for the view's lifetime: ads are measured less,
// but pages still render.
func (s *Shop) pixelsOf(ctx context.Context, country geo.Country) (marketing.Browser, bool) {
	if s.pixels == nil {
		return marketing.Browser{}, false
	}
	b, err := s.pixels.Browser(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "storefront: pixels", "err", err)
		return marketing.Browser{}, false
	}
	ask := b.Consent == marketing.ConsentRequired || b.Consent == marketing.ConsentAuto && consentRequired(country)
	return b, ask
}

// consentRequired is whether a store's country requires the shopper's
// consent before ad cookies: Saudi Arabia's PDPL does.
func consentRequired(c geo.Country) bool { return c == geo.SA }

// pxSources are the origins the store's pixels need in a page's CSP.
func (v *storeView) pxSources() csp.Sources {
	return csp.Sources{Script: v.pixels.Script, Connect: v.pixels.Connect, Frame: v.pixels.Frame}
}

// hasPixels reports whether the store's pages load any tag.
func (v *storeView) hasPixels() bool { return len(v.pixels.Pixels) > 0 }

// event adds a conversion for the page's pixels to report; a store
// without pixels reports nothing.
func (p *pageCtx) event(name, id string, value money.Money, items []string) *pxEvent {
	if !p.v.hasPixels() {
		return nil
	}
	p.events = append(p.events, pxEvent{Name: name, ID: id, Value: value.Amount(), Units: value.Currency().MinorUnits(),
		Currency: string(value.Currency()), Items: items})
	return &p.events[len(p.events)-1]
}

// pxScript is the page's pixel configuration, as a JSON data block
// (never executed, so the CSP's script-src does not apply). Every value
// is ours or validated (the IDs match their provider's pattern), and
// encoding/json escapes <, > and &.
func (p *pageCtx) pxScript() templ.Component {
	if !p.v.hasPixels() {
		return templ.NopComponent
	}
	cfg := pxConfig{Ask: p.v.askConsent, Events: p.events}
	for _, px := range p.v.pixels.Pixels {
		cfg.Pixels = append(cfg.Pixels, pxPixel{Kind: string(px.Provider), ID: px.PixelID, Label: px.Label})
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return templ.NopComponent
	}
	return templ.Raw(`<script type="application/json" id="px">` + string(b) + `</script>`)
}

// addedCookie carries an AddToCart from the cart's form post to the cart
// page it redirects to: the event_id the server made, the variant and
// the quantity. It lives seconds and is read once.
const addedCookie = "__Host-px_add"

// setAdded records an AddToCart for the next cart page.
func (s *Shop) setAdded(w http.ResponseWriter, p *pageCtx, variant ulid.ID, qty int) string {
	if !p.v.hasPixels() || qty < 1 {
		return ""
	}
	id := s.ids.New().String()
	http.SetCookie(w, &http.Cookie{Name: addedCookie, Value: id + "." + variant.String() + "." + strconv.Itoa(qty), Path: "/",
		MaxAge: 60, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	return id
}

// maxAddedQty bounds the quantity a cookie may claim (a cart line holds
// far fewer).
const maxAddedQty = 1000

// takeAdded reads and clears the AddToCart a form post left: its
// event_id, variant and quantity.
func takeAdded(w http.ResponseWriter, r *http.Request) (id string, variant ulid.ID, qty int, ok bool) {
	c, err := r.Cookie(addedCookie)
	if err != nil {
		return "", ulid.ID{}, 0, false
	}
	http.SetCookie(w, &http.Cookie{Name: addedCookie, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return "", ulid.ID{}, 0, false
	}
	if _, err = ulid.Parse(parts[0]); err != nil {
		return "", ulid.ID{}, 0, false
	}
	if variant, err = ulid.Parse(parts[1]); err != nil {
		return "", ulid.ID{}, 0, false
	}
	if qty, err = strconv.Atoi(parts[2]); err != nil || qty < 1 || qty > maxAddedQty {
		return "", ulid.ID{}, 0, false
	}
	return parts[0], variant, qty, true
}

// Visit is where an order's shopper came from (ADR-0043): the campaign's
// UTM parameters, and, on a store whose pixels may run for this shopper,
// the ad providers' cookies and click IDs, the IP address and the user
// agent. Orders clean every field; none is required.
type Visit struct {
	UTMSource, UTMMedium, UTMCampaign, UTMContent, UTMTerm string
	FBP, FBC, TTP, TTCLID, ScCID, GCLID                    string
	IP, UserAgent                                          string
}

// Cookies the browser keeps for the order: app.js writes sourceCookie
// from the landing URL (pages are cached, so the server never sees it);
// px.js writes consentCookie with the shopper's answer.
const (
	sourceCookie  = "ift_src"
	consentCookie = "px_c"
)

// visit reads the order's Visit from the request.
func (s *Shop) visit(r *http.Request, p *pageCtx) Visit {
	src := url.Values{}
	if c, err := r.Cookie(sourceCookie); err == nil {
		if raw, unErr := url.QueryUnescape(c.Value); unErr == nil {
			if q, qErr := url.ParseQuery(raw); qErr == nil {
				src = q
			}
		}
	}
	v := Visit{UTMSource: src.Get("utm_source"), UTMMedium: src.Get("utm_medium"), UTMCampaign: src.Get("utm_campaign"),
		UTMContent: src.Get("utm_content"), UTMTerm: src.Get("utm_term")}
	if !p.v.hasPixels() {
		return v // no provider to send them to: keep none
	}
	if c, err := r.Cookie(consentCookie); p.v.askConsent && (err != nil || c.Value != "yes") {
		return v // not agreed (or not asked yet)
	}
	cookie := func(name string) string {
		if c, err := r.Cookie(name); err == nil {
			return c.Value
		}
		return ""
	}
	v.FBP, v.FBC, v.TTP = cookie("_fbp"), cookie("_fbc"), cookie("_ttp")
	if fbclid, ts := src.Get("fbclid"), src.Get("ts"); v.FBC == "" && fbclid != "" && ts != "" {
		v.FBC = "fb.1." + ts + "." + fbclid // Meta's format, when its tag has not written _fbc yet
	}
	v.TTCLID, v.ScCID, v.GCLID = src.Get("ttclid"), src.Get("ScCid"), src.Get("gclid")
	v.IP, v.UserAgent = s.clientIP(r), r.UserAgent()
	return v
}
