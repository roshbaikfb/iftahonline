package app_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
)

// TestTextCodesPlacedInOneQuery (P6 review): a webhook's batch of
// customers' texts finds the stores of their codes in one query,
// however many texts it carries, inside the webhook's request; a text
// with an unknown code or none is left out.
func TestTextCodesPlacedInOneQuery(t *testing.T) {
	t.Parallel()
	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	f := newFixture(t, options{number: "+201000000000", db: pool})
	a, b := f.store(t), f.store(t)
	owner, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	codes := map[string]tenant.StoreID{}
	for i := range 40 {
		ctx := a
		if i%2 == 1 {
			ctx = b
		}
		store, _ := tenant.FromContext(ctx)
		code := "K" + strconv.Itoa(1000000 + i)[1:] + "Q" // 8 characters of the code's alphabet
		if err = postgres.InTenantTx(ctx, owner, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, `INSERT INTO order_confirmations (store_id, order_id, status, expires_at, wa_code) VALUES ($1, $2, 'pending', $3, $4)`,
				store.ID, f.ids.New(), time.Now().Add(time.Hour), code)
			return execErr
		}); err != nil {
			t.Fatal(err)
		}
		codes[code] = store
	}
	var events []channel.Event
	for code := range codes {
		events = append(events, channel.Event{Kind: channel.TextEvent, ID: "wamid." + code, From: "+201012345678", Text: "أكّد طلبي " + code})
	}
	events = append(events,
		channel.Event{Kind: channel.TextEvent, ID: "wamid.unknown", From: "+201012345678", Text: "ZZZZZZZZ"},
		channel.Event{Kind: channel.TextEvent, ID: "wamid.none", From: "+201012345678", Text: "مرحبا"})
	w := f.wa.Webhook(events...)
	before := capture.Count("confirmation_store")
	parts, err := f.svc.PlatformProviders()[0].VerifyPlatform(t.Context(), webhooks.Request{Header: w.Header, Body: w.Body})
	if err != nil {
		t.Fatal(err)
	}
	if n := capture.Count("confirmation_store") - before; n != 1 {
		t.Fatalf("%d lookups for %d texts, want 1", n, len(events))
	}
	if len(parts) != len(codes) {
		t.Fatalf("%d parts, want %d", len(parts), len(codes))
	}
	for _, p := range parts {
		code := p.EventID[len("wamid."):]
		if want, ok := codes[code]; !ok || p.Store != want {
			t.Errorf("text %s placed in %v, want %v", code, p.Store, want)
		}
	}
}
