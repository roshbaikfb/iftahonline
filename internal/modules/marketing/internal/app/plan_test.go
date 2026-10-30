package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/capi"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func wantPlanLimit(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, entitlements.ErrLimitReached) || errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("%s without the plan: %v", what, err)
	}
}

// TestPixelsAndFeedsNeedThePlan: without them in the plan the merchant
// connects no pixel and opens no feed, and what was set up before (a
// lapsed trial) goes quiet: pages load no pixel, no conversion is sent,
// and the feed URL opens nothing (ADR-0046).
func TestPixelsAndFeedsNeedThePlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	q := &queue{}
	before := f.withConversions(q, &sent{}, nil)
	if _, err := before.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Enabled: true, ServerToken: ptr("meta-token-1")}); err != nil {
		t.Fatal(err)
	}
	key, err := before.RenewFeedKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: f.pool, Source: entitlements.Fixed{}})
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Sealer: f.sealer, Events: f.pub, Jobs: q, Plan: ent.Entitlements()})

	_, err = svc.SavePixel(ctx, domain.TikTok, domain.PixelInput{PixelID: "C4ABCDEF1234567890AB", Enabled: true})
	wantPlanLimit(t, "a pixel", err)
	_, err = svc.RenewFeedKey(ctx)
	wantPlanLimit(t, "a feed key", err)

	if b, bErr := svc.Browser(ctx); bErr != nil || len(b.Pixels) != 0 || len(b.Script) != 0 {
		t.Errorf("pages load %+v (%v)", b, bErr)
	}
	if err = svc.Track(ctx, app.Conversion{Name: capi.AddToCart, EventID: "e1", Time: time.Now(), Value: egp(t, 100)}); err != nil || len(q.take()) != 0 {
		t.Errorf("a conversion was enqueued (%v)", err)
	}
	if ok, aErr := svc.FeedAllowed(ctx, key); aErr != nil || ok {
		t.Errorf("the old feed key opens: %v %v", ok, aErr)
	}
	if ok, aErr := before.FeedAllowed(ctx, key); aErr != nil || !ok {
		t.Errorf("with the plan it opens: %v %v", ok, aErr)
	}
}
