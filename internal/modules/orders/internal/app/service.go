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
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
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

// Coupons is what orders use of promotions (ADR-0042); cmd/platform
// adapts the promotions module to it. A code that does not apply is a
// *CouponRejected.
type Coupons interface {
	// Preview is what code would take off items costing subtotal.
	Preview(ctx context.Context, code string, subtotal money.Money) (CouponDiscount, error)
	// RedeemTx takes code's use for an order of customer, in tx.
	RedeemTx(ctx context.Context, tx pgx.Tx, code string, subtotal money.Money, customer, order ulid.ID) (CouponDiscount, error)
}

// CouponDiscount is what a coupon takes off an order: Amount off the
// items, and the delivery rate when FreeShipping.
type CouponDiscount struct {
	Amount       money.Money
	FreeShipping bool
}

// CouponRejected is a code that does not apply: Reason as promotions
// names it (not_found, inactive, not_started, expired, min_subtotal,
// used_up, per_customer).
type CouponRejected struct {
	Reason           string
	MinSubtotalMinor int64
}

func (e *CouponRejected) Error() string { return "orders: coupon rejected: " + e.Reason }

// couponMessages say why a code does not apply, for the field error.
var couponMessages = map[string]string{ //nolint:gochecknoglobals // constant table
	"inactive": "this code is not active", "not_started": "this code is not valid yet", "expired": "this code has expired",
	"min_subtotal": "the order is below this code's minimum", "used_up": "this code has been used up",
	"per_customer": "you have already used this code",
}

// couponError turns a rejected code into the order's field error; other
// errors pass through.
func couponError(err error) error {
	var r *CouponRejected
	if !errors.As(err, &r) {
		return err
	}
	msg, ok := couponMessages[r.Reason]
	if !ok {
		r.Reason, msg = "not_found", "this code does not exist"
	}
	return errs.Validation(errs.FieldError{Field: "coupon_code", Code: r.Reason, Message: msg})
}

// errCouponChanged: the coupon's terms changed between the order's
// quote and its placing; the shopper submits again.
var errCouponChanged = errs.Validation(errs.FieldError{Field: "coupon_code", Code: "changed", Message: "this code changed; submit again"})

// previewCoupon is what the order's code takes off; nil without a code.
func (s *Service) previewCoupon(ctx context.Context, code string, subtotal money.Money) (*CouponDiscount, error) {
	if code == "" {
		return nil, nil
	}
	if s.coupons == nil {
		return nil, couponError(&CouponRejected{Reason: "not_found"})
	}
	d, err := s.coupons.Preview(ctx, code, subtotal)
	if err != nil {
		return nil, couponError(err)
	}
	return &d, nil
}

// StoreInfo is what orders need to know about the store in ctx.
type StoreInfo struct {
	Currency money.Currency
	Country  geo.Country
	Location *time.Location // the store's time zone, for exports
}

// RiskCheck gives a new storefront order the store's risk rules' verdict
// on its customer's phone (ADR-0066); cmd/platform adapts the risk
// module to it.
type RiskCheck interface {
	Check(ctx context.Context, p phone.E164) (domain.Risk, error)
}

// CardCheck says whether the store in ctx can take card payments now: a
// gateway connected, and the plan's card_payments (cmd/platform adapts
// payments.CardCheck). A prepaid delivery needs it (ADR-0067).
type CardCheck interface {
	CardEnabled(ctx context.Context) (bool, error)
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
	coupons   Coupons
	stores    StoreSource
	auth      authz.Authorizer
	plan      entitlements.Entitlements
	risk      RiskCheck
	cards     CardCheck
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
	// Coupons applies coupon codes; nil makes every code not_found.
	Coupons Coupons
	Stores  StoreSource
	// Auth checks each transition's own permission (domain.Step).
	Auth authz.Authorizer
	// Plan counts the month's orders against the store's plan, never
	// refusing one (ADR-0045); nil counts nothing (tests).
	Plan entitlements.Entitlements
	// Risk applies the store's risk rules to storefront orders
	// (ADR-0066); nil applies none.
	Risk RiskCheck
	// Cards says whether a delivery can be prepaid by card (ADR-0067);
	// nil cannot, so a prepay verdict holds the order instead.
	Cards CardCheck
}

// NewService builds the service. A nil Audit or Events discards.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	if d.Events == nil {
		d.Events = eventbus.Nop{}
	}
	if d.Plan == nil {
		d.Plan = entitlements.Unrestricted()
	}
	return &Service{db: d.DB, ids: d.IDs, audit: d.Audit, events: d.Events, catalog: d.Catalog, customers: d.Customers,
		shipping: d.Shipping, coupons: d.Coupons, stores: d.Stores, auth: d.Auth, plan: d.Plan, risk: d.Risk, cards: d.Cards, now: time.Now}
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
	o, coupon, err := s.price(ctx, d, info.Currency)
	if err != nil {
		return Placement{}, err
	}
	// The rules look at shoppers' cash orders: staff placing an order
	// have spoken to the customer, and a card order is paid upfront.
	if d.Source == domain.SourceStorefront && o.PaymentMethod == domain.PaymentCOD && s.risk != nil {
		r, rErr := s.risk.Check(ctx, d.Contact.Phone)
		if rErr != nil {
			return Placement{}, rErr
		}
		o.RiskAction, o.RiskBand = r.Action, r.Band
		if r.Action == domain.RiskPrepay {
			if rErr = s.prepay(ctx, &o); rErr != nil {
				return Placement{}, rErr
			}
		}
	}

	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return s.placeTx(ctx, tx, d, &o, coupon) })
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

func (s *Service) placeTx(ctx context.Context, tx pgx.Tx, d domain.Draft, o *domain.Order, coupon *CouponDiscount) error {
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
	// The month's orders against the plan: counted, never refused; the
	// merchant hears at 80% and 100% (entitlements.UsageCrossed).
	if _, err = s.plan.CountTx(ctx, tx, entitlements.OrdersPerMonth, 1); err != nil {
		return err
	}
	if !d.Signals.IsZero() { // with the order, so its conversions find them (ADR-0043)
		if err = store.InsertSignals(ctx, tx, *o, d.Signals); err != nil {
			return err
		}
	}
	if coupon != nil {
		// The code's use is taken last, so the coupon's row is locked
		// only until this commit (ADR-0042), now that the customer is
		// known (the per-customer limit); terms that changed since the
		// quote send the shopper back, and nothing of the order stays.
		got, rErr := s.coupons.RedeemTx(ctx, tx, d.CouponCode, o.Subtotal, c.ID, o.ID)
		if rErr != nil {
			return couponError(rErr)
		}
		if got.Amount.Amount() != coupon.Amount.Amount() || got.FreeShipping != coupon.FreeShipping {
			return errCouponChanged
		}
	}
	return s.recordTx(ctx, tx, *o, "", domain.StatusPending, "", "", "order.placed", domain.EventOrderPlaced,
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
// The coupon it previews (nil without one) is what placing must redeem.
func (s *Service) price(ctx context.Context, d domain.Draft, cur money.Currency) (domain.Order, *CouponDiscount, error) {
	id := s.ids.New()
	o := domain.Order{ID: id, Status: domain.StatusPending, Version: 1, Source: d.Source, Contact: d.Contact, Attribution: d.Attribution,
		CreatedAt: domain.CreatedAtOf(id)}
	o.UpdatedAt, o.StatusChangedAt = o.CreatedAt, o.CreatedAt
	var err error
	if o.Lines, o.Subtotal, o.ItemCount, err = s.priceLines(ctx, d.Lines, cur); err != nil {
		return domain.Order{}, nil, err
	}
	coupon, err := s.previewCoupon(ctx, d.CouponCode, o.Subtotal)
	if err != nil {
		return domain.Order{}, nil, err
	}
	q, err := s.charges(ctx, d.Contact.Governorate, d.Contact.City, o.Subtotal, d.PaymentMethod, coupon)
	if err != nil {
		return domain.Order{}, nil, err
	}
	if !q.Deliverable {
		return domain.Order{}, nil, errNotDelivered
	}
	o.Shipping, o.CODFee, o.Discount, o.Total = q.Shipping, q.CODFee, q.Discount, q.Total
	if coupon != nil {
		o.CouponCode = d.CouponCode
	}
	o.PaymentMethod, o.PaymentStatus = d.PaymentMethod, domain.PaymentUnpaid
	if d.PaymentMethod == domain.PaymentCard {
		o.PaymentStatus = domain.PaymentAwaiting
	}
	return o, coupon, nil
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
			VariantTitle: strings.Join(v.OptionValues, " / "), SKU: v.SKU, Qty: l.Qty, UnitPrice: v.Price, Total: total, UnitCost: v.Cost}
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
	Subtotal money.Money
	// Discount is the coupon's, off the items; CouponCode the code.
	Discount     money.Money
	CouponCode   string
	Shipping     money.Money
	CODFee       money.Money
	Total        money.Money
	Deliverable  bool // false: the store does not deliver there (amounts are the subtotal only)
	FreeShipping bool
	// FreeOverMinor is the free-shipping threshold in force, and Delivery
	// how long delivery takes; nil when shipping has none to say.
	FreeOverMinor *int64
	Delivery      *shipping.DeliveryDays
}

// charges quotes delivering subtotal's order to governorate and city; a
// card order pays no cash-on-delivery fee.
// A coupon takes its amount off the items, and waives the delivery rate
// when it gives free shipping (the COD fee stays).
func (s *Service) charges(ctx context.Context, governorate, city string, subtotal money.Money, method domain.PaymentMethod,
	coupon *CouponDiscount) (Quote, error) {
	q := Quote{Subtotal: subtotal, Total: subtotal, Deliverable: true}
	var err error
	if q.Shipping, err = money.Zero(subtotal.Currency()); err != nil {
		return Quote{}, err
	}
	q.CODFee, q.Discount = q.Shipping, q.Shipping
	if coupon != nil {
		q.Discount = coupon.Amount
		if q.Total, err = q.Total.Sub(coupon.Amount); err != nil {
			return Quote{}, err
		}
	}
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
	q.Shipping, q.FreeShipping = sq.Shipping, sq.FreeShipping
	q.FreeOverMinor, q.Delivery = sq.FreeOverMinor, sq.Delivery
	if coupon != nil && coupon.FreeShipping {
		if q.Shipping, err = money.Zero(subtotal.Currency()); err != nil {
			return Quote{}, err
		}
		q.FreeShipping = true
	}
	if method != domain.PaymentCard {
		q.CODFee = sq.CODFee
	}
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
	coupon, err := s.previewCoupon(ctx, q.CouponCode, subtotal)
	if err != nil {
		return Quote{}, err
	}
	out, err := s.charges(ctx, q.Governorate, q.City, subtotal, q.PaymentMethod, coupon)
	if coupon != nil {
		out.CouponCode = q.CouponCode
	}
	return out, err
}

// SetPaymentStatusTx records a card order's payment status on the
// gateway's word (ADR-0029), in the caller's transaction, with an audit
// entry and an orders.order.payment_changed event. actor names the
// system ("gateway:paymob"). Setting the status it has is a no-op.
func (s *Service) SetPaymentStatusTx(ctx context.Context, tx pgx.Tx, id ulid.ID, st domain.PaymentStatus, actor string) (changed bool, err error) {
	if !st.Valid() || !actorRE.MatchString(actor) {
		return false, fmt.Errorf("orders: invalid payment status %q or actor %q", st, actor)
	}
	o, err := store.Get(ctx, tx, id, true)
	if err != nil {
		return false, notFound(err)
	}
	// A cash order's online money is its deposit (ADR-0067).
	deposit := o.PaymentMethod == domain.PaymentCOD && !o.Deposit.IsZero()
	was := o.PaymentStatus
	if deposit {
		was = o.DepositStatus
	}
	if (o.PaymentMethod != domain.PaymentCard && !deposit) || was == st {
		return false, nil
	}
	set := store.SetPaymentStatus
	if deposit {
		set = store.SetDepositStatus
	}
	if err = set(ctx, tx, id, st); err != nil {
		return false, notFound(err)
	}
	action := "order.payment_" + string(st)
	if deposit {
		action = "order.deposit_" + string(st)
	}
	if err = s.audit.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: "order", TargetID: id.String(),
		Metadata: map[string]any{"from": string(was), "to": string(st), "actor": actor}}); err != nil {
		return false, err
	}
	m, err := event.MetaFor(ctx, s.ids, domain.EventOrderPaymentChanged, 1)
	if err != nil {
		return false, err
	}
	return true, s.events.PublishTx(ctx, tx, domain.OrderPaymentChanged{Meta: m, OrderID: id, From: was, To: st, Deposit: deposit})
}

// Transition moves an order to status to, if the state machine allows it
// and the caller holds that step's permission. version, when not zero,
// must be the order's current version (optimistic concurrency).
func (s *Service) Transition(ctx context.Context, id ulid.ID, to domain.Status, note string, version int) (domain.Order, error) {
	return s.TransitionWithReason(ctx, id, to, note, "", version)
}

// TransitionWithReason is Transition with why the order did not reach its
// customer (ADR-0065): refused or no_answer, only on a move to cancelled,
// failed_delivery or returned.
func (s *Service) TransitionWithReason(ctx context.Context, id ulid.ID, to domain.Status, note, reason string, version int) (domain.Order, error) {
	note, err := cleanNote(note)
	if err != nil {
		return domain.Order{}, err
	}
	r, err := domain.ParseReason(reason, to)
	if err != nil {
		return domain.Order{}, err
	}
	var o domain.Order
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		o, _, txErr = s.transitionTx(ctx, tx, id, move{to: to, note: note, reason: r, version: version})
		return txErr
	})
	return o, notFound(err)
}

var actorRE = regexp.MustCompile(`^[a-z]+:[a-z0-9_]{1,32}$`)

// TransitionSystemTx moves an order on behalf of a system (a carrier's
// tracking, ADR-0027) in the caller's transaction: the state machine's
// edges and stock effects, without a person's permission. actor names
// the system ("carrier:bosta") on the timeline and in the audit log.
// Moving an order to the status it has is a no-op (moved is false), so
// a repeated tracking update changes nothing.
func (s *Service) TransitionSystemTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to domain.Status, actor, note string) (moved bool, err error) {
	return s.TransitionSystemReasonTx(ctx, tx, id, to, actor, note, domain.ReasonNone)
}

// TransitionSystemReasonTx is TransitionSystemTx with an outcome reason
// (ADR-0065), e.g. unconfirmed for a confirmation left unanswered. Only a
// move to cancelled, failed_delivery or returned takes one.
func (s *Service) TransitionSystemReasonTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to domain.Status, actor, note string,
	reason domain.OutcomeReason) (moved bool, err error) {
	if !actorRE.MatchString(actor) {
		return false, fmt.Errorf("orders: invalid system actor %q", actor)
	}
	if reason != domain.ReasonNone && !domain.TakesReason(to) {
		return false, fmt.Errorf("orders: reason %q on a move to %s", reason, to)
	}
	switch reason {
	case domain.ReasonNone, domain.ReasonRefused, domain.ReasonNoAnswer, domain.ReasonUnconfirmed:
	default:
		return false, fmt.Errorf("orders: unknown reason %q", reason)
	}
	if note, err = cleanNote(note); err != nil {
		return false, err
	}
	_, moved, err = s.transitionTx(ctx, tx, id, move{to: to, note: note, reason: reason, actor: actor})
	return moved, notFound(err)
}

// prepay asks a cash order's shopper to pay the delivery upfront
// (ADR-0067): the shipping and the COD fee, by card. A store that cannot
// take cards now, or an order that costs nothing to deliver, holds the
// order for review instead: the rules wanted more than a plain order.
func (s *Service) prepay(ctx context.Context, o *domain.Order) error {
	deposit, err := o.Shipping.Add(o.CODFee)
	if err != nil {
		return err
	}
	cards := false
	if s.cards != nil && !deposit.IsZero() {
		if cards, err = s.cards.CardEnabled(ctx); err != nil {
			return err
		}
	}
	if !cards {
		o.RiskAction = domain.RiskHold
		return nil
	}
	o.Deposit, o.DepositStatus = deposit, domain.PaymentAwaiting
	return nil
}

// ReleaseHold lifts the risk rules' hold of a pending order (ADR-0066),
// with orders.manage: the merchant reviewed it and may now confirm it.
// An order not held is ErrNotHeld.
func (s *Service) ReleaseHold(ctx context.Context, id ulid.ID) (domain.Order, error) {
	if err := s.auth.Authorize(ctx, authz.PermOrdersManage); err != nil {
		return domain.Order{}, err
	}
	var o domain.Order
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := store.Get(ctx, tx, id, true); err != nil {
			return err
		}
		released, found, err := store.ReleaseHold(ctx, tx, id, s.now().UTC().Truncate(time.Microsecond))
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrNotHeld
		}
		o = released
		e := domain.Event{ID: s.ids.New(), From: domain.StatusPending, To: domain.StatusPending, Note: "released from the risk hold", At: o.UpdatedAt}
		if p, ok := authz.PrincipalFrom(ctx); ok {
			e.ActorUserID = &p.UserID
		}
		if err = store.InsertEvent(ctx, tx, o.ID, e); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "order.released", TargetType: "order", TargetID: o.ID.String()})
	})
	return o, notFound(err)
}

// move is one transition as asked: by a person (actor empty), or by a
// system on actor's behalf.
type move struct {
	to      domain.Status
	note    string
	reason  domain.OutcomeReason
	version int
	actor   string
}

// transitionTx makes one move. A person's move needs the step's
// permission; a system's skips it and treats the current status as
// already reached.
func (s *Service) transitionTx(ctx context.Context, tx pgx.Tx, id ulid.ID, m move) (domain.Order, bool, error) {
	to, note, actor := m.to, m.note, m.actor
	o, err := store.Get(ctx, tx, id, true)
	if err != nil {
		return domain.Order{}, false, err
	}
	if m.version != 0 && m.version != o.Version {
		return domain.Order{}, false, ErrVersionConflict
	}
	if actor != "" && o.Status == to {
		return o, false, nil
	}
	step, err := domain.Transition(o.Status, to)
	if err != nil {
		return domain.Order{}, false, err
	}
	if to == domain.StatusConfirmed {
		switch {
		case o.RiskAction == domain.RiskHold:
			return domain.Order{}, false, domain.ErrOrderHeld
		case o.RiskAction == domain.RiskVerify && strings.HasPrefix(actor, "customer:"):
			return domain.Order{}, false, domain.ErrNeedsCall
		case o.DepositStatus == domain.PaymentAwaiting && strings.HasPrefix(actor, "customer:"):
			return domain.Order{}, false, domain.ErrDepositDue
		}
	}
	if actor == "" {
		if err = s.auth.Authorize(ctx, step.Permission); err != nil {
			return domain.Order{}, false, err
		}
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
		return domain.Order{}, false, err
	}
	moved, err := store.SetStatus(ctx, tx, o.ID, to, m.reason, s.now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return domain.Order{}, false, err
	}
	return moved, true, s.recordTx(ctx, tx, moved, o.Status, to, note, actor, "order."+string(to), domain.EventOrderStatusChanged,
		func(m event.Meta) event.Event {
			return domain.OrderStatusChanged{Meta: m, OrderID: o.ID, From: o.Status, To: to}
		})
}

// recordTx writes what every state change leaves behind: the timeline
// row, the audit entry (IDs and statuses only, no contact details) and
// the domain event of type typ.
func (s *Service) recordTx(ctx context.Context, tx pgx.Tx, o domain.Order, from, to domain.Status, note, actor, action, typ string,
	build func(event.Meta) event.Event) error {
	e := domain.Event{ID: s.ids.New(), From: from, To: to, Actor: actor, Note: note, At: o.StatusChangedAt}
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
	if actor != "" {
		meta["actor"] = actor
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
