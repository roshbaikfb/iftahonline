package app_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/site"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestSiteFromTheConsole: the site's contact details and FAQ are edited
// with a reason, and every change is in the admin log (ADR-0073). The
// only test of the package on the site's tables.
func TestSiteFromTheConsole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)

	set, faqs, err := f.svc.SiteContent(ctx)
	if err != nil || set.WhatsApp != "+201153450716" || len(faqs) != 10 {
		t.Fatalf("content = %+v %d, %v", set, len(faqs), err)
	}
	details := site.Settings{WhatsApp: "+201153450716", Email: "hello@iftahonline.com", Socials: map[site.Social]string{site.YouTube: "https://youtube.com/@iftahonline"}}
	if _, err = f.svc.SaveSiteSettings(ctx, details, "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	if _, err = f.svc.SaveSiteSettings(ctx, site.Settings{Email: "nope"}, "contact details"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a bad email: %v", err)
	}
	if set, err = f.svc.SaveSiteSettings(ctx, details, "contact details"); err != nil || set.Email != details.Email {
		t.Fatalf("saved = %+v, %v", set, err)
	}

	q := site.FAQ{Position: 15, QuestionAr: "هل تدعمون الدفع بفودافون كاش؟", AnswerAr: "قريبًا."}
	if _, err = f.svc.CreateSiteFAQ(ctx, q, ""); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	added, err := f.svc.CreateSiteFAQ(ctx, q, "a common question")
	if err != nil {
		t.Fatal(err)
	}
	added.OnHome = true
	if _, err = f.svc.UpdateSiteFAQ(ctx, added, "show it on the home page"); err != nil {
		t.Fatal(err)
	}
	if _, faqs, _ = f.svc.SiteContent(ctx); len(faqs) != 11 || faqs[2].ID != added.ID || !faqs[2].OnHome {
		t.Fatalf("after adding: %d, third = %+v", len(faqs), faqs[2])
	}
	if err = f.svc.DeleteSiteFAQ(ctx, added.ID, "answered elsewhere"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.DeleteSiteFAQ(ctx, added.ID, "answered elsewhere"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}

	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID && strings.HasPrefix(e.Action, "site.") {
			got = append(got, e.Action)
		}
	}
	// Newest first; the second delete is recorded too (audit first).
	want := "site.faq_deleted,site.faq_deleted,site.faq_updated,site.faq_created,site.settings_saved"
	if strings.Join(got, ",") != want {
		t.Fatalf("admin log = %v", got)
	}

	if _, _, err = f.svc.SiteContent(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if _, _, err = f.cli.SiteContent(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}
