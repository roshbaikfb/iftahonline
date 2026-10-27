package app_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// TestThemeDraft (ADR-0036): a draft changes nothing shoppers see until
// it is published over the version the merchant read; discarding it goes
// back to the published theme.
func TestThemeDraft(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	d, err := f.svc.GetDraft(ctx)
	if err != nil || d.Theme != nil || d.Home != nil || d.UpdatedAt != nil {
		t.Fatalf("no draft yet: %+v, %v", d, err)
	}
	_, err = f.svc.PublishTheme(ctx, 0)
	if !errs.Is(err, errs.KindConflict) {
		t.Fatalf("publish without a draft: %v", err)
	}

	teal := domain.DefaultTheme()
	teal.Colors.Primary, teal.Look = "#0F766E", domain.LookSoft
	if d, err = f.svc.SaveThemeDraft(ctx, teal); err != nil || d.Theme == nil || d.Theme.Colors.Primary != "#0f766e" || d.UpdatedAt == nil {
		t.Fatalf("save draft %+v, %v", d, err)
	}
	wantLog(t, f, nil, nil) // a draft tells no shop
	if live, _ := f.svc.GetTheme(ctx); live.Version != 0 || live.Settings != domain.DefaultTheme() {
		t.Fatalf("the draft leaked into the live theme: %+v", live)
	}
	bad := teal
	bad.Colors.Text = bad.Colors.Background
	_, err = f.svc.SaveThemeDraft(ctx, bad)
	wantFields(t, err, "colors.text:low_contrast")

	if _, err = f.svc.PublishTheme(ctx, 3); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("publish over a stale version: %v", err)
	}
	if d, _ = f.svc.GetDraft(ctx); d.Theme == nil {
		t.Fatal("a failed publish dropped the draft")
	}
	live, err := f.svc.PublishTheme(ctx, 0)
	if err != nil || live.Version != 1 || live.Settings.Look != domain.LookSoft {
		t.Fatalf("publish %+v, %v", live, err)
	}
	wantLog(t, f, []string{domain.EventThemeChanged}, []string{"theme.published"})
	if d, _ = f.svc.GetDraft(ctx); d.Theme != nil {
		t.Fatal("the draft stayed after publishing")
	}

	if _, err = f.svc.SaveThemeDraft(ctx, domain.DefaultTheme()); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.DiscardThemeDraft(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.DiscardThemeDraft(ctx); err != nil {
		t.Fatalf("discard is idempotent: %v", err)
	}
	if d, _ = f.svc.GetDraft(ctx); d.Theme != nil {
		t.Fatal("discarded draft still there")
	}
	if b, _ := f.svc.GetDraft(f.ctxB); b.Theme != nil || b.Home != nil {
		t.Fatalf("store B sees A's drafts: %+v", b)
	}
}

func TestHomeDraft(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	in := domain.HomeDraft{Title: "متجر منى", Sections: []domain.Section{
		section("", domain.SectionHero, `{"heading":"أهلًا","button_label":"تسوق","button_link":"/c/new"}`),
	}}
	d, err := f.svc.SaveHomeDraft(ctx, in)
	if err != nil || d.Home == nil || len(d.Home.Sections) != 1 || len(d.Home.Sections[0].ID) != 26 {
		t.Fatalf("save home draft %+v, %v", d, err)
	}
	wantLog(t, f, nil, nil)
	if live, _ := f.svc.GetHome(ctx); live.Version != 0 {
		t.Fatalf("the draft leaked into the live home page: %+v", live)
	}
	bad := in
	bad.Sections = []domain.Section{section("hero", domain.SectionHero, `{"button_label":"x","button_link":"javascript:alert(1)"}`)}
	_, err = f.svc.SaveHomeDraft(ctx, bad)
	wantFields(t, err, "sections[0].settings.button_link:pattern")

	page, err := f.svc.PublishHome(ctx, 0)
	if err != nil || page.Version != 1 || page.Title != "متجر منى" || len(page.Sections) != 1 {
		t.Fatalf("publish home %+v, %v", page, err)
	}
	wantLog(t, f, []string{"storefront.page.changed home home  false"}, []string{"page.created", "home.published"})
	if d, _ = f.svc.GetDraft(ctx); d.Home != nil {
		t.Fatal("the home draft stayed after publishing")
	}
	if _, err = f.svc.PublishHome(ctx, 1); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("publish without a draft: %v", err)
	}
	if err = f.svc.DiscardHomeDraft(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestPreview: a token opens its own store's drafts until it expires;
// only its hash is stored, and another store's token opens nothing.
func TestPreview(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	teal := domain.DefaultTheme()
	teal.Colors.Primary = "#0f766e"
	if _, err := f.svc.SaveThemeDraft(f.ctxA, teal); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.CreatePreview(f.ctxA)
	if err != nil || len(p.Token) < 40 || p.ExpiresAt.IsZero() {
		t.Fatalf("preview %+v, %v", p, err)
	}
	d, ok, err := f.svc.PreviewDraft(f.ctxA, p.Token)
	if err != nil || !ok || d.Theme == nil || d.Theme.Colors.Primary != "#0f766e" || d.Home != nil {
		t.Fatalf("preview draft %+v %v %v", d, ok, err)
	}
	for name, try := range map[string]func() (bool, error){
		"another store": func() (bool, error) { _, opened, e := f.svc.PreviewDraft(f.ctxB, p.Token); return opened, e },
		"wrong token":   func() (bool, error) { _, opened, e := f.svc.PreviewDraft(f.ctxA, p.Token+"x"); return opened, e },
		"empty":         func() (bool, error) { _, opened, e := f.svc.PreviewDraft(f.ctxA, ""); return opened, e },
	} {
		if opened, e := try(); opened || e != nil {
			t.Errorf("%s opened a preview: %v %v", name, opened, e)
		}
	}
	// An expired link opens nothing.
	old := sha256.Sum256([]byte("expired-token"))
	err = postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		return store.InsertPreview(f.ctxA, tx, old[:], time.Now().Add(-time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err = f.svc.PreviewDraft(f.ctxA, "expired-token"); ok || err != nil {
		t.Fatalf("an expired link opened a preview: %v %v", ok, err)
	}
	// Without a draft the preview shows the published shop: still a valid link.
	q, err := f.svc.CreatePreview(f.ctxB)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok, err = f.svc.PreviewDraft(f.ctxB, q.Token); err != nil || !ok || d.Theme != nil {
		t.Fatalf("draftless preview %+v %v %v", d, ok, err)
	}

	for range domain.MaxPreviews - 1 {
		if _, err = f.svc.CreatePreview(f.ctxA); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.svc.CreatePreview(f.ctxA); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("21st live link: %v", err)
	}
}
