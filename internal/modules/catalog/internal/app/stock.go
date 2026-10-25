package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Stock errors. Match with errors.Is.
var (
	ErrVariantNotFound     = errs.NotFound("variant_not_found", "variant not found")
	ErrVariantNotSellable  = errs.Conflict("variant_not_sellable", "the variant is not for sale")
	ErrInsufficientStock   = errs.Conflict("insufficient_stock", "not enough stock")
	ErrReservationRefUsed  = errs.Conflict("reservation_ref_used", "this ref already holds a different reservation")
	ErrReservationNotFound = errs.NotFound("reservation_not_found", "reservation not found")
	ErrReservationState    = errs.Conflict("reservation_state", "the reservation's state does not allow this")
)

// StockError names the variant a reservation failed on. It matches
// ErrInsufficientStock or ErrVariantNotSellable with errors.Is.
type StockError struct {
	VariantID ulid.ID
	Err       error
}

func (e *StockError) Error() string { return fmt.Sprintf("%v (variant %s)", e.Err, e.VariantID) }

// Unwrap returns the sentinel.
func (e *StockError) Unwrap() error { return e.Err }

// GetSellableVariant returns a variant of an active product, with its
// price and stock.
func (s *Service) GetSellableVariant(ctx context.Context, id ulid.ID) (domain.SellableVariant, error) {
	var v domain.SellableVariant
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		v, err = store.GetSellableVariant(ctx, tx, id)
		return err
	})
	return v, notFound(err, ErrVariantNotFound)
}

// MaxSellableIDs bounds one SellableVariants call (a cart's lines).
const MaxSellableIDs = 100

// SellableVariants returns the sellable variants among ids in the order
// given, skipping the others, in a constant number of queries.
func (s *Service) SellableVariants(ctx context.Context, ids []ulid.ID) ([]domain.SellableVariant, error) {
	return sellableVariants(ctx, s.db, ids)
}

func sellableVariants(ctx context.Context, db postgres.Beginner, ids []ulid.ID) ([]domain.SellableVariant, error) {
	if len(ids) > MaxSellableIDs {
		return nil, errs.Validation(errs.FieldError{Field: "ids", Code: "too_many", Message: fmt.Sprintf("at most %d variants", MaxSellableIDs)})
	}
	if len(ids) == 0 {
		return []domain.SellableVariant{}, nil
	}
	var out []domain.SellableVariant
	err := postgres.InTenantTx(ctx, db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.ListSellableVariants(ctx, tx, ids)
		return txErr
	})
	return out, err
}

// ReserveStock holds stock for every line of req, all or nothing, until
// ReleaseStock or the TTL. Each line is one conditional UPDATE on the
// variant's inventory row (ADR-0011), so concurrent reservations can
// never hold more than on_hand. Lines are reserved in variant ID order,
// so two multi-line reservations cannot deadlock.
//
// The ref makes it idempotent: the same ref and lines return the
// existing reservation; the same ref with other lines is
// ErrReservationRefUsed.
func (s *Service) ReserveStock(ctx context.Context, req domain.ReserveRequest) (domain.Reservation, error) {
	var res domain.Reservation
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		res, txErr = s.ReserveStockTx(ctx, tx, req)
		return txErr
	})
	return res, err
}

// ReserveStockTx is ReserveStock in the caller's tenant transaction
// (ADR-0007), so an order and its stock commit or roll back together.
func (s *Service) ReserveStockTx(ctx context.Context, tx pgx.Tx, req domain.ReserveRequest) (domain.Reservation, error) {
	req, err := domain.ValidateReserve(req)
	if err != nil {
		return domain.Reservation{}, err
	}
	return s.reserveTx(ctx, tx, req)
}

func (s *Service) reserveTx(ctx context.Context, tx pgx.Tx, req domain.ReserveRequest) (domain.Reservation, error) {
	existing, err := store.GetReservation(ctx, tx, req.Ref)
	switch {
	case err == nil && existing.SameLines(req):
		return existing, nil
	case err == nil:
		return domain.Reservation{}, ErrReservationRefUsed
	case !errors.Is(err, domain.ErrNotFound):
		return domain.Reservation{}, err
	}
	lines := make([]store.ReservedLine, len(req.Lines))
	var shown []ulid.ID // products whose sold-out state or "only N left" changed
	for i, l := range req.Lines {
		var r store.Reserved
		if r, err = store.Reserve(ctx, tx, l.VariantID, l.Qty); err != nil {
			return domain.Reservation{}, err
		}
		if !r.OK {
			return domain.Reservation{}, s.whyNotReserved(ctx, tx, l)
		}
		if r.Counted && domain.ShownStockChanged(r.Available+l.Qty, r.Available) {
			shown = append(shown, r.ProductID)
		}
		lines[i] = store.ReservedLine{ID: s.ids.New(), Line: l, Counted: r.Counted}
	}
	var expires time.Time
	var expiresAt *time.Time
	if !req.Hold {
		expires = s.now().Add(req.TTL).UTC().Truncate(time.Microsecond)
		expiresAt = &expires
	}
	if err = store.InsertReservation(ctx, tx, req.Ref, expiresAt, lines); err != nil {
		if postgres.IsUniqueViolation(err, "stock_reservations_ref_variant_key") {
			return domain.Reservation{}, ErrReservationRefUsed // a concurrent call with the same ref won
		}
		return domain.Reservation{}, err
	}
	if expiresAt != nil {
		if err = s.scheduleExpiry(ctx, tx, req.Ref, expires); err != nil {
			return domain.Reservation{}, err
		}
	}
	if err = s.events.stockChanged(ctx, tx, shown); err != nil {
		return domain.Reservation{}, err
	}
	return domain.Reservation{Ref: req.Ref, Lines: req.Lines, Status: domain.ReservationActive, Held: req.Hold, ExpiresAt: expires}, nil
}

// whyNotReserved tells a short line from one that is not for sale. It
// runs only on the failure path.
func (s *Service) whyNotReserved(ctx context.Context, tx pgx.Tx, l domain.ReserveLine) error {
	if _, err := store.GetSellableVariant(ctx, tx, l.VariantID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &StockError{VariantID: l.VariantID, Err: ErrVariantNotSellable}
		}
		return err
	}
	return &StockError{VariantID: l.VariantID, Err: ErrInsufficientStock}
}

// ReleaseStock gives back the stock held for ref. Releasing a ref that
// was already released or has expired does nothing; a committed or
// returned ref is ErrReservationState.
func (s *Service) ReleaseStock(ctx context.Context, ref string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return s.ReleaseStockTx(ctx, tx, ref) })
}

// ReleaseStockTx is ReleaseStock in the caller's tenant transaction.
func (s *Service) ReleaseStockTx(ctx context.Context, tx pgx.Tx, ref string) error {
	return s.step(ctx, tx, ref, release(), domain.ReservationReleased, domain.ReservationExpired)
}

// CommitStockTx ships the units held for ref: on_hand and reserved both
// drop, so availability does not change. Committing again does nothing;
// a released, expired or returned ref is ErrReservationState.
func (s *Service) CommitStockTx(ctx context.Context, tx pgx.Tx, ref string) error {
	return s.step(ctx, tx, ref, commit(), domain.ReservationCommitted)
}

// RestockTx puts the units committed for ref back on hand (a returned
// order). Restocking again does nothing; a ref that was never committed
// is ErrReservationState.
func (s *Service) RestockTx(ctx context.Context, tx pgx.Tx, ref string) error {
	return s.step(ctx, tx, ref, restock(), domain.ReservationReturned)
}

// step applies mv to ref. When no line moved, a ref already in one of
// done is a repeat (no-op); any other existing ref is in the wrong state.
func (s *Service) step(ctx context.Context, tx pgx.Tx, ref string, mv move, done ...domain.ReservationStatus) error {
	if err := domain.ValidateRef(ref); err != nil {
		return err
	}
	n, err := moveReservation(ctx, tx, s.events, ref, mv, false)
	if err != nil || n > 0 {
		return err
	}
	r, err := store.GetReservation(ctx, tx, ref)
	if err != nil {
		return notFound(err, ErrReservationNotFound)
	}
	if slices.Contains(done, r.Status) {
		return nil
	}
	return ErrReservationState
}

// move is one step of a reservation's life, with what it does to the
// inventory row of each counted line.
type move struct {
	from, to             domain.ReservationStatus
	onHandSign, holdSign int // multiplied by qty
}

// The steps. Functions, not package variables: no global state.
func release() move {
	return move{from: domain.ReservationActive, to: domain.ReservationReleased, holdSign: -1}
}
func expire() move {
	return move{from: domain.ReservationActive, to: domain.ReservationExpired, holdSign: -1}
}
func commit() move {
	return move{from: domain.ReservationActive, to: domain.ReservationCommitted, onHandSign: -1, holdSign: -1}
}
func restock() move {
	return move{from: domain.ReservationCommitted, to: domain.ReservationReturned, onHandSign: 1}
}

// moveReservation applies mv to ref's lines and their counted units, in
// variant ID order (no deadlocks), publishing a stock change for products
// whose shown stock changed (back in stock, or the "only N left" count).
// It returns the number of lines moved.
func moveReservation(ctx context.Context, tx pgx.Tx, ev Events, ref string, mv move, onlyExpired bool) (int, error) {
	lines, err := store.MoveReservation(ctx, tx, ref, mv.from, mv.to, onlyExpired)
	if err != nil {
		return 0, err
	}
	slices.SortFunc(lines, func(a, b store.ReservedLine) int {
		return bytes.Compare(a.Line.VariantID[:], b.Line.VariantID[:])
	})
	var shown []ulid.ID
	for _, l := range lines {
		if !l.Counted {
			continue
		}
		productID, available, err := store.Adjust(ctx, tx, l.Line.VariantID, mv.onHandSign*l.Line.Qty, mv.holdSign*l.Line.Qty)
		if err != nil {
			return 0, err
		}
		// Availability moves by (onHand - hold) × qty: a commit moves both
		// and changes nothing a shopper sees.
		before := available - (mv.onHandSign-mv.holdSign)*l.Line.Qty
		if domain.ShownStockChanged(before, available) {
			shown = append(shown, productID)
		}
	}
	return len(lines), ev.stockChanged(ctx, tx, shown)
}

// ExpireReservationArgs releases a reservation once its TTL has passed.
type ExpireReservationArgs struct {
	jobs.TenantArgs
	Ref string `json:"ref"`
}

// Kind implements river.JobArgs.
func (ExpireReservationArgs) Kind() string { return "catalog.expire_reservation" }

func (s *Service) scheduleExpiry(ctx context.Context, tx pgx.Tx, ref string, at time.Time) error {
	return s.enqueue(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ExpireReservationArgs{TenantArgs: t, Ref: ref} },
		&river.InsertOpts{ScheduledAt: at})
}

// ExpireWorker runs ExpireReservationArgs.
type ExpireWorker struct {
	river.WorkerDefaults[ExpireReservationArgs]
	DB     postgres.Beginner
	Events Events
}

// Work expires the ref's lines whose TTL has passed. If the job ran
// early (clock skew between the worker and the database), it snoozes
// until the reservation's expiry.
func (w *ExpireWorker) Work(ctx context.Context, job *river.Job[ExpireReservationArgs]) error {
	var snooze time.Duration
	err := postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		n, err := moveReservation(ctx, tx, w.Events, job.Args.Ref, expire(), true)
		if err != nil || n > 0 {
			return err
		}
		r, err := store.GetReservation(ctx, tx, job.Args.Ref)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.Status == domain.ReservationActive && !r.Held { // a held ref never expires
			snooze = max(time.Until(r.ExpiresAt), 0) + time.Second
		}
		return nil
	})
	if err != nil {
		return err
	}
	if snooze > 0 {
		return river.JobSnooze(snooze)
	}
	return nil
}
