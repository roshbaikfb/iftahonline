package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// COD reconciliation timing.
const (
	// PullEvery is how often settlements are pulled from carriers that
	// have an API for them.
	PullEvery = 24 * time.Hour
	// PullWindow is how far back each pull looks: payouts that appear
	// late are still caught, and lines already stored are skipped.
	PullWindow = 7 * 24 * time.Hour
	// MaxReportSpan bounds a report's period.
	MaxReportSpan = 366 * 24 * time.Hour
	// DefaultHeldPage and MaxHeldPage bound the held list.
	DefaultHeldPage = 50
	MaxHeldPage     = 200
)

var errReportPeriod = errs.Validation(errs.FieldError{Field: "from", Code: "range", Message: "from must be before to, at most a year apart"})

// Imported is what an import did.
type Imported struct {
	Lines  int // lines read
	Added  store.SettlementsAdded
	Errors []domain.LineError // lines skipped (at most domain.MaxLineErrors)
}

// ImportStatement reads a carrier's settlement statement (CSV) for one of
// the store's connections and stores its lines, matched to shipments.
// Importing the same statement again adds nothing.
func (s *Service) ImportStatement(ctx context.Context, connectionID ulid.ID, r io.Reader) (Imported, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return Imported{}, err
	}
	var conn store.SealedConnection
	if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		conn, txErr = store.GetConnection(ctx, tx, connectionID)
		return txErr
	}); errors.Is(err, domain.ErrNotFound) {
		return Imported{}, ErrConnectionNotFound // a disconnected one is fine: statements come after
	} else if err != nil {
		return Imported{}, err
	}
	loc := info.Location
	if loc == nil {
		loc = time.UTC
	}
	lines, bad, err := domain.ParseStatement(r, info.Currency, loc, s.clock())
	if errors.Is(err, domain.ErrStatement) {
		return Imported{}, errs.Validation(errs.FieldError{Field: "file", Code: "unreadable", Message: err.Error()})
	}
	if err != nil {
		return Imported{}, err
	}
	out := Imported{Lines: len(lines) + len(bad), Errors: bad}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if out.Added, txErr = store.InsertSettlements(ctx, tx, s.ids, conn.Connection, info.Currency, "import", lines); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "cod_statement.imported", TargetType: "carrier_connection",
			TargetID: connectionID.String(), Metadata: map[string]any{"imported": out.Added.Imported, "duplicates": out.Added.Duplicates,
				"matched": out.Added.Matched, "skipped": len(bad)}})
	})
	return out, err
}

// PullSettlementsArgs pulls a connection's settlements from its carrier
// every PullEvery, for as long as the connection is active and the
// carrier has an API for them.
type PullSettlementsArgs struct {
	jobs.TenantArgs
	ConnectionID ulid.ID `json:"connection_id"`
}

// Kind implements river.JobArgs.
func (PullSettlementsArgs) Kind() string { return "shipping.pull_settlements" }

// PullSettlements runs PullSettlementsArgs once. It reports whether to
// pull again (after PullEvery).
func (s *Service) PullSettlements(ctx context.Context, connectionID ulid.ID) (again bool, err error) {
	adapter, creds, err := s.Credentials(ctx, connectionID)
	if errors.Is(err, ErrConnectionNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	now := s.clock()
	got, err := adapter.CODSettlements(ctx, creds, now.Add(-PullWindow), now)
	switch {
	case errors.Is(err, carrier.ErrUnsupported), errors.Is(err, carrier.ErrAuth):
		return false, nil // no API (Bosta: statements are imported), or the key no longer works
	case err != nil:
		return true, err
	}
	info, err := s.stores(ctx)
	if err != nil {
		return true, err
	}
	lines := make([]domain.SettlementLine, 0, len(got))
	for _, g := range got {
		if g.Amount.Currency() != info.Currency {
			return true, fmt.Errorf("shipping: %s settlement in %s, store in %s", adapter.Name(), g.Amount.Currency(), info.Currency)
		}
		lines = append(lines, domain.SettlementLine{TrackingNumber: g.TrackingNumber, Amount: g.Amount, SettledAt: g.SettledAt, Reference: text.Clip(g.Reference, 300)})
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, txErr := store.GetConnection(ctx, tx, connectionID)
		if txErr != nil {
			return txErr
		}
		_, txErr = store.InsertSettlements(ctx, tx, s.ids, c.Connection, info.Currency, "api", lines)
		return txErr
	})
	return true, err
}

// schedulePullTx starts a connection's settlement pulls.
func (s *Service) schedulePullTx(ctx context.Context, tx pgx.Tx, connectionID ulid.ID) error {
	return s.enqueueTx(ctx, tx, func(t jobs.TenantArgs) river.JobArgs {
		return PullSettlementsArgs{TenantArgs: t, ConnectionID: connectionID}
	}, nil)
}

// CODReport is the cash-on-delivery position of parcels delivered in a
// period: per carrier, what they should bring in, what was paid out, and
// what the carriers still hold (aged); plus the paid-out lines that match
// no shipment.
type CODReport struct {
	From, To        time.Time
	Carriers        []domain.CODCarrier
	UnmatchedLines  int
	UnmatchedAmount money.Money
}

// Report builds the COD report of parcels delivered in [from, to).
func (s *Service) Report(ctx context.Context, from, to time.Time) (CODReport, error) {
	if !from.Before(to) || to.Sub(from) > MaxReportSpan {
		return CODReport{}, errReportPeriod
	}
	info, err := s.stores(ctx)
	if err != nil {
		return CODReport{}, err
	}
	out := CODReport{From: from, To: to}
	var unmatched int64
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if out.Carriers, txErr = store.CODSummary(ctx, tx, from, to, s.clock()); txErr != nil {
			return txErr
		}
		out.UnmatchedLines, unmatched, txErr = store.Unmatched(ctx, tx)
		return txErr
	})
	if err != nil {
		return CODReport{}, err
	}
	out.UnmatchedAmount, err = money.New(unmatched, info.Currency)
	return out, err
}

// Cursor is a keyset position in a list.
type Cursor = domain.Cursor

// HeldPage is one page of delivered parcels whose cash is still held.
type HeldPage struct {
	Items []domain.HeldParcel
	Next  *domain.Cursor
}

// Held lists delivered COD parcels not fully paid out, oldest first.
func (s *Service) Held(ctx context.Context, after *domain.Cursor, limit int) (HeldPage, error) {
	if limit == 0 {
		limit = DefaultHeldPage
	}
	if limit < 1 || limit > MaxHeldPage {
		return HeldPage{}, errPageSize
	}
	var items []domain.HeldParcel
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		items, txErr = store.Held(ctx, tx, after, limit+1)
		return txErr
	})
	if err != nil {
		return HeldPage{}, err
	}
	p := HeldPage{Items: items}
	if len(items) > limit {
		p.Items = items[:limit]
		last := p.Items[limit-1]
		p.Next = &domain.Cursor{At: last.DeliveredAt, ID: last.ShipmentID}
	}
	return p, nil
}

// MaxChargesOrders bounds one CarrierCharges call.
const MaxChargesOrders = 500

// CarrierCharges is what carriers kept of some of the store's orders'
// parcels, from their payouts (ADR-0068): orders without settled parcels
// are missing. At most MaxChargesOrders orders, one query.
func (s *Service) CarrierCharges(ctx context.Context, orderIDs []ulid.ID) (map[ulid.ID]money.Money, error) {
	if len(orderIDs) > MaxChargesOrders {
		return nil, fmt.Errorf("shipping: %d orders, at most %d", len(orderIDs), MaxChargesOrders)
	}
	var out map[ulid.ID]money.Money
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		out, err = store.CarrierCharges(ctx, tx, orderIDs)
		return err
	})
	return out, err
}
