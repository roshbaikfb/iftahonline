package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Agencies set up from the console (ADR-0059).

// InsertAgency creates an agency account; tx must be behind its wall.
func InsertAgency(ctx context.Context, tx Tx, a domain.Agency) error {
	if err := billingdb.New(tx).InsertAgency(ctx, billingdb.InsertAgencyParams{ID: a.ID, Name: a.Name, Currency: a.Currency,
		Country: a.Country, PlanCode: &a.PlanCode}); err != nil {
		return fmt.Errorf("billing store: insert agency: %w", err)
	}
	return nil
}

// Agencies lists every agency, newest first, through
// app.agency_accounts (names and counts only).
func Agencies(ctx context.Context, tx Tx) ([]domain.Agency, error) {
	rows, err := billingdb.New(tx).ListAgencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing store: agencies: %w", err)
	}
	out := make([]domain.Agency, len(rows))
	for i, r := range rows {
		out[i] = domain.Agency{Account: domain.Account{ID: r.ID, Kind: domain.AccountAgency, Name: r.Name, Currency: r.Currency,
			Country: domain.CountryOf(r.Currency)}, PlanCode: r.PlanCode, Stores: r.Stores, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// LockAgency is the agency account id, locked until tx ends; tx must be
// behind its wall. ok is false when id is not an agency.
func LockAgency(ctx context.Context, tx Tx, id ulid.ID) (a domain.Agency, ok bool, err error) {
	r, err := billingdb.New(tx).LockAgency(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return a, false, nil
	case err != nil:
		return a, false, fmt.Errorf("billing store: lock agency: %w", err)
	}
	return domain.Agency{Account: domain.Account{ID: r.ID, Kind: domain.AccountAgency, Name: r.Name, Currency: r.Currency, Country: r.Country},
		PlanCode: r.PlanCode, CreatedAt: r.CreatedAt}, true, nil
}

// ActivePlan is code's version on sale; ok is false when none is.
func ActivePlan(ctx context.Context, tx Tx, code string) (p domain.Plan, ok bool, err error) {
	r, err := billingdb.New(tx).GetActivePlanByCode(ctx, code)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return p, false, nil
	case err != nil:
		return p, false, fmt.Errorf("billing store: active plan: %w", err)
	}
	kind, err := domain.ParseKind(r.Kind)
	if err != nil {
		return p, false, err
	}
	return domain.Plan{ID: r.ID, Code: r.Code, Version: r.Version, Kind: kind, NameAr: r.NameAr, NameEn: r.NameEn,
		TrialDays: r.TrialDays, Public: r.Public, Active: r.Active, Sort: r.Sort}, true, nil
}

// HasOpenInvoice reports whether account owes an invoice; tx must be
// behind its wall.
func HasOpenInvoice(ctx context.Context, tx Tx, account ulid.ID) (bool, error) {
	ok, err := billingdb.New(tx).HasOpenInvoice(ctx, account)
	if err != nil {
		return false, fmt.Errorf("billing store: open invoice: %w", err)
	}
	return ok, nil
}

// JoinAccount makes the store in ctx a member of account; tx must be
// behind the account's wall.
func JoinAccount(ctx context.Context, tx Tx, account ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = billingdb.New(tx).InsertAccountStore(ctx, billingdb.InsertAccountStoreParams{AccountID: account, MemberStoreID: store}); err != nil {
		return fmt.Errorf("billing store: join account: %w", err)
	}
	return nil
}

// LeaveAccount removes the store in ctx from account; tx must be behind
// the account's wall. Its invoices stay.
func LeaveAccount(ctx context.Context, tx Tx, account ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if _, err = billingdb.New(tx).DeleteAccountStore(ctx, billingdb.DeleteAccountStoreParams{AccountID: account, MemberStoreID: store}); err != nil {
		return fmt.Errorf("billing store: leave account: %w", err)
	}
	return nil
}

// PutSubscription writes the store-in-ctx's subscription whole, creating
// it when it had none, in account (nil: none yet); a billed renewal is
// forgotten.
func PutSubscription(ctx context.Context, tx Tx, s domain.Subscription, account *ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	var cycle *string
	if s.Cycle != "" {
		c := string(s.Cycle)
		cycle = &c
	}
	if err = billingdb.New(tx).PutSubscription(ctx, billingdb.PutSubscriptionParams{
		StoreID: store, PlanID: s.PlanID, Status: string(s.Status), Cycle: cycle,
		TrialEndsAt: ptr(s.TrialEndsAt), PeriodStart: ptr(s.PeriodStart), PeriodEnd: ptr(s.PeriodEnd),
		GraceUntil: ptr(s.GraceUntil), SuspendedAt: ptr(s.SuspendedAt),
		CancelAtPeriodEnd: s.CancelAtPeriodEnd, TrialUsed: s.TrialUsed, AccountID: account,
	}); err != nil {
		return fmt.Errorf("billing store: put subscription: %w", err)
	}
	return nil
}
