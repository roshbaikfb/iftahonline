package app_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type recorder struct {
	mu      sync.Mutex
	actions []string
	meta    []map[string]any
}

func (r *recorder) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, e.Action)
	r.meta = append(r.meta, e.Metadata)
	return nil
}

type published struct {
	mu     sync.Mutex
	events []domain.PixelsChanged
}

func (p *published) PublishTx(_ context.Context, _ pgx.Tx, ev event.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev.(domain.PixelsChanged))
	return nil
}

type fixture struct {
	svc    *app.Service
	pool   *pgxpool.Pool
	ids    *ulid.Generator
	rec    *recorder
	pub    *published
	sealer *envelope.Sealer
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	k, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := envelope.New(envelope.FromKeyring(k))
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{pool: pool, ids: ulid.NewGenerator(), rec: &recorder{}, pub: &published{}, sealer: sealer}
	f.svc = app.NewService(app.Deps{DB: pool, IDs: f.ids, Sealer: sealer, Events: f.pub, Audit: f.rec})
	return f
}

func (f fixture) store(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "mkt-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

// token reads a pixel's sealed token straight from the table.
func (f fixture) token(ctx context.Context, t *testing.T, p domain.Provider) (id ulid.ID, blob []byte) {
	t.Helper()
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, server_token FROM store_pixels WHERE provider = $1`, string(p)).Scan(&id, &blob)
	}); err != nil {
		t.Fatal(err)
	}
	return id, blob
}

func ptr(s string) *string { return &s }

const metaID = "123456789012345"

func TestSavePixel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)

	p, err := f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: " " + metaID + " ", Enabled: true, ServerToken: ptr("EAAB-secret-token-1")})
	if err != nil {
		t.Fatal(err)
	}
	if p.PixelID != metaID || !p.Enabled || !p.HasServerToken || p.Version != 1 {
		t.Fatalf("saved %+v", p)
	}
	id, blob := f.token(ctx, t, domain.Meta)
	if bytes.Contains(blob, []byte("EAAB-secret-token-1")) {
		t.Fatal("the token is stored in the clear")
	}
	store, _ := tenant.FromContext(ctx)
	plain, err := f.sealer.Open(ctx, blob, envelope.ConnectionAAD("marketing", "meta", store, id))
	if err != nil || string(plain) != "EAAB-secret-token-1" {
		t.Fatalf("sealed token opens to %q: %v", plain, err)
	}
	for _, m := range f.rec.meta {
		for _, v := range m {
			if s, ok := v.(string); ok && strings.Contains(s, "secret") {
				t.Fatal("the token reached the audit log")
			}
		}
	}

	// A change without a token keeps it; "" removes it.
	if p, err = f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Enabled: false, Version: 1}); err != nil || !p.HasServerToken || p.Enabled || p.Version != 2 {
		t.Fatalf("kept token: %+v %v", p, err)
	}
	if p, err = f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Enabled: true, ServerToken: ptr(""), Version: 2}); err != nil || p.HasServerToken {
		t.Fatalf("removed token: %+v %v", p, err)
	}

	// Over a stale version, or a first save racing another.
	if _, err = f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Version: 2}); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("stale save: %v", err)
	}
	if _, err = f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID}); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("a second first save: %v", err)
	}
	if _, err = f.svc.SavePixel(ctx, domain.TikTok, domain.PixelInput{PixelID: "C4ABCDEF1234567890AB", Version: 3}); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("an update of a pixel that is not there: %v", err)
	}

	if strings.Join(f.rec.actions, ",") != "pixel.saved,pixel.saved,pixel.saved" || len(f.pub.events) != 3 || f.pub.events[0].Provider != domain.Meta {
		t.Fatalf("audit %v events %+v", f.rec.actions, f.pub.events)
	}
}

func TestSavePixelRefuses(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	if _, err := f.svc.SavePixel(ctx, "gtm", domain.PixelInput{PixelID: "GTM-ABCD"}); !errors.Is(err, app.ErrUnknownProvider) {
		t.Fatalf("unknown provider: %v", err)
	}
	if _, err := f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: "nope"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("bad ID: %v", err)
	}
	if _, err := f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID}); err != nil {
		t.Fatalf("a valid save: %v", err)
	}
	if _, err := f.svc.SavePixel(t.Context(), domain.Meta, domain.PixelInput{PixelID: metaID}); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("no store: %v", err)
	}
	if len(f.pub.events) != 1 {
		t.Fatalf("refused saves published: %+v", f.pub.events)
	}
}

func TestDeletePixel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	if _, err := f.svc.SavePixel(ctx, domain.Snap, domain.PixelInput{PixelID: "0f8fad5b-d9cb-469f-a165-70867728950e", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeletePixel(ctx, domain.Snap); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeletePixel(ctx, domain.Snap); !errors.Is(err, app.ErrPixelNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := f.svc.DeletePixel(ctx, "gtm"); !errors.Is(err, app.ErrUnknownProvider) {
		t.Fatalf("unknown provider: %v", err)
	}
	if pixels, err := f.svc.Pixels(ctx); err != nil || len(pixels) != 0 {
		t.Fatalf("after delete: %+v %v", pixels, err)
	}
	if strings.Join(f.rec.actions, ",") != "pixel.saved,pixel.deleted" || len(f.pub.events) != 2 {
		t.Fatalf("audit %v events %d", f.rec.actions, len(f.pub.events))
	}
}

func TestSettings(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	s, err := f.svc.Settings(ctx)
	if err != nil || s.Consent != domain.ConsentAuto || s.Version != 0 {
		t.Fatalf("defaults %+v %v", s, err)
	}
	if s, err = f.svc.SaveSettings(ctx, domain.Settings{Consent: domain.ConsentRequired}); err != nil || s.Version != 1 {
		t.Fatalf("first save %+v %v", s, err)
	}
	if s, err = f.svc.SaveSettings(ctx, domain.Settings{Consent: domain.ConsentOff, Version: 1}); err != nil || s.Consent != domain.ConsentOff || s.Version != 2 {
		t.Fatalf("second save %+v %v", s, err)
	}
	for name, in := range map[string]domain.Settings{"stale": {Consent: domain.ConsentAuto, Version: 1}, "racing first": {Consent: domain.ConsentAuto}} {
		if _, err = f.svc.SaveSettings(ctx, in); !errs.Is(err, errs.KindConflict) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for name, in := range map[string]domain.Settings{"mode": {Consent: "always"}, "version": {Consent: domain.ConsentOff, Version: -1}} {
		if _, err = f.svc.SaveSettings(ctx, in); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("bad %s: %v", name, err)
		}
	}
	if len(f.pub.events) != 2 || f.pub.events[0].Provider != "" {
		t.Fatalf("events %+v", f.pub.events)
	}
}

func TestBrowser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	other := f.store(t)
	if _, err := f.svc.SavePixel(other, domain.TikTok, domain.PixelInput{PixelID: "C4ABCDEF1234567890AB", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Enabled: true, ServerToken: ptr("EAAB-secret-token-1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SavePixel(ctx, domain.GA4, domain.PixelInput{PixelID: "G-ABC123XYZ", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveSettings(ctx, domain.Settings{Consent: domain.ConsentRequired}); err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.Browser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// only the store's own, enabled pixel; another store's TikTok is not here
	if len(b.Pixels) != 1 || b.Pixels[0].PixelID != metaID || b.Consent != domain.ConsentRequired ||
		strings.Join(b.Script, " ") != "https://connect.facebook.net" {
		t.Fatalf("browser %+v", b)
	}
	pixels, err := f.svc.Pixels(ctx)
	if err != nil || len(pixels) != 2 {
		t.Fatalf("pixels %+v %v", pixels, err)
	}
}
