package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Searches reach the index only on a cache miss; these bound what a
// visitor and a store can ask of it.
var (
	searchPerIP    = ratelimit.Limit{Burst: 30, Period: time.Minute}  //nolint:gochecknoglobals // constant policy
	searchPerStore = ratelimit.Limit{Burst: 600, Period: time.Minute} //nolint:gochecknoglobals // constant policy
)

// query is what of a URL's query string a page uses, and so what its
// cache key holds: anything else (tracking parameters, junk) neither
// changes the page nor splits the cache.
type query struct {
	after string // a category page's cursor
	text  string // the search as typed (trimmed, one line), for the page
	key   string // the search in normalized form, for the cache key
	page  int    // the search's page, from 1
}

// parseQuery reads the query parts the page at rest uses.
func parseQuery(rest string, v url.Values) query {
	var q query
	if a := v.Get("after"); a != "" {
		if _, err := ulid.Parse(a); err == nil {
			q.after = a
		}
	}
	if rest != "/search" {
		return q
	}
	text := strings.Join(strings.Fields(v.Get("q")), " ")
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > catalog.MaxShopSearchLen {
		text = ""
	}
	q.text, q.key, q.page = text, arabic.NormalizeSearch(text), 1
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n >= 1 && n <= catalog.MaxShopSearchPage {
		q.page = n
	}
	return q
}

// cacheKey is the page's cache key: its language, path and the query
// parts it uses.
func (q query) cacheKey(l Locale, rest string) string {
	return string(l) + " " + rest + "?" + q.after + "&" + q.key + "&" + strconv.Itoa(q.page)
}

// allowSearch applies the search limits; a limiter failure lets the
// search through (it changes nothing, and the index has its own timeout).
func (s *Shop) allowSearch(r *http.Request, store tenant.StoreID) (bool, time.Duration) {
	if s.limiter == nil {
		return true, 0
	}
	ctx := r.Context()
	for _, c := range []struct {
		key   string
		limit ratelimit.Limit
	}{{"search:ip:" + s.clientIP(r), searchPerIP}, {"search:store:" + store.String(), searchPerStore}} {
		res, err := s.limiter.Allow(ctx, c.key, c.limit)
		if err != nil {
			s.log.WarnContext(ctx, "storefront: search limiter unavailable", "err", err)
			return true, 0
		}
		if !res.Allowed {
			return false, res.RetryAfter
		}
	}
	return true, 0
}

// search is /search: the store's active products matching the text, a
// grid page at a time. Without text it asks for some.
func (s *Shop) search(ctx context.Context, p *pageCtx, q query) (result, error) {
	if !s.searchOn {
		return s.notFound(p)
	}
	p.title, p.noindex, p.q = p.l.T(MsgSearchLabel)+" | "+p.v.name, true, q.text
	view := searchView{q: q.text}
	if q.key == "" {
		return s.render(p, http.StatusOK, searchPage(p, view))
	}
	p.title = q.text + " | " + p.v.name
	res, err := s.catalog.Search(ctx, catalog.StorefrontSearch{Text: q.text, Page: q.page})
	if errs.KindOf(err) == errs.KindValidation {
		return s.render(p, http.StatusOK, searchPage(p, view))
	}
	if errors.Is(err, catalog.ErrSearchUnavailable) {
		return s.notFound(p)
	}
	if err != nil {
		return result{}, err
	}
	view.items, view.total = res.Items, res.Total
	if res.More {
		view.more = p.localize(p.l, "/search") + "?q=" + url.QueryEscape(q.text) + "&page=" + strconv.Itoa(q.page+1)
	}
	p.tagListing() // a product edit may change what matches
	p.tagProducts(res.Items)
	return s.render(p, http.StatusOK, searchPage(p, view))
}

// searchView is the search page.
type searchView struct {
	q     string
	items []catalog.StorefrontProduct
	total int64
	more  string
}

// Results counts search results the way each language says it: نتيجة
// واحدة، نتيجتان، 3 نتائج، 11 نتيجة.
func (l Locale) Results(n int64) string {
	if l == English {
		if n == 1 {
			return "1 result"
		}
		return strconv.FormatInt(n, 10) + " results"
	}
	switch {
	case n == 1:
		return "نتيجة واحدة"
	case n == 2:
		return "نتيجتان"
	case n >= 3 && n <= 10:
		return strconv.FormatInt(n, 10) + " نتائج"
	}
	return strconv.FormatInt(n, 10) + " نتيجة"
}
