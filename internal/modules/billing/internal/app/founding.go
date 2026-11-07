package app

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// The founding-merchants program (S4b-2, approved 2026-11-02):
//   - a merchant subscribing while seats are left pays its first period
//     with the program's discount, and takes a seat once it is paid;
//   - each renewal is charged the lower of its locked price and the
//     current one (a new plan or cycle locks its price), then the
//     discount, as a line of its own;
//   - leaving the subscription, or a renewal unpaid 30 days past due,
//     ends the membership; the seat is not given again.

// foundingOpen is the program's discount for the store-in-ctx's owner
// subscribing now, or 0 (no owner reader, off, full, or already joined).
func (s *Service) foundingOpen(ctx context.Context, tx pgx.Tx) (int32, error) {
	if s.owner == nil {
		return 0, nil
	}
	owner, err := s.owner(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("billing: owner: %w", err)
	}
	return store.FoundingOpen(ctx, tx, owner)
}

// foundingJoin gives the store in ctx a seat once its first period of p
// is paid, locking that price; a store that cannot join (off, full, its
// owner already in) stays out, silently.
func (s *Service) foundingJoin(ctx context.Context, tx pgx.Tx, p domain.Plan, cycle domain.Cycle, currency string) error {
	if s.owner == nil {
		return nil
	}
	price, sold, err := store.PlanPrice(ctx, tx, p.ID, currency, cycle)
	if err != nil || !sold {
		return err
	}
	owner, err := s.owner(ctx, tx)
	if err != nil {
		return fmt.Errorf("billing: owner: %w", err)
	}
	seat, err := store.FoundingJoin(ctx, tx, owner, currency, p.Code, cycle, price)
	if err != nil {
		return err
	}
	if seat > 0 && s.log != nil {
		s.log.InfoContext(ctx, "billing: a founding merchant joined", "seat", seat)
	}
	return nil
}

// foundingRenewal are a renewal's lines: the subscription at its price,
// a founding member's at its locked price with its discount after it.
func foundingRenewal(ctx context.Context, tx pgx.Tx, currency string, m *member, r store.Renewal) ([]domain.Line, error) {
	line := domain.Line{Kind: domain.LineSubscription, StoreID: m.id, Quantity: 1, AmountMinor: r.AmountMinor,
		Description: fmt.Sprintf("%s: باقة %s (%s)", m.name, r.PlanName, cycleAr(r.Cycle))}
	if r.Kind != domain.KindSubscription {
		return []domain.Line{line}, nil
	}
	fm, ok, err := store.FoundingMember(ctx, tx)
	if err != nil || !ok || !fm.Active() {
		return []domain.Line{line}, err
	}
	charged, discount, relock := fm.Renewal(r.PlanCode, r.Cycle, currency, r.AmountMinor)
	if relock {
		if err = store.FoundingRelock(ctx, tx, currency, r.PlanCode, r.Cycle, charged); err != nil {
			return nil, err
		}
	}
	if charged < r.AmountMinor {
		line.AmountMinor = charged
		line.Description += " · سعر التاجر المؤسس المثبّت"
	}
	lines := []domain.Line{line}
	if discount > 0 {
		lines = append(lines, domain.DiscountLine(m.id, discount, fm.DiscountBps))
	}
	return lines, nil
}

// FoundingSweepArgs ends the memberships of stores that left (daily). It
// is about no one store.
type FoundingSweepArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (FoundingSweepArgs) Kind() string { return "billing.founding_sweep" }

// FoundingSweep ends the memberships of stores that left their
// subscription, or left a renewal unpaid past domain.UnpaidGrace.
func (s *Service) FoundingSweep(ctx context.Context) error {
	now := s.now().UTC()
	var n int
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		var sErr error
		n, sErr = store.FoundingSweep(ctx, tx, now, now.Add(-domain.UnpaidGrace))
		return sErr
	})
	if err == nil && n > 0 && s.log != nil {
		s.log.InfoContext(ctx, "billing: founding memberships ended", "count", n)
	}
	return err
}

// FoundingSweepWorker runs FoundingSweep.
type FoundingSweepWorker struct {
	river.WorkerDefaults[FoundingSweepArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *FoundingSweepWorker) Work(ctx context.Context, _ *river.Job[FoundingSweepArgs]) error {
	return w.Svc.FoundingSweep(ctx)
}

// FoundingSweepPeriodic runs the sweep daily, and at start.
func FoundingSweepPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return FoundingSweepArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

// The console's side of the program, on the admin pool.

// FoundingProgram is the program's settings.
func (s *Service) FoundingProgram(ctx context.Context) (domain.Program, error) {
	var p domain.Program
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		p, txErr = store.FoundingProgram(ctx, tx)
		return txErr
	})
	return p, err
}

// SaveFoundingProgram checks and writes the program's settings. A new
// discount reaches only merchants who join after it; seats below those
// taken close the program to new members.
func (s *Service) SaveFoundingProgram(ctx context.Context, p domain.Program) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error { return store.SaveFoundingProgram(ctx, tx, p) })
}

// FoundingMembers are every member, by seat, and the seats taken.
func (s *Service) FoundingMembers(ctx context.Context) ([]domain.Member, error) {
	var out []domain.Member
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.FoundingList(ctx, tx)
		return txErr
	})
	return out, err
}

// FoundingSeats are the program as the site shows it.
func (s *Service) FoundingSeats(ctx context.Context) (domain.Seats, error) {
	var out domain.Seats
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.FoundingSeats(ctx, tx)
		return txErr
	})
	return out, err
}
