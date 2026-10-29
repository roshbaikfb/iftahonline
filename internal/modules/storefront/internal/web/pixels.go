package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/csp"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Pixels are the store's ad pixels (marketing.Pixels, ADR-0043).
type Pixels interface {
	Browser(ctx context.Context) (marketing.Browser, error)
}

// Conversions reports conversions from the server too
// (marketing.Conversions, ADR-0043).
type Conversions interface {
	Track(ctx context.Context, c marketing.Conversion) error
}

// Added keeps an AddToCart between the cart's form post and the cart
// page, on the server (storefront store.Added): its event_id is never
// the shopper's to name.
type Added interface {
	Put(ctx context.Context, tokenHash []byte, value string, ttl time.Duration) error
	Take(ctx context.Context, tokenHash []byte) (string, bool, error)
}

// Server-side conversions are bounded per address and per store: pages
// are public, and every event costs a job and a call to each provider
// with the merchant's token. Past a limit the page still reports its
// event from the browser.
var (
	conversionPerIP    = ratelimit.Limit{Burst: 30, Period: 10 * time.Minute}   //nolint:gochecknoglobals // constant policy
	conversionPerStore = ratelimit.Limit{Burst: 3000, Period: 10 * time.Minute} //nolint:gochecknoglobals // constant policy
)

// track reports a conversion the page also reports, from the server,
// with the same event_id, when the shopper's visit allows ad signals
// (the store has pixels, and consent where it asks) and the limits do.
// A failure costs the event, never the page.
func (s *Shop) track(r *http.Request, p *pageCtx, name, id string, value money.Money, items []string) {
	if s.conversions == nil || id == "" {
		return
	}
	v := s.visit(r, p)
	if v.IP == "" { // not allowed: the visit carries no signals
		return
	}
	store, tenantErr := tenant.FromContext(r.Context())
	if tenantErr != nil {
		return
	}
	if ok, _ := s.allowGuarded(r, store, "conversion", conversionPerIP, conversionPerStore); !ok {
		return
	}
	c := marketing.Conversion{Name: name, EventID: id, Time: s.now(), Value: value, Items: items, URL: p.abs(p.l, p.path),
		User: marketing.User{IP: v.IP, UserAgent: v.UserAgent, FBP: v.FBP, FBC: v.FBC, TTP: v.TTP, TTCLID: v.TTCLID, ScCID: v.ScCID}}
	if err := s.conversions.Track(r.Context(), c); err != nil {
		s.log.WarnContext(r.Context(), "storefront: conversion", "event", name, "err", err)
	}
}

// Browser events a page reports to the store's pixels. The provider
// names are px.js's to map.
const (
	EventViewContent      = "ViewContent"
	EventAddToCart        = "AddToCart"
	EventInitiateCheckout = "InitiateCheckout"
	EventPurchase         = "Purchase"
)

// checkoutEventID is the event_id of a cart's InitiateCheckout: the same
// for the same cart and lines, so a reload of checkout is one event (the
// server's jobs are unique by it, the browser keeps what it sent), and a
// new one once the cart changes. Only a hash of the token goes in it.
func checkoutEventID(tok authtoken.Token, view app.CartView) string {
	h := sha256.New()
	h.Write(tok.Hash())
	for _, l := range view.Lines {
		h.Write([]byte("|" + l.VariantID.String() + ":" + strconv.Itoa(l.Qty)))
	}
	return "ic-" + hex.EncodeToString(h.Sum(nil))[:32]
}

// purchaseEventID is the event_id of an order's Purchase: the thank-you
// page and the server's event carry the same one (marketing owns it).
func purchaseEventID(ref string) string {
	if id, err := ulid.Parse(ref); err == nil {
		return marketing.PurchaseEventID(id)
	}
	return "purchase:" + ref // not an order ID: the browser's alone
}

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

// addedTTL is how long an AddToCart waits for the cart page the form
// post redirects to.
const addedTTL = time.Minute

// setAdded records an AddToCart for the cart's next page, on the server.
func (s *Shop) setAdded(ctx context.Context, tok authtoken.Token, p *pageCtx, variant ulid.ID, qty int) {
	if s.added == nil || tok == "" || !p.v.hasPixels() || qty < 1 {
		return
	}
	value := s.ids.New().String() + "." + variant.String() + "." + strconv.Itoa(qty)
	if err := s.added.Put(ctx, tok.Hash(), value, addedTTL); err != nil {
		s.log.WarnContext(ctx, "storefront: keep AddToCart", "err", err)
	}
}

// maxAddedQty bounds the quantity an add may report (a cart line holds
// far fewer).
const maxAddedQty = 1000

// takeAdded returns and forgets the AddToCart the cart's last form post
// left: its event_id, variant and quantity.
func (s *Shop) takeAdded(ctx context.Context, tok authtoken.Token) (id string, variant ulid.ID, qty int, ok bool) {
	if s.added == nil || tok == "" {
		return "", ulid.ID{}, 0, false
	}
	value, ok, err := s.added.Take(ctx, tok.Hash())
	if err != nil {
		s.log.WarnContext(ctx, "storefront: take AddToCart", "err", err)
	}
	if !ok {
		return "", ulid.ID{}, 0, false
	}
	return parseAdded(value)
}

// parseAdded reads "<event id>.<variant>.<qty>".
func parseAdded(value string) (id string, variant ulid.ID, qty int, ok bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return "", ulid.ID{}, 0, false
	}
	if _, err := ulid.Parse(parts[0]); err != nil {
		return "", ulid.ID{}, 0, false
	}
	variant, err := ulid.Parse(parts[1])
	if err != nil {
		return "", ulid.ID{}, 0, false
	}
	if qty, err = strconv.Atoi(parts[2]); err != nil || qty < 1 || qty > maxAddedQty {
		return "", ulid.ID{}, 0, false
	}
	return parts[0], variant, qty, true
}

// sourceOf reads the campaign app.js kept in sourceCookie: a query
// string, escaped once more. Anything else reads as no campaign.
func sourceOf(cookie string) url.Values {
	raw, err := url.QueryUnescape(cookie)
	if err != nil {
		return url.Values{}
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return url.Values{}
	}
	return q
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
		src = sourceOf(c.Value)
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
