package web_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
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
	o := billing.Offers{Currency: currency, Features: []billing.Feature{{Key: "products", Kind: "limit", NameAr: "المنتجات", NameEn: "Products"},
		{Key: "profit", Kind: "flag", NameAr: "لوحة الربح الصافي", NameEn: "Net profit dashboard"}}}
	plan := func(code, kind, name, nameEn string, trial int32, grants billing.Grants) billing.Offer {
		of := billing.Offer{}
		of.Code, of.Kind, of.NameAr, of.NameEn, of.TrialDays, of.Grants = code, billing.Kind(kind), name, nameEn, trial, grants
		return of
	}
	free := plan("free", "free", "مجانية", "Free", 0, billing.Grants{"products": {Value: 50}, "staff": {Value: 1}})
	pays := plan("pays", "pays", "ادفع حسب مبيعاتك", "Pay as you sell", 0, billing.Grants{"products": {Value: 1000}, "profit": {Value: 1}})
	merchant := plan("merchant", "subscription", "تاجر", "Merchant", 14, billing.Grants{"products": {Value: 500}, "profit": {Value: 1}})
	brand := plan("brand", "subscription", "براند", "Brand", 14, billing.Grants{"products": {Unlimited: true}, "profit": {Value: 1}})
	if currency == "EGP" {
		pays.Fee = &billing.CurrencyFee{Currency: "EGP", FeeRule: billing.FeeRule{RateBps: 250}}
		merchant.Monthly, merchant.Annual = i64(29900), i64(299000)
		brand.Monthly = i64(149900)
	}
	o.Plans = []billing.Offer{free, pays, merchant, brand}
	return o, nil
}

type fixture struct {
	site    *web.Site
	plans   *fakePlans
	content *fakeContent
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	plans, content := &fakePlans{}, newFakeContent()
	s, err := web.New(web.Deps{Plans: plans, Content: content, ImagesOrigin: "https://img.iftahonline.com/",
		Images: func(key string, size int, format string) string {
			return fmt.Sprintf("https://img.iftahonline.com/sig/%d/%s.%s", size, key, format)
		},
		FontFaces: "@font-face{}", FontPreload: []string{"/assets/f.woff2"}, Host: "iftahonline.com", HTTPS: true,
		DashboardURL: "https://app.iftahonline.com/", WhatsApp: "+201153450716", CountryHeader: "CF-IPCountry",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{site: s, plans: plans, content: content}
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
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=300, s-maxage=600" {
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

// TestCompressionAndIcon: pages and crawler files go gzipped to a client
// that takes it (a phone's first paint waits for them), plain to one
// that does not; the brand mark answers /favicon.ico; the policy lets the
// page fetch its own origin (robots.txt checks) and nothing else.
func TestCompressionAndIcon(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, path := range []string{"/", "/robots.txt", "/llms-full.txt"} {
		plain := f.get(t, path)
		zipped := f.get(t, path, "Accept-Encoding", "gzip, br")
		if plain.Header().Get("Content-Encoding") != "" || zipped.Header().Get("Content-Encoding") != "gzip" ||
			!strings.Contains(zipped.Header().Get("Vary"), "Accept-Encoding") {
			t.Fatalf("%s: plain %v, zipped %v", path, plain.Header(), zipped.Header())
		}
		zr, err := gzip.NewReader(zipped.Body)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(zr)
		if err != nil || string(got) != plain.Body.String() {
			t.Fatalf("%s: unzipped differs (%v)", path, err)
		}
		if path == "/" && zipped.Body.Len() > plain.Body.Len()/3 {
			t.Errorf("home zipped %d of %d bytes", zipped.Body.Len(), plain.Body.Len())
		}
	}
	icon := f.get(t, "/favicon.ico")
	if icon.Code != http.StatusOK || icon.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(icon.Body.String(), "<svg") {
		t.Fatalf("favicon: %d %v", icon.Code, icon.Header())
	}
	if csp := f.get(t, "/").Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") || strings.Contains(csp, "script-src") {
		t.Fatalf("CSP = %q", csp)
	}
}

// allPages are every indexable page, as the sitemap lists them.
func allPages(t *testing.T, f fixture) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(`<loc>https://iftahonline.com([^<]*)</loc>`).FindAllStringSubmatch(f.get(t, "/sitemap.xml").Body.String(), -1) {
		out = append(out, m[1])
	}
	return out
}

// TestEveryPage: each page the sitemap lists, in Arabic and in English,
// answers, names its own canonical URL, its other language and a
// description, keeps to the policy (no script, no style attribute), and
// speaks of what a merchant gains, never of how the platform is built
// (the owner's rule).
func TestEveryPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	paths := allPages(t, f)
	if len(paths) != 26 { // 13 pages in each language
		t.Fatalf("sitemap lists %d pages: %v", len(paths), paths)
	}
	for _, path := range paths {
		rec := f.get(t, path)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", path, rec.Code)
			continue
		}
		wantContains(t, body, `<link rel="canonical" href="https://iftahonline.com`+path+`">`, `<meta name="description" content="`, "<h1>",
			`<link rel="alternate" hreflang="ar" href="https://iftahonline.com`, `<link rel="alternate" hreflang="en" href="https://iftahonline.com/en`,
			`<link rel="alternate" hreflang="x-default" href="https://iftahonline.com`)
		if strings.Contains(body, "<script>") || strings.Contains(body, ` style="`) {
			t.Errorf("%s: inline script or style attribute", path)
		}
		for _, word := range []string{"معزول", "عزل", "قاعدة بيانات", "قواعد البيانات", "سيرفر", "خادم", "برمجة متقدمة",
			"isolat", "database", "server", "Postgres", "tenant"} {
			if strings.Contains(body, word) {
				t.Errorf("%s: technical wording %q", path, word)
			}
		}
		if path != "/" && path != "/en" && ld(t, body)["BreadcrumbList"] == nil {
			t.Errorf("%s: no BreadcrumbList", path)
		}
	}
}

// TestFeaturePages: each feature has its page, reached from the
// features index, the home page and the footer; its trail is home →
// features → it, its questions are FAQ data, and it links the others.
func TestFeaturePages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	index := f.get(t, "/features").Body.String()
	home := f.get(t, "/").Body.String()
	for _, slug := range []string{"whatsapp-confirmation", "fake-orders-protection", "net-profit", "shipping-cod", "online-store", "marketing"} {
		link := `href="/features/` + slug + `"`
		if !strings.Contains(index, link) {
			t.Errorf("index: no link to %s", slug)
		}
		rec := f.get(t, "/features/"+slug)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=300, s-maxage=600" {
			t.Fatalf("%s: %d %v", slug, rec.Code, rec.Header())
		}
		wantContains(t, body, `<a href="/features" aria-current="true">المميزات</a>`, `<a href="/features">المميزات</a> / <span>`)
		if n := strings.Count(body, `class="card link" href="/features/`); n != 5 {
			t.Errorf("%s: %d related links, want 5", slug, n)
		}
		if strings.Contains(body, `class="card link" `+link) {
			t.Errorf("%s: links itself", slug)
		}
		data := ld(t, body)
		trail, _ := data["BreadcrumbList"]["itemListElement"].([]any)
		if len(trail) != 3 || trail[2].(map[string]any)["item"] != "https://iftahonline.com/features/"+slug {
			t.Errorf("%s: trail %v", slug, trail)
		}
		if data["FAQPage"] == nil {
			t.Errorf("%s: no FAQPage", slug)
		}
	}
	for _, slug := range []string{"whatsapp-confirmation", "fake-orders-protection", "net-profit"} {
		if !strings.Contains(home, `class="more" href="/features/`+slug+`"`) {
			t.Errorf("home: no link to %s", slug)
		}
	}
	wantContains(t, f.get(t, "/features/net-profit").Body.String(), "في الباقات المدفوعة")
	if rec := f.get(t, "/features/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown feature: %d", rec.Code)
	}
	if rec := f.get(t, "/featuresmarketing"); rec.Code != http.StatusNotFound {
		t.Fatalf("a path that only ends like a feature: %d", rec.Code)
	}
}

// TestAboutAndLegal: the about page names the company, its address and
// its country, as schema.org data too; the terms and the privacy policy
// are reached from every page's footer.
func TestAboutAndLegal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := f.get(t, "/about").Body.String()
	wantContains(t, body, "Codezilo Ltd", "5c New Quebec Street", "W1H 7RG", `aria-label="علم المملكة المتحدة"`, `<a href="/about" aria-current="page">`)
	data := ld(t, body)
	if data["AboutPage"] == nil || data["Organization"] == nil {
		t.Fatalf("about: %v", data)
	}
	home := f.get(t, "/").Body.String()
	wantContains(t, home, `<a href="/terms">الشروط والأحكام</a>`, `<a href="/privacy">سياسة الخصوصية</a>`)
	wantContains(t, f.get(t, "/privacy").Body.String(), "<h1>سياسة الخصوصية</h1>", "Paymob", "لا نبيع البيانات لأحد")
	wantContains(t, f.get(t, "/terms").Body.String(), "<h1>الشروط والأحكام</h1>", "Codezilo Ltd")
}

// TestEnglish: the English site lives under /en with the same pages: in
// English and left to right, with plans named and priced in English, its
// links staying in English, and each page linking its Arabic twin.
func TestEnglish(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	home := f.get(t, "/en").Body.String()
	wantContains(t, home, `<html lang="en" dir="ltr">`, "<title>iftahonline | The online store platform for cash on delivery</title>",
		`<link rel="canonical" href="https://iftahonline.com/en">`, `<meta property="og:locale" content="en_US">`,
		`href="/en/pricing"`, `href="/en/features/whatsapp-confirmation"`, `href="/" hreflang="ar" lang="ar">العربية</a>`,
		"EGP 299", "2.5%", "Merchant", "Recommended", "Example", "text=Hello%2C")
	for _, arabicWord := range []string{"ج.م", "موصى بها", "الأسعار"} {
		if strings.Contains(home, arabicWord) {
			t.Errorf("English home shows %q", arabicWord)
		}
	}
	data := ld(t, home)
	if org := data["Organization"]; org == nil || org["name"] != "iftahonline" || org["@id"] != "https://iftahonline.com/#organization" {
		t.Fatalf("Organization = %v", org)
	}
	if app := data["SoftwareApplication"]; app["inLanguage"] != "en" {
		t.Fatalf("SoftwareApplication = %v", app)
	}

	pricing := f.get(t, "/en/pricing", "CF-IPCountry", "SA").Body.String()
	wantContains(t, pricing, "<strong>US dollars</strong>", `href="/en/pricing?currency=EGP"`, "Dollar prices are coming soon", "$0")
	pricing = f.get(t, "/en/pricing", "CF-IPCountry", "EG").Body.String()
	wantContains(t, pricing, "EGP 2,990", "Free trial: 14 days", "Unlimited", "Net profit dashboard")
	wantContains(t, pricing, "1 team member<")

	feature := f.get(t, "/en/features/net-profit")
	trail := ld(t, feature.Body.String())["BreadcrumbList"]["itemListElement"].([]any)
	if feature.Code != http.StatusOK || trail[1].(map[string]any)["item"] != "https://iftahonline.com/en/features" {
		t.Fatalf("English feature: %d %v", feature.Code, trail)
	}
	wantContains(t, feature.Body.String(), "On paid plans", `<a href="/en/features" aria-current="true">Features</a>`)
	wantContains(t, f.get(t, "/en/privacy").Body.String(), "<h1>Privacy policy</h1>", "We never sell data")
	wantContains(t, f.get(t, "/en/about").Body.String(), `aria-label="Flag of the United Kingdom"`, "Codezilo Ltd")
	wantContains(t, f.get(t, "/pricing").Body.String(), `href="/en/pricing" hreflang="en" lang="en">English</a>`)

	for _, path := range []string{"/en/nope", "/en/features/nope", "/en/robots.txt", "/english"} {
		if rec := f.get(t, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	sitemap := f.get(t, "/sitemap.xml").Body.String()
	wantContains(t, sitemap, `xmlns:xhtml="http://www.w3.org/1999/xhtml"`,
		`<loc>https://iftahonline.com/en/pricing</loc><lastmod>`+"2026-11-02"+`</lastmod><xhtml:link rel="alternate" hreflang="ar" href="https://iftahonline.com/pricing"/>`)
	wantContains(t, f.get(t, "/llms.txt").Body.String(), "## In English", "[Pricing](https://iftahonline.com/en/pricing)")
}

// TestConsoleContent: what the console sets shows on the site: both
// WhatsApp numbers, the email and the profiles in every page's footer
// and in the schema.org data, the company's number linked to its public
// record, and the FAQ as edited (an Arabic-only question stays off the
// English site).
func TestConsoleContent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.content.set(func(c *domain.Content) {
		c.Settings = domain.Settings{WhatsApp: "+201153450716", WhatsAppIntl: "+447700900123", Email: "hello@iftahonline.com", CompanyNumber: "12345678",
			Socials: map[domain.Social]string{domain.Instagram: "https://instagram.com/iftahonline", domain.Facebook: "https://facebook.com/iftahonline"}}
		c.FAQs = append(c.FAQs, domain.FAQ{QuestionAr: "سؤال بالعربية فقط؟", AnswerAr: "إجابته."})
	}, nil)

	for _, path := range []string{"/features", "/en/pricing"} {
		wantContains(t, f.get(t, path).Body.String(), `href="https://wa.me/447700900123?text=`, "+447700900123", `href="mailto:hello@iftahonline.com"`,
			`<a href="https://facebook.com/iftahonline" rel="noopener me" target="_blank">Facebook</a><a href="https://instagram.com/iftahonline"`)
	}
	about := f.get(t, "/about").Body.String()
	wantContains(t, about, `href="https://find-and-update.company-information.service.gov.uk/company/12345678"`, "12345678")
	org := ld(t, about)["Organization"]
	if org["email"] != "hello@iftahonline.com" || len(org["contactPoint"].([]any)) != 3 || len(org["sameAs"].([]any)) != 2 {
		t.Fatalf("Organization = %v", org)
	}
	if id := org["parentOrganization"].(map[string]any)["identifier"].(map[string]any); id["value"] != "12345678" {
		t.Fatalf("company number = %v", id)
	}

	if n := len(ld(t, f.get(t, "/faq").Body.String())["FAQPage"]["mainEntity"].([]any)); n != 11 {
		t.Fatalf("Arabic FAQ has %d questions, want 11", n)
	}
	en := f.get(t, "/en/faq").Body.String()
	if n := len(ld(t, en)["FAQPage"]["mainEntity"].([]any)); n != 10 || strings.Contains(en, "سؤال بالعربية فقط") {
		t.Fatalf("English FAQ has %d questions, want 10 in English", n)
	}
}

// TestContentDown: with the console's content never read, the pages
// built on its FAQ say so plainly (and are not cached); the others still
// work with the default number; llms.txt leaves the FAQ out.
func TestContentDown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.content.set(nil, errors.New("db down"))
	for _, path := range []string{"/", "/faq", "/en/faq"} {
		if rec := f.get(t, path); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v", path, rec.Code, rec.Header())
		}
	}
	about := f.get(t, "/about")
	if about.Code != http.StatusOK || !strings.Contains(about.Body.String(), `href="https://wa.me/201153450716?text=`) {
		t.Fatalf("about: %d", about.Code)
	}
	full := f.get(t, "/llms-full.txt").Body.String()
	if strings.Contains(full, "## أسئلة شائعة") || !strings.Contains(full, "## الباقات في مصر") {
		t.Fatal("llms-full.txt with an FAQ it could not read")
	}
}

// TestAboutContent: the console's story, founders, team, reviews, video
// and logo show on the about page (and the home page for the reviews and
// the video), in the page's language; images come from imgproxy, the
// only image origin the policy adds with YouTube's thumbnails; without
// any, the sections are absent.
func TestAboutContent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	empty := f.get(t, "/about").Body.String()
	for _, absent := range []string{`id="h-founders"`, `id="h-team"`, `id="h-reviews"`, `id="h-video"`, `class="logo"`, "og:image"} {
		if strings.Contains(empty, absent) {
			t.Errorf("without content, the about page shows %s", absent)
		}
	}

	f.content.set(func(c *domain.Content) {
		c.Identity = domain.Identity{Story: domain.Story{Ar: "بدأنا من متجر صغير.\n\nثم بنينا المنصة.", En: "We started from a small store.", VideoID: "dQw4w9WgXcQ"},
			LogoKey: "platform/site/LOGO.png", ShareKey: "platform/site/SHARE.jpeg"}
		c.People = []domain.Person{
			{Kind: domain.Founder, NameAr: "أحمد رشبيك", NameEn: "Ahmed Roshbaik", RoleAr: "المدير التقني", RoleEn: "CTO", BioAr: "بنى المنصة.",
				PhotoKey: "platform/site/AHMED.jpeg", LinkedIn: "https://www.linkedin.com/in/ahmed"},
			{Kind: domain.Founder, NameAr: "محمد علي", RoleAr: "المدير التنفيذي"},
			{Kind: domain.Team, NameAr: "سارة", RoleAr: "الدعم", BioAr: "ترد على التجار."},
		}
		c.Testimonials = []domain.Testimonial{
			{Name: "منى", StoreName: "متجر منى", StoreURL: "https://mona.example", QuoteAr: "قلّت الطلبات الوهمية عندي كثيرًا.", QuoteEn: "Fake orders dropped a lot."},
			{Name: "خالد", QuoteAr: "التأكيد على واتساب وفّر وقتي."},
		}
	}, nil)

	about := f.get(t, "/about")
	body := about.Body.String()
	wantContains(t, body, "<p>بدأنا من متجر صغير.</p><p>ثم بنينا المنصة.</p>", `id="h-founders"`, "أحمد رشبيك", "شريك مؤسس",
		`src="https://img.iftahonline.com/sig/320/platform/site/AHMED.jpeg.webp"`, `href="https://www.linkedin.com/in/ahmed"`,
		`<span class="avatar letters big" aria-hidden="true">مع</span>`, `id="h-team"`, "ترد على التجار.",
		`id="h-reviews"`, "قلّت الطلبات الوهمية", `href="https://mona.example" rel="noopener nofollow"`, "التأكيد على واتساب",
		`href="https://www.youtube.com/watch?v=dQw4w9WgXcQ"`, `src="https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg"`,
		`<img class="logo" src="https://img.iftahonline.com/sig/160/platform/site/LOGO.png.webp" alt="افتح أونلاين"`,
		`<meta property="og:image" content="https://img.iftahonline.com/sig/1200/platform/site/SHARE.jpeg.jpg">`, `content="summary_large_image"`)
	if csp := about.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data: https://i.ytimg.com https://img.iftahonline.com;") {
		t.Fatalf("CSP = %q", csp)
	}
	org := ld(t, body)["Organization"]
	founders, _ := org["founder"].([]any)
	if len(founders) != 2 || org["logo"] == nil || founders[0].(map[string]any)["jobTitle"] != "المدير التقني" {
		t.Fatalf("Organization = %v", org)
	}
	wantContains(t, f.get(t, "/").Body.String(), `id="h-reviews"`, `id="h-video"`)

	en := f.get(t, "/en/about").Body.String()
	wantContains(t, en, "We started from a small store.", "Ahmed Roshbaik", "CTO", "Co-founder", "محمد علي", "Fake orders dropped a lot.")
	for _, absent := range []string{"بدأنا من متجر", "التأكيد على واتساب", "المدير التنفيذي"} {
		if strings.Contains(en, absent) {
			t.Errorf("the English about page shows %q", absent)
		}
	}
}
