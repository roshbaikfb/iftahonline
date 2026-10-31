package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Merchants choose their plan (ADR-0060).

// Offer is a plan on sale as the store in ctx sees it: in its currency.
type Offer struct {
	domain.PlanVersion
	Monthly, Annual *int64              // prices; nil when the cycle is not sold
	Fee             *domain.CurrencyFee // pay as you sell
	Current         bool                // the plan the store is on
	TrialAvailable  bool                // a trial it may still start
}

// Offers are the public plans on sale and the catalog naming their
// features.
type Offers struct {
	Currency string
	Features []domain.Feature
	Plans    []Offer
}

// Outcome is what a choice did; CheckoutURL is the gateway's page when
// the first period is to be paid.
type Outcome struct {
	Action      domain.Action
	CheckoutURL string
}

var (
	errPlanUnknown = errs.NotFound("plan_not_found", "no plan with this code is on sale")
	errNoPrice     = errs.Validation(errs.FieldError{Field: "cycle", Code: "not_sold", Message: "the plan is not sold for this cycle in the store's currency"})
	errNoTrial     = errs.Conflict("trial_unavailable", "a trial is for a store on the free plan that never had one")
)

// view is the store-in-ctx's subscription (the free plan without one)
// and the plan it is on.
type view struct {
	sub     domain.Subscription
	has     bool
	current domain.Plan
	free    domain.Plan
}

func (s *Service) viewTx(ctx context.Context, tx pgx.Tx) (view, error) {
	var v view
	var err error
	if v.free, err = store.FreePlan(ctx, tx); err != nil {
		return v, err
	}
	if v.sub, v.has, err = store.Subscription(ctx, tx); err != nil {
		return v, err
	}
	if !v.has {
		v.sub, v.current = domain.OnFree(v.free), v.free
		return v, nil
	}
	v.current, err = store.Plan(ctx, tx, v.sub.PlanID)
	return v, err
}

// Offers lists the plans the store in ctx may choose, priced in its
// currency.
func (s *Service) Offers(ctx context.Context) (Offers, error) {
	if s.stores == nil {
		return Offers{}, errors.New("billing: no stores")
	}
	info, err := s.stores(ctx)
	if err != nil {
		return Offers{}, err
	}
	out := Offers{Currency: info.Currency}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		v, txErr := s.viewTx(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if out.Features, txErr = store.Features(ctx, tx); txErr != nil {
			return txErr
		}
		plans, txErr := store.Plans(ctx, tx)
		if txErr != nil {
			return txErr
		}
		onPlan := v.sub.PlanApplies(s.now()) || v.sub.Kind == domain.KindFree
		for _, p := range plans {
			if !p.Active || !p.Public || p.Kind == domain.KindAgency {
				continue
			}
			o := Offer{PlanVersion: p, Current: onPlan && p.Code == v.current.Code,
				TrialAvailable: p.Kind.Paid() && p.TrialDays > 0 && !v.sub.TrialUsed && v.current.Kind == domain.KindFree}
			for _, pr := range p.Prices {
				if pr.Currency != info.Currency {
					continue
				}
				amount := pr.AmountMinor
				if pr.Cycle == domain.Monthly {
					o.Monthly = &amount
				} else {
					o.Annual = &amount
				}
			}
			for i := range p.Fees {
				if p.Fees[i].Currency == info.Currency {
					o.Fee = &p.Fees[i]
				}
			}
			out.Plans = append(out.Plans, o)
		}
		return nil
	})
	return out, err
}

// activePublic is code's version on sale to merchants.
func activePublic(ctx context.Context, tx pgx.Tx, code string) (domain.Plan, error) {
	p, ok, err := store.ActivePlan(ctx, tx, code)
	if err != nil {
		return domain.Plan{}, err
	}
	if !ok || !p.Public {
		return domain.Plan{}, errPlanUnknown
	}
	return p, nil
}

// Choose applies the store-in-ctx's choice of plan code (cycle for a
// paid plan), as the approved rules say (domain.Choose): at once, at the
// paid period's end, or, for a paid plan from the free plan, a trial or
// pay as you sell, once the first period is paid on the gateway's page,
// whose URL the outcome carries.
func (s *Service) Choose(ctx context.Context, code string, cycle domain.Cycle, payer Payer) (Outcome, error) {
	if s.stores == nil {
		return Outcome{}, errors.New("billing: no stores")
	}
	info, err := s.stores(ctx)
	if err != nil {
		return Outcome{}, err
	}
	var out Outcome
	var invoice ulid.ID
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		v, txErr := s.viewTx(ctx, tx)
		if txErr != nil {
			return txErr
		}
		p, txErr := activePublic(ctx, tx, code)
		if txErr != nil {
			return txErr
		}
		billed, txErr := store.RenewalInvoice(ctx, tx)
		if txErr == nil && billed != nil {
			txErr = domain.ErrSettleFirst // a renewal billed: its price and plan are set
		}
		if txErr != nil {
			return txErr
		}
		choice, txErr := v.sub.Choose(v.current, p, v.free, cycle, s.now())
		if txErr != nil {
			return txErr
		}
		out.Action = choice.Action
		if choice.Action != domain.ActPay {
			return s.writeChoiceTx(ctx, tx, v, choice.Next)
		}
		invoice, txErr = s.subscribeInvoiceTx(ctx, tx, v, p, cycle, info)
		return txErr
	})
	if err != nil || out.Action != domain.ActPay {
		return out, err
	}
	out.CheckoutURL, err = s.PayNow(ctx, invoice, payer)
	return out, err
}

// writeChoiceTx saves next: a new row for a store that had none.
func (s *Service) writeChoiceTx(ctx context.Context, tx pgx.Tx, v view, next domain.Subscription) error {
	if !v.has {
		return s.putTx(ctx, tx, next, nil)
	}
	return s.saveTx(ctx, tx, next)
}

// subscribeInvoiceTx writes the first period's invoice of p for the store
// in ctx, in its own account (opened now if it has none), replacing an
// unpaid earlier one. The subscription itself waits for the payment.
func (s *Service) subscribeInvoiceTx(ctx context.Context, tx pgx.Tx, v view, p domain.Plan, cycle domain.Cycle, info StoreInfo) (ulid.ID, error) {
	price, sold, err := store.PlanPrice(ctx, tx, p.ID, info.Currency, cycle)
	if err != nil {
		return ulid.ID{}, err
	}
	if !sold {
		return ulid.ID{}, errNoPrice
	}
	self, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("billing: %w", err)
	}
	if !v.has {
		if err = store.PutSubscription(ctx, tx, v.sub, nil); err != nil {
			return ulid.ID{}, err
		}
	}
	account, has, err := store.SubscriptionAccount(ctx, tx)
	if err != nil {
		return ulid.ID{}, err
	}
	acct := domain.Account{ID: account, Kind: domain.AccountStore, Name: info.Name, Currency: info.Currency, Country: domain.CountryOf(info.Currency)}
	if !has {
		acct.ID = s.ids.New()
		if err = store.OpenStoreAccount(ctx, tx, acct); err != nil {
			return ulid.ID{}, err
		}
	} else if err = store.UseAccount(ctx, tx, account); err != nil {
		return ulid.ID{}, err
	}
	if err = store.VoidSubscribeInvoices(ctx, tx, acct.ID); err != nil {
		return ulid.ID{}, err
	}
	tax, err := store.TaxRate(ctx, tx, acct.Country)
	if err != nil {
		return ulid.ID{}, err
	}
	lines := []domain.Line{{Kind: domain.LineSubscription, StoreID: self.ID, Quantity: 1, AmountMinor: price,
		Description: fmt.Sprintf("%s: باقة %s (%s)", info.Name, p.NameAr, cycleAr(cycle))}}
	totals, ok := domain.Totals(lines, tax)
	if !ok {
		return ulid.ID{}, errNoPrice // a free price: nothing to pay
	}
	now := s.now().UTC()
	inv := store.Invoice{ID: s.ids.New(), Account: acct.ID, Period: domain.PeriodOf(now), Currency: info.Currency, Totals: totals, Issued: now, Lines: lines}
	if _, err = store.InsertSubscribeInvoice(ctx, tx, inv, store.Subscribe{ForStore: self.ID, Plan: p.ID, Cycle: cycle}); err != nil {
		return ulid.ID{}, err
	}
	return inv.ID, nil
}

// StartTrial starts the trial of plan code for the store in ctx: once,
// from the free plan, without a card.
func (s *Service) StartTrial(ctx context.Context, code string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		v, err := s.viewTx(ctx, tx)
		if err != nil {
			return err
		}
		p, err := activePublic(ctx, tx, code)
		if err != nil {
			return err
		}
		next, err := v.sub.StartTrial(p, s.now())
		if errors.Is(err, domain.ErrTransition) {
			return errNoTrial
		}
		if err != nil {
			return err
		}
		return s.writeChoiceTx(ctx, tx, v, next)
	})
}

// CancelRenewal ends the store-in-ctx's paid plan at its period's end
// (then the free plan).
func (s *Service) CancelRenewal(ctx context.Context) error {
	return s.reschedule(ctx, func(v view) (domain.Plan, domain.Cycle) { return v.free, "" })
}

// ResumeRenewal keeps the store-in-ctx's current plan and cycle after
// the period, dropping a cancellation or a scheduled change.
func (s *Service) ResumeRenewal(ctx context.Context) error {
	return s.reschedule(ctx, func(v view) (domain.Plan, domain.Cycle) { return v.current, v.sub.Cycle })
}

// reschedule is a choice made during a paid period.
func (s *Service) reschedule(ctx context.Context, target func(view) (domain.Plan, domain.Cycle)) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		v, err := s.viewTx(ctx, tx)
		if err != nil {
			return err
		}
		if v.sub.Status != domain.Active || !v.sub.Kind.Paid() {
			return domain.ErrNoChange
		}
		p, c := target(v)
		choice, err := v.sub.Choose(v.current, p, v.free, c, s.now())
		if err != nil {
			return err
		}
		return s.saveTx(ctx, tx, choice.Next)
	})
}
