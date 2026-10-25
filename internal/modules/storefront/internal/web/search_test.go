package web_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

func withSearch(d *web.Deps) { d.Search = true }

func TestSearchPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withSearch)
	for i := range 25 {
		f.catalog.products = append(f.catalog.products, product(fmt.Sprintf("عطر %02d", i), fmt.Sprintf("perfume-%02d", i), 10000, true))
	}
	rec := f.get(t, "/search?q="+url.QueryEscape("  عطر  "))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	wantContains(t, body,
		"<h1>نتائج البحث عن «عطر»</h1>", "<small>25 نتيجة</small>", `<meta name="robots" content="noindex">`,
		// the header's box keeps the search
		`<input type="search" id="q" name="q" value="عطر"`,
		`<a class="btn" href="/search?q=%D8%B9%D8%B7%D8%B1&amp;page=2">عرض المزيد</a>`,
	)
	if n := strings.Count(body, `<div class="card">`); n != 24 {
		t.Fatalf("%d cards on page 1, want 24", n)
	}
	if tags := rec.Header().Get("Cache-Tag"); !strings.Contains(tags, "l-"+f.store.String()) || rec.Header().Get("Cache-Control") != "public, max-age=0, s-maxage=60" {
		t.Fatalf("cached like other public pages, renewed by product edits: %q %q", tags, rec.Header().Get("Cache-Control"))
	}
	page2 := f.get(t, "/search?q=عطر&page=2").Body.String()
	if n := strings.Count(page2, `<div class="card">`); n != 1 || strings.Contains(page2, "عرض المزيد") {
		t.Fatalf("page 2: %d cards", n)
	}
}

// TestSearchCache: one normalized search is one cache entry, whatever
// the spelling or the junk around it; the index is asked once.
func TestSearchCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withSearch)
	for _, q := range []string{"/search?q=تيشيرت", "/search?q=" + url.QueryEscape(" تيشيرت "), "/search?q=تيشيرت&utm_source=x&page=junk"} {
		if rec := f.get(t, q); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "نتيجة واحدة") {
			t.Fatalf("%s: %d", q, rec.Code)
		}
	}
	if n := f.catalog.calls0("search"); n != 1 {
		t.Fatalf("index asked %d times for one search", n)
	}
}

func TestSearchEmptyAndNothingFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withSearch)
	prompt := f.get(t, "/search").Body.String()
	wantContains(t, prompt, "<h1>ابحث في المتجر</h1>", "اكتب ما تبحث عنه", `id="search-page-q"`)
	none := f.get(t, "/search?q="+url.QueryEscape("ساعة ذهب")).Body.String()
	wantContains(t, none, "<h1>لم نجد نتائج لـ «ساعة ذهب»</h1>", "جرّب كلمة أخرى أو تصفّح التصنيفات", `<a href="/c/tshirts">تيشيرتات</a>`)
	long := f.get(t, "/search?q="+strings.Repeat("ع", 101)).Body.String()
	wantContains(t, long, "<h1>ابحث في المتجر</h1>") // too long: treated as no search
	if n := f.catalog.calls0("search"); n != 1 {
		t.Fatalf("index asked %d times; an empty or too long search must not reach it", n)
	}
}

func TestSearchOff(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if rec := f.get(t, "/search?q=x"); rec.Code != http.StatusNotFound {
		t.Fatalf("search without an index: %d", rec.Code)
	}
	if strings.Contains(f.get(t, "/").Body.String(), `role="search"`) {
		t.Fatal("a search box without search")
	}
}

func TestSearchLimits(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t, withSearch)
	f.limiter.allow["search:ip:203.0.113.7"] = 2
	for i := range 2 {
		if rec := f.get(t, fmt.Sprintf("/search?q=a%d", i)); rec.Code != http.StatusOK {
			t.Fatal(rec.Code)
		}
	}
	rec := f.get(t, "/search?q=a9")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d", rec.Code)
	}
	// A cached search costs the index nothing and is never limited.
	if rec = f.get(t, "/search?q=a0"); rec.Code != http.StatusOK {
		t.Fatalf("cached search limited: %d", rec.Code)
	}
	f.limiter.fail = true
	if rec = f.get(t, "/search?q=b"); rec.Code != http.StatusOK {
		t.Fatalf("limiter down: %d", rec.Code)
	}
}

func TestResultsWording(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		n      int64
		ar, en string
	}{{1, "نتيجة واحدة", "1 result"}, {2, "نتيجتان", "2 results"}, {3, "3 نتائج", "3 results"}, {11, "11 نتيجة", "11 results"}, {0, "0 نتيجة", "0 results"}} {
		if got := web.Arabic.Results(tt.n); got != tt.ar {
			t.Errorf("ar %d = %q", tt.n, got)
		}
		if got := web.English.Results(tt.n); got != tt.en {
			t.Errorf("en %d = %q", tt.n, got)
		}
	}
}
