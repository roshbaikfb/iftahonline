// Package store adapts the sqlc queries in ordersdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time. Lookups of one order
// pass its partition key (domain.CreatedAtOf).
package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/store/ordersdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = ordersdb.DBTX

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("orders store: %w", err)
	}
	return id.ID, nil
}

// Insert writes a new order with its lines.
func Insert(ctx context.Context, tx Tx, o domain.Order, linesHash [32]byte) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	c := o.Contact
	q := ordersdb.New(tx)
	if err = q.InsertOrder(ctx, ordersdb.InsertOrderParams{
		ID: o.ID, StoreID: store, CreatedAt: o.CreatedAt, Source: string(o.Source), CustomerID: o.CustomerID,
		Name: c.Name, Phone: c.Phone.Unmasked(), Governorate: c.Governorate, City: c.City, Address: c.Address, Notes: c.Notes,
		Currency: string(o.Total.Currency()), SubtotalMinor: o.Subtotal.Amount(), ShippingMinor: o.Shipping.Amount(),
		CodFeeMinor: o.CODFee.Amount(), TotalMinor: o.Total.Amount(), ItemCount: int32(o.ItemCount), LinesHash: linesHash[:], //nolint:gosec // bounded by MaxLines*MaxQty
	}); err != nil {
		return fmt.Errorf("orders store: insert order: %w", err)
	}
	p := ordersdb.InsertOrderItemsParams{StoreID: store, OrderCreatedAt: o.CreatedAt, OrderID: o.ID}
	for _, l := range o.Lines {
		p.VariantIds = append(p.VariantIds, l.VariantID)
		p.ProductIds = append(p.ProductIds, l.ProductID)
		p.Titles = append(p.Titles, l.Title)
		p.VariantTitles = append(p.VariantTitles, l.VariantTitle)
		p.Skus = append(p.Skus, l.SKU)
		p.Qtys = append(p.Qtys, int32(l.Qty)) //nolint:gosec // bounded by domain.MaxQty
		p.UnitPrices = append(p.UnitPrices, l.UnitPrice.Amount())
		p.Totals = append(p.Totals, l.Total.Amount())
	}
	if err = q.InsertOrderItems(ctx, p); err != nil {
		return fmt.Errorf("orders store: insert items: %w", err)
	}
	return nil
}

// InsertEvent appends to an order's timeline.
func InsertEvent(ctx context.Context, tx Tx, orderID ulid.ID, e domain.Event) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	var from *string
	if e.From != "" {
		s := string(e.From)
		from = &s
	}
	if err = ordersdb.New(tx).InsertOrderEvent(ctx, ordersdb.InsertOrderEventParams{
		ID: e.ID, StoreID: store, OrderCreatedAt: domain.CreatedAtOf(orderID), OrderID: orderID, OccurredAt: e.At,
		FromStatus: from, ToStatus: string(e.To), ActorUserID: e.ActorUserID, Actor: e.Actor, Note: e.Note,
	}); err != nil {
		return fmt.Errorf("orders store: insert event: %w", err)
	}
	return nil
}

// Get returns an order without its lines, or domain.ErrNotFound. With
// forUpdate the row stays locked until the transaction ends.
func Get(ctx context.Context, tx Tx, id ulid.ID, forUpdate bool) (domain.Order, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Order{}, err
	}
	var row ordersdb.Order
	if forUpdate {
		row, err = ordersdb.New(tx).GetOrderForUpdate(ctx, ordersdb.GetOrderForUpdateParams{StoreID: store, CreatedAt: domain.CreatedAtOf(id), ID: id})
	} else {
		row, err = ordersdb.New(tx).GetOrder(ctx, ordersdb.GetOrderParams{StoreID: store, CreatedAt: domain.CreatedAtOf(id), ID: id})
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Order{}, domain.ErrNotFound
	case err != nil:
		return domain.Order{}, fmt.Errorf("orders store: get order: %w", err)
	}
	return toOrder(row)
}

// SetStatus moves an order (locked by Get) to status to.
func SetStatus(ctx context.Context, tx Tx, id ulid.ID, to domain.Status, at time.Time) (domain.Order, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Order{}, err
	}
	row, err := ordersdb.New(tx).SetOrderStatus(ctx, ordersdb.SetOrderStatusParams{
		ToStatus: string(to), At: at, StoreID: store, CreatedAt: domain.CreatedAtOf(id), ID: id,
	})
	if err != nil {
		return domain.Order{}, fmt.Errorf("orders store: set status: %w", err)
	}
	return toOrder(row)
}

// Lines returns an order's lines, priced in cur (the order's currency).
func Lines(ctx context.Context, tx Tx, id ulid.ID, cur money.Currency) ([]domain.Line, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := ordersdb.New(tx).ListOrderItems(ctx, ordersdb.ListOrderItemsParams{StoreID: store, OrderCreatedAt: domain.CreatedAtOf(id), OrderID: id})
	if err != nil {
		return nil, fmt.Errorf("orders store: lines: %w", err)
	}
	return toLines(rows, cur)
}

// LinesOf fills the lines of a page of orders in one query.
func LinesOf(ctx context.Context, tx Tx, orders []domain.Order) error {
	if len(orders) == 0 {
		return nil
	}
	store, err := current(ctx)
	if err != nil {
		return err
	}
	p := ordersdb.ListItemsOfOrdersParams{StoreID: store}
	at := make(map[ulid.ID]int, len(orders))
	for i, o := range orders {
		p.OrderIds = append(p.OrderIds, o.ID)
		p.CreatedAts = append(p.CreatedAts, o.CreatedAt)
		at[o.ID] = i
	}
	rows, err := ordersdb.New(tx).ListItemsOfOrders(ctx, p)
	if err != nil {
		return fmt.Errorf("orders store: lines of orders: %w", err)
	}
	for _, r := range rows {
		o := &orders[at[r.OrderID]]
		lines, err := toLines([]ordersdb.OrderItem{r}, o.Total.Currency())
		if err != nil {
			return err
		}
		o.Lines = append(o.Lines, lines[0])
	}
	return nil
}

// Events returns an order's timeline, oldest first.
func Events(ctx context.Context, tx Tx, id ulid.ID) ([]domain.Event, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := ordersdb.New(tx).ListOrderEvents(ctx, ordersdb.ListOrderEventsParams{StoreID: store, OrderCreatedAt: domain.CreatedAtOf(id), OrderID: id})
	if err != nil {
		return nil, fmt.Errorf("orders store: events: %w", err)
	}
	out := make([]domain.Event, len(rows))
	for i, r := range rows {
		out[i] = domain.Event{ID: r.ID, To: domain.Status(r.ToStatus), ActorUserID: r.ActorUserID, Actor: r.Actor, Note: r.Note, At: r.OccurredAt}
		if r.FromStatus != nil {
			out[i].From = domain.Status(*r.FromStatus)
		}
	}
	return out, nil
}

// List returns one page of orders (without lines), driven by the index of
// the most selective filter.
func List(ctx context.Context, tx Tx, f domain.Filter) ([]domain.Order, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	var status, gov *string
	if f.Status != nil {
		s := string(*f.Status)
		status = &s
	}
	if f.Governorate != "" {
		gov = &f.Governorate
	}
	lim := int32(f.Limit) //nolint:gosec // bounded by domain.MaxListLimit
	q := ordersdb.New(tx)
	var rows []ordersdb.Order
	switch {
	case f.CustomerID != nil:
		rows, err = q.ListOrdersByCustomer(ctx, ordersdb.ListOrdersByCustomerParams{StoreID: store, CustomerID: *f.CustomerID,
			FromAt: f.From, ToAt: f.To, BeforeID: f.Before, Status: status, Governorate: gov, Lim: lim})
	case f.Phone != nil:
		rows, err = q.ListOrdersByPhone(ctx, ordersdb.ListOrdersByPhoneParams{StoreID: store, Phone: f.Phone.Unmasked(),
			FromAt: f.From, ToAt: f.To, BeforeID: f.Before, Status: status, Governorate: gov, Lim: lim})
	case status != nil:
		rows, err = q.ListOrdersByStatus(ctx, ordersdb.ListOrdersByStatusParams{StoreID: store, Status: *status,
			FromAt: f.From, ToAt: f.To, BeforeID: f.Before, Governorate: gov, Lim: lim})
	default:
		rows, err = q.ListOrders(ctx, ordersdb.ListOrdersParams{StoreID: store,
			FromAt: f.From, ToAt: f.To, BeforeID: f.Before, Governorate: gov, Lim: lim})
	}
	if err != nil {
		return nil, fmt.Errorf("orders store: list: %w", err)
	}
	out := make([]domain.Order, len(rows))
	for i, r := range rows {
		if out[i], err = toOrder(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// LockPhone serializes orders by one phone in the store until the
// transaction ends.
func LockPhone(ctx context.Context, tx Tx, p phone.E164) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	h := fnv.New64a()
	h.Write([]byte("orders.phone:"))
	h.Write(store[:])
	h.Write([]byte(p.Unmasked()))
	if err = ordersdb.New(tx).LockOrderPhone(ctx, int64(h.Sum64())); err != nil { //nolint:gosec // a lock key: any 64 bits do
		return fmt.Errorf("orders store: lock phone: %w", err)
	}
	return nil
}

// FindDuplicate returns a recent order by p with the same lines, if any.
func FindDuplicate(ctx context.Context, tx Tx, p phone.E164, since time.Time, linesHash [32]byte) (ulid.ID, bool, error) {
	store, err := current(ctx)
	if err != nil {
		return ulid.ID{}, false, err
	}
	id, err := ordersdb.New(tx).FindDuplicateOrder(ctx, ordersdb.FindDuplicateOrderParams{
		StoreID: store, Phone: p.Unmasked(), Since: since, LinesHash: linesHash[:]})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ulid.ID{}, false, nil
	case err != nil:
		return ulid.ID{}, false, fmt.Errorf("orders store: find duplicate: %w", err)
	}
	return id, true, nil
}

// Key is a stored idempotency key.
type Key struct {
	Fingerprint [32]byte
	OrderID     ulid.ID
}

// InsertKey records key for orderID. false means the key already exists
// (the caller reads it with GetKey).
func InsertKey(ctx context.Context, tx Tx, key string, fp [32]byte, orderID ulid.ID) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	_, err = ordersdb.New(tx).InsertIdempotencyKey(ctx, ordersdb.InsertIdempotencyKeyParams{StoreID: store, Key: key, Fingerprint: fp[:], OrderID: orderID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("orders store: insert key: %w", err)
	}
	return true, nil
}

// GetKey returns a stored key, or domain.ErrNotFound.
func GetKey(ctx context.Context, tx Tx, key string) (Key, error) {
	store, err := current(ctx)
	if err != nil {
		return Key{}, err
	}
	row, err := ordersdb.New(tx).GetIdempotencyKey(ctx, ordersdb.GetIdempotencyKeyParams{StoreID: store, Key: key})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Key{}, domain.ErrNotFound
	case err != nil:
		return Key{}, fmt.Errorf("orders store: get key: %w", err)
	}
	k := Key{OrderID: row.OrderID}
	copy(k.Fingerprint[:], row.Fingerprint)
	return k, nil
}

// PurgeKeys deletes a few of the store's keys created before before.
func PurgeKeys(ctx context.Context, tx Tx, before time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = ordersdb.New(tx).PurgeIdempotencyKeys(ctx, ordersdb.PurgeIdempotencyKeysParams{StoreID: store, Before: before}); err != nil {
		return fmt.Errorf("orders store: purge keys: %w", err)
	}
	return nil
}

func toOrder(r ordersdb.Order) (domain.Order, error) {
	cur := money.Currency(r.Currency)
	sub, err := money.New(r.SubtotalMinor, cur)
	if err != nil {
		return domain.Order{}, fmt.Errorf("orders store: %w", err)
	}
	ship, err := money.New(r.ShippingMinor, cur)
	if err != nil {
		return domain.Order{}, fmt.Errorf("orders store: %w", err)
	}
	cod, err := money.New(r.CodFeeMinor, cur)
	if err != nil {
		return domain.Order{}, fmt.Errorf("orders store: %w", err)
	}
	total, err := money.New(r.TotalMinor, cur)
	if err != nil {
		return domain.Order{}, fmt.Errorf("orders store: %w", err)
	}
	p, err := phone.ParseE164(r.Phone)
	if err != nil {
		return domain.Order{}, fmt.Errorf("orders store: stored phone: %w", err)
	}
	return domain.Order{
		ID: r.ID, Status: domain.Status(r.Status), Version: int(r.Version), Source: domain.Source(r.Source), CustomerID: r.CustomerID,
		Contact:  domain.Contact{Name: r.Name, Phone: p, Governorate: r.Governorate, City: r.City, Address: r.Address, Notes: r.Notes},
		Subtotal: sub, Shipping: ship, CODFee: cod, Total: total, ItemCount: int(r.ItemCount),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, StatusChangedAt: r.StatusChangedAt,
	}, nil
}

func toLines(rows []ordersdb.OrderItem, cur money.Currency) ([]domain.Line, error) {
	out := make([]domain.Line, len(rows))
	for i, r := range rows {
		unit, err := money.New(r.UnitPriceMinor, cur)
		if err != nil {
			return nil, fmt.Errorf("orders store: %w", err)
		}
		total, err := money.New(r.TotalMinor, cur)
		if err != nil {
			return nil, fmt.Errorf("orders store: %w", err)
		}
		out[i] = domain.Line{VariantID: r.VariantID, ProductID: r.ProductID, Title: r.Title, VariantTitle: r.VariantTitle,
			SKU: r.Sku, Qty: int(r.Qty), UnitPrice: unit, Total: total}
	}
	return out, nil
}
