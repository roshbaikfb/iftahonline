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

// Charge is a charge of an account's card on file.
type Charge struct {
	Token       string // the gateway's card token, opened
	AmountMinor int64
	Currency    string
	Reference   string // the invoice's number: the gateway's merchant reference
}

// ChargeResult is the gateway's answer.
type ChargeResult struct {
	Approved  bool
	Reference string // the gateway's transaction
	Reason    string // why it was declined, for the merchant
}

// Gateway charges cards on file (7d-2: Paymob; tests: a fake).
type Gateway interface {
	// Charge charges the card. An error means the outcome is not known:
	// nothing is recorded and the job retries. A decline is a result.
	Charge(ctx context.Context, c Charge) (ChargeResult, error)
}

// Sealer seals the gateway's card tokens (platform/envelope).
type Sealer interface {
	Seal(ctx context.Context, plaintext, aad []byte) ([]byte, error)
	Open(ctx context.Context, blob, aad []byte) ([]byte, error)
}

// Holds holds or releases the store in ctx for billing, in its tenant
// transaction tx: cmd/platform adapts tenancy (ADR-0051).
type Holds func(ctx context.Context, tx pgx.Tx, hold bool) error

func methodAAD(account ulid.ID) []byte { return []byte("billing-method:" + account.String()) }

// Notice is what a store's owners are told about its billing.
type Notice = domain.Notice

// Notifier tells the store-in-ctx's owners a notice (email): it runs in
// the billing.notify job; cmd/platform adapts the mailer.
type Notifier func(ctx context.Context, n Notice) error

// ChargeInvoiceArgs charges an account's open invoice.
type ChargeInvoiceArgs struct {
	jobs.GlobalArgs
	AccountID ulid.ID `json:"account_id"`
	InvoiceID ulid.ID `json:"invoice_id"`
}

// Kind implements river.JobArgs.
func (ChargeInvoiceArgs) Kind() string { return "billing.charge_invoice" }

// DunningArgs is the hourly dunning run.
type DunningArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (DunningArgs) Kind() string { return "billing.dunning" }

// SuspendArgs suspends a store whose grace ran out unpaid.
type SuspendArgs struct {
	jobs.TenantArgs
}

// Kind implements river.JobArgs.
func (SuspendArgs) Kind() string { return "billing.suspend" }

// CancelArgs cancels a store suspended for the whole retention.
type CancelArgs struct {
	jobs.TenantArgs
}

// Kind implements river.JobArgs.
func (CancelArgs) Kind() string { return "billing.cancel" }

// NotifyArgs tells a store's owners a notice.
type NotifyArgs struct {
	jobs.TenantArgs
	Notice Notice `json:"notice"`
}

// Kind implements river.JobArgs.
func (NotifyArgs) Kind() string { return "billing.notify" }

// SavePaymentMethod seals and saves an account's card on file (7d-2:
// from the gateway's token callback). The account comes from a system
// caller, never a request.
func (s *Service) SavePaymentMethod(ctx context.Context, account ulid.ID, gateway, token, label string) error {
	if s.sealer == nil {
		return errors.New("billing: no sealer")
	}
	sealed, err := s.sealer.Seal(ctx, []byte(token), methodAAD(account))
	if err != nil {
		return fmt.Errorf("billing: seal card token: %w", err)
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		return store.SavePaymentMethod(ctx, tx, account, store.Method{Gateway: gateway, Token: sealed, Label: label})
	})
}

// Dunning is the hourly run: open invoices whose attempt is due are
// charged, stores whose grace ran out are suspended, stores suspended
// for the retention are cancelled (ADR-0051).
func (s *Service) Dunning(ctx context.Context) error {
	if s.jobs == nil {
		return errors.New("billing: no job inserter")
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		tasks, err := store.DunningDue(ctx, tx, s.now())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			sctx := tenant.WithStore(ctx, tenant.StoreID{ID: t.ID})
			sid := jobs.TenantArgs{StoreID: tenant.StoreID{ID: t.ID}}
			switch t.Kind {
			case "charge":
				_, err = s.jobs.InsertTx(ctx, tx, ChargeInvoiceArgs{AccountID: t.Account, InvoiceID: t.ID}, unique())
			case "suspend":
				_, err = s.jobs.InsertTx(sctx, tx, SuspendArgs{TenantArgs: sid}, unique())
			case "cancel":
				_, err = s.jobs.InsertTx(sctx, tx, CancelArgs{TenantArgs: sid}, unique())
			}
			if err != nil {
				return fmt.Errorf("billing: enqueue %s: %w", t.Kind, err)
			}
		}
		return nil
	})
}

// ChargeInvoice charges an open invoice on its account's card on file,
// outside any transaction, then records the outcome: paid settles the
// account's stores, declined (or no card) counts a failure.
func (s *Service) ChargeInvoice(ctx context.Context, account, invoice ulid.ID) error {
	var (
		inv    store.OpenInvoice
		method store.Method
		has    bool
		open   bool
	)
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		if err = store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		if inv, open, err = store.LockOpenInvoice(ctx, tx, invoice); err != nil || !open {
			return err
		}
		method, has, err = store.PaymentMethod(ctx, tx, account)
		return err
	})
	if err != nil || !open {
		return err
	}
	a := store.Attempt{ID: s.ids.New(), Invoice: invoice, Account: account, Gateway: method.Gateway, AmountMinor: inv.TotalMinor, Outcome: "no_method"}
	if has {
		res, cErr := s.charge(ctx, account, method, inv)
		if cErr != nil {
			return cErr // the outcome is not known: retry the job
		}
		a.Outcome, a.Reference, a.Reason = "declined", res.Reference, res.Reason
		if res.Approved {
			a.Outcome = "approved"
		}
	} else {
		a.Gateway, a.Reason = "none", "no card on file"
	}
	return s.recordAttempt(ctx, account, a)
}

func (s *Service) charge(ctx context.Context, account ulid.ID, m store.Method, inv store.OpenInvoice) (ChargeResult, error) {
	gw, ok := s.gateways[m.Gateway]
	if !ok || s.sealer == nil {
		return ChargeResult{}, fmt.Errorf("billing: no gateway %q", m.Gateway)
	}
	token, err := s.sealer.Open(ctx, m.Token, methodAAD(account))
	if err != nil {
		return ChargeResult{}, fmt.Errorf("billing: open card token: %w", err)
	}
	return gw.Charge(ctx, Charge{Token: string(token), AmountMinor: inv.TotalMinor, Currency: inv.Currency, Reference: inv.Number})
}

// SettleInvoice records an invoice paid outside the card on file (a
// transfer the platform confirmed: the console, 7e), with reference.
func (s *Service) SettleInvoice(ctx context.Context, account, invoice ulid.ID, reference string) error {
	return s.recordAttempt(ctx, account, store.Attempt{ID: s.ids.New(), Invoice: invoice, Account: account, Gateway: "manual",
		Outcome: "manual", Reference: reference})
}

// recordAttempt writes an attempt and its consequences in one
// transaction, with the invoice locked: a payment settles, anything else
// counts a failure. An invoice no longer open records nothing.
func (s *Service) recordAttempt(ctx context.Context, account ulid.ID, a store.Attempt) error {
	var members []ulid.ID
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		inv, open, err := store.LockOpenInvoice(ctx, tx, a.Invoice)
		if err != nil || !open {
			return err
		}
		if a.AmountMinor == 0 {
			a.AmountMinor = inv.TotalMinor
		}
		if err = store.InsertAttempt(ctx, tx, a); err != nil {
			return err
		}
		if members, err = store.AccountStores(ctx, tx, account); err != nil {
			return err
		}
		if a.Outcome == "approved" || a.Outcome == "manual" {
			return s.settleTx(ctx, tx, inv, members)
		}
		return s.failTx(ctx, tx, inv, members, a.Reason)
	})
}

// settleTx closes the invoice and settles each store: its next period
// when the invoice renewed it, back to active, released (ADR-0051).
func (s *Service) settleTx(ctx context.Context, tx pgx.Tx, inv store.OpenInvoice, members []ulid.ID) error {
	if _, err := store.MarkInvoicePaid(ctx, tx, inv.ID, s.now().UTC()); err != nil {
		return err
	}
	for _, m := range members {
		sctx, err := store.AsStore(ctx, tx, m)
		if err != nil {
			return err
		}
		sub, ok, err := store.Subscription(sctx, tx)
		if err != nil || !ok {
			return err
		}
		billed, err := store.RenewalInvoice(sctx, tx)
		if err != nil {
			return err
		}
		renewed := billed != nil && *billed == inv.ID
		if renewed {
			if err = store.ClearRenewal(sctx, tx); err != nil {
				return err
			}
		}
		next, changed, err := sub.Settle(renewed)
		if err != nil {
			return err
		}
		if changed {
			if err = s.saveTx(sctx, tx, next); err != nil {
				return err
			}
		}
		if err = s.hold(sctx, tx, false); err != nil {
			return err
		}
		if err = s.notifyTx(sctx, tx, Notice{Kind: domain.NoticePaid, Invoice: inv.Number, TotalMinor: inv.TotalMinor, Currency: inv.Currency}); err != nil {
			return err
		}
	}
	return nil
}

// failTx counts a failed attempt: the next retry, and the stores past
// due with their grace counted from the first failure.
func (s *Service) failTx(ctx context.Context, tx pgx.Tx, inv store.OpenInvoice, members []ulid.ID, reason string) error {
	now := s.now().UTC()
	first := inv.FirstFailed
	if first.IsZero() {
		first = now
	}
	next, _ := domain.NextAttempt(first, inv.Attempts+1)
	if err := store.MarkInvoiceFailed(ctx, tx, inv.ID, now, next, reason); err != nil {
		return err
	}
	for _, m := range members {
		sctx, err := store.AsStore(ctx, tx, m)
		if err != nil {
			return err
		}
		sub, ok, err := store.Subscription(sctx, tx)
		if err != nil || !ok {
			return err
		}
		n := Notice{Kind: domain.NoticePaymentFailed, Invoice: inv.Number, TotalMinor: inv.TotalMinor, Currency: inv.Currency,
			RetryAt: next, Reason: reason}
		if sub.Status == domain.Active && sub.Kind.Billed() {
			pd, pErr := sub.PaymentFailed(first, s.policy)
			if pErr != nil {
				return pErr
			}
			if err = s.saveTx(sctx, tx, pd); err != nil {
				return err
			}
			n.GraceUntil = pd.GraceUntil
		} else {
			n.GraceUntil = sub.GraceUntil
		}
		if err = s.notifyTx(sctx, tx, n); err != nil {
			return err
		}
	}
	return nil
}

// Suspend closes the store in ctx when its grace ran out unpaid: its
// storefront shows a maintenance page, its data is kept.
func (s *Service) Suspend(ctx context.Context) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		sub, ok, err := store.Subscription(ctx, tx)
		if err != nil || !ok {
			return err
		}
		next, err := sub.Suspend(s.now())
		if errors.Is(err, domain.ErrTransition) {
			return nil // paid meanwhile
		}
		if err != nil {
			return err
		}
		if err = s.saveTx(ctx, tx, next); err != nil {
			return err
		}
		if err = s.hold(ctx, tx, true); err != nil {
			return err
		}
		return s.notifyTx(ctx, tx, Notice{Kind: domain.NoticeSuspended})
	})
}

// Cancel marks the store in ctx cancelled when it was suspended for the
// whole retention: its data may be deleted (not by P7).
func (s *Service) Cancel(ctx context.Context) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		sub, ok, err := store.Subscription(ctx, tx)
		if err != nil || !ok {
			return err
		}
		next, err := sub.Close(s.now(), s.policy)
		if errors.Is(err, domain.ErrTransition) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.saveTx(ctx, tx, next)
	})
}

func (s *Service) hold(ctx context.Context, tx pgx.Tx, on bool) error {
	if s.holds == nil {
		return errors.New("billing: no store holds")
	}
	return s.holds(ctx, tx, on)
}

// notifyTx enqueues a notice to the store-in-ctx's owners with the state
// change it is about.
func (s *Service) notifyTx(ctx context.Context, tx pgx.Tx, n Notice) error {
	if s.jobs == nil {
		return errors.New("billing: no job inserter")
	}
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	if _, err = s.jobs.InsertTx(ctx, tx, NotifyArgs{TenantArgs: t, Notice: n}, nil); err != nil {
		return fmt.Errorf("billing: enqueue notice: %w", err)
	}
	return nil
}

// Notify sends a notice (the billing.notify job).
func (s *Service) Notify(ctx context.Context, n Notice) error {
	if s.notifier == nil {
		return errors.New("billing: no notifier")
	}
	return s.notifier(ctx, n)
}

// DunningWorker runs DunningArgs.
type DunningWorker struct {
	river.WorkerDefaults[DunningArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *DunningWorker) Work(ctx context.Context, _ *river.Job[DunningArgs]) error {
	return w.Svc.Dunning(ctx)
}

// ChargeInvoiceWorker runs ChargeInvoiceArgs.
type ChargeInvoiceWorker struct {
	river.WorkerDefaults[ChargeInvoiceArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *ChargeInvoiceWorker) Work(ctx context.Context, j *river.Job[ChargeInvoiceArgs]) error {
	return w.Svc.ChargeInvoice(ctx, j.Args.AccountID, j.Args.InvoiceID)
}

// SuspendWorker runs SuspendArgs.
type SuspendWorker struct {
	river.WorkerDefaults[SuspendArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *SuspendWorker) Work(ctx context.Context, _ *river.Job[SuspendArgs]) error {
	return w.Svc.Suspend(ctx)
}

// CancelWorker runs CancelArgs.
type CancelWorker struct {
	river.WorkerDefaults[CancelArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *CancelWorker) Work(ctx context.Context, _ *river.Job[CancelArgs]) error {
	return w.Svc.Cancel(ctx)
}

// NotifyWorker runs NotifyArgs.
type NotifyWorker struct {
	river.WorkerDefaults[NotifyArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *NotifyWorker) Work(ctx context.Context, j *river.Job[NotifyArgs]) error {
	return w.Svc.Notify(ctx, j.Args.Notice)
}

// DunningPeriodic runs the dunning hourly, and at worker start.
func DunningPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return DunningArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}
