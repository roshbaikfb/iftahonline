package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The founding-merchants program (S4b-2). A store's membership is read
// and relocked in its tenant transaction; what crosses stores goes
// through the app.founding_* functions.

// FoundingOpen is the program's discount for a merchant owned by owner
// subscribing now, or 0.
func FoundingOpen(ctx context.Context, tx Tx, owner ulid.ID) (int32, error) {
	bps, err := billingdb.New(tx).FoundingOpen(ctx, owner)
	if err != nil {
		return 0, fmt.Errorf("billing store: founding open: %w", err)
	}
	return bps, nil
}

// FoundingJoin gives the store in ctx the next seat, locking its price;
// 0 when it cannot join.
func FoundingJoin(ctx context.Context, tx Tx, owner ulid.ID, currency, planCode string, cycle domain.Cycle, locked int64) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	seat, err := billingdb.New(tx).FoundingJoin(ctx, billingdb.FoundingJoinParams{StoreID: store, OwnerID: owner, Currency: currency,
		PlanCode: planCode, Cycle: string(cycle), LockedMinor: locked})
	if err != nil {
		return 0, fmt.Errorf("billing store: founding join: %w", err)
	}
	return int(seat), nil
}

// FoundingMember is the store-in-ctx's membership; false when it never
// joined.
func FoundingMember(ctx context.Context, tx Tx) (domain.Member, bool, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Member{}, false, err
	}
	r, err := billingdb.New(tx).FoundingMember(ctx, store)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Member{}, false, nil
	}
	if err != nil {
		return domain.Member{}, false, fmt.Errorf("billing store: founding member: %w", err)
	}
	m := domain.Member{StoreID: r.StoreID, OwnerID: r.OwnerID, Seat: int(r.Seat), DiscountBps: r.DiscountBps, Currency: r.Currency,
		PlanCode: r.PlanCode, Cycle: domain.Cycle(r.Cycle), LockedMinor: r.LockedMinor, JoinedAt: r.JoinedAt}
	if r.LostAt != nil {
		m.LostAt = *r.LostAt
	}
	if r.LostReason != nil {
		m.LostReason = domain.Lost(*r.LostReason)
	}
	return m, true, nil
}

// FoundingRelock locks a member's price of another plan or cycle.
func FoundingRelock(ctx context.Context, tx Tx, currency, planCode string, cycle domain.Cycle, locked int64) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = billingdb.New(tx).FoundingRelock(ctx, billingdb.FoundingRelockParams{Currency: currency, PlanCode: planCode, Cycle: string(cycle),
		LockedMinor: locked, StoreID: store}); err != nil {
		return fmt.Errorf("billing store: founding relock: %w", err)
	}
	return nil
}

// FoundingSweep ends the memberships of stores that left their
// subscription or left a renewal unpaid past unpaidBefore.
func FoundingSweep(ctx context.Context, tx Tx, now, unpaidBefore time.Time) (int, error) {
	n, err := billingdb.New(tx).FoundingSweep(ctx, billingdb.FoundingSweepParams{Now: now, UnpaidBefore: unpaidBefore})
	if err != nil {
		return 0, fmt.Errorf("billing store: founding sweep: %w", err)
	}
	return int(n), nil
}

// FoundingProgram is the program's settings.
func FoundingProgram(ctx context.Context, tx Tx) (domain.Program, error) {
	r, err := billingdb.New(tx).FoundingProgram(ctx)
	if err != nil {
		return domain.Program{}, fmt.Errorf("billing store: founding program: %w", err)
	}
	return domain.Program{Enabled: r.Enabled, Seats: int(r.Seats), DiscountBps: r.DiscountBps}, nil
}

// SaveFoundingProgram writes the program's settings.
func SaveFoundingProgram(ctx context.Context, tx Tx, p domain.Program) error {
	if err := billingdb.New(tx).SaveFoundingProgram(ctx, billingdb.SaveFoundingProgramParams{Enabled: p.Enabled,
		Seats: int32(p.Seats), DiscountBps: p.DiscountBps}); err != nil { //nolint:gosec // Validate bounds the seats
		return fmt.Errorf("billing store: save founding program: %w", err)
	}
	return nil
}

// Seats is the program as the site shows it.
type Seats struct {
	domain.Program
	Taken int
}

// FoundingSeats are the program's seats and how many are taken.
func FoundingSeats(ctx context.Context, tx Tx) (Seats, error) {
	r, err := billingdb.New(tx).FoundingSeats(ctx)
	if err != nil {
		return Seats{}, fmt.Errorf("billing store: founding seats: %w", err)
	}
	return Seats{Program: domain.Program{Enabled: r.Enabled, Seats: int(r.Seats), DiscountBps: r.DiscountBps}, Taken: int(r.Taken)}, nil
}

// FoundingList is every member, by seat, for the console.
func FoundingList(ctx context.Context, tx Tx) ([]domain.Member, error) {
	rows, err := billingdb.New(tx).FoundingList(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: founding list: %w", err)
	}
	out := make([]domain.Member, len(rows))
	for i, r := range rows {
		out[i] = domain.Member{StoreID: r.StoreID, Seat: int(r.Seat), DiscountBps: r.DiscountBps, Currency: r.Currency, PlanCode: r.PlanCode,
			Cycle: domain.Cycle(r.Cycle), LockedMinor: r.LockedMinor, JoinedAt: r.JoinedAt, LostAt: r.LostAt.UTC(), LostReason: domain.Lost(r.LostReason)}
		if r.LostAt.Year() == 1 {
			out[i].LostAt = time.Time{}
		}
	}
	return out, nil
}
