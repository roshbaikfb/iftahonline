package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/web"
)

func i64(v int64) *int64 { return &v }

// fakePlans sells in pounds only, as the platform does until dollar
// prices are set; calls counts the reads.
type fakePlans struct {
	calls atomic.Int64
	err   error
}

func (f *fakePlans) PublicOffers(_ context.Context, currency string) (billing.Offers, error) {
	f.calls.Add(1)
	if f.err != nil {
		return billing.Offers{}, f.err
	}
	o := billing.Offers{Currency: currency, Features: []billing.Feature{{Key: "products", Kind: "limit", NameAr: "المنتجات"}, {Key: "profit", Kind: "flag", NameAr: "لوحة الربح الصافي"}}}
	plan := func(code, kind, name string, trial int32, grants billing.Grants) billing.Offer {
		of := billing.Offer{}
		of.Code, of.Kind, of.NameAr, of.TrialDays, of.Grants = code, billing.Kind(kind), name, trial, grants
		return of
	}
	free := plan("free", "free", "مجانية", 0, billing.Grants{"products": {Value: 50}})
	pays := plan("pays", "pays", "ادفع حسب مبيعاتك", 0, billing.Grants{"products": {Value: 1000}, "profit": {Value: 1}})
	merchant := plan("merchant", "subscription", "تاجر", 14, billing.Grants{"products": {Value: 500}, "profit": {Value: 1}})
	brand := plan("brand", "subscription", "براند", 14, billing.Grants{"products": {Unlimited: true}, "profit": {Value: 1}})
	if currency == "EGP" {
		pays.Fee = &billing.CurrencyFee{Currency: "EGP", FeeRule: billing.FeeRule{RateBps: 250}}
		merchant.Monthly, merchant.Annual = i64(29900), i64(299000)
		brand.Monthly = i64(149900)
	}
	o.Plans = []billing.Offer{free, pays, merchant, brand}
	return o, nil
}

type fixture struct {
	site  *web.Site
	plans *fakePlans
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	plans := &fakePlans{}
	s, err := web.New(web.Deps{Plans: plans, FontFaces: "@font-face{}", FontPreload: []string{"/assets/f.woff2"}, Host: "iftahonline.com", HTTPS: true,
		DashboardURL: "https://app.iftahonline.com/", WhatsApp: "+201153450716", CountryHeader: "CF-IPCountry",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{site: s, plans: plans}
}

func (f fixture) get(t *testing.T, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.site.ServeHTTP(rec, r)
	return rec
}

func wantContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
}

var ldRE = regexp.MustCompile(`<script type="application/ld\+json">(.*?)</script>`)

// ld parses a page's JSON-LD blocks by @type.
func ld(t *testing.T, body string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, m := range ldRE.FindAllStringSubmatch(body, -1) {
		var v map[string]any
		if err := json.Unmarshal([]byte(m[1]), &v); err != nil {
			t.Fatalf("JSON-LD does not parse: %v\n%s", err, m[1])
		}
		out[v["@type"].(string)] = v
	}
	return out
}

// TestHome: the home page, for search engines and assistants: its title
// and description, its canonical URL, its schema.org data, and the ways
// to sign up and to talk to us.
func TestHome(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := f.get(t, "/")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=300, s-maxage=3600" {
		t.Fatalf("home: %d %v", rec.Code, rec.Header())
	}
	wantContains(t, body, `<html lang="ar" dir="rtl">`, `<title>افتح أونلاين | منصة متاجر إلكترونية للدفع عند الاستلام</title>`,
		`<link rel="canonical" href="https://iftahonline.com/">`, `<meta property="og:url" content="https://iftahonline.com/">`,
		`<link rel="preload" href="/assets/f.woff2" as="font" type="font/woff2" crossorigin>`,
		`href="https://app.iftahonline.com/signup"`, `href="https://app.iftahonline.com/login"`,
		`href="https://wa.me/201153450716?text=`, `+20 115 345 0716`, "موصى بها", "299 ج.م", "2.5٪", "مثال توضيحي")
	if strings.Contains(body, "الأكثر اختيارًا") {
		t.Error("a popularity claim without data")
	}
	data := ld(t, body)
	org := data["Organization"]
	if org == nil || org["name"] != "افتح أونلاين" || !strings.Contains(strings.Join(toStrings(org["alternateName"]), ","), "iftahonline") {
		t.Fatalf("Organization = %v", org)
	}
	if parent := org["parentOrganization"].(map[string]any); parent["name"] != "Codezilo Ltd" {
		t.Fatalf("parent = %v", parent)
	}
	app := data["SoftwareApplication"]
	offers, _ := app["offers"].([]any)
	if app == nil || len(offers) != 3 { // free, merchant, brand: pay as you sell has no monthly price
		t.Fatalf("SoftwareApplication = %v", app)
	}
	if o := offers[1].(map[string]any); o["price"] != "299" || o["priceCurrency"] != "EGP" {
		t.Fatalf("an offer = %v", o)
	}
	if faq := data["FAQPage"]; faq == nil || len(faq["mainEntity"].([]any)) != 5 {
		t.Fatalf("FAQPage = %v", faq)
	}
	if data["WebSite"] == nil {
		t.Fatal("no WebSite")
	}
	// No script, and the inline style is the one the policy allows.
	if strings.Contains(body, "<script>") || strings.Contains(body, ` style="`) {
		t.Fatal("inline script or style attribute: the policy blocks them")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "style-src 'sha256-") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// TestPricingCurrency: pounds for Egypt and an unknown country, dollars
// elsewhere; a visitor may switch; until dollar prices are set, a
// dollar visitor is told so, not shown pounds. The page is never kept
// at the edge, and plans are read once per currency for a while.
func TestPricingCurrency(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, tt := range []struct {
		country, query string
		usd            bool
	}{{"EG", "", false}, {"", "", false}, {"SA", "", true}, {"AE", "", true}, {"US", "", true}, {"SA", "?currency=EGP", false}, {"EG", "?currency=usd", true}} {
		rec := f.get(t, "/pricing"+tt.query, "CF-IPCountry", tt.country)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" || rec.Header().Get("Vary") != "CF-IPCountry" {
			t.Fatalf("%s%s: %d %v", tt.country, tt.query, rec.Code, rec.Header())
		}
		if tt.usd {
			wantContains(t, body, "<strong>الدولار الأمريكي</strong>", "الأسعار بالدولار قريبًا", "قريبًا")
			if strings.Contains(body, "ج.م") && !strings.Contains(body, "0 ج.م") {
				t.Errorf("%s%s: pounds shown to a dollar visitor", tt.country, tt.query)
			}
		} else {
			wantContains(t, body, "<strong>الجنيه المصري</strong>", "299 ج.م", "2,990 ج.م", "1,499 ج.م", "تجربة مجانية 14 يومًا", "بلا حد")
		}
		wantContains(t, body, `<link rel="canonical" href="https://iftahonline.com/pricing">`)
	}
	if n := f.plans.calls.Load(); n != 2 {
		t.Fatalf("plans read %d times, want once per currency", n)
	}
}

func TestFAQAndNotFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := f.get(t, "/faq")
	body := rec.Body.String()
	data := ld(t, body)
	if rec.Code != http.StatusOK || len(data["FAQPage"]["mainEntity"].([]any)) != 10 || data["BreadcrumbList"] == nil {
		t.Fatalf("faq: %d %v", rec.Code, data)
	}
	wantContains(t, body, "افتح اونلاين", "iftahonline")
	rec = f.get(t, "/nope")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `<meta name="robots" content="noindex">`) ||
		strings.Contains(rec.Body.String(), `rel="canonical"`) {
		t.Fatalf("not found: %d", rec.Code)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	post := httptest.NewRecorder()
	f.site.ServeHTTP(post, r)
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", post.Code)
	}
}

// TestCrawlers: robots.txt allows search engines and AI assistants by
// name and points to the sitemap; the sitemap lists the pages; llms.txt
// tells assistants what the platform is, and the full one its plans and
// answers.
func TestCrawlers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	robots := f.get(t, "/robots.txt").Body.String()
	wantContains(t, robots, "User-agent: *\nAllow: /", "User-agent: GPTBot\nAllow: /", "User-agent: Google-Extended\nAllow: /",
		"User-agent: PerplexityBot", "User-agent: ClaudeBot", "Sitemap: https://iftahonline.com/sitemap.xml")
	if strings.Contains(robots, "Disallow") {
		t.Fatal("robots.txt disallows something")
	}
	sitemap := f.get(t, "/sitemap.xml").Body.String()
	wantContains(t, sitemap, "<loc>https://iftahonline.com/</loc>", "<loc>https://iftahonline.com/pricing</loc>", "<loc>https://iftahonline.com/faq</loc>", "<lastmod>")
	short := f.get(t, "/llms.txt").Body.String()
	wantContains(t, short, "# افتح أونلاين (iftahonline)", "افتح اون لاين", "eftahonline", "[الأسعار](https://iftahonline.com/pricing)", "llms-full.txt")
	full := f.get(t, "/llms-full.txt").Body.String()
	wantContains(t, full, "## الباقات في مصر", "تاجر: 299 ج.م في الشهر", "2.5٪ من كل طلب يُسلَّم", "### بأي عملة أدفع الاشتراك؟")
}

// TestPlansDown: the pages that show prices say so plainly when billing
// cannot be read, and are not cached; the others still work.
func TestPlansDown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plans.err = errors.New("db down")
	for _, path := range []string{"/", "/pricing"} {
		if rec := f.get(t, path); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v", path, rec.Code, rec.Header())
		}
	}
	if rec := f.get(t, "/faq"); rec.Code != http.StatusOK {
		t.Errorf("faq: %d", rec.Code)
	}
}

func TestNewNeedsItsDependencies(t *testing.T) {
	t.Parallel()
	if _, err := web.New(web.Deps{}); err == nil {
		t.Fatal("no dependencies accepted")
	}
}
