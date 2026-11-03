package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
)

func TestGuide(t *testing.T) {
	t.Parallel()
	ok := func() domain.Guide {
		return domain.Guide{Slug: "reduce-fake-orders", Status: domain.Draft, TitleAr: "كيف تقلل الطلبات الوهمية",
			SummaryAr: "خطوات عملية لتقليل رفض الاستلام في الدفع عند الاستلام.", BodyAr: strings.Repeat("نص الدليل. ", 10)}
	}
	for _, tt := range []struct {
		name  string
		edit  func(*domain.Guide)
		wrong []string
	}{
		{"an Arabic draft", func(*domain.Guide) {}, nil},
		{"published with English", func(g *domain.Guide) {
			g.Status, g.TitleEn, g.SummaryEn, g.BodyEn = domain.Published, "Reduce fake orders", "Practical steps to cut refused deliveries.", strings.Repeat("Guide text. ", 10)
		}, nil},
		{"half English", func(g *domain.Guide) { g.TitleEn = "Reduce fake orders" }, []string{"summary_en", "body_en"}},
		{"English body without a title", func(g *domain.Guide) { g.BodyEn = strings.Repeat("x ", 40) }, []string{"title_en", "summary_en"}},
		{"Arabic letters in the slug", func(g *domain.Guide) { g.Slug = "الطلبات" }, []string{"slug"}},
		{"spaces in the slug", func(g *domain.Guide) { g.Slug = "fake orders" }, []string{"slug"}},
		{"a double dash", func(g *domain.Guide) { g.Slug = "fake--orders" }, []string{"slug"}},
		{"a short body", func(g *domain.Guide) { g.BodyAr = "قصير" }, []string{"body_ar"}},
		{"an unknown status", func(g *domain.Guide) { g.Status = "archived" }, []string{"status"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := ok()
			tt.edit(&g)
			if got := fields(t, g.Validate()); strings.Join(got, ",") != strings.Join(tt.wrong, ",") {
				t.Fatalf("wrong fields = %v, want %v", got, tt.wrong)
			}
		})
	}
	g := domain.Guide{Slug: "  Reduce-Fake-Orders ", TitleAr: " عنوان طويل "}
	g.Normalize()
	if g.Slug != "reduce-fake-orders" || g.TitleAr != "عنوان طويل" {
		t.Fatalf("normalized = %+v", g)
	}
}

func TestGuidePublish(t *testing.T) {
	t.Parallel()
	first, now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name   string
		status domain.GuideStatus
		had    time.Time
		want   time.Time
	}{
		{"published the first time: now", domain.Published, time.Time{}, now},
		{"published again: its first date", domain.Published, first, first},
		{"a draft never published: none", domain.Draft, time.Time{}, time.Time{}},
		{"unpublished: keeps its date for later", domain.Draft, first, first},
	} {
		g := domain.Guide{Status: tt.status}
		g.Publish(tt.had, now)
		if !g.PublishedAt.Equal(tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, g.PublishedAt, tt.want)
		}
	}
}

func TestReadingAndExcerpt(t *testing.T) {
	t.Parallel()
	if got := domain.ReadingMinutes(strings.Repeat("كلمة ", 401)); got != 3 {
		t.Fatalf("401 words = %d minutes", got)
	}
	if got := domain.ReadingMinutes(""); got != 1 {
		t.Fatalf("nothing = %d minutes", got)
	}
	if got := domain.Excerpt("الدفع عند الاستلام في مصر", 12); got != "الدفع عند…" {
		t.Fatalf("excerpt = %q", got)
	}
	if got := domain.Excerpt("قصير", 12); got != "قصير" {
		t.Fatalf("short = %q", got)
	}
}
