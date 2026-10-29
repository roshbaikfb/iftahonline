package app_test

import (
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
)

// TestFeeds: off until a key is made; a new key replaces the old one;
// turning off twice is one change; only the store's own key opens its
// feeds.
func TestFeeds(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, other := f.store(t), f.store(t)
	if key, ok, err := f.svc.FeedKey(ctx); err != nil || ok || key != "" {
		t.Fatalf("before: %q %v %v", key, ok, err)
	}
	if ok, err := f.svc.FeedAllowed(ctx, strings.Repeat("a", domain.FeedKeyLen)); err != nil || ok {
		t.Fatalf("off: %v %v", ok, err)
	}
	first, err := f.svc.RenewFeedKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.RenewFeedKey(ctx)
	if err != nil || second == first {
		t.Fatalf("renew: %q %q %v", first, second, err)
	}
	if key, ok, getErr := f.svc.FeedKey(ctx); getErr != nil || !ok || key != second {
		t.Fatalf("after renew: %q %v %v", key, ok, getErr)
	}
	theirs, err := f.svc.RenewFeedKey(other)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{second: true, first: false, theirs: false, "": false, strings.ToUpper(second): false, second[:31]: false} {
		if ok, allowErr := f.svc.FeedAllowed(ctx, key); allowErr != nil || ok != want {
			t.Errorf("FeedAllowed(%q) = %v %v, want %v", key, ok, allowErr, want)
		}
	}
	for range 2 {
		if err = f.svc.DisableFeeds(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := f.svc.FeedAllowed(ctx, second); err != nil || ok {
		t.Fatalf("after disable: %v %v", ok, err)
	}
	if ok, err := f.svc.FeedAllowed(other, theirs); err != nil || !ok {
		t.Fatalf("the other store's: %v %v", ok, err)
	}
	if got := strings.Join(f.rec.actions, ","); got != "product_feed.key_renewed,product_feed.key_renewed,product_feed.key_renewed,product_feed.disabled" {
		t.Fatalf("audit %s", got)
	}
	if f.pub.feeds != 4 {
		t.Fatalf("%d feed events, want 4", f.pub.feeds)
	}
}
