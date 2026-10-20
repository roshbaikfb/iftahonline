package app_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
)

func TestShopReads(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	c, err := f.svc.ShopContent(ctx)
	if err != nil || c.Theme != domain.DefaultTheme() || c.Logo != nil || len(c.Policies) != 0 {
		t.Fatalf("defaults %+v, %v", c, err)
	}
	theme := domain.DefaultTheme()
	theme.Font = domain.FontSystem
	if _, err = f.svc.UpdateTheme(ctx, theme, 0); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ slug, status string }{
		{domain.PolicyTerms, "published"}, {domain.PolicyShipping, "published"}, {domain.PolicyRefund, "draft"},
	} {
		if _, err = f.svc.SavePolicy(ctx, p.slug, domain.PageInput{Title: p.slug, Body: "text", Status: p.status}); err != nil {
			t.Fatal(err)
		}
	}
	c, err = f.svc.ShopContent(ctx)
	if err != nil || c.Theme.Font != domain.FontSystem || len(c.Policies) != 2 ||
		c.Policies[0].Slug != domain.PolicyShipping || c.Policies[1].Slug != domain.PolicyTerms {
		t.Fatalf("content %+v, %v", c, err)
	}

	if _, err = f.svc.PublishedPolicy(ctx, domain.PolicyTerms); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{domain.PolicyRefund, domain.PolicyPrivacy, "cookies"} {
		if _, err = f.svc.PublishedPolicy(ctx, slug); !errors.Is(err, app.ErrPageNotFound) {
			t.Fatalf("policy %q: %v", slug, err)
		}
	}

	if _, err = f.svc.CreateLandingPage(ctx, domain.PageInput{Title: "Draft offer", Slug: "draft-offer"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CreateLandingPage(ctx, domain.PageInput{Title: "Live offer", Slug: "live-offer", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	if p, getErr := f.svc.PublishedLanding(ctx, "live-offer"); getErr != nil || p.Title != "Live offer" {
		t.Fatalf("live landing %+v, %v", p, getErr)
	}
	for _, slug := range []string{"draft-offer", "missing"} {
		if _, err = f.svc.PublishedLanding(ctx, slug); !errors.Is(err, app.ErrPageNotFound) {
			t.Fatalf("landing %q: %v", slug, err)
		}
	}
	if _, err = f.svc.PublishedLanding(f.ctxB, "live-offer"); !errors.Is(err, app.ErrPageNotFound) {
		t.Fatalf("store B reads A's landing page: %v", err)
	}
	if b, _ := f.svc.ShopContent(f.ctxB); len(b.Policies) != 0 || b.Theme != domain.DefaultTheme() {
		t.Fatalf("store B sees A's content: %+v", b)
	}
}
