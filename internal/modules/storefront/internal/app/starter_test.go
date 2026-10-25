package app_test

import (
	"encoding/json"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
)

// The starter template sets the look and the home page, keeps what the
// merchant wrote, and leaves their policies alone (ADR-0034).
func TestStarterLook(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	mine := domain.DefaultTheme()
	mine.Announcement, mine.About = "عرض خاص", "متجر منى"
	if _, err := f.svc.UpdateTheme(ctx, mine, 0); err != nil {
		t.Fatal(err)
	}
	look := app.StarterLook{
		Colors: domain.Colors{Primary: "#92400e", Accent: "#15803d", Background: "#fffbeb", Text: "#0f172a"},
		Font:   domain.FontTajawal, Radius: domain.RadiusLarge, Look: domain.LookSoft, Announcement: "الدفع عند الاستلام",
		Home: []domain.Section{
			{ID: "hero", Type: domain.SectionHero, Settings: json.RawMessage(`{"heading":"عطور تدوم","align":"center"}`)},
			{ID: "newest", Type: domain.SectionProductGrid, Settings: json.RawMessage(`{"source":"newest","limit":8}`)},
		},
	}
	for range 2 { // the template may run twice
		if err := f.svc.ApplyLook(ctx, look); err != nil {
			t.Fatal(err)
		}
	}
	th, err := f.svc.GetTheme(ctx)
	s := th.Settings
	if err != nil || s.Look != domain.LookSoft || s.Colors.Primary != "#92400e" || s.Font != domain.FontTajawal || s.Radius != domain.RadiusLarge ||
		s.Announcement != "عرض خاص" || s.About != "متجر منى" {
		t.Fatalf("theme = %+v, %v", s, err)
	}
	home, err := f.svc.GetHome(ctx)
	if err != nil || len(home.Sections) != 2 || home.Sections[0].ID != "hero" {
		t.Fatalf("home = %+v, %v", home.Sections, err)
	}

	// A template announcement fills an empty one only.
	if err = f.svc.ApplyLook(f.ctxB, look); err != nil {
		t.Fatal(err)
	}
	if th, _ = f.svc.GetTheme(f.ctxB); th.Settings.Announcement != "الدفع عند الاستلام" {
		t.Fatalf("store B's announcement = %q", th.Settings.Announcement)
	}

	// Policies: a draft where there is none, the merchant's untouched.
	if _, err = f.svc.SavePolicy(ctx, domain.PolicyPrivacy, domain.PageInput{Title: "خصوصيتي", Body: "نصي", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{domain.PolicyRefund, domain.PolicyPrivacy} {
		for range 2 {
			if err = f.svc.EnsureDraftPolicy(ctx, p, "سياسة", "نص القالب"); err != nil {
				t.Fatal(err)
			}
		}
	}
	refund, _ := f.svc.GetPolicy(ctx, domain.PolicyRefund)
	privacy, _ := f.svc.GetPolicy(ctx, domain.PolicyPrivacy)
	if refund.Status != domain.StatusDraft || refund.Body != "نص القالب" || refund.Version != 1 {
		t.Fatalf("refund = %+v", refund)
	}
	if privacy.Status != domain.StatusPublished || privacy.Body != "نصي" {
		t.Fatalf("the merchant's privacy policy changed: %+v", privacy)
	}
	if err = f.svc.EnsureDraftPolicy(ctx, "cookies", "x", "y"); err == nil {
		t.Fatal("an unknown policy was accepted")
	}
}
