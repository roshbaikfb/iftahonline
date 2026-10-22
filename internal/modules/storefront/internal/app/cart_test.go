package app_test

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeVariants is the catalog: what it holds is for sale, and counts
// calls so tests can see the cart reads it once.
type fakeVariants struct {
	mu    sync.Mutex
	vs    map[ulid.ID]catalog.SellableVariant
	calls int
}

func (f *fakeVariants) SellableVariants(_ context.Context, ids []ulid.ID) ([]catalog.SellableVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	var out []catalog.SellableVariant
	for _, id := range ids {
		if v, ok := f.vs[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *fakeVariants) put(v catalog.SellableVariant) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vs[v.VariantID] = v
}

func (f *fakeVariants) remove(id ulid.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.vs, id)
}

func sellable(t *testing.T, title string, price int64, tracked bool, onHand int) catalog.SellableVariant {
	t.Helper()
	p, err := money.New(price, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.SellableVariant{VariantID: ulid.NewGenerator().New(), ProductID: ulid.NewGenerator().New(), ProductTitle: title,
		ProductSlug: "p", Price: p, Stock: catalog.Stock{Tracked: tracked, OnHand: onHand}}
}

func newCartFixture(t *testing.T) (fixture, *fakeVariants) {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	reg, err := sections.New()
	if err != nil {
		t.Fatal(err)
	}
	opts, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ids := ulid.NewGenerator()
	fv := &fakeVariants{vs: map[ulid.ID]catalog.SellableVariant{}}
	f := fixture{svc: app.NewService(app.Deps{DB: pool, IDs: ids, Sections: reg, Variants: fv, Carts: store.NewCarts(rdb)}), log: &log{}, rdb: rdb}
	f.ctxA = createStore(t, pool, ids)
	f.ctxB = createStore(t, pool, ids)
	return f, fv
}

func wantConflict(t *testing.T, err error, code string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestCartLifecycle(t *testing.T) {
	t.Parallel()
	f, fv := newCartFixture(t)
	ctx := f.ctxA
	tee := sellable(t, "تيشيرت", 15000, true, 5)
	hat := sellable(t, "كاب", 9900, false, 0)
	fv.put(tee)
	fv.put(hat)

	if v, err := f.svc.Cart(ctx, ""); err != nil || len(v.Lines) != 0 || v.Subtotal != nil {
		t.Fatalf("no cart: %+v %v", v, err)
	}
	tok, err := f.svc.AddToCart(ctx, "", tee.VariantID, 2)
	if err != nil || tok == "" {
		t.Fatalf("first add: %q %v", tok, err)
	}
	again, err := f.svc.AddToCart(ctx, tok, tee.VariantID, 1) // same line: adds up
	if err != nil || again != tok {
		t.Fatalf("second add: %v (token changed: %v)", err, again != tok)
	}
	if _, err = f.svc.AddToCart(ctx, tok, hat.VariantID, 20); err != nil {
		t.Fatal(err)
	}
	fv.calls = 0
	v, err := f.svc.Cart(ctx, tok)
	if err != nil || len(v.Lines) != 2 || v.Units != 23 || fv.calls != 1 {
		t.Fatalf("cart %+v, %v, %d catalog calls", v, err, fv.calls)
	}
	if l := v.Lines[0]; l.ProductTitle != "تيشيرت" || l.Qty != 3 || l.Total.Amount() != 45000 || !l.Available {
		t.Fatalf("line %+v", l)
	}
	if v.Subtotal == nil || v.Subtotal.Amount() != 45000+20*9900 {
		t.Fatalf("subtotal %v", v.Subtotal)
	}
	if n, _ := f.svc.CartUnits(ctx, tok); n != 23 {
		t.Fatalf("units %d", n)
	}

	// Stock and limits.
	_, err = f.svc.AddToCart(ctx, tok, tee.VariantID, 3) // 6 > 5 on hand
	wantConflict(t, err, "insufficient_stock")
	if v, _ = f.svc.Cart(ctx, tok); v.Lines[0].Qty != 3 {
		t.Fatalf("a refused add must change nothing: %d", v.Lines[0].Qty)
	}
	_, err = f.svc.AddToCart(ctx, tok, hat.VariantID, 21)
	wantFields(t, err, "qty:range")
	if _, err = f.svc.AddToCart(ctx, tok, hat.VariantID, 5); err != nil { // capped at 20, not refused
		t.Fatal(err)
	}
	_, err = f.svc.AddToCart(ctx, tok, ulid.NewGenerator().New(), 1)
	wantConflict(t, err, "variant_unavailable")

	// Updates.
	if err = f.svc.UpdateCartLine(ctx, tok, tee.VariantID, 1); err != nil {
		t.Fatal(err)
	}
	err = f.svc.UpdateCartLine(ctx, tok, tee.VariantID, 9)
	wantConflict(t, err, "insufficient_stock")
	if err = f.svc.UpdateCartLine(ctx, tok, hat.VariantID, 0); err != nil {
		t.Fatal(err)
	}
	if v, _ = f.svc.Cart(ctx, tok); len(v.Lines) != 1 || v.Lines[0].Qty != 1 {
		t.Fatalf("after updates %+v", v.Lines)
	}
	if err = f.svc.UpdateCartLine(ctx, "", tee.VariantID, 1); err != nil {
		t.Fatalf("no cart is nothing to update: %v", err)
	}

	// A product that stops selling drops out of the cart's view; a
	// stock drop shows as unavailable.
	tee.Stock.OnHand = 0
	fv.put(tee)
	if v, _ = f.svc.Cart(ctx, tok); v.Lines[0].Available {
		t.Fatal("an out-of-stock line must show as unavailable")
	}
	fv.remove(tee.VariantID)
	if v, _ = f.svc.Cart(ctx, tok); len(v.Lines) != 0 || v.Subtotal != nil {
		t.Fatalf("unsellable lines must drop out: %+v", v)
	}
}

func TestCartIsolation(t *testing.T) {
	t.Parallel()
	f, fv := newCartFixture(t)
	tee := sellable(t, "Tee", 100, false, 0)
	fv.put(tee)
	tok, err := f.svc.AddToCart(f.ctxA, "", tee.VariantID, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Store A's token opens nothing in store B.
	if v, _ := f.svc.Cart(f.ctxB, tok); len(v.Lines) != 0 {
		t.Fatalf("store B reads A's cart: %+v", v)
	}
	if n, _ := f.svc.CartUnits(f.ctxB, tok); n != 0 {
		t.Fatal("store B counts A's cart")
	}
	if err = f.svc.UpdateCartLine(f.ctxB, tok, tee.VariantID, 0); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.svc.Cart(f.ctxA, tok); len(v.Lines) != 1 {
		t.Fatal("store B changed A's cart")
	}
	// Adding in B with A's token makes B's own cart and a new token.
	tokB, err := f.svc.AddToCart(f.ctxB, tok, tee.VariantID, 1)
	if err != nil || tokB == tok {
		t.Fatalf("B's add: %v, same token %v", err, tokB == tok)
	}
	// Junk and forged tokens open nothing.
	forged, _ := authtoken.New()
	if v, _ := f.svc.Cart(f.ctxA, forged); len(v.Lines) != 0 {
		t.Fatal("a forged token opened a cart")
	}
}

func TestCartFull(t *testing.T) {
	t.Parallel()
	f, fv := newCartFixture(t)
	var tok authtoken.Token
	for range domain.MaxCartLines {
		v := sellable(t, "Item", 100, false, 0)
		fv.put(v)
		var err error
		if tok, err = f.svc.AddToCart(f.ctxA, tok, v.VariantID, 1); err != nil {
			t.Fatal(err)
		}
	}
	extra := sellable(t, "One more", 100, false, 0)
	fv.put(extra)
	_, err := f.svc.AddToCart(f.ctxA, tok, extra.VariantID, 1)
	wantConflict(t, err, "cart_full")
	if n, _ := f.svc.CartUnits(f.ctxA, tok); n != domain.MaxCartLines {
		t.Fatalf("units %d after a refused add", n)
	}
}

// TestCartInRedis: a cart is one Redis hash per store and token hash with
// a TTL that every change renews (ADR-0022). Redis never holds the
// token itself; an expired cart is simply gone, and the next add starts
// a new one; placing the order clears it.
func TestCartInRedis(t *testing.T) {
	t.Parallel()
	f, fv := newCartFixture(t)
	ctx := f.ctxA
	tee := sellable(t, "Tee", 100, false, 0)
	fv.put(tee)
	tok, err := f.svc.AddToCart(ctx, "", tee.VariantID, 1)
	if err != nil {
		t.Fatal(err)
	}
	store, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := f.rdb.Keys(ctx, "sf:cart:"+store.String()+":*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("cart keys %v %v", keys, err)
	}
	if strings.Contains(keys[0], string(tok)) || !strings.HasSuffix(keys[0], hex.EncodeToString(tok.Hash())) {
		t.Fatalf("key %q must name the token's hash, never the token", keys[0])
	}
	ttl, err := f.rdb.TTL(ctx, keys[0]).Result()
	if err != nil || ttl < domain.CartTTL-time.Minute || ttl > domain.CartTTL {
		t.Fatalf("ttl %v %v", ttl, err)
	}
	// Every change renews the TTL.
	if err = f.rdb.Expire(ctx, keys[0], time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.UpdateCartLine(ctx, tok, tee.VariantID, 3); err != nil {
		t.Fatal(err)
	}
	if ttl, _ = f.rdb.TTL(ctx, keys[0]).Result(); ttl < domain.CartTTL-time.Minute {
		t.Fatalf("ttl not renewed: %v", ttl)
	}
	// Expired: nothing is left, and adding starts a new cart and token.
	if err = f.rdb.Del(ctx, keys[0]).Err(); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.svc.Cart(ctx, tok); len(v.Lines) != 0 {
		t.Fatalf("expired cart still shows %+v", v)
	}
	tok2, err := f.svc.AddToCart(ctx, tok, tee.VariantID, 1)
	if err != nil || tok2 == tok {
		t.Fatalf("add after expiry: %v, same token %v", err, tok2 == tok)
	}
	// The order clears it.
	if err = f.svc.ClearCart(ctx, tok2); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.svc.CartUnits(ctx, tok2); n != 0 {
		t.Fatalf("units after clearing: %d", n)
	}
	if err = f.svc.ClearCart(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// Removing a line that is not there, or updating one, changes nothing.
	tok3, _ := f.svc.AddToCart(ctx, "", tee.VariantID, 2)
	other := sellable(t, "Other", 100, false, 0)
	fv.put(other)
	if err = f.svc.UpdateCartLine(ctx, tok3, other.VariantID, 4); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.svc.Cart(ctx, tok3); len(v.Lines) != 1 || v.Units != 2 {
		t.Fatalf("updating a missing line added it: %+v", v)
	}
}
