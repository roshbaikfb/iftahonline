// Package app holds the coupon use cases (ADR-0042): the merchant's
// coupons, and an order's: previewing a code at checkout, taking its use
// in the order's transaction, and giving it back when the order is
// cancelled before it ships.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Errors the HTTP layer renders.
var (
	ErrCouponNotFound = errs.NotFound("coupon_not_found", "coupon not found")
	errCodeTaken      = errs.Conflict("coupon_code_taken", "another coupon has this code")
	errCodeInUse      = errs.Conflict("coupon_code_in_use", "orders hold this coupon: its code cannot change; make a new coupon")
	errUsed           = errs.Conflict("coupon_used", "orders hold this coupon: turn it off instead of deleting it")
)

// Service implements the use cases.
type Service struct {
	db    postgres.Beginner
	ids   *ulid.Generator
	audit audit.Recorder
	now   func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB  postgres.Beginner
	IDs *ulid.Generator
	// Audit records merchant actions; nil discards.
	Audit audit.Recorder
	// Now is the clock (tests); nil is time.Now.
	Now func() time.Time
}

// NewService builds the service.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{db: d.DB, ids: d.IDs, audit: d.Audit, now: d.Now}
}

// Page sizes of the coupons' list.
const (
	DefaultPage = 50
	MaxPage     = 100
)

// CouponPage is a page of coupons; Next is the cursor of the next one,
// empty on the last.
type CouponPage struct {
	Items []domain.Coupon
	Next  string
}

// lastID is past every ULID: a first page's bound.
var lastID = ulid.ID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff} //nolint:gochecknoglobals // constant

// Coupons lists a page of the store's coupons, newest first, after
// cursor (a page's Next; "" is the first).
func (s *Service) Coupons(ctx context.Context, cursor string, limit int) (CouponPage, error) {
	if limit == 0 {
		limit = DefaultPage
	}
	if limit < 1 || limit > MaxPage {
		return CouponPage{}, errs.Validation(errs.FieldError{Field: "limit", Code: "range", Message: "1 to 100"})
	}
	before := lastID
	if cursor != "" {
		id, err := ulid.Parse(cursor)
		if err != nil {
			return CouponPage{}, errs.Validation(errs.FieldError{Field: "cursor", Code: "invalid", Message: "a page's next_cursor"})
		}
		before = id
	}
	var items []domain.Coupon
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		items, err = store.List(ctx, tx, before, limit+1) // one more says whether a page follows
		return err
	})
	if err != nil {
		return CouponPage{}, err
	}
	page := CouponPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.Next = page.Items[limit-1].ID.String()
	}
	return page, nil
}

// Coupon returns one coupon.
func (s *Service) Coupon(ctx context.Context, id ulid.ID) (domain.Coupon, error) {
	var c domain.Coupon
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		c, err = store.Get(ctx, tx, id)
		return err
	})
	return c, mapErr(err)
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return ErrCouponNotFound
	case errors.Is(err, store.ErrCodeTaken):
		return errCodeTaken
	}
	return err
}

// clean normalizes a coupon the merchant wrote and checks it.
func clean(c domain.Coupon) (domain.Coupon, error) {
	if code, ok := domain.NormalizeCode(c.Code); ok {
		c.Code = code
	}
	if c.Kind != domain.Percent {
		c.Percent = 0
	}
	if c.Kind != domain.Fixed {
		c.AmountMinor = 0
	}
	return c, c.Validate()
}

// Create adds a coupon.
func (s *Service) Create(ctx context.Context, in domain.Coupon) (domain.Coupon, error) {
	c, err := clean(in)
	if err != nil {
		return domain.Coupon{}, err
	}
	c.ID = s.ids.New()
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if c, txErr = store.Insert(ctx, tx, c); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "coupon.created", TargetType: "coupon", TargetID: c.ID.String(),
			Metadata: map[string]any{"code": c.Code, "kind": string(c.Kind)}})
	})
	return c, mapErr(err)
}

// Update changes a coupon's terms. Its code may change only while no
// order holds it (the orders show the code they took).
func (s *Service) Update(ctx context.Context, id ulid.ID, in domain.Coupon) (domain.Coupon, error) {
	c, err := clean(in)
	if err != nil {
		return domain.Coupon{}, err
	}
	c.ID = id
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, txErr := store.Get(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		if cur.Code != c.Code && cur.Uses > 0 {
			return errCodeInUse
		}
		if c, txErr = store.Update(ctx, tx, c); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "coupon.updated", TargetType: "coupon", TargetID: id.String(),
			Metadata: map[string]any{"code": c.Code, "active": c.Active}})
	})
	return c, mapErr(err)
}

// Delete deletes a coupon no order ever held; one that was used can
// only be turned off.
func (s *Service) Delete(ctx context.Context, id ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, txErr := store.Get(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		deleted, txErr := store.DeleteUnused(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		if !deleted {
			return errUsed
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "coupon.deleted", TargetType: "coupon", TargetID: id.String(),
			Metadata: map[string]any{"code": c.Code}})
	})
	return mapErr(err)
}

// Preview is what a code would take off an order whose items cost
// subtotal, for the store in ctx: a quote before the order (the
// per-customer limit is checked when it is placed). A code that does
// not apply is a *domain.Rejected.
func (s *Service) Preview(ctx context.Context, code string, subtotal money.Money) (domain.Discount, error) {
	norm, ok := domain.NormalizeCode(code)
	if !ok {
		return domain.Discount{}, &domain.Rejected{Reason: domain.NotFound}
	}
	var d domain.Discount
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, err := store.ByCode(ctx, tx, norm)
		if errors.Is(err, domain.ErrNotFound) {
			return &domain.Rejected{Reason: domain.NotFound}
		}
		if err != nil {
			return err
		}
		d, err = c.Apply(subtotal, s.now(), -1)
		return err
	})
	return d, err
}

// RedeemTx takes a code's use for an order, in the order's transaction,
// as its last step: the coupon's row is locked only from the use taken
// to the commit, so concurrent orders take the last use once without
// waiting on each other's whole order. A customer's own concurrent
// orders take the code one at a time (the per-customer limit); other
// customers wait for nothing. A code that does not apply is a
// *domain.Rejected.
func (s *Service) RedeemTx(ctx context.Context, tx pgx.Tx, code string, subtotal money.Money, customer, order ulid.ID) (domain.Discount, error) {
	norm, ok := domain.NormalizeCode(code)
	if !ok {
		return domain.Discount{}, &domain.Rejected{Reason: domain.NotFound}
	}
	c, err := store.ByCode(ctx, tx, norm)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Discount{}, &domain.Rejected{Reason: domain.NotFound}
	}
	if err != nil {
		return domain.Discount{}, err
	}
	if c.MaxPerCustomer != nil {
		if err = store.LockCustomer(ctx, tx, c.ID, customer); err != nil {
			return domain.Discount{}, err
		}
	}
	uses, err := store.CustomerUses(ctx, tx, c.ID, customer)
	if err != nil {
		return domain.Discount{}, err
	}
	d, err := c.Apply(subtotal, s.now(), uses)
	if err != nil {
		return domain.Discount{}, err
	}
	switch _, err = store.Redeem(ctx, tx, order, customer, d); {
	case errors.Is(err, store.ErrNoUseLeft):
		// Taken by another order meanwhile, or turned off: say which.
		if now, getErr := store.ByCode(ctx, tx, norm); getErr == nil && !now.Active {
			return domain.Discount{}, &domain.Rejected{Reason: domain.Inactive}
		}
		return domain.Discount{}, &domain.Rejected{Reason: domain.UsedUp}
	case err != nil:
		return domain.Discount{}, err
	}
	return d, nil
}

// ReleaseOrder gives back the use an order held, once: the order was
// cancelled before it shipped.
func (s *Service) ReleaseOrder(ctx context.Context, order ulid.ID) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		_, err := store.Release(ctx, tx, order)
		return err
	})
}
