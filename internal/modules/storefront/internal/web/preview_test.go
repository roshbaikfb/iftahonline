package web_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
)

// TestPreview (ADR-0036): a live preview link shows the drafts with a
// banner, keeps the token on every link, and is never cached or indexed;
// the same page without it, or with a dead token, is the published shop.
func TestPreview(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	draft := domain.DefaultTheme()
	draft.Colors.Primary = "#0f766e"
	f.content.previews = map[string]domain.Draft{"live-token": {
		Theme: &draft,
		Home: &domain.HomeDraft{Title: "مسودة", Sections: []domain.Section{
			{ID: "h", Type: domain.SectionHero, Settings: json.RawMessage(`{"heading":"تشكيلة الشتاء وصلت","button_label":"تسوّق","button_link":"/c/tshirts"}`)},
		}},
	}}

	res := f.get(t, "/?preview=live-token")
	body := res.Body.String()
	wantContains(t, body,
		`<div class="preview-bar" role="status">معاينة: هذه التغييرات لم تُنشر بعد، والعملاء يرون النسخة المنشورة.</div>`,
		`--c-primary:#0f766e`,
		`تشكيلة الشتاء وصلت`,
		`href="/c/tshirts?preview=live-token"`,
		`<meta name="robots" content="noindex">`,
	)
	if h := res.Header(); h.Get("Cache-Control") != "private, no-store" || h.Get("X-Robots-Tag") != "noindex" || h.Get("Cache-Tag") != "" {
		t.Fatalf("preview headers: %v", h)
	}
	if !strings.Contains(res.Header().Get("Content-Security-Policy"), "'sha256-") || strings.Count(res.Header().Get("Content-Security-Policy"), "'sha256-") != 2 {
		t.Fatalf("the preview's own style is not allowed by the CSP: %q", res.Header().Get("Content-Security-Policy"))
	}

	// Shoppers never see the drafts, before or after a preview.
	for _, path := range []string{"/", "/?preview=dead-token"} {
		res = f.get(t, path)
		body = res.Body.String()
		if strings.Contains(body, "preview-bar") || strings.Contains(body, "#0f766e") || strings.Contains(body, "تشكيلة الشتاء وصلت") {
			t.Fatalf("%s shows the drafts", path)
		}
		if res.Header().Get("Cache-Control") == "private, no-store" {
			t.Fatalf("%s is not cacheable", path)
		}
	}

	// Other pages keep the preview, and a product page still gets the draft colours.
	body = f.get(t, "/p/cotton-tee?preview=live-token").Body.String()
	wantContains(t, body, `class="preview-bar"`, `--c-primary:#0f766e`)
}
