package marketing

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/capi"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Endpoints are the providers' server APIs; zero fields are the real
// ones. Tests point them at their own servers.
type Endpoints struct {
	Meta, TikTok, Snap, GA4 string
}

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	IDs  *ulid.Generator
	// Sealer seals and opens the Conversions API tokens (api, worker);
	// nil on the storefront, which holds no keys and needs none.
	Sealer *envelope.Sealer
	// Events carries PixelsChanged.
	Events eventbus.Publisher
	// Audit records merchant actions (identity's audit log); nil discards.
	Audit audit.Recorder
	// Jobs enqueues the server-side conversions; nil on roles that report
	// none (the api).
	Jobs jobs.Inserter
	// Orders reads an order's Purchase (the worker); nil elsewhere.
	Orders orders.Conversions
	// Endpoints and Client send the conversions (the worker).
	Endpoints Endpoints
	Client    *http.Client
}

// Module is the wired marketing module.
type Module struct {
	svc *app.Service
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Events == nil {
		return nil, errors.New("marketing: missing dependency")
	}
	or := func(s, def string) string {
		if s == "" {
			return def
		}
		return s
	}
	c := d.Client
	if c == nil {
		c = &http.Client{Timeout: capi.Timeout}
	}
	e := d.Endpoints
	senders := map[domain.Provider]capi.Sender{
		domain.Meta:   capi.Meta{Base: or(e.Meta, capi.MetaBase), Client: c},
		domain.TikTok: capi.TikTok{Base: or(e.TikTok, capi.TikTokBase), Client: c},
		domain.Snap:   capi.Snap{Base: or(e.Snap, capi.SnapBase), Client: c},
		domain.GA4:    capi.GA4{Base: or(e.GA4, capi.GA4Base), Client: c},
	}
	var ords app.Orders
	if d.Orders != nil {
		ords = purchases{d.Orders}
	}
	var sealer app.Sealer
	if d.Sealer != nil {
		sealer = d.Sealer // never a typed nil in the interface
	}
	return &Module{svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Sealer: sealer, Events: d.Events, Audit: d.Audit,
		Jobs: d.Jobs, Orders: ords, Senders: senders})}, nil
}

// Pixels is what the storefront renders.
func (m *Module) Pixels() Pixels { return m.svc }

// Conversions is what the storefront reports server-side conversions
// through.
func (m *Module) Conversions() Conversions { return m.svc }

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, auth, scope, resp)
}

// AddWorkers registers the conversion jobs' worker (worker role).
func (m *Module) AddWorkers(w *river.Workers) error { return m.svc.AddWorkers(w) }

// OrderPlacedHandler reports a placed order's Purchase to the store's
// providers. Subscribe it as PurchasesSubscriber to
// orders.EventOrderPlaced (worker role).
func (m *Module) OrderPlacedHandler() eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orders.OrderPlaced) error {
		return m.svc.TrackPurchase(ctx, ev.OrderID)
	})
}

// purchases adapts orders.Conversions to the app's port.
type purchases struct{ c orders.Conversions }

func (p purchases) Purchase(ctx context.Context, id ulid.ID) (app.Purchase, bool, error) {
	o, s, ok, err := p.c.Conversion(ctx, id)
	if err != nil || !ok {
		return app.Purchase{}, false, err
	}
	items := make([]string, len(o.Lines))
	for i, l := range o.Lines {
		items[i] = l.VariantID.String()
	}
	country, _ := geo.CountryOf(o.Total.Currency())
	return app.Purchase{OrderID: o.ID, PlacedAt: o.CreatedAt, Total: o.Total, Items: items, Phone: o.Contact.Phone.Unmasked(),
		CustomerID: o.CustomerID.String(), Country: string(country),
		Signals: capi.User{IP: s.IP, UserAgent: s.UserAgent, FBP: s.FBP, FBC: s.FBC, TTP: s.TTP, TTCLID: s.TTCLID, ScCID: s.ScCID}}, true, nil
}
