// Package payapi serves the checkout tag of api/openapi.yaml: the
// shopper's card payment through the platform's API host (ADR-0030).
// The storefront never holds a merchant's gateway keys; it sends the
// shopper here, and from here to the gateway and back.
package payapi

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// MaxReturnURL bounds the return URL.
const MaxReturnURL = 2000

// payPerIP bounds checkouts started from one address: a shopper retries
// a few times; a script creating payments does not need us.
var payPerIP = ratelimit.Limit{Burst: 20, Period: 10 * time.Minute} //nolint:gochecknoglobals // constant policy

var (
	errNotFound = errs.NotFound("not_found", "not found")
	errBusy     = errs.RateLimited(10 * time.Minute)
)

// HostOwner reports which store a host (a Host header value) belongs to.
type HostOwner func(ctx context.Context, host string) (tenant.StoreID, error)

// ClientIPFunc is the server's real-IP lookup.
type ClientIPFunc func(ctx context.Context) (netip.Addr, bool)

// Handlers implements ServerInterface.
type Handlers struct {
	svc      *app.Service
	hosts    HostOwner
	limiter  *ratelimit.Limiter
	clientIP ClientIPFunc
	resp     *problem.Responder
	log      *slog.Logger
}

var _ ServerInterface = (*Handlers)(nil)

// Deps are the handlers' dependencies.
type Deps struct {
	Service  *app.Service
	Hosts    HostOwner
	Limiter  *ratelimit.Limiter
	ClientIP ClientIPFunc
	Problems *problem.Responder
	Log      *slog.Logger
}

// Mount registers the checkout routes on mux (public: no session).
func Mount(mux *http.ServeMux, d Deps) {
	h := &Handlers{svc: d.Service, hosts: d.Hosts, limiter: d.Limiter, clientIP: d.ClientIP, resp: d.Problems, log: d.Log}
	HandlerWithOptions(h, StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			d.Problems.Write(w, r, errs.BadRequest("invalid request", err))
		},
	})
}

// StartCardPayment implements ServerInterface.
func (h *Handlers) StartCardPayment(w http.ResponseWriter, r *http.Request, storeID StoreID, orderID ULID, params StartCardPaymentParams) {
	ctx, store, order, back, ok := h.target(w, r, storeID, orderID, params.Return)
	if !ok {
		return
	}
	if h.limiter != nil {
		ip, _ := h.clientIP(ctx)
		res, err := h.limiter.Allow(ctx, "pay:ip:"+ip.String(), payPerIP)
		switch {
		case err != nil:
			h.log.WarnContext(ctx, "payments: checkout limiter unavailable", "err", err) // the payment is reused; let the shopper pay
		case !res.Allowed:
			w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter.Seconds())+1))
			h.resp.Write(w, r, errBusy)
			return
		}
	}
	c, err := h.svc.StartCheckout(ctx, order, h.svc.ReturnURL(store, order, back))
	switch {
	case err == nil:
		redirect(w, c.URL)
	case errors.Is(err, app.ErrNotPayable), errors.Is(err, app.ErrCardUnavailable):
		redirect(w, back) // paid, cancelled or no longer by card: the thank-you page says which
	default:
		h.log.WarnContext(ctx, "payments: checkout not started", "err", err)
		redirect(w, back) // the thank-you page offers to try again
	}
}

// FinishCardPayment implements ServerInterface.
func (h *Handlers) FinishCardPayment(w http.ResponseWriter, r *http.Request, storeID StoreID, orderID ULID, to string) {
	raw, err := base64.RawURLEncoding.DecodeString(to)
	if err != nil {
		h.resp.Write(w, r, errNotFound)
		return
	}
	ctx, _, order, back, ok := h.target(w, r, storeID, orderID, string(raw))
	if !ok {
		return
	}
	if err = h.svc.RefreshOrder(ctx, order); err != nil {
		h.log.WarnContext(ctx, "payments: refresh on return", "err", err) // the webhook catches up
	}
	redirect(w, back)
}

// target reads the store, the order and the return URL, which must be
// an http(s) URL on one of the same store's hosts; anything else is 404.
func (h *Handlers) target(w http.ResponseWriter, r *http.Request, storeID, orderID, back string) (
	context.Context, tenant.StoreID, ulid.ID, string, bool,
) {
	store, err := tenant.ParseStoreID(storeID)
	if err != nil {
		h.resp.Write(w, r, errNotFound)
		return nil, tenant.StoreID{}, ulid.ID{}, "", false
	}
	order, err := ulid.Parse(orderID)
	if err != nil {
		h.resp.Write(w, r, errNotFound)
		return nil, tenant.StoreID{}, ulid.ID{}, "", false
	}
	u, err := url.Parse(back)
	if err != nil || len(back) > MaxReturnURL || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		h.resp.Write(w, r, errNotFound)
		return nil, tenant.StoreID{}, ulid.ID{}, "", false
	}
	owner, err := h.hosts(r.Context(), u.Host)
	if err != nil || owner != store {
		h.resp.Write(w, r, errNotFound)
		return nil, tenant.StoreID{}, ulid.ID{}, "", false
	}
	return tenant.WithStore(r.Context(), store), store, order, u.String(), true
}

func redirect(w http.ResponseWriter, to string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Location", to)
	w.WriteHeader(http.StatusSeeOther)
}
