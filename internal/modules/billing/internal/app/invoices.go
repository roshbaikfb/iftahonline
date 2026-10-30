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
	Currency string
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

func unique() *river.InsertOpts { return &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}} }

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
			if _, err = s.jobs.InsertTx(tenant.WithStore(ctx, sid), tx, InvoiceStoreArgs{TenantArgs: jobs.TenantArgs{StoreID: sid}, Period: period}, unique()); err != nil {
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
		account, ok, err := store.SubscriptionAccount(ctx, tx)
		if errors.Is(err, store.ErrNoSubscription) {
			return nil // nothing to bill: fees and renewals need a plan
		}
		if err != nil {
			return err
		}
		if !ok {
			account = s.ids.New()
			if err = store.OpenStoreAccount(ctx, tx, domain.Account{ID: account, Kind: domain.AccountStore, Name: info.Name,
				Currency: info.Currency, Country: domain.CountryOf(info.Currency)}); err != nil {
				return err
			}
		}
		if _, err = s.jobs.InsertTx(ctx, tx, InvoiceAccountArgs{AccountID: account, Period: period}, unique()); err != nil {
			return fmt.Errorf("billing: enqueue account invoice: %w", err)
		}
		return nil
	})
}

// member is one of an account's stores on an invoice.
type member struct {
	id      ulid.ID
	name    string
	renewed bool // its renewal is on the invoice (or the agency's one)
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
	if _, err = store.InsertInvoice(ctx, tx, inv); err != nil {
		return err
	}
	for _, m := range members {
		sctx, sErr := store.AsStore(ctx, tx, m.id)
		if sErr != nil {
			return sErr
		}
		if sErr = store.MarkBilled(sctx, tx, inv.ID, period, acct.Currency, m.renewed); sErr != nil {
			return sErr
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
		m.renewed = true
		if r.Kind != domain.KindAgency || !*agencyBilled {
			*agencyBilled = *agencyBilled || r.Kind == domain.KindAgency
			lines = append(lines, domain.Line{Kind: domain.LineSubscription, StoreID: m.id, Quantity: 1, AmountMinor: r.AmountMinor,
				Description: fmt.Sprintf("%s: باقة %s (%s)", m.name, r.PlanName, cycleAr(r.Cycle))})
		}
	}
	sums, err := store.SumFees(sctx, tx, period, acct.Currency)
	if err != nil {
		return nil, err
	}
	if sums.Due > 0 {
		lines = append(lines, domain.Line{Kind: domain.LineSellFees, StoreID: m.id, Quantity: sums.Due, AmountMinor: sums.DueMinor,
			Description: fmt.Sprintf("%s: رسوم المبيعات حتى %s (%d طلب مُسلَّم)", m.name, period.Format("2006-01"), sums.Due)})
	}
	if sums.Credits > 0 {
		lines = append(lines, domain.Line{Kind: domain.LineFeeCredit, StoreID: m.id, Quantity: sums.Credits, AmountMinor: -sums.CreditsMinor,
			Description: fmt.Sprintf("%s: رصيد رسوم طلبات مرتجعة (%d طلب)", m.name, sums.Credits)})
	}
	return lines, nil
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

// AddWorkers registers the invoice jobs.
func (s *Service) AddWorkers(w *river.Workers) error {
	for _, add := range []func() error{
		func() error { return river.AddWorkerSafely(w, &InvoiceMonthWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &InvoiceStoreWorker{Svc: s}) },
		func() error { return river.AddWorkerSafely(w, &InvoiceAccountWorker{Svc: s}) },
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
