// Package app holds the analytics module's use cases (ADR-0069): keeping
// each order's facts from the orders module's events, computing the
// store's daily profit figures in scheduled jobs, and reading them for
// the dashboard on the read pool (ADR-0064).
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// StoreInfo is what analytics needs of the store in ctx: its currency
// and its time zone (an order's day is the store's).
type StoreInfo struct {
	Currency string
	Location *time.Location
}

// StoreSource returns the StoreInfo of the store in ctx; cmd/platform
// adapts tenancy.
type StoreSource func(ctx context.Context) (StoreInfo, error)

// Deps are the service's dependencies. Roles fill what they use: the
// worker learns and computes (Orders, Charges, Jobs); the api reads
// (Read) and takes the merchant's entries.
type Deps struct {
	DB      postgres.Beginner
	Read    *postgres.ReadPool
	Orders  orders.Outcomes
	Charges shipping.Charges
	Stores  StoreSource
	Jobs    jobs.Inserter
	// Plan says whether the store's plan has the dashboard; nil has it
	// (tests).
	Plan  entitlements.Entitlements
	Audit audit.Recorder
	IDs   *ulid.Generator
	Now   func() time.Time
}

// Service implements the use cases.
type Service struct {
	db      postgres.Beginner
	read    *postgres.ReadPool
	orders  orders.Outcomes
	charges shipping.Charges
	stores  StoreSource
	jobs    jobs.Inserter
	plan    entitlements.Entitlements
	audit   audit.Recorder
	ids     *ulid.Generator
	now     func() time.Time
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	if d.Plan == nil {
		d.Plan = entitlements.Unrestricted()
	}
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{db: d.DB, read: d.Read, orders: d.Orders, charges: d.Charges, stores: d.Stores, jobs: d.Jobs, plan: d.Plan,
		audit: d.Audit, ids: d.IDs, now: d.Now}
}

// Errors.
var (
	ErrNotFound = errs.NotFound("ad_spend_not_found", "ad spend not found")
	errNoRead   = errors.New("analytics: the dashboard needs the read pool (api role)")
)

// fact turns an order into its facts (ADR-0069): its store-local day,
// what the customer pays for the items (the discount spread over the
// lines by their totals), the delivery charged, and the line costs.
func fact(o orders.Order, loc *time.Location) (store.Fact, error) {
	revenue, err := o.Subtotal.Sub(o.Discount)
	if err != nil {
		return store.Fact{}, err
	}
	onDelivery, err := o.OnDelivery()
	if err != nil {
		return store.Fact{}, err
	}
	d := domain.Order{ID: o.ID.String(), Day: o.CreatedAt.In(loc).Format(store.DayLayout), State: domain.StateOf(string(o.Status)),
		Governorate: o.Contact.Governorate, Campaign: text.Clip(o.Attribution.Campaign, 100), Revenue: revenue.Amount(),
		Shipping: o.Shipping.Amount(), CODFee: o.CODFee.Amount(), OnDelivery: onDelivery.Amount()}
	if o.DepositStatus == orders.PaymentPaid {
		d.DepositPaid = o.Deposit.Amount()
	}
	f := store.Fact{OrderID: o.ID, Currency: string(o.Total.Currency()), RiskBand: o.RiskBand}
	sub, discount, left := o.Subtotal.Amount(), o.Discount.Amount(), o.Discount.Amount()
	for i, l := range o.Lines {
		share := int64(0)
		if sub > 0 {
			share = discount * l.Total.Amount() / sub
		}
		if i == len(o.Lines)-1 {
			share = left // the last line takes what integer shares left
		}
		left -= share
		line := domain.Line{ProductID: l.ProductID.String(), Title: text.Clip(l.Title, 200), Qty: l.Qty, Revenue: l.Total.Amount() - share}
		if l.UnitCost != nil {
			c := l.UnitCost.Amount() * int64(l.Qty)
			line.Cost = &c
		}
		d.Lines = append(d.Lines, line)
		f.ProductIDs = append(f.ProductIDs, l.ProductID)
	}
	f.Order = d
	return f, nil
}

// Learn keeps what one of the store's orders is now; the orders module's
// events call it, in any order and any number of times. The store's
// first order queues the backfill of its past.
func (s *Service) Learn(ctx context.Context, id ulid.ID) error {
	o, err := s.orders.Get(ctx, id)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return nil // gone meanwhile: nothing to learn
	}
	if err != nil {
		return fmt.Errorf("analytics: read order: %w", err)
	}
	info, err := s.stores(ctx)
	if err != nil {
		return err
	}
	f, err := fact(o, info.Location)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.SaveFact(ctx, tx, f); err != nil {
			return err
		}
		queued, err := store.QueueBackfill(ctx, tx)
		if err != nil || !queued || s.jobs == nil {
			return err
		}
		t, err := jobs.ForTenant(ctx)
		if err != nil {
			return err
		}
		if _, err = s.jobs.InsertTx(ctx, tx, BackfillArgs{TenantArgs: t}, nil); err != nil {
			return fmt.Errorf("analytics: queue backfill: %w", err)
		}
		return nil
	})
}

// backfillPage is how many past orders a backfill transaction keeps.
const backfillPage = 100

// Backfill keeps the facts of all the store's past orders, newest first,
// a page per transaction; it is idempotent.
func (s *Service) Backfill(ctx context.Context) (int, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return 0, err
	}
	var before ulid.ID
	n := 0
	for {
		// A page of orders with their lines: two queries, not one an order.
		page, err := s.orders.BeforeWithLines(ctx, before, backfillPage)
		if err != nil {
			return n, fmt.Errorf("analytics: backfill: %w", err)
		}
		if len(page) == 0 {
			return n, nil
		}
		facts := make([]store.Fact, 0, len(page))
		for _, o := range page {
			f, fErr := fact(o, info.Location)
			if fErr != nil {
				return n, fErr
			}
			facts = append(facts, f)
		}
		if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
			for _, f := range facts {
				if sErr := store.SaveFact(ctx, tx, f); sErr != nil {
					return sErr
				}
			}
			return nil
		}); err != nil {
			return n, err
		}
		n += len(page)
		if len(page) < backfillPage {
			return n, nil
		}
		before = page[len(page)-1].ID
	}
}

// rollupDays is how many dirty days one transaction computes.
const rollupDays = 31

// Rollup computes the store's dirty days again, a month of days per
// transaction, until none is left. A day an order moves into meanwhile
// is marked again and computed by the next run.
func (s *Service) Rollup(ctx context.Context) (int, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for {
		var days []time.Time
		err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
			var tErr error
			if days, tErr = store.TakeDirty(ctx, tx, rollupDays); tErr != nil || len(days) == 0 {
				return tErr
			}
			settings, tErr := store.Settings(ctx, tx)
			if tErr != nil {
				return tErr
			}
			for _, day := range days {
				os, ads, dErr := store.Day(ctx, tx, day)
				if dErr != nil {
					return dErr
				}
				var rows []domain.Row
				if len(os) > 0 || len(ads) > 0 {
					rows = domain.DayRows(os, settings, ads)
				}
				if dErr = store.SaveDay(ctx, tx, day, info.Currency, rows); dErr != nil {
					return dErr
				}
			}
			return nil
		})
		if err != nil || len(days) == 0 {
			return total, err
		}
		total += len(days)
	}
}

// Sweep asks shipping what carriers kept of the store's delivered and
// returned orders of the last 60 days whose charge is not known yet, and
// records what is (ADR-0068): their days are computed again. A batch of
// shipping.MaxChargesOrders per call, at most 20 batches a run.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	since := s.now().AddDate(0, 0, -60)
	found := 0
	skip := 0
	for range 20 {
		var batch map[ulid.ID]time.Time
		if err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
			batch, err = store.Uncharged(ctx, tx, since, shipping.MaxChargesOrders, skip)
			return err
		}); err != nil {
			return found, err
		}
		if len(batch) == 0 {
			return found, nil
		}
		ids := make([]ulid.ID, 0, len(batch))
		for id := range batch {
			ids = append(ids, id)
		}
		charges, err := s.charges.CarrierCharges(ctx, ids)
		if err != nil {
			return found, fmt.Errorf("analytics: carrier charges: %w", err)
		}
		set := make(map[ulid.ID]int64, len(charges))
		var days []time.Time
		for id, c := range charges {
			set[id] = c.Amount()
			days = append(days, batch[id])
		}
		if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
			if cErr := store.SetCarrierCharges(ctx, tx, set); cErr != nil || len(days) == 0 {
				return cErr
			}
			return store.MarkDirty(ctx, tx, days...)
		}); err != nil {
			return found, err
		}
		found += len(set)
		skip += len(batch) - len(set) // charged ones leave the list; the rest are skipped next
		if len(batch) < shipping.MaxChargesOrders {
			return found, nil
		}
	}
	return found, nil
}
