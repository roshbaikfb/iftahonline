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
	req, err := domain.ValidateReserve(req)
	if err != nil {
		return domain.Reservation{}, err
	}
	var res domain.Reservation
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		res, txErr = s.reserveTx(ctx, tx, req)
		return txErr
	})
	return res, err
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
	var soldOut []ulid.ID
	for i, l := range req.Lines {
		var r store.Reserved
		if r, err = store.Reserve(ctx, tx, l.VariantID, l.Qty); err != nil {
			return domain.Reservation{}, err
		}
		if !r.OK {
			return domain.Reservation{}, s.whyNotReserved(ctx, tx, l)
		}
		if r.SoldOut {
			soldOut = append(soldOut, r.ProductID)
		}
		lines[i] = store.ReservedLine{ID: s.ids.New(), Line: l, Counted: r.Counted}
	}
	expires := s.now().Add(req.TTL).UTC().Truncate(time.Microsecond)
	if err = store.InsertReservation(ctx, tx, req.Ref, expires, lines); err != nil {
		if postgres.IsUniqueViolation(err, "stock_reservations_ref_variant_key") {
			return domain.Reservation{}, ErrReservationRefUsed // a concurrent call with the same ref won
		}
		return domain.Reservation{}, err
	}
	if err = s.scheduleExpiry(ctx, tx, req.Ref, expires); err != nil {
		return domain.Reservation{}, err
	}
	if err = s.events.stockChanged(ctx, tx, soldOut); err != nil {
		return domain.Reservation{}, err
	}
	return domain.Reservation{Ref: req.Ref, Lines: req.Lines, Status: domain.ReservationActive, ExpiresAt: expires}, nil
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
// was already released or has expired does nothing.
func (s *Service) ReleaseStock(ctx context.Context, ref string) error {
	if err := domain.ValidateRef(ref); err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		n, err := endReservation(ctx, tx, s.events, ref, domain.ReservationReleased, false)
		if err != nil || n > 0 {
			return err
		}
		if _, err := store.GetReservation(ctx, tx, ref); err != nil {
			return notFound(err, ErrReservationNotFound)
		}
		return nil
	})
}

// endReservation ends ref's active lines and gives their counted units
// back, in variant ID order, publishing a stock change for products that
// come back in stock. It returns the number of lines ended.
func endReservation(ctx context.Context, tx pgx.Tx, ev Events, ref string, status domain.ReservationStatus, onlyExpired bool) (int, error) {
	lines, err := store.EndReservation(ctx, tx, ref, status, onlyExpired)
	if err != nil {
		return 0, err
	}
	slices.SortFunc(lines, func(a, b store.ReservedLine) int {
		return bytes.Compare(a.Line.VariantID[:], b.Line.VariantID[:])
	})
	var restocked []ulid.ID
	for _, l := range lines {
		if !l.Counted {
			continue
		}
		productID, back, err := store.Unreserve(ctx, tx, l.Line.VariantID, l.Line.Qty)
		if err != nil {
			return 0, err
		}
		if back {
			restocked = append(restocked, productID)
		}
	}
	return len(lines), ev.stockChanged(ctx, tx, restocked)
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
		n, err := endReservation(ctx, tx, w.Events, job.Args.Ref, domain.ReservationExpired, true)
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
		if r.Status == domain.ReservationActive {
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
