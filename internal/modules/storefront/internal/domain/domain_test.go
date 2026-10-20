package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func fields(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Fatalf("not a validation error: %v", err)
	}
	out := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		out[i] = f.Field + ":" + f.Code
	}
	slices.Sort(out)
	return out
}

func TestValidateTheme(t *testing.T) {
	t.Parallel()
	ok := domain.DefaultTheme()
	tests := []struct {
		name   string
		mutate func(*domain.ThemeSettings)
		want   []string
	}{
		{"defaults", func(*domain.ThemeSettings) {}, nil},
		{"upper case and spaces normalized", func(s *domain.ThemeSettings) { s.Colors.Primary = "  #AB12CD " }, nil},
		{"bad colors", func(s *domain.ThemeSettings) {
			s.Colors.Primary, s.Colors.Accent, s.Colors.Background = "red", "#12345", "#gggggg"
		}, []string{"colors.accent:invalid", "colors.background:invalid", "colors.primary:invalid"}},
		{"css injection", func(s *domain.ThemeSettings) { s.Colors.Text = "#000;}body{x" }, []string{"colors.text:invalid"}},
		{"low contrast", func(s *domain.ThemeSettings) { s.Colors.Text, s.Colors.Background = "#777777", "#888888" }, []string{"colors.text:low_contrast"}},
		{"dark theme", func(s *domain.ThemeSettings) { s.Colors.Text, s.Colors.Background = "#f9fafb", "#111827" }, nil},
		{"bad font and radius", func(s *domain.ThemeSettings) { s.Font, s.Radius = "comic-sans", "huge" }, []string{"font:invalid", "radius:invalid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := ok
			tt.mutate(&in)
			out, err := domain.ValidateTheme(in)
			if got := fields(t, err); !slices.Equal(got, tt.want) {
				t.Fatalf("fields %q, want %q", got, tt.want)
			}
			if err == nil && out.Colors.Primary != strings.ToLower(strings.TrimSpace(in.Colors.Primary)) {
				t.Fatalf("primary not normalized: %q", out.Colors.Primary)
			}
		})
	}
}

func TestContrast(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want float64
	}{
		{"#000000", "#ffffff", 21},
		{"#ffffff", "#000000", 21},
		{"#777777", "#777777", 1},
		{"#0f766e", "#ffffff", 5.47},
		{"bad", "#ffffff", 1},
	}
	for _, tt := range tests {
		if got := domain.ContrastRatio(tt.a, tt.b); math.Abs(got-tt.want) > 0.01 {
			t.Errorf("ContrastRatio(%s, %s) = %.2f, want %.2f", tt.a, tt.b, got, tt.want)
		}
	}
	for bg, want := range map[string]string{"#0f766e": "#ffffff", "#f59e0b": "#111111", "#ffffff": "#111111", "#000000": "#ffffff"} {
		if got := domain.OnColor(bg); got != want {
			t.Errorf("OnColor(%s) = %s, want %s", bg, got, want)
		}
	}
}

// fakeSettings accepts settings unless they contain "bad", which it
// reports on a nested field, as the schema registry would.
type fakeSettings struct{}

func (fakeSettings) ValidateSettings(_ domain.SectionType, raw json.RawMessage) []errs.FieldError {
	if strings.Contains(string(raw), "bad") {
		return []errs.FieldError{{Field: "items[0].name", Code: "maxLength", Message: "too long"}}
	}
	if strings.Contains(string(raw), "root") {
		return []errs.FieldError{{Field: "", Code: "type", Message: "not an object"}}
	}
	return nil
}

func ids() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("gen%d", n) }
}

func sec(id string, typ domain.SectionType, settings string) domain.Section {
	return domain.Section{ID: id, Type: typ, Settings: json.RawMessage(settings)}
}

func TestValidateLandingPage(t *testing.T) {
	t.Parallel()
	in := domain.PageInput{
		Title: "  عرض الصيف ", Description: "خصومات حتى ٥٠٪",
		Sections: []domain.Section{sec("", domain.SectionHero, `{}`), sec(" Main ", domain.SectionProductLanding, ``)},
	}
	out, err := domain.ValidatePage(domain.KindLanding, "", in, fakeSettings{}, ids())
	if err != nil {
		t.Fatal(err)
	}
	if out.Slug != "عرض-الصيف" || out.Title != "عرض الصيف" || out.Status != "draft" {
		t.Fatalf("normalized %+v", out)
	}
	if out.Sections[0].ID != "gen1" || out.Sections[1].ID != "main" || string(out.Sections[1].Settings) != "{}" {
		t.Fatalf("sections %+v", out.Sections)
	}

	many := make([]domain.Section, domain.MaxSections+1)
	for i := range many {
		many[i] = sec("", domain.SectionFAQ, `{}`)
	}
	big := `{"text":"` + strings.Repeat("x", domain.MaxSectionsBytes) + `"}`
	tests := []struct {
		name string
		in   domain.PageInput
		want []string
	}{
		{"no title", domain.PageInput{}, []string{"slug:required", "title:length"}},
		{"symbols only", domain.PageInput{Title: "!!!"}, []string{"slug:required"}},
		{"bad slug", domain.PageInput{Title: "x", Slug: "a b"}, []string{"slug:invalid"}},
		{"bad status", domain.PageInput{Title: "x", Status: "live"}, []string{"status:invalid"}},
		{"control chars", domain.PageInput{Title: "a\x00b", Description: "line\nbreak"}, []string{"description:invalid", "title:invalid"}},
		{"long description", domain.PageInput{Title: "x", Description: strings.Repeat("د", 301)}, []string{"description:length"}},
		{"body not allowed", domain.PageInput{Title: "x", Body: "text"}, []string{"body:not_allowed"}},
		{"negative version", domain.PageInput{Title: "x", Version: -1}, []string{"version:range"}},
		{"too many sections", domain.PageInput{Title: "x", Sections: many}, []string{"sections:too_many"}},
		{"too large", domain.PageInput{Title: "x", Sections: []domain.Section{sec("a", domain.SectionFAQ, big)}}, []string{"sections:too_large"}},
		{"section errors", domain.PageInput{Title: "x", Sections: []domain.Section{
			sec("dup", domain.SectionFAQ, `{}`),
			sec("dup", domain.SectionFAQ, `{"bad":1}`),
			sec("UP_per!", domain.SectionType("iframe"), `{}`),
			sec(strings.Repeat("a", 33), domain.SectionHero, `{"root":1}`),
		}}, []string{"sections[1].id:duplicate", "sections[1].settings.items[0].name:maxLength", "sections[2].id:invalid", "sections[2].type:invalid", "sections[3].id:invalid", "sections[3].settings:type"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.ValidatePage(domain.KindLanding, "", tt.in, fakeSettings{}, ids())
			if got := fields(t, err); !slices.Equal(got, tt.want) {
				t.Fatalf("fields %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateHomeAndPolicy(t *testing.T) {
	t.Parallel()
	home, err := domain.ValidatePage(domain.KindHome, "", domain.PageInput{Slug: "ignored", Status: "draft",
		Sections: domain.DefaultHome().Sections}, fakeSettings{}, ids())
	if err != nil || home.Slug != domain.HomeSlug || home.Status != "published" || home.Title != "" || len(home.Sections) != 3 {
		t.Fatalf("home %+v, %v", home, err)
	}

	policy, err := domain.ValidatePage(domain.KindPolicy, domain.PolicyRefund, domain.PageInput{
		Title: "سياسة الاسترجاع", Body: "  يمكن الاسترجاع خلال ١٤ يومًا.\n\nبشرط سلامة المنتج.\t ", Status: "published",
	}, fakeSettings{}, ids())
	if err != nil || policy.Slug != "refund" || policy.Status != "published" || !strings.HasSuffix(policy.Body, "المنتج.") || len(policy.Sections) != 0 {
		t.Fatalf("policy %+v, %v", policy, err)
	}

	tests := []struct {
		name   string
		kind   domain.PageKind
		policy string
		in     domain.PageInput
		want   []string
	}{
		{"unknown policy", domain.KindPolicy, "cookies", domain.PageInput{}, []string{"policy:invalid"}},
		{"policy sections", domain.KindPolicy, domain.PolicyTerms, domain.PageInput{Sections: []domain.Section{sec("a", domain.SectionFAQ, `{}`)}}, []string{"sections:not_allowed"}},
		{"policy body too long", domain.KindPolicy, domain.PolicyTerms, domain.PageInput{Body: strings.Repeat("a", domain.MaxPolicyBodyLen+1)}, []string{"body:length"}},
		{"policy control char", domain.KindPolicy, domain.PolicyTerms, domain.PageInput{Body: "a\x07b"}, []string{"body:length"}},
		{"unknown kind", domain.PageKind("blog"), "", domain.PageInput{}, []string{"kind:invalid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.ValidatePage(tt.kind, tt.policy, tt.in, fakeSettings{}, ids())
			if got := fields(t, err); !slices.Equal(got, tt.want) {
				t.Fatalf("fields %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	if _, err := domain.ValidateTheme(domain.DefaultTheme()); err != nil {
		t.Fatalf("default theme invalid: %v", err)
	}
	h := domain.DefaultHome()
	if h.Kind != domain.KindHome || h.Status != domain.StatusPublished || h.Version != 0 || len(h.Sections) == 0 {
		t.Fatalf("default home %+v", h)
	}
	for _, p := range domain.PolicySlugs() {
		if d := domain.DefaultPolicy(p); d.Slug != p || d.Status != domain.StatusDraft || d.Body != "" {
			t.Fatalf("default policy %+v", d)
		}
	}
	if len(domain.Fonts()) != 5 || len(domain.Radii()) != 4 || len(domain.SectionTypes()) != 7 {
		t.Fatal("enum lists changed: update the OpenAPI enums and docs too")
	}
}
