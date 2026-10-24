package sections_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

const pid = "01JB8Z3W5Q9X2M4N6P7R8S9T0V"

func registry(t testing.TB) *sections.Registry {
	t.Helper()
	r, err := sections.New()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func fieldCodes(fes []errs.FieldError) []string {
	out := make([]string, len(fes))
	for i, f := range fes {
		out[i] = f.Field + ":" + f.Code
	}
	slices.Sort(out)
	return out
}

func TestSchemas(t *testing.T) {
	t.Parallel()
	r := registry(t)
	got := r.Schemas()
	if len(got) != len(domain.SectionTypes()) {
		t.Fatalf("%d schemas for %d types", len(got), len(domain.SectionTypes()))
	}
	for i, ts := range got {
		if ts.Type != domain.SectionTypes()[i] {
			t.Fatalf("schema %d is %s, want %s", i, ts.Type, domain.SectionTypes()[i])
		}
		var doc map[string]any
		if err := json.Unmarshal(ts.Schema, &doc); err != nil {
			t.Fatalf("%s: %v", ts.Type, err)
		}
		if doc["additionalProperties"] != false || doc["title"] == "" || doc["$schema"] == nil {
			t.Fatalf("%s: schema must be closed, titled and versioned: %v", ts.Type, doc)
		}
	}
	if !json.Valid(r.Definitions()) {
		t.Fatal("definitions are not JSON")
	}
}

func TestValidateSettings(t *testing.T) {
	t.Parallel()
	r := registry(t)
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	tests := []struct {
		name string
		typ  domain.SectionType
		raw  string
		want []string // sorted "field:code"; empty means valid
	}{
		{"hero empty", domain.SectionHero, `{}`, nil},
		{"hero full", domain.SectionHero, `{"heading":"عروض الصيف","subheading":"خصم ٣٠٪","button_label":"تسوق الآن","button_link":"/c/summer","image_product_id":"` + pid + `","align":"start"}`, nil},
		{"hero https link", domain.SectionHero, `{"button_label":"واتساب","button_link":"https://wa.me/201000000000"}`, nil},
		{"hero javascript link", domain.SectionHero, `{"button_label":"x","button_link":"javascript:alert(1)"}`, []string{"button_link:pattern"}},
		{"hero protocol-relative link", domain.SectionHero, `{"button_label":"x","button_link":"//evil.example"}`, []string{"button_link:pattern"}},
		{"hero http link", domain.SectionHero, `{"button_label":"x","button_link":"http://shop.example"}`, []string{"button_link:pattern"}},
		{"hero label without link", domain.SectionHero, `{"button_label":"x"}`, []string{"button_link:dependentRequired"}},
		{"hero line break", domain.SectionHero, `{"heading":"a\nb"}`, []string{"heading:pattern"}},
		{"hero too long", domain.SectionHero, `{"heading":"` + strings.Repeat("ع", 121) + `"}`, []string{"heading:maxLength"}},
		{"hero unknown field", domain.SectionHero, `{"script":"<script>"}`, []string{"script:unknown"}},
		{"hero bad id", domain.SectionHero, `{"image_product_id":"not-an-id"}`, []string{"image_product_id:pattern"}},
		{"hero bad align", domain.SectionHero, `{"align":"left"}`, []string{"align:enum"}},
		{"hero badge", domain.SectionHero, `{"badge":"تشكيلة جديدة","button_link":"#s-newest"}`, nil},
		{"hero badge too long", domain.SectionHero, `{"badge":"` + strings.Repeat("ع", 31) + `"}`, []string{"badge:maxLength"}},
		{"hero bad in-page link", domain.SectionHero, `{"button_link":"#x\" onclick"}`, []string{"button_link:pattern"}},

		{"grid newest", domain.SectionProductGrid, `{"source":"newest","limit":12,"title":"الأحدث"}`, nil},
		{"grid category", domain.SectionProductGrid, `{"source":"category","category_id":"` + pid + `"}`, nil},
		{"grid category missing", domain.SectionProductGrid, `{"source":"category"}`, []string{"category_id:required"}},
		{"grid manual", domain.SectionProductGrid, `{"source":"manual","product_ids":["` + pid + `"]}`, nil},
		{"grid manual empty", domain.SectionProductGrid, `{"source":"manual","product_ids":[]}`, []string{"product_ids:minItems"}},
		{"grid no source", domain.SectionProductGrid, `{}`, []string{"source:required"}},
		{"grid limit", domain.SectionProductGrid, `{"source":"newest","limit":100}`, []string{"limit:maximum"}},
		// JSON Schema counts 8.0 as an integer; the renderer's Go types do
		// not, so such settings must be refused, not stored and then
		// fail to render.
		{"grid fractional-looking integer", domain.SectionProductGrid, `{"source":"newest","limit":8.0}`, []string{"limit:type"}},
		{"reviews exponent rating", domain.SectionReviews, `{"items":[{"name":"a","rating":5e0,"text":"b"}]}`, []string{"items[0].rating:type"}},
		{"grid carousel", domain.SectionProductGrid, `{"source":"newest","layout":"carousel"}`, nil},
		{"grid bad layout", domain.SectionProductGrid, `{"source":"newest","layout":"masonry"}`, []string{"layout:enum"}},
		{"grid duplicate ids", domain.SectionProductGrid, `{"source":"manual","product_ids":["` + pid + `","` + pid + `"]}`, []string{"product_ids:uniqueItems"}},

		{"landing", domain.SectionProductLanding, `{"product_id":"` + pid + `","highlights":["قطن ١٠٠٪","شحن مجاني"],"show_description":false,"cta_label":"اطلب الآن"}`, nil},
		{"landing no product", domain.SectionProductLanding, `{}`, []string{"product_id:required"}},
		{"landing empty highlight", domain.SectionProductLanding, `{"product_id":"` + pid + `","highlights":[""]}`, []string{"highlights[0]:minLength"}},

		{"reviews", domain.SectionReviews, `{"items":[{"name":"منى","location":"القاهرة","rating":5,"text":"ممتاز\nوصل بسرعة"}]}`, nil},
		{"reviews bad item", domain.SectionReviews, `{"items":[{"name":"","rating":6}]}`, []string{"items[0].name:minLength", "items[0].rating:maximum", "items[0].text:required"}},
		{"reviews none", domain.SectionReviews, `{"items":[]}`, []string{"items:minItems"}},

		{"faq", domain.SectionFAQ, `{"title":"أسئلة","items":[{"question":"متى يصل الطلب؟","answer":"خلال ٣ أيام"}]}`, nil},
		{"faq missing answer", domain.SectionFAQ, `{"items":[{"question":"q"}]}`, []string{"items[0].answer:required"}},

		{"badges defaults", domain.SectionTrustBadges, `{"items":[{"icon":"cod"},{"icon":"shipping","title":"شحن ٢٤ ساعة"}]}`, nil},
		{"badges bad icon", domain.SectionTrustBadges, `{"items":[{"icon":"skull"}]}`, []string{"items[0].icon:enum"}},
		{"badges too many", domain.SectionTrustBadges, `{"items":[{"icon":"cod"},{"icon":"cod"},{"icon":"cod"},{"icon":"cod"},{"icon":"cod"},{"icon":"cod"},{"icon":"cod"}]}`, []string{"items:maxItems"}},

		{"countdown", domain.SectionCountdown, `{"title":"ينتهي العرض خلال","ends_at":"` + future + `"}`, nil},
		{"countdown bad date", domain.SectionCountdown, `{"ends_at":"next friday"}`, []string{"ends_at:format"}},
		{"countdown lower-case date", domain.SectionCountdown, `{"ends_at":"2026-12-31t22:00:00z"}`, []string{"ends_at:invalid"}},
		{"countdown leap second", domain.SectionCountdown, `{"ends_at":"2026-12-31T23:59:60Z"}`, []string{"ends_at:invalid"}},
		{"countdown missing", domain.SectionCountdown, `{}`, []string{"ends_at:required"}},

		{"category tiles top", domain.SectionCategoryTiles, `{}`, nil},
		{"category tiles listed", domain.SectionCategoryTiles, `{"title":"تسوّق حسب التصنيف","category_ids":["` + pid + `"],"limit":6}`, nil},
		{"category tiles too many", domain.SectionCategoryTiles, `{"limit":13}`, []string{"limit:maximum"}},
		{"category tiles bad id", domain.SectionCategoryTiles, `{"category_ids":["x"]}`, []string{"category_ids[0]:pattern"}},

		{"promo tiles", domain.SectionPromoTiles, `{"items":[{"label":"لفترة محدودة","title":"خصم 20% على التيشيرتات","link":"/c/tshirts","tone":"amber"},{"title":"إكسسوارات الشتاء","link":"/c/winter","tone":"green"}]}`, nil},
		{"promo tiles none", domain.SectionPromoTiles, `{"items":[]}`, []string{"items:minItems"}},
		{"promo tiles four", domain.SectionPromoTiles, `{"items":[{"title":"a","link":"/"},{"title":"b","link":"/"},{"title":"c","link":"/"},{"title":"d","link":"/"}]}`, []string{"items:maxItems"}},
		{"promo tile needs a link", domain.SectionPromoTiles, `{"items":[{"title":"a"}]}`, []string{"items[0].link:required"}},
		{"promo tile javascript", domain.SectionPromoTiles, `{"items":[{"title":"a","link":"javascript:x"}]}`, []string{"items[0].link:pattern"}},
		{"promo tile bad tone", domain.SectionPromoTiles, `{"items":[{"title":"a","link":"/","tone":"neon"}]}`, []string{"items[0].tone:enum"}},

		{"whatsapp", domain.SectionWhatsAppCTA, `{"title":"محتاج مساعدة؟","text":"كلّمنا على واتساب"}`, nil},
		{"whatsapp line break", domain.SectionWhatsAppCTA, `{"text":"a\nb"}`, []string{"text:pattern"}},

		{"not an object", domain.SectionFAQ, `[1,2]`, []string{":type"}},
		{"not JSON", domain.SectionFAQ, `{`, []string{":invalid"}},
		{"unknown type", domain.SectionType("iframe"), `{}`, []string{":invalid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fieldCodes(r.ValidateSettings(tt.typ, json.RawMessage(tt.raw)))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDefaultsAreValid: the pages a new store starts with pass the same
// validation as a merchant's save.
func TestDefaultsAreValid(t *testing.T) {
	t.Parallel()
	r := registry(t)
	for _, s := range domain.DefaultHome().Sections {
		if fes := r.ValidateSettings(s.Type, s.Settings); len(fes) > 0 {
			t.Fatalf("default %s section: %v", s.ID, fes)
		}
		if _, err := sections.Decode(s.Type, s.Settings); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecode(t *testing.T) {
	t.Parallel()
	v, err := sections.Decode(domain.SectionProductGrid, json.RawMessage(`{"source":"manual","product_ids":["`+pid+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	g, ok := v.(*sections.ProductGrid)
	if !ok || g.Source != "manual" || len(g.ProductIDs) != 1 || g.ProductIDs[0].String() != pid || g.Limit != 8 {
		t.Fatalf("grid %+v", v)
	}
	v, err = sections.Decode(domain.SectionHero, json.RawMessage(`{}`))
	if h, isHero := v.(*sections.Hero); err != nil || !isHero || h.Align != "center" {
		t.Fatalf("hero defaults: %v %+v", err, v)
	}
	v, err = sections.Decode(domain.SectionCountdown, json.RawMessage(`{"ends_at":"2026-12-31T22:00:00Z"}`))
	if c, isCountdown := v.(*sections.Countdown); err != nil || !isCountdown || c.EndsAt.Year() != 2026 {
		t.Fatalf("countdown: %v %+v", err, v)
	}
	for _, typ := range domain.SectionTypes() {
		if _, err = sections.Decode(typ, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	if _, err = sections.Decode("iframe", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown type decoded")
	}
	if _, err = sections.Decode(domain.SectionFAQ, json.RawMessage(`{"items":"x"}`)); err == nil {
		t.Fatal("bad settings decoded")
	}
}

// FuzzValidateSettings: settings come from the merchant's browser. Any
// bytes must yield a bounded list of field errors, never a panic, and
// whatever passes must decode for the renderer.
func FuzzValidateSettings(f *testing.F) {
	r := registry(f)
	f.Add("hero", `{"heading":"x","button_label":"y","button_link":"/p/a"}`)
	f.Add("product_grid", `{"source":"manual","product_ids":["`+pid+`"]}`)
	f.Add("reviews", `{"items":[{"name":"a","rating":5,"text":"b"}]}`)
	f.Add("countdown", `{"ends_at":"2026-12-31T22:00:00Z"}`)
	f.Add("faq", `{"items":[{"question":"q","answer":"a"}],"x":{"y":[1,2,{"z":null}]}}`)
	f.Add("trust_badges", `{"items":[{"icon":"cod","title":"\u0000"}]}`)
	f.Add("category_tiles", `{"category_ids":["`+pid+`"],"limit":3}`)
	f.Add("promo_tiles", `{"items":[{"label":"x","title":"y","link":"#s-a","tone":"green"}]}`)
	f.Add("whatsapp_cta", `{"title":"x","text":"y"}`)
	f.Fuzz(func(t *testing.T, typ, raw string) {
		st := domain.SectionType(typ)
		fes := r.ValidateSettings(st, json.RawMessage(raw))
		if len(fes) > 60 {
			t.Fatalf("%d field errors: the report must stay bounded", len(fes))
		}
		if len(fes) == 0 {
			if _, err := sections.Decode(st, json.RawMessage(raw)); err != nil {
				t.Fatalf("valid %s settings %q do not decode: %v", typ, raw, err)
			}
		}
	})
}
