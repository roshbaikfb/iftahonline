package search

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Pinned to deploy/compose/docker-compose.yml.
const (
	meiliImage = "getmeili/meilisearch:v1.55.0"
	masterKey  = "test-master-key-0123456789abcdef"
)

func startMeili(t *testing.T) *Client {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{
		Image:        meiliImage,
		Env:          map[string]string{"MEILI_MASTER_KEY": masterKey, "MEILI_NO_ANALYTICS": "true", "MEILI_ENV": "development"},
		ExposedPorts: []string{"7700/tcp"},
		WaitingFor:   wait.ForHTTP("/health").WithPort("7700/tcp").WithStartupTimeout(time.Minute),
	}})
	if c != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	}
	if err != nil {
		t.Fatal(err)
	}
	url, err := c.PortEndpoint(ctx, "7700/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	cl := New(url, masterKey, "products", 20*time.Second)
	for range 2 { // idempotent
		if err := cl.EnsureIndex(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return cl
}

func doc(store tenant.StoreID, id ulid.ID, title, status string, cats ...string) Doc {
	return Doc{ID: id.String(), StoreID: store.String(), Title: title, Status: status, CategoryIDs: cats, PriceMinor: 100,
		Currency: "EGP", CreatedAt: id.Time().Unix(), SearchTitle: domain.NormalizeSearch(title), SearchText: ""}
}

func ids(r Result) []string {
	out := make([]string, len(r.Hits))
	for i, h := range r.Hits {
		out[i] = h.Title
	}
	slices.Sort(out)
	return out
}

// TestTenantIsolation is the P2 acceptance test: search never returns
// another store's products, whatever the query.
func TestTenantIsolation(t *testing.T) {
	cl := startMeili(t)
	gen := ulid.NewGenerator()
	a, b := tenant.StoreID{ID: gen.New()}, tenant.StoreID{ID: gen.New()}
	ctxA, ctxB := tenant.WithStore(t.Context(), a), tenant.WithStore(t.Context(), b)
	catID := gen.New()
	cat := catID.String()
	shoeA, shirtA := gen.New(), gen.New()
	shoeB := gen.New()
	if err := cl.Upsert(ctxA, []Doc{
		doc(a, shoeA, "أحذية رياضية", "active", cat),
		doc(a, shirtA, "قميص قطن", "draft"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := cl.Upsert(ctxB, []Doc{doc(b, shoeB, "أحذية رياضية فاخرة", "active", cat)}); err != nil {
		t.Fatal(err)
	}

	for _, q := range []Query{
		{Text: ""}, {Text: domain.NormalizeSearch("احذيه")}, {Text: domain.NormalizeSearch("فاخرة")},
		{Text: `" OR store_id = "` + b.String()}, {Text: b.String()}, {Status: "active"}, {CategoryID: &catID},
	} {
		q.Limit = 50
		res, err := cl.Search(ctxA, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		for _, h := range res.Hits {
			if h.StoreID != a.String() || h.ID == shoeB.String() {
				t.Fatalf("query %+v returned store B's product %s", q, h.ID)
			}
		}
	}

	// Arabic forms meet: the merchant wrote "أحذية", the shopper types "احذيه".
	res, err := cl.Search(ctxA, Query{Text: domain.NormalizeSearch("احذيه"), Limit: 10})
	if err != nil || !slices.Equal(ids(res), []string{"أحذية رياضية"}) {
		t.Fatalf("arabic search = %v %v", ids(res), err)
	}
	if res, _ = cl.Search(ctxB, Query{Text: domain.NormalizeSearch("إحذية"), Limit: 10}); !slices.Equal(ids(res), []string{"أحذية رياضية فاخرة"}) {
		t.Fatalf("store B search = %v", ids(res))
	}
	if res, _ = cl.Search(ctxA, Query{Status: "draft", Limit: 10}); !slices.Equal(ids(res), []string{"قميص قطن"}) {
		t.Fatalf("status filter = %v", ids(res))
	}

	// A cannot index into B, nor delete B's documents.
	if err = cl.Upsert(ctxA, []Doc{doc(b, gen.New(), "planted", "active")}); !errors.Is(err, ErrForeignDoc) {
		t.Fatalf("foreign upsert: %v", err)
	}
	if err = cl.Delete(ctxA, []ulid.ID{shoeB, shoeA}); err != nil {
		t.Fatal(err)
	}
	if res, _ = cl.Search(ctxB, Query{Limit: 10}); len(res.Hits) != 1 {
		t.Fatalf("A deleted B's document: %v", ids(res))
	}
	if res, _ = cl.Search(ctxA, Query{Limit: 10}); !slices.Equal(ids(res), []string{"قميص قطن"}) {
		t.Fatalf("A's delete = %v", ids(res))
	}

	if _, err = cl.Search(t.Context(), Query{}); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("search without tenant: %v", err)
	}
}
