package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestGuidesEndToEnd: a draft stays off the site; published, it is
// dated, shown and keeps its date when unpublished and published again;
// addresses are unique; covers follow the photos' rules; the preview is
// the site's safe HTML.
func TestGuidesEndToEnd(t *testing.T) {
	ctx := t.Context()
	appPool, adminPool := open(t, infra.AppURL), open(t, infra.AdminURL)
	clk := &clock{now: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)}
	b := &bucket{objects: map[string][]byte{}}
	console := app.NewService(app.Deps{DB: adminPool, Admin: adminPool, IDs: ulid.NewGenerator(), Now: clk.Now,
		Media: app.Media{Objects: b, Limits: media.Limits{MaxBytes: 10 << 20, MaxPixels: 40_000_000}}})
	site := app.NewService(app.Deps{DB: appPool, Now: clk.Now})

	draft := domain.Guide{Slug: "reduce-fake-orders", Status: domain.Draft, TitleAr: "كيف تقلل الطلبات الوهمية",
		SummaryAr: "خطوات عملية لتقليل رفض الاستلام في الدفع عند الاستلام.", BodyAr: "# أولًا\n\n" + strings.Repeat("نص الدليل. ", 20)}
	g, err := console.CreateGuide(ctx, draft, b.upload(t, console))
	if err != nil || g.CoverKey == "" || !g.PublishedAt.IsZero() {
		t.Fatalf("draft = %+v, %v", g, err)
	}
	if _, err = console.CreateGuide(ctx, draft, ""); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("the same address twice: %v", err)
	}
	if _, err = console.CreateGuide(ctx, domain.Guide{Slug: "x", Status: domain.Draft}, ""); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an empty guide: %v", err)
	}
	if c, _ := site.Content(ctx); len(c.Guides) != 0 {
		t.Fatalf("a draft on the site: %+v", c.Guides)
	}

	// Published: dated now, on the site after its minute.
	g.Status = domain.Published
	if g, err = console.UpdateGuide(ctx, g, "", false); err != nil || !g.PublishedAt.Equal(clk.Now()) || g.CoverKey == "" {
		t.Fatalf("published = %+v, %v", g, err)
	}
	first := g.PublishedAt
	clk.add(app.CacheTTL)
	c, err := site.Content(ctx)
	if err != nil || len(c.Guides) != 1 || c.Guides[0].Slug != "reduce-fake-orders" || !strings.HasPrefix(c.Guides[0].BodyAr, "# أولًا") {
		t.Fatalf("site guides = %+v, %v", c.Guides, err)
	}

	// Unpublished then published a day later: its first date stays.
	g.Status = domain.Draft
	if g, err = console.UpdateGuide(ctx, g, "", false); err != nil {
		t.Fatal(err)
	}
	clk.add(24 * time.Hour)
	g.Status, g.TitleEn, g.SummaryEn, g.BodyEn = domain.Published, "Reduce fake orders", "Practical steps to cut refused deliveries.",
		strings.Repeat("Guide text. ", 20)
	if g, err = console.UpdateGuide(ctx, g, "", true); err != nil || !g.PublishedAt.Equal(first) || g.CoverKey != "" {
		t.Fatalf("published again = %+v, %v", g, err)
	}
	if len(b.keys()) != 0 {
		t.Fatalf("the removed cover is left: %v", b.keys())
	}
	list, err := console.Guides(ctx)
	if err != nil || len(list) != 1 || list[0].BodyAr != "" || !list[0].HasEnglish() {
		t.Fatalf("console list = %+v, %v", list, err)
	}
	whole, err := console.Guide(ctx, g.ID)
	if err != nil || whole.BodyEn == "" {
		t.Fatalf("whole = %+v, %v", whole, err)
	}
	if _, err = console.Guide(ctx, ulid.NewGenerator().New()); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("an unknown guide: %v", err)
	}

	// The preview is the site's rendering: safe, headings moved down.
	html, err := console.Preview(ctx, "# عنوان\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1))")
	if err != nil || !strings.Contains(html, "<h2") || strings.Contains(html, "<script") || strings.Contains(html, "javascript:") {
		t.Fatalf("preview = %q, %v", html, err)
	}
	if _, err = console.Preview(ctx, strings.Repeat("a", 40001)); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a long preview: %v", err)
	}

	if err = console.DeleteGuide(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if err = console.DeleteGuide(ctx, g.ID); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}
	if _, err = site.Guides(ctx); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("the site's pool edits guides: %v", err)
	}
}
