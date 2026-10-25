package web_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// bestsellersHome is a home page with one bestsellers grid.
func bestsellersHome(f checkoutFixture) {
	f.content.home = domain.Page{Kind: domain.KindHome, Status: domain.StatusPublished, Sections: []domain.Section{
		{ID: "best", Type: domain.SectionProductGrid, Settings: json.RawMessage(`{"source":"bestsellers","limit":8}`)},
	}}
}

// TestBestsellersGrid: the grid shows what sold most, in that order, once
// four products for sale have sold; until then the newest, titled "new
// arrivals". The bestsellers cost one orders call, and their products
// come in the one catalog call by ID.
func TestBestsellersGrid(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	bestsellersHome(f)
	var sold []ulid.ID
	for i := range 5 {
		p := product(fmt.Sprintf("منتج %d", i), fmt.Sprintf("sold-%d", i), 10000, true)
		f.catalog.products = append(f.catalog.products, p)
		sold = append(sold, p.ID)
	}
	f.orders.log.mu.Lock()
	f.orders.log.best = []ulid.ID{sold[3], sold[0], sold[4], sold[1]}
	f.orders.log.mu.Unlock()
	body := f.get(t, "/").Body.String()
	if !strings.Contains(body, "<h2>الأكثر مبيعاً</h2>") {
		t.Fatal("no bestsellers title")
	}
	order := []int{strings.Index(body, "sold-3"), strings.Index(body, "sold-0"), strings.Index(body, "sold-4"), strings.Index(body, "sold-1")}
	for i := 1; i < len(order); i++ {
		if order[i-1] < 0 || order[i] < order[i-1] {
			t.Fatalf("not in the order sold: %v", order)
		}
	}
	if strings.Contains(body, "sold-2") || f.catalog.calls0("byIDs") != 1 || f.catalog.calls0("list") != 0 || f.orders.log.bestCalls != 1 {
		t.Fatalf("calls: byIDs %d, list %d, bestsellers %d", f.catalog.calls0("byIDs"), f.catalog.calls0("list"), f.orders.log.bestCalls)
	}
}

func TestBestsellersFallBackToNewest(t *testing.T) {
	t.Parallel()
	for name, set := range map[string]func(f checkoutFixture, sold []ulid.ID){
		"three sold": func(f checkoutFixture, sold []ulid.ID) { f.orders.log.best = sold[:3] },
		"four sold, one off sale": func(f checkoutFixture, sold []ulid.ID) {
			f.orders.log.best = sold[:4]
			f.catalog.products[len(f.catalog.products)-4].Variants[0].Available = false
		},
		"orders down": func(f checkoutFixture, sold []ulid.ID) {
			f.orders.log.best, f.orders.log.bestErr = sold, errors.New("db down")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newCheckoutFixture(t)
			bestsellersHome(f)
			var sold []ulid.ID
			for i := range 4 {
				p := product(fmt.Sprintf("منتج %d", i), fmt.Sprintf("sold-%d", i), 10000, true)
				f.catalog.products = append(f.catalog.products, p)
				sold = append(sold, p.ID)
			}
			f.orders.log.mu.Lock()
			set(f, sold)
			f.orders.log.mu.Unlock()
			body := f.get(t, "/").Body.String()
			if !strings.Contains(body, "<h2>وصل حديثًا</h2>") || strings.Contains(body, "الأكثر مبيعاً</h2>") {
				t.Fatal("too few bestsellers: the newest, titled new arrivals")
			}
			if f.catalog.calls0("list") != 1 {
				t.Fatalf("%d newest queries, want 1", f.catalog.calls0("list"))
			}
		})
	}
	// A store that takes no orders has no bestsellers.
	f := newFixture(t)
	f.content.home = domain.Page{Kind: domain.KindHome, Status: domain.StatusPublished, Sections: []domain.Section{
		{ID: "best", Type: domain.SectionProductGrid, Settings: json.RawMessage(`{"source":"bestsellers"}`)},
	}}
	if !strings.Contains(f.get(t, "/").Body.String(), "<h2>وصل حديثًا</h2>") {
		t.Fatal("no orders: the newest")
	}
}
