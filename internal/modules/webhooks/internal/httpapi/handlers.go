// Package httpapi serves the webhooks tag of api/openapi.yaml. The
// handlers are not strict: the raw body must reach the provider's
// signature check byte for byte. On a connection's route the store comes
// from the path (there is no session) and becomes the tenant only for
// the connection lookup, which RLS confines to it; on a platform-owned
// account's route each store comes from the verified delivery.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ClientIPFunc returns the client address resolved by the server's RealIP
// middleware (middleware.ClientIP).
type ClientIPFunc func(ctx context.Context) (netip.Addr, bool)

// Handlers implements ServerInterface.
type Handlers struct {
	svc      *app.Service
	resp     *problem.Responder
	clientIP ClientIPFunc
}

var _ ServerInterface = (*Handlers)(nil)

var errTooLarge = errs.BadRequest("the webhook body is larger than 64 KiB", nil)

// Mount registers the webhook route on mux.
func Mount(mux *http.ServeMux, svc *app.Service, resp *problem.Responder, clientIP ClientIPFunc) {
	HandlerWithOptions(&Handlers{svc: svc, resp: resp, clientIP: clientIP}, StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			resp.Write(w, r, errs.BadRequest("invalid request", err))
		},
	})
}

// ReceiveWebhook implements ServerInterface.
func (h *Handlers) ReceiveWebhook(w http.ResponseWriter, r *http.Request, provider string, storeID StoreID, connectionID ULID) {
	store, err := tenant.ParseStoreID(storeID)
	if err != nil {
		h.resp.Write(w, r, app.ErrNotFound)
		return
	}
	conn, err := ulid.Parse(connectionID)
	if err != nil {
		h.resp.Write(w, r, app.ErrNotFound)
		return
	}
	body, ok := h.readBody(w, r)
	if !ok {
		return
	}
	ip, _ := h.clientIP(r.Context())
	ctx := tenant.WithStore(r.Context(), store)
	duplicate, err := h.svc.Receive(ctx, provider, conn, app.Request{Header: r.Header, Query: r.URL.Query(), Body: body}, ip)
	if err != nil {
		h.resp.Write(w, r, err)
		return
	}
	writeReceipt(w, duplicate)
}

// ReceivePlatformWebhook implements ServerInterface.
func (h *Handlers) ReceivePlatformWebhook(w http.ResponseWriter, r *http.Request, provider string) {
	body, ok := h.readBody(w, r)
	if !ok {
		return
	}
	ip, _ := h.clientIP(r.Context())
	duplicate, err := h.svc.ReceivePlatform(r.Context(), provider, app.Request{Header: r.Header, Query: r.URL.Query(), Body: body}, ip)
	if err != nil {
		h.resp.Write(w, r, err)
		return
	}
	writeReceipt(w, duplicate)
}

// VerifyWebhook implements ServerInterface. The provider reads the raw
// query (params documents Meta's).
func (h *Handlers) VerifyWebhook(w http.ResponseWriter, r *http.Request, provider string, storeID StoreID, connectionID ULID, _ VerifyWebhookParams) {
	store, err := tenant.ParseStoreID(storeID)
	if err != nil {
		h.resp.Write(w, r, app.ErrNotFound)
		return
	}
	conn, err := ulid.Parse(connectionID)
	if err != nil {
		h.resp.Write(w, r, app.ErrNotFound)
		return
	}
	ip, _ := h.clientIP(r.Context())
	challenge, err := h.svc.Challenge(tenant.WithStore(r.Context(), store), provider, conn, r.URL.Query(), ip)
	if err != nil {
		h.resp.Write(w, r, err)
		return
	}
	writeChallenge(w, challenge)
}

// VerifyPlatformWebhook implements ServerInterface.
func (h *Handlers) VerifyPlatformWebhook(w http.ResponseWriter, r *http.Request, provider string, _ VerifyPlatformWebhookParams) {
	ip, _ := h.clientIP(r.Context())
	challenge, err := h.svc.ChallengePlatform(r.Context(), provider, r.URL.Query(), ip)
	if err != nil {
		h.resp.Write(w, r, err)
		return
	}
	writeChallenge(w, challenge)
}

// readBody reads at most domain.MaxBody bytes; on failure it has written
// the problem.
func (h *Handlers) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, domain.MaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.resp.Write(w, r, errTooLarge)
			return nil, false
		}
		h.resp.Write(w, r, errs.BadRequest("could not read the body", err))
		return nil, false
	}
	return body, true
}

// writeChallenge echoes a validated challenge (URL-safe characters only)
// as text that browsers will not sniff.
func writeChallenge(w http.ResponseWriter, challenge string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	// G705: domain.ValidChallenge allowed only [A-Za-z0-9._~-]; errcheck: the client is gone.
	_, _ = io.WriteString(w, challenge) //nolint:errcheck,gosec // see above
}

func writeReceipt(w http.ResponseWriter, duplicate bool) {
	out := WebhookReceipt{Status: Accepted}
	if duplicate {
		out.Status = Duplicate
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out) //nolint:errcheck // the client is gone; nothing to do
}
