package web_test

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// BenchmarkProductPage is the storefront's hottest path: a product page
// for one store (ADR-0018). "warm" is what a shopper usually gets from an
// instance (page in the memory cache); "cold" renders from scratch on
// every request (store view and page rebuilt; the catalog is a fake, so
// this is the cost of our own code, not the database). Both report p95.
func BenchmarkProductPage(b *testing.B) {
	for _, mode := range []string{"warm", "cold"} {
		b.Run(mode, func(b *testing.B) {
			f := newFixture(b)
			if rec := f.get(b, "/p/cotton-tee"); rec.Code != http.StatusOK {
				b.Fatalf("status %d", rec.Code)
			}
			durations := make([]time.Duration, 0, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if mode == "cold" {
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
