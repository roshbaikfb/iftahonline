// Package store adapts the sqlc queries in analyticsdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time. The dashboard's reads
// (Totals, Days, Breakdown) run on the read pool's transactions
// (ADR-0064); everything else on the primary.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/store/analyticsdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = analyticsdb.DBTX

// ErrNotFound is a row the store does not have.
var ErrNotFound = errors.New("analytics store: not found")

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("analytics store: %w", err)
	}
	return id.ID, nil
}

// DayLayout is how days are written.
const DayLayout = "2006-01-02"

func parseDay(s string) (time.Time, error) {
	d, err := time.Parse(DayLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("analytics store: day %q: %w", s, err)
	}
	return d, nil
}

// Fact is one order's facts as written: the domain's, with the order's
// IDs and what profit does not need.
type Fact struct {
	OrderID    ulid.ID
	Currency   string
	RiskBand   string
	ProductIDs []ulid.ID // one per line, in order
	Order      domain.Order
}

// SaveFact writes an order's facts and lines, and marks its day (and the
// day it had, if it moved) dirty, in the caller's transaction.
func SaveFact(ctx context.Context, tx Tx, f Fact) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := analyticsdb.New(tx)
	day, err := parseDay(f.Order.Day)
	if err != nil {
		return err
	}
	days, err := q.OrderFactDay(ctx, analyticsdb.OrderFactDayParams{StoreID: store, OrderID: f.OrderID})
	if err != nil {
		return fmt.Errorf("analytics store: fact day: %w", err)
	}
	o := f.Order
	var band *string
	if f.RiskBand != "" {
		band = &f.RiskBand
	}
	if err = q.UpsertOrderFact(ctx, analyticsdb.UpsertOrderFactParams{StoreID: store, OrderID: f.OrderID, Day: day, State: string(o.State),
		Governorate: o.Governorate, Campaign: o.Campaign, Currency: f.Currency, RevenueMinor: o.Revenue, ShippingMinor: o.Shipping,
		CodFeeMinor: o.CODFee, OnDeliveryMinor: o.OnDelivery, DepositPaidMinor: o.DepositPaid, RiskBand: band}); err != nil {
		return fmt.Errorf("analytics store: save fact: %w", err)
	}
	if err = q.DeleteOrderLines(ctx, analyticsdb.DeleteOrderLinesParams{StoreID: store, OrderID: f.OrderID}); err != nil {
		return fmt.Errorf("analytics store: delete lines: %w", err)
	}
	if len(o.Lines) > 0 {
		p := analyticsdb.InsertOrderLinesParams{StoreID: store, OrderID: f.OrderID, ProductIds: f.ProductIDs}
		for _, l := range o.Lines {
			cost := int64(-1)
			if l.Cost != nil {
				cost = *l.Cost
			}
			p.Titles = append(p.Titles, l.Title)
			p.Qtys = append(p.Qtys, int32(l.Qty)) //nolint:gosec // bounded by orders' MaxQty
			p.Revenues = append(p.Revenues, l.Revenue)
			p.Costs = append(p.Costs, cost)
		}
		if err = q.InsertOrderLines(ctx, p); err != nil {
			return fmt.Errorf("analytics store: insert lines: %w", err)
		}
	}
	return markDirty(ctx, q, store, append(days, day))
}

func markDirty(ctx context.Context, q *analyticsdb.Queries, store ulid.ID, days []time.Time) error {
	if err := q.MarkDirty(ctx, analyticsdb.MarkDirtyParams{StoreID: store, Days: days}); err != nil {
		return fmt.Errorf("analytics store: mark dirty: %w", err)
	}
	return nil
}

// MarkDirty marks days to compute again.
func MarkDirty(ctx context.Context, tx Tx, days ...time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	return markDirty(ctx, analyticsdb.New(tx), store, days)
}

// MarkAllDirty marks every day with orders or ad spend (the estimates
// changed).
func MarkAllDirty(ctx context.Context, tx Tx) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = analyticsdb.New(tx).MarkAllDirty(ctx, store); err != nil {
		return fmt.Errorf("analytics store: mark all dirty: %w", err)
	}
	return nil
}

// TakeDirty removes and returns up to n dirty days.
func TakeDirty(ctx context.Context, tx Tx, n int) ([]time.Time, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	days, err := analyticsdb.New(tx).TakeDirty(ctx, analyticsdb.TakeDirtyParams{StoreID: store, MaxDays: int32(n)}) //nolint:gosec // small
	if err != nil {
		return nil, fmt.Errorf("analytics store: take dirty: %w", err)
	}
	return days, nil
}

// Day reads one day's orders with their lines, and its ad spend by
// campaign: three queries.
func Day(ctx context.Context, tx Tx, day time.Time) ([]domain.Order, map[string]int64, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, nil, err
	}
	q := analyticsdb.New(tx)
	rows, err := q.DayOrders(ctx, analyticsdb.DayOrdersParams{StoreID: store, Day: day})
	if err != nil {
		return nil, nil, fmt.Errorf("analytics store: day orders: %w", err)
	}
	lines, err := q.DayLines(ctx, analyticsdb.DayLinesParams{StoreID: store, Day: day})
	if err != nil {
		return nil, nil, fmt.Errorf("analytics store: day lines: %w", err)
	}
	byOrder := map[ulid.ID][]domain.Line{}
	for _, l := range lines {
		byOrder[l.OrderID] = append(byOrder[l.OrderID], domain.Line{ProductID: l.ProductID.String(), Title: l.Title, Qty: int(l.Qty),
			Revenue: l.RevenueMinor, Cost: l.CostMinor})
	}
	out := make([]domain.Order, len(rows))
	for i, r := range rows {
		out[i] = domain.Order{ID: r.OrderID.String(), Day: day.Format(DayLayout), State: domain.State(r.State), Governorate: r.Governorate,
			Campaign: r.Campaign, Revenue: r.RevenueMinor, Shipping: r.ShippingMinor, CODFee: r.CodFeeMinor, OnDelivery: r.OnDeliveryMinor,
			DepositPaid: r.DepositPaidMinor, CarrierCharge: r.CarrierChargeMinor, Lines: byOrder[r.OrderID]}
	}
	spend, err := q.DayAdSpend(ctx, analyticsdb.DayAdSpendParams{StoreID: store, Day: day})
	if err != nil {
		return nil, nil, fmt.Errorf("analytics store: day ad spend: %w", err)
	}
	ads := make(map[string]int64, len(spend))
	for _, s := range spend {
		ads[s.Campaign] = s.AmountMinor
	}
	return out, ads, nil
}

// SaveDay replaces one day's figures with rows, in one statement after
// the delete.
func SaveDay(ctx context.Context, tx Tx, day time.Time, currency string, rows []domain.Row) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := analyticsdb.New(tx)
	if err = q.DeleteDay(ctx, analyticsdb.DeleteDayParams{StoreID: store, Day: day}); err != nil {
		return fmt.Errorf("analytics store: delete day: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	p := analyticsdb.InsertDayRowsParams{StoreID: store, Day: day, Currency: currency}
	for _, r := range rows {
		p.Dims = append(p.Dims, string(r.Dim))
		p.Keys = append(p.Keys, r.Key)
		p.Labels = append(p.Labels, r.Label)
		p.Orders = append(p.Orders, int32(r.Orders))                //nolint:gosec // a day's orders
		p.Delivered = append(p.Delivered, int32(r.Delivered))       //nolint:gosec // a day's orders
		p.Returned = append(p.Returned, int32(r.Returned))          //nolint:gosec // a day's orders
		p.Cancelled = append(p.Cancelled, int32(r.Cancelled))       //nolint:gosec // a day's orders
		p.Open = append(p.Open, int32(r.Open))                      //nolint:gosec // a day's orders
		p.CostMissing = append(p.CostMissing, int32(r.CostMissing)) //nolint:gosec // a day's orders
		p.Revenues = append(p.Revenues, r.Revenue)
		p.DeliveryPaid = append(p.DeliveryPaid, r.DeliveryPaid)
		p.ProductCosts = append(p.ProductCosts, r.ProductCost)
		p.ShippingCosts = append(p.ShippingCosts, r.ShippingCost)
		p.CodFees = append(p.CodFees, r.CODFees)
		p.ReturnCosts = append(p.ReturnCosts, r.ReturnCost)
		p.AdSpend = append(p.AdSpend, r.AdSpend)
	}
	if err = q.InsertDayRows(ctx, p); err != nil {
		return fmt.Errorf("analytics store: insert day: %w", err)
	}
	return nil
}

// Uncharged returns up to n settled-state orders since since whose
// carrier charge is not known, with their days, past the first skip.
func Uncharged(ctx context.Context, tx Tx, since time.Time, n, skip int) (map[ulid.ID]time.Time, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := analyticsdb.New(tx).Uncharged(ctx, analyticsdb.UnchargedParams{StoreID: store, Since: since, MaxOrders: int32(n), Skip: int32(skip)}) //nolint:gosec // small
	if err != nil {
		return nil, fmt.Errorf("analytics store: uncharged: %w", err)
	}
	out := make(map[ulid.ID]time.Time, len(rows))
	for _, r := range rows {
		out[r.OrderID] = r.Day
	}
	return out, nil
}

// SetCarrierCharges records carriers' charges, in minor units, by order.
func SetCarrierCharges(ctx context.Context, tx Tx, charges map[ulid.ID]int64) error {
	if len(charges) == 0 {
		return nil
	}
	store, err := current(ctx)
	if err != nil {
		return err
	}
	p := analyticsdb.SetCarrierChargesParams{StoreID: store}
	for id, c := range charges {
		p.OrderIds = append(p.OrderIds, id)
		p.Charges = append(p.Charges, c)
	}
	if err = analyticsdb.New(tx).SetCarrierCharges(ctx, p); err != nil {
		return fmt.Errorf("analytics store: set carrier charges: %w", err)
	}
	return nil
}

// Settings returns the store's estimates (zero value without a row).
func Settings(ctx context.Context, tx Tx) (domain.Settings, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Settings{}, err
	}
	rows, err := analyticsdb.New(tx).ProfitSettings(ctx, store)
	if err != nil {
		return domain.Settings{}, fmt.Errorf("analytics store: settings: %w", err)
	}
	if len(rows) == 0 {
		return domain.Settings{}, nil
	}
	r := rows[0]
	return domain.Settings{DeliveryCost: r.DeliveryCostMinor, ReturnCost: r.ReturnCostMinor, CODFeeBP: int64(r.CodFeeBp)}, nil
}

// SaveSettings writes the store's estimates.
func SaveSettings(ctx context.Context, tx Tx, s domain.Settings) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = analyticsdb.New(tx).SaveProfitSettings(ctx, analyticsdb.SaveProfitSettingsParams{StoreID: store,
		DeliveryCostMinor: s.DeliveryCost, ReturnCostMinor: s.ReturnCost, CodFeeBp: int32(s.CODFeeBP)}); err != nil { //nolint:gosec // validated ≤ 10000
		return fmt.Errorf("analytics store: save settings: %w", err)
	}
	return nil
}

// AdSpend is one entry of ad spend.
type AdSpend struct {
	ID        ulid.ID
	Day       time.Time
	Platform  string
	Campaign  string
	Currency  string
	Amount    int64
	Source    string
	Note      string
	CreatedAt time.Time
}

func toAdSpend(r analyticsdb.AdSpend) AdSpend {
	return AdSpend{ID: r.ID, Day: r.Day, Platform: r.Platform, Campaign: r.Campaign, Currency: r.Currency, Amount: r.AmountMinor,
		Source: r.Source, Note: r.Note, CreatedAt: r.CreatedAt}
}

// InsertAdSpend writes an entry and marks its day dirty.
func InsertAdSpend(ctx context.Context, tx Tx, a AdSpend) (AdSpend, error) {
	store, err := current(ctx)
	if err != nil {
		return AdSpend{}, err
	}
	q := analyticsdb.New(tx)
	r, err := q.InsertAdSpend(ctx, analyticsdb.InsertAdSpendParams{ID: a.ID, StoreID: store, Day: a.Day, Platform: a.Platform,
		Campaign: a.Campaign, Currency: a.Currency, AmountMinor: a.Amount, Source: a.Source, Note: a.Note})
	if err != nil {
		return AdSpend{}, fmt.Errorf("analytics store: insert ad spend: %w", err)
	}
	return toAdSpend(r), markDirty(ctx, q, store, []time.Time{a.Day})
}

// DeleteAdSpend removes an entry and marks its day dirty; ErrNotFound
// when the store has none with id.
func DeleteAdSpend(ctx context.Context, tx Tx, id ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := analyticsdb.New(tx)
	day, err := q.DeleteAdSpend(ctx, analyticsdb.DeleteAdSpendParams{StoreID: store, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("analytics store: delete ad spend: %w", err)
	}
	return markDirty(ctx, q, store, []time.Time{day})
}

// ReplaceImported removes what an ad platform's import said of [from,
// to), and marks those days dirty, before the new lines go in.
func ReplaceImported(ctx context.Context, tx Tx, platform string, from, to time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = analyticsdb.New(tx).DeleteImportedAdSpend(ctx, analyticsdb.DeleteImportedAdSpendParams{StoreID: store, Platform: platform,
		FromDay: from, ToDay: to}); err != nil {
		return fmt.Errorf("analytics store: replace imported ad spend: %w", err)
	}
	var days []time.Time
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}
	return markDirty(ctx, analyticsdb.New(tx), store, days)
}

// ListAdSpend returns the entries of [from, to), newest first, at most
// 500.
func ListAdSpend(ctx context.Context, tx Tx, from, to time.Time) ([]AdSpend, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := analyticsdb.New(tx).ListAdSpend(ctx, analyticsdb.ListAdSpendParams{StoreID: store, FromDay: from, ToDay: to})
	if err != nil {
		return nil, fmt.Errorf("analytics store: list ad spend: %w", err)
	}
	out := make([]AdSpend, len(rows))
	for i, r := range rows {
		out[i] = toAdSpend(r)
	}
	return out, nil
}

// AdSpendTotals counts and sums every entry of [from, to).
func AdSpendTotals(ctx context.Context, tx Tx, from, to time.Time) (count int, amountMinor int64, err error) {
	store, err := current(ctx)
	if err != nil {
		return 0, 0, err
	}
	r, err := analyticsdb.New(tx).AdSpendTotals(ctx, analyticsdb.AdSpendTotalsParams{StoreID: store, FromDay: from, ToDay: to})
	if err != nil {
		return 0, 0, fmt.Errorf("analytics store: ad spend totals: %w", err)
	}
	return int(r.Entries), r.AmountMinor, nil
}

// Totals sums the store's figures over [from, to) (read pool).
func Totals(ctx context.Context, tx Tx, from, to time.Time) (domain.Figures, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Figures{}, err
	}
	r, err := analyticsdb.New(tx).ProfitTotals(ctx, analyticsdb.ProfitTotalsParams{StoreID: store, FromDay: from, ToDay: to})
	if err != nil {
		return domain.Figures{}, fmt.Errorf("analytics store: totals: %w", err)
	}
	return domain.Figures{Orders: int(r.Orders), Delivered: int(r.Delivered), Returned: int(r.Returned), Cancelled: int(r.Cancelled),
		Open: int(r.Open), CostMissing: int(r.CostMissing), Revenue: r.RevenueMinor, DeliveryPaid: r.DeliveryPaidMinor,
		ProductCost: r.ProductCostMinor, ShippingCost: r.ShippingCostMinor, CODFees: r.CodFeesMinor, ReturnCost: r.ReturnCostMinor,
		AdSpend: r.AdSpendMinor}, nil
}

// DayFigures is one day's totals, for the chart.
type DayFigures struct {
	Day time.Time
	domain.Figures
}

// Days returns the store's totals day by day over [from, to) (read pool).
func Days(ctx context.Context, tx Tx, from, to time.Time) ([]DayFigures, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := analyticsdb.New(tx).ProfitDays(ctx, analyticsdb.ProfitDaysParams{StoreID: store, FromDay: from, ToDay: to})
	if err != nil {
		return nil, fmt.Errorf("analytics store: days: %w", err)
	}
	out := make([]DayFigures, len(rows))
	for i, r := range rows {
		out[i] = DayFigures{Day: r.Day, Figures: domain.Figures{Orders: int(r.Orders), Revenue: r.RevenueMinor, DeliveryPaid: r.DeliveryPaidMinor,
			ProductCost: r.ProductCostMinor, ShippingCost: r.ShippingCostMinor, CODFees: r.CodFeesMinor, ReturnCost: r.ReturnCostMinor,
			AdSpend: r.AdSpendMinor}}
	}
	return out, nil
}

// Breakdown returns one dimension's keys over [from, to), most
// profitable first, at most 200 (read pool).
func Breakdown(ctx context.Context, tx Tx, dim domain.Dimension, from, to time.Time) ([]domain.Row, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := analyticsdb.New(tx).ProfitBreakdown(ctx, analyticsdb.ProfitBreakdownParams{StoreID: store, Dim: string(dim), FromDay: from, ToDay: to})
	if err != nil {
		return nil, fmt.Errorf("analytics store: breakdown: %w", err)
	}
	out := make([]domain.Row, len(rows))
	for i, r := range rows {
		out[i] = domain.Row{Dim: dim, Key: r.Key, Label: r.Label, Figures: domain.Figures{Orders: int(r.Orders), Delivered: int(r.Delivered),
			Returned: int(r.Returned), Cancelled: int(r.Cancelled), Open: int(r.Open), CostMissing: int(r.CostMissing), Revenue: r.RevenueMinor,
			DeliveryPaid: r.DeliveryPaidMinor, ProductCost: r.ProductCostMinor, ShippingCost: r.ShippingCostMinor, CODFees: r.CodFeesMinor,
			ReturnCost: r.ReturnCostMinor, AdSpend: r.AdSpendMinor}}
	}
	return out, nil
}

// DirtyStores lists the stores with days to compute (no tenant).
func DirtyStores(ctx context.Context, tx Tx) ([]ulid.ID, error) {
	ids, err := analyticsdb.New(tx).DirtyStores(ctx)
	if err != nil {
		return nil, fmt.Errorf("analytics store: dirty stores: %w", err)
	}
	return ids, nil
}

// SweepStores lists the stores with recent orders whose carrier charge
// is not known (no tenant).
func SweepStores(ctx context.Context, tx Tx) ([]ulid.ID, error) {
	ids, err := analyticsdb.New(tx).SweepStores(ctx)
	if err != nil {
		return nil, fmt.Errorf("analytics store: sweep stores: %w", err)
	}
	return ids, nil
}

// QueueBackfill marks the store's past as queued; false when it was.
func QueueBackfill(ctx context.Context, tx Tx) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	if _, err = analyticsdb.New(tx).QueueBackfill(ctx, store); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("analytics store: queue backfill: %w", err)
	}
	return true, nil
}
