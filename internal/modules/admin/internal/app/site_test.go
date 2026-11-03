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

	c, err := f.svc.SiteContent(ctx)
	if err != nil || c.Settings.WhatsApp != "+201153450716" || len(c.FAQs) != 10 {
		t.Fatalf("content = %+v %d, %v", c.Settings, len(c.FAQs), err)
	}
	set := c.Settings
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
	if c, _ = f.svc.SiteContent(ctx); len(c.FAQs) != 11 || c.FAQs[2].ID != added.ID || !c.FAQs[2].OnHome {
		t.Fatalf("after adding: %d, third = %+v", len(c.FAQs), c.FAQs[2])
	}
	if err = f.svc.DeleteSiteFAQ(ctx, added.ID, "answered elsewhere"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.DeleteSiteFAQ(ctx, added.ID, "answered elsewhere"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}

	// The about page: the story, a founder, a review; uploads need the
	// bucket (not wired here) and nothing is recorded for them.
	if _, err = f.svc.SaveSiteStory(ctx, "قصتنا", "", "https://vimeo.com/1", "the story"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a Vimeo video: %v", err)
	}
	if st, stErr := f.svc.SaveSiteStory(ctx, "قصتنا", "", "https://youtu.be/dQw4w9WgXcQ", "the story"); stErr != nil || st.VideoID != "dQw4w9WgXcQ" {
		t.Fatalf("story = %+v, %v", st, stErr)
	}
	if _, err = f.svc.SiteUpload(ctx, "image/png"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("upload without a bucket: %v", err)
	}
	if _, err = f.svc.SetSiteImage(ctx, site.Logo, "platform/site/uploads/x", "the logo"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("a logo without a bucket: %v", err)
	}
	if _, err = f.svc.CreateSitePerson(ctx, site.Person{Kind: site.Founder, NameAr: "أ", RoleAr: "المدير"}, "", "a founder"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a one-letter name: %v", err)
	}
	founder, err := f.svc.CreateSitePerson(ctx, site.Person{Kind: site.Founder, NameAr: "أحمد", RoleAr: "المدير التقني"}, "", "a founder")
	if err != nil {
		t.Fatal(err)
	}
	founder.RoleEn = "CTO"
	if _, err = f.svc.UpdateSitePerson(ctx, founder, "", false, "English role"); err != nil {
		t.Fatal(err)
	}
	review, err := f.svc.CreateSiteTestimonial(ctx, site.Testimonial{Name: "منى", QuoteAr: "قلّت الطلبات الوهمية عندي."}, "", "a real review")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.UpdateSiteTestimonial(ctx, review, "", false, "same review"); err != nil {
		t.Fatal(err)
	}
	if c, _ = f.svc.SiteContent(ctx); len(c.People) != 1 || c.People[0].RoleEn != "CTO" || len(c.Testimonials) != 1 || c.Identity.Story.Ar != "قصتنا" {
		t.Fatalf("about content = %+v", c)
	}
	if err = f.svc.DeleteSitePerson(ctx, founder.ID, "left"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.DeleteSiteTestimonial(ctx, review.ID, "withdrawn"); err != nil {
		t.Fatal(err)
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
	want := "site.testimonial_deleted,site.person_deleted,site.testimonial_updated,site.testimonial_created,site.person_updated,site.person_created," +
		"site.story_saved,site.faq_deleted,site.faq_deleted,site.faq_updated,site.faq_created,site.settings_saved"
	if strings.Join(got, ",") != want {
		t.Fatalf("admin log = %v", got)
	}

	if _, err = f.svc.SiteContent(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if _, err = f.cli.SiteContent(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}

// TestSiteGuidesFromTheConsole: guides are written, published and
// removed with a reason, each change in the admin log; the preview
// records nothing.
func TestSiteGuidesFromTheConsole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)

	draft := site.Guide{Slug: "cod-shipping-egypt", Status: site.Draft, TitleAr: "تسعير الشحن في مصر", SummaryAr: "كيف تحدد أسعار الشحن ورسوم التحصيل.",
		BodyAr: strings.Repeat("نص الدليل. ", 10)}
	if _, err := f.svc.CreateSiteGuide(ctx, draft, "", "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("no reason: %v", err)
	}
	g, err := f.svc.CreateSiteGuide(ctx, draft, "", "the first guide")
	if err != nil {
		t.Fatal(err)
	}
	g.Status = site.Published
	if g, err = f.svc.UpdateSiteGuide(ctx, g, "", false, "reviewed"); err != nil || g.PublishedAt.IsZero() {
		t.Fatalf("published = %+v, %v", g, err)
	}
	if list, listErr := f.svc.SiteGuides(ctx); listErr != nil || len(list) != 1 {
		t.Fatalf("guides = %+v, %v", list, listErr)
	}
	if whole, getErr := f.svc.SiteGuide(ctx, g.ID); getErr != nil || whole.BodyAr == "" {
		t.Fatalf("guide = %+v, %v", whole, getErr)
	}
	if html, prevErr := f.svc.PreviewGuide(ctx, "# عنوان"); prevErr != nil || !strings.Contains(html, "<h2") {
		t.Fatalf("preview = %q, %v", html, prevErr)
	}
	if err = f.svc.DeleteSiteGuide(ctx, g.ID, "replaced"); err != nil {
		t.Fatal(err)
	}

	page, err := f.svc.Audit(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Items {
		if e.AdminID != nil && *e.AdminID == me.Admin.ID && strings.HasPrefix(e.Action, "site.guide") {
			got = append(got, e.Action)
		}
	}
	if strings.Join(got, ",") != "site.guide_deleted,site.guide_updated,site.guide_created" {
		t.Fatalf("admin log = %v", got)
	}
	if _, err = f.cli.SiteGuides(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("cli: %v", err)
	}
}
