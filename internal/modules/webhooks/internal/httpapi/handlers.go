// Package httpapi serves the webhooks tag of api/openapi.yaml. The
// handler is not strict: the raw body must reach the provider's
// signature check byte for byte. The store comes from the path (there is
// no session) and becomes the tenant only for the connection lookup,
// which RLS confines to it.
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, domain.MaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.resp.Write(w, r, errTooLarge)
			return
		}
		h.resp.Write(w, r, errs.BadRequest("could not read the body", err))
		return
	}
	ip, _ := h.clientIP(r.Context())
	ctx := tenant.WithStore(r.Context(), store)
	duplicate, err := h.svc.Receive(ctx, provider, conn, app.Request{Header: r.Header, Query: r.URL.Query(), Body: body}, ip)
	if err != nil {
		h.resp.Write(w, r, err)
		return
	}
	out := WebhookReceipt{Status: Accepted}
	if duplicate {
		out.Status = Duplicate
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out) //nolint:errcheck // the client is gone; nothing to do
}
