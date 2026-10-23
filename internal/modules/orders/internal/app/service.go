// Package app holds the orders use cases (ADR-0021): placing an order
// (idempotent, deduplicated, with its customer and stock in one
// transaction), moving it through the state machine, and reading it.
// Every state change writes the order's timeline, an audit entry and a
// domain event in the transaction that changes it.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Errors. Match with errors.Is.
var (
	ErrOrderNotFound        = errs.NotFound("order_not_found", "order not found")
	ErrIdempotencyKeyReused = errs.Conflict("idempotency_key_reused", "this Idempotency-Key was already used for a different order")
	ErrVersionConflict      = errs.Conflict("version_conflict", "the order changed since it was read; reload it and try again")
	errCurrencyMismatch     = errors.New("orders: a variant is priced in another currency than the store's")
	errNotDelivered         = errs.Validation(errs.FieldError{Field: "governorate", Code: "not_delivered",
		Message: "the store does not deliver to this governorate"})
)

// KeyRetention is how long an Idempotency-Key is remembered.
const KeyRetention = 7 * 24 * time.Hour

// Catalog is what orders use of the catalog's api.go.
type Catalog interface {
	SellableVariants(ctx context.Context, ids []ulid.ID) ([]catalog.SellableVariant, error)
	ReserveStockTx(ctx context.Context, tx pgx.Tx, req catalog.ReserveRequest) (catalog.Reservation, error)
	ReleaseStockTx(ctx context.Context, tx pgx.Tx, ref string) error
	CommitStockTx(ctx context.Context, tx pgx.Tx, ref string) error
	RestockTx(ctx context.Context, tx pgx.Tx, ref string) error
}

// Customers is what orders use of the customers' api.go.
type Customers interface {
	Blocked(ctx context.Context, p phone.E164) (bool, error)
	RecordOrderTx(ctx context.Context, tx pgx.Tx, p phone.E164, name string) (customers.Customer, error)
}

// Shipping is what orders use of the shipping module's api.go.
type Shipping interface {
	Quote(ctx context.Context, req shipping.QuoteRequest) (shipping.Quote, error)
}

// StoreInfo is what orders need to know about the store in ctx.
type StoreInfo struct {
	Currency money.Currency
	Country  geo.Country
	Location *time.Location // the store's time zone, for exports
}

// StoreSource returns the StoreInfo of the store in ctx; cmd/platform
// adapts the tenancy module to it.
type StoreSource func(ctx context.Context) (StoreInfo, error)

// Service implements the use cases.
type Service struct {
	db        postgres.Beginner
	ids       *ulid.Generator
	audit     audit.Recorder
	events    eventbus.Publisher
	catalog   Catalog
	customers Customers
	shipping  Shipping
	stores    StoreSource
	auth      authz.Authorizer
	now       func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB        postgres.Beginner
	IDs       *ulid.Generator
	Audit     audit.Recorder
	Events    eventbus.Publisher
	Catalog   Catalog
	Customers Customers
	// Shipping quotes the delivery rate and COD fee; nil charges nothing
	// (tests that are not about money).
	Shipping Shipping
	Stores   StoreSource
	// Auth checks each transition's own permission (domain.Step).
	Auth authz.Authorizer
}

// NewService builds the service. A nil Audit or Events discards.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	if d.Events == nil {
		d.Events = eventbus.Nop{}
	}
	return &Service{db: d.DB, ids: d.IDs, audit: d.Audit, events: d.Events, catalog: d.Catalog, customers: d.Customers,
		shipping: d.Shipping, stores: d.Stores, auth: d.Auth, now: time.Now}
}

// SetClock replaces the clock (tests of the duplicate window).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Placement is the outcome of PlaceOrder.
type Placement struct {
	Order domain.Order
	// Replayed: the Idempotency-Key was seen before; this is the order the
	// first request created.
	Replayed bool
	// Duplicate: the same phone ordered the same lines within the
	// duplicate window; this is that earlier order.
	Duplicate bool
}

// errAlreadyPlaced aborts the transaction when the order turns out to
// exist already (a replayed key or a duplicate submit).
type errAlreadyPlaced struct {
	id        ulid.ID
	duplicate bool
}

func (e errAlreadyPlaced) Error() string { return "orders: already placed" }

// PlaceOrder places a new order: validated, priced from the catalog, with
// its customer recorded and its stock held, in one transaction.
//
//   - A repeated Idempotency-Key with the same request returns the first
//     order (Replayed); with another request it is ErrIdempotencyKeyReused.
//     Concurrent repeats wait for the first and return its order.
//   - From the storefront, the same phone ordering the same lines within
//     domain.DuplicateWindow returns the earlier order (Duplicate): the
//     form's double submit without a key.
//   - A blocked customer is customers.ErrCustomerBlocked; callers facing
//     shoppers must turn it into a generic refusal.
func (s *Service) PlaceOrder(ctx context.Context, in domain.NewOrder) (Placement, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return Placement{}, err
	}
	d, err := domain.ValidateNewOrder(in, info.Country)
	if err != nil {
		return Placement{}, err
	}
	if d.IdempotencyKey != "" {
		if p, done, replayErr := s.replay(ctx, d); done || replayErr != nil {
			return p, replayErr
		}
	}
	blocked, err := s.customers.Blocked(ctx, d.Contact.Phone)
	if err != nil {
		return Placement{}, err
	}
	if blocked {
		return Placement{}, customers.ErrCustomerBlocked
	}
	o, err := s.price(ctx, d, info.Currency)
	if err != nil {
		return Placement{}, err
	}

	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return s.placeTx(ctx, tx, d, &o) })
	var placed errAlreadyPlaced
	switch {
	case errors.As(err, &placed) && placed.duplicate:
		existing, getErr := s.Get(ctx, placed.id)
		return Placement{Order: existing, Duplicate: true}, getErr
	case errors.As(err, &placed):
		// Lost the race for the key: the winner has committed by now.
		return s.mustReplay(ctx, d)
	case err != nil:
		return Placement{}, err
	}
	return Placement{Order: o}, nil
}

func (s *Service) placeTx(ctx context.Context, tx pgx.Tx, d domain.Draft, o *domain.Order) error {
	if d.Source == domain.SourceStorefront {
		if err := store.LockPhone(ctx, tx, d.Contact.Phone); err != nil {
			return err
		}
		id, found, err := store.FindDuplicate(ctx, tx, d.Contact.Phone, o.CreatedAt.Add(-domain.DuplicateWindow), d.LinesHash)
		if err != nil {
			return err
		}
		if found {
			return errAlreadyPlaced{id: id, duplicate: true}
		}
	}
	if d.IdempotencyKey != "" {
		inserted, err := store.InsertKey(ctx, tx, d.IdempotencyKey, d.Fingerprint, o.ID)
		if err != nil {
			return err
		}
		if !inserted {
			return errAlreadyPlaced{}
		}
		if err = store.PurgeKeys(ctx, tx, o.CreatedAt.Add(-KeyRetention)); err != nil {
			return err
		}
	}
	c, err := s.customers.RecordOrderTx(ctx, tx, d.Contact.Phone, d.Contact.Name)
	if err != nil {
		return err
	}
	o.CustomerID = c.ID
	if _, err = s.catalog.ReserveStockTx(ctx, tx, catalog.ReserveRequest{Ref: stockRef(o.ID), Lines: reserveLines(d.Lines), Hold: true}); err != nil {
		return err
	}
	if err = store.Insert(ctx, tx, *o, d.LinesHash); err != nil {
		return err
	}
	return s.recordTx(ctx, tx, *o, "", domain.StatusPending, "", "order.placed", domain.EventOrderPlaced,
		func(m event.Meta) event.Event { return domain.OrderPlaced{Meta: m, OrderID: o.ID, Source: o.Source} })
}

// replay returns the order a known key created. done is false when the
// key is new.
func (s *Service) replay(ctx context.Context, d domain.Draft) (p Placement, done bool, err error) {
	var k store.Key
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		k, txErr = store.GetKey(ctx, tx, d.IdempotencyKey)
		return txErr
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return Placement{}, false, nil
	case err != nil:
		return Placement{}, true, err
	case k.Fingerprint != d.Fingerprint:
		return Placement{}, true, ErrIdempotencyKeyReused
	}
	o, err := s.Get(ctx, k.OrderID)
	return Placement{Order: o, Replayed: true}, true, err
}

func (s *Service) mustReplay(ctx context.Context, d domain.Draft) (Placement, error) {
	p, done, err := s.replay(ctx, d)
	if err == nil && !done {
		err = fmt.Errorf("orders: idempotency key %q vanished", d.IdempotencyKey)
	}
	return p, err
}

// price builds the order from the catalog's current prices (every line
// must still be for sale, in the store's currency) and the delivery
// quote for its address.
func (s *Service) price(ctx context.Context, d domain.Draft, cur money.Currency) (domain.Order, error) {
	id := s.ids.New()
	o := domain.Order{ID: id, Status: domain.StatusPending, Version: 1, Source: d.Source, Contact: d.Contact, CreatedAt: domain.CreatedAtOf(id)}
	o.UpdatedAt, o.StatusChangedAt = o.CreatedAt, o.CreatedAt
	var err error
	if o.Lines, o.Subtotal, o.ItemCount, err = s.priceLines(ctx, d.Lines, cur); err != nil {
		return domain.Order{}, err
	}
	q, err := s.charges(ctx, d.Contact.Governorate, d.Contact.City, o.Subtotal)
	if err != nil {
		return domain.Order{}, err
	}
	if !q.Deliverable {
		return domain.Order{}, errNotDelivered
	}
	o.Shipping, o.CODFee, o.Total = q.Shipping, q.CODFee, q.Total
	return o, nil
}

// priceLines prices lines from the catalog's current prices.
func (s *Service) priceLines(ctx context.Context, in []domain.LineInput, cur money.Currency) (lines []domain.Line, subtotal money.Money, items int, err error) {
	ids := make([]ulid.ID, len(in))
	for i, l := range in {
		ids[i] = l.VariantID
	}
	vs, err := s.catalog.SellableVariants(ctx, ids)
	if err != nil {
		return nil, money.Money{}, 0, err
	}
	byID := make(map[ulid.ID]catalog.SellableVariant, len(vs))
	for _, v := range vs {
		byID[v.VariantID] = v
	}
	if subtotal, err = money.Zero(cur); err != nil {
		return nil, money.Money{}, 0, err
	}
	lines = make([]domain.Line, len(in))
	var unavailable []errs.FieldError
	for i, l := range in {
		v, ok := byID[l.VariantID]
		if !ok {
			unavailable = append(unavailable, errs.FieldError{Field: fmt.Sprintf("lines[%d].variant_id", i), Code: "unavailable",
				Message: "this product is no longer for sale"})
			continue
		}
		if v.Price.Currency() != cur {
			return nil, money.Money{}, 0, errCurrencyMismatch
		}
		total, mulErr := v.Price.MulInt(int64(l.Qty))
		if mulErr != nil {
			return nil, money.Money{}, 0, mulErr
		}
		if subtotal, err = subtotal.Add(total); err != nil {
			return nil, money.Money{}, 0, err
		}
		lines[i] = domain.Line{VariantID: v.VariantID, ProductID: v.ProductID, Title: v.ProductTitle,
			VariantTitle: strings.Join(v.OptionValues, " / "), SKU: v.SKU, Qty: l.Qty, UnitPrice: v.Price, Total: total}
		items += l.Qty
	}
	if len(unavailable) > 0 {
		return nil, money.Money{}, 0, errs.Validation(unavailable...)
	}
	return lines, subtotal, items, nil
}

// Quote is what an order would cost: its lines at today's prices and
// its delivery to an address.
type Quote struct {
	Subtotal     money.Money
	Shipping     money.Money
	CODFee       money.Money
	Total        money.Money
	Deliverable  bool // false: the store does not deliver there (amounts are the subtotal only)
	FreeShipping bool
}

// charges quotes delivering subtotal's order to governorate and city.
func (s *Service) charges(ctx context.Context, governorate, city string, subtotal money.Money) (Quote, error) {
	q := Quote{Subtotal: subtotal, Total: subtotal, Deliverable: true}
	var err error
	if q.Shipping, err = money.Zero(subtotal.Currency()); err != nil {
		return Quote{}, err
	}
	q.CODFee = q.Shipping
	if s.shipping == nil {
		return q, nil
	}
	sq, err := s.shipping.Quote(ctx, shipping.QuoteRequest{Governorate: governorate, City: city, Subtotal: subtotal})
	if err != nil {
		return Quote{}, err
	}
	if !sq.Deliverable {
		q.Deliverable = false
		return q, nil
	}
	q.Shipping, q.CODFee, q.FreeShipping = sq.Shipping, sq.CODFee, sq.FreeShipping
	if q.Total, err = q.Total.Add(q.Shipping); err != nil {
		return Quote{}, err
	}
	if q.Total, err = q.Total.Add(q.CODFee); err != nil {
		return Quote{}, err
	}
	return q, nil
}

// Quote prices an order before it is placed, the same way PlaceOrder
// will: lines from the catalog, delivery from shipping.
func (s *Service) Quote(ctx context.Context, in domain.QuoteInput) (Quote, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return Quote{}, err
	}
	q, err := domain.ValidateQuote(in, info.Country)
	if err != nil {
		return Quote{}, err
	}
	_, subtotal, _, err := s.priceLines(ctx, q.Lines, info.Currency)
	if err != nil {
		return Quote{}, err
	}
	return s.charges(ctx, q.Governorate, q.City, subtotal)
}

// Transition moves an order to status to, if the state machine allows it
// and the caller holds that step's permission. version, when not zero,
// must be the order's current version (optimistic concurrency).
func (s *Service) Transition(ctx context.Context, id ulid.ID, to domain.Status, note string, version int) (domain.Order, error) {
	note, err := cleanNote(note)
	if err != nil {
		return domain.Order{}, err
	}
	var o domain.Order
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		o, txErr = s.transitionTx(ctx, tx, id, to, note, version)
		return txErr
	})
	return o, notFound(err)
}

func (s *Service) transitionTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to domain.Status, note string, version int) (domain.Order, error) {
	o, err := store.Get(ctx, tx, id, true)
	if err != nil {
		return domain.Order{}, err
	}
	if version != 0 && version != o.Version {
		return domain.Order{}, ErrVersionConflict
	}
	step, err := domain.Transition(o.Status, to)
	if err != nil {
		return domain.Order{}, err
	}
	if err = s.auth.Authorize(ctx, step.Permission); err != nil {
		return domain.Order{}, err
	}
	ref := stockRef(o.ID)
	switch step.Stock {
	case domain.StockRelease:
		err = s.catalog.ReleaseStockTx(ctx, tx, ref)
	case domain.StockCommit:
		err = s.catalog.CommitStockTx(ctx, tx, ref)
	case domain.StockRestock:
		err = s.catalog.RestockTx(ctx, tx, ref)
	case domain.StockNone:
	}
	if err != nil {
		return domain.Order{}, err
	}
	moved, err := store.SetStatus(ctx, tx, o.ID, to, s.now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return domain.Order{}, err
	}
	return moved, s.recordTx(ctx, tx, moved, o.Status, to, note, "order."+string(to), domain.EventOrderStatusChanged,
		func(m event.Meta) event.Event {
			return domain.OrderStatusChanged{Meta: m, OrderID: o.ID, From: o.Status, To: to}
		})
}

// recordTx writes what every state change leaves behind: the timeline
// row, the audit entry (IDs and statuses only, no contact details) and
// the domain event of type typ.
func (s *Service) recordTx(ctx context.Context, tx pgx.Tx, o domain.Order, from, to domain.Status, note, action, typ string,
	build func(event.Meta) event.Event) error {
	e := domain.Event{ID: s.ids.New(), From: from, To: to, Note: note, At: o.StatusChangedAt}
	if p, ok := authz.PrincipalFrom(ctx); ok {
		e.ActorUserID = &p.UserID
	}
	if err := store.InsertEvent(ctx, tx, o.ID, e); err != nil {
		return err
	}
	meta := map[string]any{"to": string(to)}
	if from != "" {
		meta["from"] = string(from)
	}
	if err := s.audit.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: "order", TargetID: o.ID.String(), Metadata: meta}); err != nil {
		return err
	}
	m, err := event.MetaFor(ctx, s.ids, typ, 1)
	if err != nil {
		return err
	}
	return s.events.PublishTx(ctx, tx, build(m))
}

// stockRef is the catalog reservation an order holds (ADR-0020).
func stockRef(id ulid.ID) string { return "order:" + id.String() }

func reserveLines(lines []domain.LineInput) []catalog.ReserveLine {
	out := make([]catalog.ReserveLine, len(lines))
	for i, l := range lines {
		out[i] = catalog.ReserveLine{VariantID: l.VariantID, Qty: l.Qty}
	}
	return out
}

func notFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return ErrOrderNotFound
	}
	return err
}
