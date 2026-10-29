package storefront_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// readyImage gives a product a processed picture, as the image job
// would (no object storage here).
func readyImage(t *testing.T, s storeRef, product string) {
	t.Helper()
	id, err := ulid.Parse(product)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := tenant.WithStore(t.Context(), s.id)
	if err = postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `INSERT INTO product_images (id, store_id, product_id, status, upload_key, object_key, format, width, height, bytes)
			VALUES ($1, $2, $3, 'ready', 'up/x', 'products/x.jpg', 'jpeg', 1200, 1200, 1000)`, ulid.NewGenerator().New(), s.id.ID, id)
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
}

// TestProductFeed: the merchant turns the feeds on through the API and
// the store's host serves them under that key only: not another store's
// key, not after a new key, not once turned off.
func TestProductFeed(t *testing.T) {
	t.Parallel()
	p, members := newPlatform(t)
	tag := strings.ToLower(ulid.NewGenerator().New().String()[18:])
	a := p.createStore(t, members, "feed-a-"+tag, "متجر العسل", "ar", "EGP")
	b := p.createStore(t, members, "feed-b-"+tag, "Other", "en", "SAR")
	prod := p.call(t, a, http.MethodPost, "/products", `{"title":"عسل","slug":"honey","status":"active","variants":[{"price_minor":30000,"on_hand":5}]}`, http.StatusCreated)
	readyImage(t, a, prod["id"].(string))
	variant := prod["variants"].([]any)[0].(map[string]any)["id"].(string)

	if rec := p.visit(t, a.host, "/feeds/google.xml?key=abcdefghijklmnopqrstuvwxyz234567"); rec.Code != http.StatusNotFound {
		t.Fatalf("feeds off: %d", rec.Code)
	}
	on := p.call(t, a, http.MethodPost, "/marketing/feed", "", http.StatusOK)
	theirs := p.call(t, b, http.MethodPost, "/marketing/feed", "", http.StatusOK)
	rec := p.visit(t, a.host, on["google_path"].(string))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "<g:id>"+variant+"</g:id>") || !strings.Contains(body, "<g:price>300.00 EGP</g:price>") ||
		!strings.Contains(body, "<g:link>https://"+a.host+"/p/honey</g:link>") || !strings.Contains(body, ".jpg</g:image_link>") ||
		!strings.Contains(body, "<g:brand>متجر العسل</g:brand>") {
		t.Fatalf("feed %d:\n%s", rec.Code, body)
	}
	if rec = p.visit(t, a.host, on["meta_path"].(string)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<g:availability>in stock</g:availability>") {
		t.Fatalf("meta feed %d:\n%s", rec.Code, rec.Body)
	}
	if rec = p.visit(t, a.host, theirs["google_path"].(string)); rec.Code != http.StatusNotFound {
		t.Fatalf("store B's key on store A: %d", rec.Code)
	}
	if rec = p.visit(t, b.host, on["google_path"].(string)); rec.Code != http.StatusNotFound {
		t.Fatalf("store A's key on store B: %d", rec.Code)
	}
	if rec = p.visit(t, b.host, theirs["google_path"].(string)); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), variant) {
		t.Fatalf("store B's feed %d shows A's product", rec.Code)
	}

	renewed := p.call(t, a, http.MethodPost, "/marketing/feed", "", http.StatusOK)
	if rec = p.visit(t, a.host, on["google_path"].(string)); rec.Code != http.StatusNotFound {
		t.Fatalf("the old key after a new one: %d", rec.Code)
	}
	if rec = p.visit(t, a.host, renewed["google_path"].(string)); rec.Code != http.StatusOK {
		t.Fatalf("the new key: %d", rec.Code)
	}
	p.call(t, a, http.MethodDelete, "/marketing/feed", "", http.StatusNoContent)
	if rec = p.visit(t, a.host, renewed["google_path"].(string)); rec.Code != http.StatusNotFound {
		t.Fatalf("turned off: %d", rec.Code)
	}
}
