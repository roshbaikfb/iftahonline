package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// log records events as "type kind slug previous deleted" and audit
// actions, after checking each event's envelope.
type log struct {
	mu      sync.Mutex
	events  []string
	actions []string
}

func (l *log) PublishTx(ctx context.Context, tx pgx.Tx, ev event.Event) error {
	m := ev.EventMeta()
	store, err := tenant.FromContext(ctx)
	if err != nil || tx == nil || m.StoreID != store || m.ID.IsZero() {
		return fmt.Errorf("bad publish: %w %+v", err, m)
	}
	line := m.Type
	if pc, ok := ev.(domain.PageChanged); ok {
		line = fmt.Sprintf("%s %s %s %s %v", m.Type, pc.Kind, pc.Slug, pc.PreviousSlug, pc.Deleted)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, line)
	return nil
}

func (l *log) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.actions = append(l.actions, e.Action)
	return nil
}

func (l *log) take() (events, actions []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	events, actions, l.events, l.actions = l.events, l.actions, nil, nil
	return events, actions
}

type fixture struct {
	pool *pgxpool.Pool
	svc  *app.Service
	log  *log
	rdb  *redis.Client // set by the cart fixture
	ctxA context.Context
	ctxB context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	reg, err := sections.New()
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	l := &log{}
	f := fixture{pool: pool, svc: app.NewService(app.Deps{DB: pool, IDs: ids, Audit: l, Events: l, Sections: reg}), log: l}
	f.ctxA = createStore(t, pool, ids)
	f.ctxB = createStore(t, pool, ids)
	return f
}

func createStore(t *testing.T, pool *pgxpool.Pool, ids *ulid.Generator) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "sf-"+strings.ToLower(id.String()[14:]))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func wantFields(t *testing.T, err error, want ...string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Fatalf("want validation error %v, got %v", want, err)
	}
	got := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		got[i] = f.Field + ":" + f.Code
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("fields %q, want %q", got, want)
	}
}

func wantLog(t *testing.T, f fixture, events, actions []string) {
	t.Helper()
	gotE, gotA := f.log.take()
	if !slices.Equal(gotE, events) || !slices.Equal(gotA, actions) {
		t.Fatalf("events %q actions %q; want %q %q", gotE, gotA, events, actions)
	}
}

func TestTheme(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	th, err := f.svc.GetTheme(ctx)
	if err != nil || th.Version != 0 || th.Settings != domain.DefaultTheme() || th.UpdatedAt != nil {
		t.Fatalf("default theme %+v, %v", th, err)
	}
	dark := domain.ThemeSettings{Colors: domain.Colors{Primary: "#22C55E", Accent: "#EAB308", Background: "#0B1120", Text: "#F8FAFC"},
		Font: domain.FontTajawal, Radius: domain.RadiusLarge}
	saved, err := f.svc.UpdateTheme(ctx, dark, 0)
	if err != nil || saved.Version != 1 || saved.Settings.Colors.Primary != "#22c55e" || saved.UpdatedAt == nil {
		t.Fatalf("first save %+v, %v", saved, err)
	}
	wantLog(t, f, []string{domain.EventThemeChanged}, []string{"theme.updated"})

	if _, err = f.svc.UpdateTheme(ctx, dark, 0); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("second create: %v", err)
	}
	dark.Radius = domain.RadiusNone
	if saved, err = f.svc.UpdateTheme(ctx, dark, 1); err != nil || saved.Version != 2 || saved.Settings.Radius != domain.RadiusNone {
		t.Fatalf("update %+v, %v", saved, err)
	}
	if _, err = f.svc.UpdateTheme(ctx, dark, 1); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if got, _ := f.svc.GetTheme(ctx); got.Version != 2 || got.Settings != saved.Settings {
		t.Fatalf("read back %+v", got)
	}
	wantLog(t, f, []string{domain.EventThemeChanged}, []string{"theme.updated"}) // failed saves publish nothing

	dark.Colors.Text = dark.Colors.Background
	_, err = f.svc.UpdateTheme(ctx, dark, 2)
	wantFields(t, err, "colors.text:low_contrast")
	_, err = f.svc.UpdateTheme(ctx, domain.DefaultTheme(), -1)
	wantFields(t, err, "version:range")

	if b, _ := f.svc.GetTheme(f.ctxB); b.Version != 0 || b.Settings != domain.DefaultTheme() {
		t.Fatalf("store B sees A's theme: %+v", b)
	}
}

func section(id string, typ domain.SectionType, settings string) domain.Section {
	return domain.Section{ID: id, Type: typ, Settings: json.RawMessage(settings)}
}

func TestHome(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	home, err := f.svc.GetHome(ctx)
	if err != nil || home.Version != 0 || len(home.Sections) != len(domain.DefaultHome().Sections) {
		t.Fatalf("default home %+v, %v", home, err)
	}
	in := domain.PageInput{Title: "متجر منى", Sections: []domain.Section{
		section("", domain.SectionHero, `{"heading":"أهلًا","button_label":"تسوق","button_link":"/c/new"}`),
		section("faq", domain.SectionFAQ, `{"items":[{"question":"الدفع؟","answer":"عند الاستلام"}]}`),
	}}
	saved, err := f.svc.SaveHome(ctx, in)
	if err != nil || saved.Version != 1 || saved.Slug != "home" || saved.Status != domain.StatusPublished || len(saved.Sections) != 2 {
		t.Fatalf("save home %+v, %v", saved, err)
	}
	if id := saved.Sections[0].ID; len(id) != 26 || id != strings.ToLower(id) {
		t.Fatalf("generated section ID %q", id)
	}
	wantLog(t, f, []string{"storefront.page.changed home home  false"}, []string{"page.created"})

	got, err := f.svc.GetHome(ctx)
	if err != nil || got.ID != saved.ID || got.Title != "متجر منى" || string(got.Sections[1].Settings) != string(saved.Sections[1].Settings) {
		t.Fatalf("read back %+v, %v", got, err)
	}

	if _, err = f.svc.SaveHome(ctx, in); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("second create: %v", err)
	}
	in.Version = 1
	in.Sections = in.Sections[1:]
	if saved, err = f.svc.SaveHome(ctx, in); err != nil || saved.Version != 2 || len(saved.Sections) != 1 {
		t.Fatalf("update %+v, %v", saved, err)
	}
	wantLog(t, f, []string{"storefront.page.changed home home  false"}, []string{"page.updated"})
	if _, err = f.svc.SaveHome(ctx, in); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale update: %v", err)
	}

	// The real schema registry names the bad setting.
	in.Version = 2
	in.Sections = []domain.Section{section("hero", domain.SectionHero, `{"button_label":"x","button_link":"javascript:alert(1)"}`)}
	_, err = f.svc.SaveHome(ctx, in)
	wantFields(t, err, "sections[0].settings.button_link:pattern")

	if b, _ := f.svc.GetHome(f.ctxB); b.Version != 0 {
		t.Fatalf("store B sees A's home: %+v", b)
	}
}

func TestPolicies(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	list, err := f.svc.ListPolicies(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("policies %+v, %v", list, err)
	}
	for i, p := range list {
		if p.Slug != domain.PolicySlugs()[i] || p.Version != 0 || p.Status != domain.StatusDraft {
			t.Fatalf("default policy %+v", p)
		}
	}
	refund, err := f.svc.SavePolicy(ctx, domain.PolicyRefund, domain.PageInput{Title: "الاسترجاع", Body: "خلال ١٤ يومًا.", Status: "published"})
	if err != nil || refund.Version != 1 || refund.Kind != domain.KindPolicy || refund.Slug != "refund" {
		t.Fatalf("save %+v, %v", refund, err)
	}
	wantLog(t, f, []string{"storefront.page.changed policy refund  false"}, []string{"page.created"})

	list, _ = f.svc.ListPolicies(ctx)
	if list[1].ID != refund.ID || list[1].Version != 1 || list[0].Version != 0 {
		t.Fatalf("list after save %+v", list)
	}
	if got, getErr := f.svc.GetPolicy(ctx, domain.PolicyRefund); getErr != nil || got.Body != "خلال ١٤ يومًا." {
		t.Fatalf("get %+v, %v", got, getErr)
	}
	if got, getErr := f.svc.GetPolicy(ctx, domain.PolicyTerms); getErr != nil || got.Version != 0 {
		t.Fatalf("default terms %+v, %v", got, getErr)
	}
	for _, bad := range []string{"cookies", "home", ""} {
		if _, err = f.svc.GetPolicy(ctx, bad); !errors.Is(err, app.ErrPageNotFound) {
			t.Fatalf("get %q: %v", bad, err)
		}
		if _, err = f.svc.SavePolicy(ctx, bad, domain.PageInput{}); !errors.Is(err, app.ErrPageNotFound) {
			t.Fatalf("save %q: %v", bad, err)
		}
	}
	_, err = f.svc.SavePolicy(ctx, domain.PolicyTerms, domain.PageInput{Sections: []domain.Section{section("a", domain.SectionFAQ, `{}`)}})
	wantFields(t, err, "sections:not_allowed")
	if b, _ := f.svc.GetPolicy(f.ctxB, domain.PolicyRefund); b.Version != 0 {
		t.Fatalf("store B sees A's policy: %+v", b)
	}
}

func TestLandingPages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	pid := ulid.NewGenerator().New().String()

	p, err := f.svc.CreateLandingPage(ctx, domain.PageInput{Title: "عرض الصيف", Status: "published", Sections: []domain.Section{
		section("main", domain.SectionProductLanding, `{"product_id":"`+pid+`","highlights":["شحن مجاني"]}`),
	}})
	if err != nil || p.Slug != "عرض-الصيف" || p.Version != 1 || p.Kind != domain.KindLanding || p.Status != domain.StatusPublished {
		t.Fatalf("create %+v, %v", p, err)
	}
	wantLog(t, f, []string{"storefront.page.changed landing عرض-الصيف  false"}, []string{"page.created"})

	_, err = f.svc.CreateLandingPage(ctx, domain.PageInput{Title: "Summer", Slug: "عرض-الصيف"})
	wantFields(t, err, "slug:taken")
	list, err := f.svc.ListLandingPages(ctx)
	if err != nil || len(list) != 1 || list[0].ID != p.ID || len(list[0].Sections) != 0 {
		t.Fatalf("list %+v, %v", list, err)
	}
	got, err := f.svc.GetLandingPage(ctx, p.ID)
	if err != nil || len(got.Sections) != 1 || got.Sections[0].ID != "main" {
		t.Fatalf("get %+v, %v", got, err)
	}

	upd := domain.PageInput{Title: "عرض الصيف", Slug: "summer-sale", Status: "draft", Version: 1, Sections: got.Sections}
	moved, err := f.svc.UpdateLandingPage(ctx, p.ID, upd)
	if err != nil || moved.Slug != "summer-sale" || moved.Version != 2 || moved.Status != domain.StatusDraft {
		t.Fatalf("update %+v, %v", moved, err)
	}
	wantLog(t, f, []string{"storefront.page.changed landing summer-sale عرض-الصيف false"}, []string{"page.updated"})
	if _, err = f.svc.UpdateLandingPage(ctx, p.ID, upd); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale update: %v", err)
	}

	// The home page is not a landing page, whatever its ID.
	home, err := f.svc.SaveHome(ctx, domain.PageInput{})
	if err != nil {
		t.Fatal(err)
	}
	f.log.take()
	if _, err = f.svc.GetLandingPage(ctx, home.ID); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("home as landing: %v", err)
	}
	if _, err = f.svc.UpdateLandingPage(ctx, home.ID, domain.PageInput{Title: "x", Version: 1}); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("update home as landing: %v", err)
	}
	if err = f.svc.DeleteLandingPage(ctx, home.ID); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("delete home as landing: %v", err)
	}

	// Another store cannot see, change or delete it.
	if _, err = f.svc.GetLandingPage(f.ctxB, p.ID); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("B get: %v", err)
	}
	if _, err = f.svc.UpdateLandingPage(f.ctxB, p.ID, domain.PageInput{Title: "hijack", Version: 2}); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("B update: %v", err)
	}
	if err = f.svc.DeleteLandingPage(f.ctxB, p.ID); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("B delete: %v", err)
	}
	if b, _ := f.svc.ListLandingPages(f.ctxB); len(b) != 0 {
		t.Fatalf("B lists A's pages: %+v", b)
	}
	wantLog(t, f, nil, nil)

	if err = f.svc.DeleteLandingPage(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	wantLog(t, f, []string{"storefront.page.changed landing summer-sale  true"}, []string{"page.deleted"})
	if _, err = f.svc.GetLandingPage(ctx, p.ID); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if err = f.svc.DeleteLandingPage(ctx, p.ID); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestLandingPageLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for i := range domain.MaxLandingPages {
		if _, err := f.svc.CreateLandingPage(f.ctxA, domain.PageInput{Title: fmt.Sprintf("Offer %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.svc.CreateLandingPage(f.ctxA, domain.PageInput{Title: "One too many"})
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindConflict || e.Code != "page_limit" {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err = f.svc.CreateLandingPage(f.ctxB, domain.PageInput{Title: "B's first"}); err != nil {
		t.Fatalf("the limit is per store: %v", err)
	}
}

func TestNoTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	checks := map[string]error{}
	_, checks["theme"] = f.svc.GetTheme(ctx)
	_, checks["home"] = f.svc.GetHome(ctx)
	_, checks["policies"] = f.svc.ListPolicies(ctx)
	_, checks["landing"] = f.svc.ListLandingPages(ctx)
	_, checks["create"] = f.svc.CreateLandingPage(ctx, domain.PageInput{Title: "x"})
	for name, err := range checks {
		if !errors.Is(err, tenant.ErrNoTenant) {
			t.Errorf("%s without a tenant: %v", name, err)
		}
	}
}
