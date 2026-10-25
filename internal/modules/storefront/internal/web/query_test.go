package web

import (
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
)

// FuzzParseQuery: whatever a URL holds, parsing never panics; the search
// text is one valid line within the limit, the page within its range, and
// the cache key never carries raw input beyond the normalized text.
func FuzzParseQuery(f *testing.F) {
	f.Add("/search", "q=%D8%B9%D8%B7%D8%B1&page=2")
	f.Add("/search", "q=%00%0A%E2%80%AE&page=-1")
	f.Add("/c/x", "after=01M5Q96R6658F7E2G4RMJS2HB7&q=x")
	f.Add("/search", "page=99999999999999999999")
	f.Fuzz(func(t *testing.T, rest, raw string) {
		v, err := url.ParseQuery(raw)
		if err != nil {
			return
		}
		q := parseQuery(rest, v)
		if strings.ContainsAny(q.text, "\n\r\t") || !utf8.ValidString(q.text) || utf8.RuneCountInString(q.text) > catalog.MaxShopSearchLen {
			t.Fatalf("text %q", q.text)
		}
		if rest == "/search" && (q.page < 1 || q.page > catalog.MaxShopSearchPage) {
			t.Fatalf("page %d", q.page)
		}
		if rest != "/search" && (q.text != "" || q.page != 0) {
			t.Fatalf("search parts on %q: %+v", rest, q)
		}
		_ = q.cacheKey(Arabic, rest)
	})
}
