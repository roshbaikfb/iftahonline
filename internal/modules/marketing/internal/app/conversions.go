package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/capi"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Conversion is a conversion the server reports to the store's
// providers (ADR-0043), with the event_id its page reported.
type Conversion struct {
	Name    string // capi.AddToCart, capi.InitiateCheckout, capi.Purchase
	EventID string
	Time    time.Time
	Value   money.Money
	Items   []string // variant IDs
	OrderID string   // Purchase only
	URL     string
	User    capi.User
}

// Purchase is an order as its conversion needs it; ok false means the
// order carries no signals (none were allowed): it is not reported.
type Purchase struct {
	OrderID    ulid.ID
	PlacedAt   time.Time
	Total      money.Money
	Items      []string
	Phone      string // E.164
	CustomerID string
	Country    string
	URL        string
	Signals    capi.User // the browser's: IP, user agent, cookies, click IDs
}

// Orders reads an order for its Purchase (orders.Conversions, adapted
// by the module).
type Orders interface {
	Purchase(ctx context.Context, id ulid.ID) (p Purchase, ok bool, err error)
}

// PurchaseEventID is the event_id of an order's Purchase: the thank-you
// page's pixel reports the same one (the storefront).
func PurchaseEventID(order ulid.ID) string { return "purchase:" + order.String() }

// ConversionArgs sends one conversion to one provider. A Purchase names
// its order, and the worker reads who it matches (phone, signals) from
// the order when it sends: no phone number waits in the job table. The
// cart's and checkout's carry the visit's signals (IP, user agent, the
// providers' cookies and click IDs: what order_signals keeps), until the
// job is done and River deletes it.
type ConversionArgs struct {
	jobs.TenantArgs
	// Unique by store, provider and event: a replayed event is sent once.
	StoreKey   string          `json:"store_key" river:"unique"`
	Provider   domain.Provider `json:"provider" river:"unique"`
	Name       string          `json:"name"`
	EventID    string          `json:"event_id" river:"unique"`
	Time       time.Time       `json:"time"`
	ValueMinor int64           `json:"value_minor"`
	Currency   string          `json:"currency"`
	Items      []string        `json:"items,omitempty"`
	OrderID    string          `json:"order_id,omitempty"`
	URL        string          `json:"url,omitempty"`
	User       capi.User       `json:"user,omitzero"` // never a phone; empty for a Purchase
}

// Kind implements river.JobArgs.
func (ConversionArgs) Kind() string { return "marketing.send_conversion" }

// conversionAttempts bounds a conversion's tries; River's backoff spreads
// them over about an hour, well inside the providers' windows.
const conversionAttempts = 8

// Track reports a conversion to every provider the store sends server
// events to (enabled, with a server API and a token), one job each, in
// one transaction. A store with none reports nothing.
func (s *Service) Track(ctx context.Context, c Conversion) error {
	if s.jobs == nil {
		return errors.New("marketing: no job inserter")
	}
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := store.Pixels(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if sp, ok := domain.SpecOf(r.Provider); !ok || !sp.Server || !r.Enabled || !r.HasServerToken {
				continue
			}
			user := c.User
			user.Phone = "" // a Purchase's is read from its order when it is sent
			args := ConversionArgs{TenantArgs: t, StoreKey: t.StoreID.String(), Provider: r.Provider, Name: c.Name, EventID: c.EventID, Time: c.Time.UTC(),
				ValueMinor: c.Value.Amount(), Currency: string(c.Value.Currency()), Items: c.Items, OrderID: c.OrderID, URL: c.URL, User: user}
			if _, err = s.jobs.InsertTx(ctx, tx, args, &river.InsertOpts{MaxAttempts: conversionAttempts,
				UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 24 * time.Hour}}); err != nil {
				return fmt.Errorf("marketing: enqueue conversion: %w", err)
			}
		}
		return nil
	})
}

// TrackPurchase reports an order's Purchase, with the event_id its
// thank-you page reported. An order without signals is not reported:
// the shopper did not allow it (or its store has no pixels).
func (s *Service) TrackPurchase(ctx context.Context, order ulid.ID) error {
	if s.orders == nil {
		return errors.New("marketing: no orders")
	}
	p, ok, err := s.orders.Purchase(ctx, order)
	if err != nil || !ok {
		return err
	}
	// Who it matches is read again when it is sent.
	return s.Track(ctx, Conversion{Name: capi.Purchase, EventID: PurchaseEventID(p.OrderID), Time: p.PlacedAt, Value: p.Total,
		Items: p.Items, OrderID: p.OrderID.String(), URL: p.URL})
}

// purchaser is who a Purchase matches: its order's phone, customer,
// country and signals, read when it is sent; ok is false when the order
// has none any more.
func (s *Service) purchaser(ctx context.Context, order string) (capi.User, bool, error) {
	id, err := ulid.Parse(order)
	if err != nil || s.orders == nil {
		return capi.User{}, false, river.JobCancel(fmt.Errorf("marketing: purchase of %q: %w", order, errors.Join(err, errNoOrders(s.orders == nil))))
	}
	p, ok, err := s.orders.Purchase(ctx, id)
	if err != nil || !ok {
		return capi.User{}, ok, err
	}
	u := p.Signals
	u.Phone, u.ExternalID, u.Country = p.Phone, p.CustomerID, p.Country
	return u, true, nil
}

func errNoOrders(missing bool) error {
	if missing {
		return errors.New("no orders")
	}
	return nil
}

// SendConversion runs one conversion job: it opens the store's token for
// the provider and the sealed user, and sends. A pixel removed, turned
// off or without its token since is dropped; a provider's refusal for
// good is too.
func (s *Service) SendConversion(ctx context.Context, a ConversionArgs) error {
	sender, ok := s.senders[a.Provider]
	if s.sealer == nil {
		return errNoSealer
	}
	if !ok {
		return river.JobCancel(fmt.Errorf("marketing: no sender for %s", a.Provider))
	}
	var row store.Row
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		row, err = store.Pixel(ctx, tx, a.Provider)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) || err == nil && (!row.Enabled || len(row.Token) == 0) {
		return nil // the merchant disconnected it since: nothing to send
	}
	if err != nil {
		return err
	}
	token, err := s.sealer.Open(ctx, row.Token, envelope.ConnectionAAD(module, string(a.Provider), a.StoreID, row.ID))
	if err != nil {
		return river.JobCancel(fmt.Errorf("marketing: open token: %w", err))
	}
	user := a.User
	if a.Name == capi.Purchase {
		var ok bool
		if user, ok, err = s.purchaser(ctx, a.OrderID); err != nil || !ok {
			return err // its signals were dropped since: nothing to match it with
		}
	}
	cur := money.Currency(a.Currency)
	e := capi.Event{Name: a.Name, ID: a.EventID, Time: a.Time, ValueMinor: a.ValueMinor, Units: cur.MinorUnits(), Currency: a.Currency,
		Items: a.Items, OrderID: a.OrderID, URL: a.URL, User: user}
	err = sender.Send(ctx, capi.Pixel{ID: row.PixelID, Token: string(token), TestCode: row.TestCode}, e)
	if errors.Is(err, capi.ErrRejected) {
		return river.JobCancel(err) // a bad token or pixel: the merchant must fix it; retrying cannot
	}
	return err
}

// AddWorkers registers the marketing jobs' workers.
func (s *Service) AddWorkers(w *river.Workers) error {
	return river.AddWorkerSafely(w, &conversionWorker{svc: s})
}

type conversionWorker struct {
	river.WorkerDefaults[ConversionArgs]
	svc *Service
}

func (w *conversionWorker) Work(ctx context.Context, job *river.Job[ConversionArgs]) error {
	return w.svc.SendConversion(ctx, job.Args)
}
