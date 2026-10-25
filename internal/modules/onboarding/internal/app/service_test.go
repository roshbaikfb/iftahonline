package app_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// templates is a fake starter-template applier: it records what it was
// asked to apply and fails while fail is set.
type templates struct {
	mu      sync.Mutex
	applied []domain.Choice
	fail    error
}

func (f *templates) Apply(_ context.Context, c domain.Choice) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return 0, f.fail
	}
	f.applied = append(f.applied, c)
	return 3, nil
}

type recorder struct {
	mu      sync.Mutex
	actions []string
}

func (r *recorder) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, e.Action)
	return nil
}

type fixture struct {
	svc  *app.Service
	pool *pgxpool.Pool
	jobs *testkit.Jobs
	tpl  *templates
	rec  *recorder
	ids  *ulid.Generator
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := fixture{pool: pool, jobs: testkit.NewJobs(), tpl: &templates{}, rec: &recorder{}, ids: ulid.NewGenerator()}
	f.svc = app.NewService(app.Deps{DB: pool, Jobs: f.jobs, Templates: f.tpl, Audit: f.rec})
	return f
}

// store makes a store; started opens its wizard as sign-up does.
func (f fixture) store(t *testing.T, started bool) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "onb-"+strings.ToLower(id.String()[14:])); err != nil {
			return err
		}
		if !started {
			return nil
		}
		if err := f.svc.StartTx(ctx, tx); err != nil {
			return err
		}
		return f.svc.StartTx(ctx, tx) // twice: a no-op
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

// lastSeq is the choice number the last enqueued apply job carries.
func (f fixture) lastSeq(t *testing.T) int {
	t.Helper()
	if len(f.jobs.Args) == 0 {
		t.Fatal("no job enqueued")
	}
	a, ok := f.jobs.Args[len(f.jobs.Args)-1].(app.ApplyTemplateArgs)
	if !ok {
		t.Fatalf("job %T", f.jobs.Args[len(f.jobs.Args)-1])
	}
	return a.Seq
}

var perfume = domain.Choice{Activity: domain.Perfume, Look: domain.Soft, Color: "#92400E"}

func TestWizardFlow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t, true)

	st, err := f.svc.Get(ctx)
	if err != nil || st.Status() != domain.InProgress || st.Template != domain.TemplateNone || st.Choice != nil {
		t.Fatalf("new wizard = %+v, %v", st, err)
	}

	// A first choice, then a second before the first is applied.
	if st, err = f.svc.Choose(ctx, perfume); err != nil || st.Template != domain.TemplatePreparing || st.Choice.Color != "#92400e" {
		t.Fatalf("Choose = %+v, %v", st, err)
	}
	first := f.lastSeq(t)
	kids := domain.Choice{Activity: domain.Kids, Look: domain.Classic, Color: "#0e7490"}
	if _, err = f.svc.Choose(ctx, kids); err != nil {
		t.Fatal(err)
	}
	second := f.lastSeq(t)
	if second != first+1 || f.jobs.Kinds()[0] != "onboarding.apply_template" {
		t.Fatalf("jobs %v, seqs %d %d", f.jobs.Kinds(), first, second)
	}

	// The first job finds its choice replaced and does nothing.
	if err = f.svc.ApplyTemplate(ctx, first, false); err != nil {
		t.Fatal(err)
	}
	if len(f.tpl.applied) != 0 {
		t.Fatalf("a replaced choice was applied: %+v", f.tpl.applied)
	}
	// The second applies the last choice.
	if err = f.svc.ApplyTemplate(ctx, second, false); err != nil {
		t.Fatal(err)
	}
	st, err = f.svc.Get(ctx)
	if err != nil || st.Template != domain.TemplateReady || st.TemplateVersion != 3 || *st.Choice != kids || len(f.tpl.applied) != 1 || f.tpl.applied[0] != kids {
		t.Fatalf("after apply = %+v, %v, applied %+v", st, err, f.tpl.applied)
	}
	// Running it again (River's at-least-once) changes nothing.
	if err = f.svc.ApplyTemplate(ctx, second, false); err != nil || len(f.tpl.applied) != 1 {
		t.Fatalf("rerun: %v, applied %+v", err, f.tpl.applied)
	}

	// Closing is idempotent and refuses later choices.
	if st, err = f.svc.Complete(ctx); err != nil || st.Status() != domain.Completed {
		t.Fatalf("Complete = %+v, %v", st, err)
	}
	if st, err = f.svc.Complete(ctx); err != nil || st.Status() != domain.Completed {
		t.Fatalf("Complete again = %+v, %v", st, err)
	}
	if _, err = f.svc.Choose(ctx, perfume); !errors.Is(err, domain.ErrClosed) {
		t.Fatalf("Choose after closing = %v; want ErrClosed", err)
	}
	if got := strings.Join(f.rec.actions, " "); got != "onboarding.template_chosen onboarding.template_chosen onboarding.completed" {
		t.Fatalf("audit = %s", got)
	}
}

func TestApplyFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t, true)
	if _, err := f.svc.Choose(ctx, perfume); err != nil {
		t.Fatal(err)
	}
	seq := f.lastSeq(t)
	f.tpl.fail = errors.New("storefront unavailable")

	// Not the last attempt: the job fails, so River retries it.
	if err := f.svc.ApplyTemplate(ctx, seq, false); err == nil {
		t.Fatal("ApplyTemplate succeeded while the templates fail")
	}
	if st, _ := f.svc.Get(ctx); st.Template != domain.TemplatePreparing {
		t.Fatalf("after a failed attempt = %+v", st)
	}
	// The last attempt records the failure for the wizard to show.
	if err := f.svc.ApplyTemplate(ctx, seq, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.svc.Get(ctx); st.Template != domain.TemplateFailed {
		t.Fatalf("after the last attempt = %+v", st)
	}
	// Choosing again tries again.
	f.tpl.fail = nil
	if _, err := f.svc.Choose(ctx, perfume); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ApplyTemplate(ctx, f.lastSeq(t), false); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.svc.Get(ctx); st.Template != domain.TemplateReady {
		t.Fatalf("after retrying = %+v", st)
	}
}

func TestStoreWithoutWizard(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t, false)
	st, err := f.svc.Get(ctx)
	if err != nil || st.Status() != domain.Completed || st.Template != domain.TemplateNone {
		t.Fatalf("Get = %+v, %v", st, err)
	}
	if _, err = f.svc.Choose(ctx, perfume); !errors.Is(err, domain.ErrClosed) {
		t.Fatalf("Choose = %v; want ErrClosed", err)
	}
	if st, err = f.svc.Complete(ctx); err != nil || st.Status() != domain.Completed {
		t.Fatalf("Complete = %+v, %v", st, err)
	}
	if len(f.jobs.Args) != 0 || len(f.rec.actions) != 0 {
		t.Fatalf("jobs %v, audit %v", f.jobs.Kinds(), f.rec.actions)
	}
}

func TestChooseValidates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t, true)
	_, err := f.svc.Choose(ctx, domain.Choice{Activity: "cars", Look: domain.Soft, Color: "#000000"})
	var ve *errs.Error
	if !errors.As(err, &ve) || ve.Kind != errs.KindValidation || len(f.jobs.Args) != 0 {
		t.Fatalf("Choose = %v, jobs %v", err, f.jobs.Kinds())
	}
}

func TestNoTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, err := f.svc.Get(t.Context()); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("Get without a store = %v", err)
	}
}
