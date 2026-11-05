package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// StoreInfo is what invoices need of a store.
type StoreInfo struct {
	Name     string
	Currency string // what the store sells in
	Country  string // the merchant's (ADR-0088); empty is its currency's
}

// country is the merchant's country.
func (i StoreInfo) country() string {
	if i.Country != "" {
		return i.Country
	}
	return domain.CountryOf(i.Currency)
}

// billed is the currency a store opening an account now is billed in
// (ADR-0090): the pound in Egypt, the dollar elsewhere. An account keeps
// the currency it opened in.
func (i StoreInfo) billed() string { return domain.BillingCurrency(i.country()) }

// billedTx is the currency the store in ctx is billed in: its account's,
// or the one it would open in (StoreInfo.billed, read from info only
// then). It leaves tx behind the account's wall when it has one.
func billedTx(ctx context.Context, tx pgx.Tx, info func() (StoreInfo, error)) (string, error) {
	account, has, err := accountOf(ctx, tx)
	if err != nil {
		return "", err
	}
	if !has {
		i, iErr := info()
		return i.billed(), iErr
	}
	if err = store.UseAccount(ctx, tx, account); err != nil {
		return "", err
	}
	a, err := store.Account(ctx, tx, account)
	return a.Currency, err
}

// known is info already read, for billedTx.
func known(info StoreInfo) func() (StoreInfo, error) {
	return func() (StoreInfo, error) { return info, nil }
}

// storeAccountTx is the store-in-ctx's account, and puts tx behind its
// wall: its own or its agency's, or one opened now in its country's
// billing currency when it has none.
func (s *Service) storeAccountTx(ctx context.Context, tx pgx.Tx, info StoreInfo) (domain.Account, error) {
	account, has, err := store.SubscriptionAccount(ctx, tx)
	if err != nil {
		return domain.Account{}, err
	}
	if !has {
		if info.Currency == "" {
			return domain.Account{}, errors.New("billing: no stores")
		}
		a := domain.Account{ID: s.ids.New(), Kind: domain.AccountStore, Name: info.Name, Currency: info.billed(), Country: info.country()}
		return a, store.OpenStoreAccount(ctx, tx, a)
	}
	if err = store.UseAccount(ctx, tx, account); err != nil {
		return domain.Account{}, err
	}
	return store.Account(ctx, tx, account)
}

// Stores reads the store in ctx; cmd/platform adapts tenancy.
type Stores func(ctx context.Context) (StoreInfo, error)

// InvoiceMonthArgs starts a month's invoices: every billable store's.
type InvoiceMonthArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (InvoiceMonthArgs) Kind() string { return "billing.invoice_month" }

// InvoiceStoreArgs gives a store its account and asks for the account's
// invoice of Period.
type InvoiceStoreArgs struct {
	jobs.TenantArgs
	Period time.Time `json:"period"`
}

// Kind implements river.JobArgs.
func (InvoiceStoreArgs) Kind() string { return "billing.invoice_store" }

// InvoiceAccountArgs writes an account's invoice of Period.
type InvoiceAccountArgs struct {
	jobs.GlobalArgs
	AccountID ulid.ID   `json:"account_id"`
	Period    time.Time `json:"period"`
}

// Kind implements river.JobArgs.
func (InvoiceAccountArgs) Kind() string { return "billing.invoice_account" }

// StartMonth asks for the previous month's invoice of every store with
// something to bill. It runs daily; what was invoiced already is skipped.
func (s *Service) StartMonth(ctx context.Context) error {
	if s.jobs == nil {
		return errors.New("billing: no job inserter")
	}
	period := domain.PreviousPeriod(s.now())
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		stores, err := store.BillableStores(ctx, tx, period)
		if err != nil {
			return err
		}
		for _, id := range stores {
			sid := tenant.StoreID{ID: id}
			if _, err = s.jobs.InsertTx(tenant.WithStore(ctx, sid), tx, InvoiceStoreArgs{TenantArgs: jobs.TenantArgs{StoreID: sid}, Period: period}, jobs.Unique()); err != nil {
				return fmt.Errorf("billing: enqueue store invoice: %w", err)
			}
		}
		return nil
	})
}

// InvoiceStore gives the store in ctx its own account when it has none
// (an agency's stores have theirs), then asks for the account's invoice
// of period.
func (s *Service) InvoiceStore(ctx context.Context, period time.Time) error {
	if s.jobs == nil || s.stores == nil {
		return errors.New("billing: no job inserter or stores")
	}
	info, err := s.stores(ctx)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		acct, err := s.storeAccountTx(ctx, tx, info)
		if errors.Is(err, store.ErrNoSubscription) {
			return nil // nothing to bill: fees and renewals need a plan
		}
		if err != nil {
			return err
		}
		if _, err = s.jobs.InsertTx(ctx, tx, InvoiceAccountArgs{AccountID: acct.ID, Period: period}, jobs.Unique()); err != nil {
			return fmt.Errorf("billing: enqueue account invoice: %w", err)
		}
		return nil
	})
}

// member is one of an account's stores on an invoice.
type member struct {
	id      ulid.ID
	name    string
	renewal *ulid.ID // the plan version its renewal on the invoice (or the agency's one) is into
}

// InvoiceAccount writes an account's invoice of period, once: each of its
// stores' renewal due by the period's end, due fees, and credits, with
// the account country's tax, in one transaction that also marks what it
// billed in each store (ADR-0049). An agency's plan is billed once.
// Nothing to bill writes nothing.
func (s *Service) InvoiceAccount(ctx context.Context, account ulid.ID, period time.Time) error {
	if s.stores == nil {
		return errors.New("billing: no stores")
	}
	var members []member
	done := false
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		exists, err := store.InvoiceExists(ctx, tx, account, period)
		if err != nil || exists {
			done = exists
			return err
		}
		ids, err := store.AccountStores(ctx, tx, account)
		for _, id := range ids {
			members = append(members, member{id: id})
		}
		return err
	})
	if err != nil || done {
		return err
	}
	// Names before the invoice's transaction: they come from tenancy,
	// which would take a second connection inside it.
	for i := range members {
		info, err := s.stores(tenant.WithStore(ctx, tenant.StoreID{ID: members[i].id}))
		if err != nil {
			return err
		}
		members[i].name = info.Name
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error { return s.invoiceTx(ctx, tx, account, period, members) })
}

func (s *Service) invoiceTx(ctx context.Context, tx pgx.Tx, account ulid.ID, period time.Time, members []member) error {
	if err := store.UseAccount(ctx, tx, account); err != nil {
		return err
	}
	// One writer of the account's invoices at a time: a fees invoice at
	// the card's minimum (ADR-0086) may be written meanwhile.
	if _, err := store.LockBalance(ctx, tx, account); err != nil {
		return err
	}
	if exists, err := store.InvoiceExists(ctx, tx, account, period); err != nil || exists {
		return err
	}
	acct, err := store.Account(ctx, tx, account)
	if err != nil {
		return err
	}
	tax, err := store.TaxRate(ctx, tx, acct.Country)
	if err != nil {
		return err
	}
	var lines []domain.Line
	agencyBilled := false
	for i := range members {
		ls, lErr := linesOf(ctx, tx, acct, period, &members[i], &agencyBilled)
		if lErr != nil {
			return lErr
		}
		lines = append(lines, ls...)
	}
	totals, ok := domain.Totals(lines, tax)
	if !ok {
		return nil // nothing to bill, or credits waiting for charges
	}
	inv := store.Invoice{ID: s.ids.New(), Account: account, Period: period, Currency: acct.Currency, Totals: totals, Issued: s.now().UTC(), Lines: lines}
	number, err := store.InsertInvoice(ctx, tx, inv)
	if err != nil {
		return err
	}
	for _, m := range members {
		sctx, sErr := store.AsStore(ctx, tx, m.id)
		if sErr != nil {
			return sErr
		}
		if sErr = store.MarkBilled(sctx, tx, inv.ID, period, acct.Currency, m.renewal); sErr != nil {
			return sErr
		}
	}
	if len(members) == 1 {
		// Paid at once from a prepaid balance (ADR-0084).
		if _, err = s.payFromBalanceTx(ctx, tx, acct, inv, number, members[0]); err != nil {
			return err
		}
	}
	return nil
}

// linesOf are one store's lines on its account's invoice: its renewal
// (an agency's plan only once: agencyBilled), its due fees and credits.
func linesOf(ctx context.Context, tx pgx.Tx, acct domain.Account, period time.Time, m *member, agencyBilled *bool) ([]domain.Line, error) {
	sctx, err := store.AsStore(ctx, tx, m.id)
	if err != nil {
		return nil, err
	}
	var lines []domain.Line
	r, ok, err := store.RenewalDue(sctx, tx, acct.Currency, domain.RenewsBy(period))
	if err != nil {
		return nil, err
	}
	if ok {
		m.renewal = &r.PlanID
		if r.Kind != domain.KindAgency || !*agencyBilled {
			*agencyBilled = *agencyBilled || r.Kind == domain.KindAgency
			renewal, rErr := foundingRenewal(sctx, tx, acct.Currency, m, r)
			if rErr != nil {
				return nil, rErr
			}
			lines = append(lines, renewal...)
		}
	}
	sums, err := store.SumFees(sctx, tx, period, acct.Currency)
	if err != nil {
		return nil, err
	}
	return append(lines, feeLines(sums, m.id, m.name, period)...), nil
}

// feeLines are a store's due fees up to period and its credits, as
// invoice lines.
func feeLines(sums store.FeeSums, id ulid.ID, name string, period time.Time) []domain.Line {
	var lines []domain.Line
	if sums.Due > 0 {
		lines = append(lines, domain.Line{Kind: domain.LineSellFees, StoreID: id, Quantity: sums.Due, AmountMinor: sums.DueMinor,
			Description: fmt.Sprintf("%s: رسوم المبيعات حتى %s (%d طلب مُسلَّم)", name, period.Format("2006-01"), sums.Due)})
	}
	if sums.Credits > 0 {
		lines = append(lines, domain.Line{Kind: domain.LineFeeCredit, StoreID: id, Quantity: sums.Credits, AmountMinor: -sums.CreditsMinor,
			Description: fmt.Sprintf("%s: رصيد رسوم طلبات مرتجعة (%d طلب)", name, sums.Credits)})
	}
	return lines
}

func cycleAr(c domain.Cycle) string {
	if c == domain.Annual {
		return "سنوي"
	}
	return "شهري"
}

// InvoiceMonthWorker runs InvoiceMonthArgs.
type InvoiceMonthWorker struct {
	river.WorkerDefaults[InvoiceMonthArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *InvoiceMonthWorker) Work(ctx context.Context, _ *river.Job[InvoiceMonthArgs]) error {
	return w.Svc.StartMonth(ctx)
}

// InvoiceStoreWorker runs InvoiceStoreArgs.
type InvoiceStoreWorker struct {
	river.WorkerDefaults[InvoiceStoreArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *InvoiceStoreWorker) Work(ctx context.Context, j *river.Job[InvoiceStoreArgs]) error {
	return w.Svc.InvoiceStore(ctx, j.Args.Period)
}

// InvoiceAccountWorker runs InvoiceAccountArgs.
type InvoiceAccountWorker struct {
	river.WorkerDefaults[InvoiceAccountArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *InvoiceAccountWorker) Work(ctx context.Context, j *river.Job[InvoiceAccountArgs]) error {
	return w.Svc.InvoiceAccount(ctx, j.Args.AccountID, j.Args.Period)
}

// AddWorkers registers the invoice, period and dunning jobs.
func (s *Service) AddWorkers(w *river.Workers) error {
	for _, add := range []func() error{
		func() error { return river.AddWorkerSafely(w, &InvoiceMonthWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &InvoiceStoreWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &InvoiceAccountWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &EndPeriodsWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &EndPeriodWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &DunningWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &ChargeInvoiceWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &SuspendWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &HoldWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &CancelWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &NotifyWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &ProcessCallbackWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &FoundingSweepWorker{Svc: s}) },
	} {
		if err := add(); err != nil {
			return fmt.Errorf("billing: workers: %w", err)
		}
	}
	return nil
}

// InvoicesPeriodic starts the month's invoices daily, and at worker
// start; a day that finds them written does nothing.
func InvoicesPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return InvoiceMonthArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}
