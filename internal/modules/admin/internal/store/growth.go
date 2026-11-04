package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store/admindb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's daily record of what billing keeps no history of
// (ADR-0081): the recurring revenue per day, and who paid as each month
// began.

func date(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

// utcDate is a date column as midnight UTC of its day.
func utcDate(d pgtype.Date) time.Time {
	return time.Date(d.Time.Year(), d.Time.Month(), d.Time.Day(), 0, 0, 0, 0, time.UTC)
}

// DayMRR is one day's recurring revenue in one currency.
type DayMRR struct {
	Day      time.Time // midnight UTC of the Cairo date
	Currency string
	MRRMinor int64
	Accounts int64
}

// SaveMRR records the day's reading; a later one the same day replaces it.
func SaveMRR(ctx context.Context, db DB, m DayMRR, at time.Time) error {
	if err := admindb.New(db).SaveGrowthMRR(ctx, admindb.SaveGrowthMRRParams{Day: date(m.Day), Currency: m.Currency, MrrMinor: m.MRRMinor,
		Accounts: m.Accounts, TakenAt: at}); err != nil {
		return fmt.Errorf("admin store: save mrr: %w", err)
	}
	return nil
}

// StartMonth records the month's paying accounts, read on day: only the
// first reading of a month is kept. False when the month already has one.
func StartMonth(ctx context.Context, db DB, month, day time.Time, accounts []ulid.ID) (bool, error) {
	q := admindb.New(db)
	n, err := q.StartGrowthMonth(ctx, admindb.StartGrowthMonthParams{Month: date(month), TakenOn: date(day)})
	if err != nil {
		return false, fmt.Errorf("admin store: start month: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	if accounts == nil {
		accounts = []ulid.ID{}
	}
	if err = q.SaveGrowthPaying(ctx, admindb.SaveGrowthPayingParams{Month: date(month), Accounts: accounts}); err != nil {
		return false, fmt.Errorf("admin store: save paying: %w", err)
	}
	return true, nil
}

// MRRSince are the days' readings from since on, oldest first.
func MRRSince(ctx context.Context, db DB, since time.Time) ([]DayMRR, error) {
	rows, err := admindb.New(db).GrowthMRR(ctx, date(since))
	if err != nil {
		return nil, fmt.Errorf("admin store: mrr: %w", err)
	}
	out := make([]DayMRR, len(rows))
	for i, r := range rows {
		out[i] = DayMRR{Day: utcDate(r.Day), Currency: r.Currency, MRRMinor: r.MrrMinor, Accounts: r.Accounts}
	}
	return out, nil
}

// Churn are the months from since on whose start and the next month's
// were both read in their first days, oldest first.
func Churn(ctx context.Context, db DB, since time.Time) ([]domain.MonthChurn, error) {
	rows, err := admindb.New(db).GrowthChurn(ctx, admindb.GrowthChurnParams{Since: date(since), GraceDays: domain.ChurnGraceDays})
	if err != nil {
		return nil, fmt.Errorf("admin store: churn: %w", err)
	}
	out := make([]domain.MonthChurn, len(rows))
	for i, r := range rows {
		out[i] = domain.MonthChurn{Month: utcDate(r.Month), Start: r.Start, Joined: r.Joined, Lost: r.Lost}
	}
	return out, nil
}
