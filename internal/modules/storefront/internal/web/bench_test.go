package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

// BenchmarkProductPage is the storefront's hottest path: a product page
// for one store (ADR-0018). "warm" is what a shopper usually gets from an
// instance (page in the memory cache); "cold" renders from scratch on
// every request (store view and page rebuilt; the catalog is a fake, so
// this is the cost of our own code, not the database). Both report p95.
func BenchmarkProductPage(b *testing.B) {
	for _, mode := range []string{"warm", "cold", "free-warm", "free-cold"} {
		b.Run(mode, func(b *testing.B) {
			var opts []func(*web.Deps)
			if strings.HasPrefix(mode, "free-") { // a free store's ads (ADR-0062)
				opts = append(opts, withAds(mixed(), fakePlan{}))
			}
			f := newFixture(b, opts...)
			if rec := f.get(b, "/p/cotton-tee"); rec.Code != http.StatusOK {
				b.Fatalf("status %d", rec.Code)
			}
			durations := make([]time.Duration, 0, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if strings.HasSuffix(mode, "cold") {
					f.shop.Invalidate(f.store)
				}
				start := time.Now()
				rec := f.get(b, "/p/cotton-tee")
				durations = append(durations, time.Since(start))
				if rec.Code != http.StatusOK {
					b.Fatalf("status %d", rec.Code)
				}
			}
			b.StopTimer()
			slices.Sort(durations)
			p95 := durations[len(durations)*95/100]
			b.ReportMetric(float64(p95.Microseconds())/1000, "p95-ms")
		})
	}
}

// BenchmarkQuickOrder is the one-step form's submit through our own code:
// same-origin check, form parsing, the limits (a fake limiter), the
// order port (a fake) and the redirect. The real order's database work
// is measured by make loadtest-orders.
func BenchmarkQuickOrder(b *testing.B) {
	ord := fakeOrders{log: &orderLog{}}
	f := newFixture(b, func(d *web.Deps) {
		d.Orders, d.Limiter = ord, &countLimiter{used: map[string]int{}, allow: map[string]int{}}
	})
	form := url.Values{"variant": {f.shirt.Variants[0].ID.String()}, "qty": {"1"}, "key": {"bench"}, "name": {"منى أحمد"},
		"phone": {"01012345678"}, "governorate": {"EG-C"}, "city": {"مدينة نصر"}, "address": {"12 شارع عباس العقاد"}}.Encode()
	durations := make([]time.Duration, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/order", strings.NewReader(form))
		rq.Host = host
		rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rq.Header.Set("Origin", "https://"+host)
		rec := httptest.NewRecorder()
		start := time.Now()
		f.h.ServeHTTP(rec, rq)
		durations = append(durations, time.Since(start))
		if rec.Code != http.StatusSeeOther {
			b.Fatalf("status %d", rec.Code)
		}
	}
	b.StopTimer()
	slices.Sort(durations)
	b.ReportMetric(float64(durations[len(durations)*95/100].Microseconds())/1000, "p95-ms")
}
