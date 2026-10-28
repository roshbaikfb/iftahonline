package app_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/webhooktest"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func handshake(token, challenge string) url.Values {
	return url.Values{"hub.mode": {"subscribe"}, "hub.verify_token": {token}, "hub.challenge": {challenge}}
}

// plain is a provider without a URL handshake.
type plain struct{ app.Provider }

func (plain) Name() string { return "plain" }

func TestHandshake(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := tenant.WithStore(t.Context(), f.storeA)
	if got, err := f.svc.Challenge(ctx, "fake", f.connA, handshake("secret-a", "1158201444"), f.ip); err != nil || got != "1158201444" {
		t.Fatalf("handshake = %q, %v", got, err)
	}
	tests := map[string]struct {
		provider string
		conn     ulid.ID
		query    url.Values
		want     error
	}{
		"wrong token":          {"fake", f.connA, handshake("guess", "1"), app.ErrForged},
		"other store's token":  {"fake", f.connA, handshake("secret-b", "1"), app.ErrForged},
		"not subscribe":        {"fake", f.connA, url.Values{"hub.verify_token": {"secret-a"}, "hub.challenge": {"1"}}, app.ErrForged},
		"B's connection":       {"fake", f.connB, handshake("secret-b", "1"), app.ErrNotFound},
		"unknown provider":     {"nobody", f.connA, handshake("secret-a", "1"), app.ErrNotFound},
		"no handshake":         {"plain", f.connA, handshake("secret-a", "1"), app.ErrNotFound},
		"markup challenge":     {"fake", f.connA, handshake("secret-a", "<script>alert(1)</script>"), app.ErrBadChallenge},
		"empty challenge":      {"fake", f.connA, handshake("secret-a", ""), app.ErrBadChallenge},
		"too long challenge":   {"fake", f.connA, handshake("secret-a", strings.Repeat("9", domain.MaxChallenge+1)), app.ErrBadChallenge},
		"line break challenge": {"fake", f.connA, handshake("secret-a", "1\r\nSet-Cookie: x"), app.ErrBadChallenge},
	}
	if err := f.svc.Register(plain{webhooktest.New()}); err != nil {
		t.Fatal(err)
	}
	for name, tt := range tests {
		if _, err := f.svc.Challenge(ctx, tt.provider, tt.conn, tt.query, f.ip); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
	}
	if _, err := f.svc.Challenge(t.Context(), "fake", f.connA, handshake("secret-a", "1"), f.ip); !errors.Is(err, tenant.ErrNoTenant) {
		t.Errorf("no tenant: %v", err)
	}
	logs := f.log.String()
	if !strings.Contains(logs, `"reason":"handshake"`) || strings.Contains(logs, "guess") {
		t.Fatalf("handshake rejections logged wrong:\n%s", logs)
	}
}

func (f fixture) platform(t *testing.T) {
	t.Helper()
	if err := f.svc.RegisterPlatform(webhooktest.Platform{Provider: f.provider}); err != nil {
		t.Fatal(err)
	}
}

func delivery(events ...webhooktest.Event) app.Request {
	b, _ := json.Marshal(map[string]any{"events": events}) //nolint:errcheck // plain structs
	return signed([]byte(webhooktest.PlatformToken), string(b))
}

func TestPlatformHandshake(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.platform(t)
	if got, err := f.svc.ChallengePlatform(t.Context(), "fakeplatform", handshake(webhooktest.PlatformToken, "42"), f.ip); err != nil || got != "42" {
		t.Fatalf("handshake = %q, %v", got, err)
	}
	if _, err := f.svc.ChallengePlatform(t.Context(), "fakeplatform", handshake("secret-a", "42"), f.ip); !errors.Is(err, app.ErrForged) {
		t.Errorf("wrong token: %v", err)
	}
	if _, err := f.svc.ChallengePlatform(t.Context(), "fakeplatform", handshake(webhooktest.PlatformToken, "a b"), f.ip); !errors.Is(err, app.ErrBadChallenge) {
		t.Errorf("bad challenge: %v", err)
	}
	if _, err := f.svc.ChallengePlatform(t.Context(), "fake", handshake("secret-a", "42"), f.ip); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("a provider without a platform account: %v", err)
	}
}

// TestPlatformDeliverySplitsByStore: one delivery carries events of two
// stores; each lands in its own store once, replays are duplicates, and
// neither store sees the other's.
func TestPlatformDeliverySplitsByStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.platform(t)
	ctx := t.Context()
	a, b := f.storeA.String(), f.storeB.String()
	req := delivery(
		webhooktest.Event{Store: a, Connection: f.connA.String(), ID: "wamid.1"},
		webhooktest.Event{Store: b, Connection: f.connB.String(), ID: "wamid.2"},
		webhooktest.Event{Store: a, Connection: f.connA.String(), ID: "wamid.3"},
	)
	for i := range 5 {
		dup, err := f.svc.ReceivePlatform(ctx, "fakeplatform", req, f.ip)
		if err != nil || dup != (i > 0) {
			t.Fatalf("delivery %d: dup=%v err=%v", i, dup, err)
		}
	}
	if nA, nB := f.receiptCount(t, f.storeA), f.receiptCount(t, f.storeB); nA != 4 || nB != 2 {
		t.Fatalf("rows A=%d B=%d, want 4 and 2 (receipt + body each)", nA, nB)
	}
	accepted := f.provider.Accepted()
	if len(accepted) != 3 {
		t.Fatalf("jobs = %d, want 3", len(accepted))
	}
	for _, r := range accepted {
		if r.Provider != "fakeplatform" {
			t.Fatalf("receipt provider = %q", r.Provider)
		}
	}
	// The part's body is what the store's job reads, and only its store
	// reads it.
	ctxA, ctxB := tenant.WithStore(ctx, f.storeA), tenant.WithStore(ctx, f.storeB)
	r, ct, body, err := f.svc.Get(ctxA, accepted[0].ID)
	if err != nil || r.EventID != "wamid.1" || ct != "application/json" || !strings.Contains(string(body), `"wamid.1"`) || strings.Contains(string(body), "wamid.2") {
		t.Fatalf("A's part = %+v %q %s, %v", r, ct, body, err)
	}
	if _, _, _, err = f.svc.Get(ctxB, accepted[0].ID); !errors.Is(err, app.ErrReceiptAbsent) {
		t.Fatalf("B reads A's part: %v", err)
	}
}

func TestPlatformRefusals(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.platform(t)
	ctx := t.Context()
	good := webhooktest.Event{Store: f.storeA.String(), Connection: f.connA.String(), ID: "wamid.ok"}
	b, _ := json.Marshal(map[string]any{"events": []webhooktest.Event{good}}) //nolint:errcheck // plain structs

	if _, err := f.svc.ReceivePlatform(ctx, "fakeplatform", signed([]byte("secret-a"), string(b)), f.ip); !errors.Is(err, app.ErrForged) {
		t.Errorf("a store's secret on the platform route: %v", err)
	}
	if _, err := f.svc.ReceivePlatform(ctx, "fake", delivery(good), f.ip); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("no platform account: %v", err)
	}
	bad := good
	bad.ID = "has space"
	if _, err := f.svc.ReceivePlatform(ctx, "fakeplatform", delivery(good, bad), f.ip); !errors.Is(err, app.ErrBadEventID) {
		t.Errorf("a bad part: %v", err)
	}
	many := make([]webhooktest.Event, domain.MaxParts+1)
	for i := range many {
		many[i] = good
		many[i].ID = fmt.Sprintf("wamid.%d", i)
	}
	if _, err := f.svc.ReceivePlatform(ctx, "fakeplatform", delivery(many...), f.ip); !errors.Is(err, app.ErrBadEventID) {
		t.Errorf("too many parts: %v", err)
	}
	if n := f.receiptCount(t, f.storeA); n != 0 {
		t.Fatalf("refused deliveries stored %d rows", n)
	}

	// A part for a store that does not exist is dropped; the rest count.
	gone := webhooktest.Event{Store: tenant.StoreID{ID: ulid.NewGenerator().New()}.String(), Connection: f.connA.String(), ID: "wamid.gone"}
	if dup, err := f.svc.ReceivePlatform(ctx, "fakeplatform", delivery(gone, good), f.ip); err != nil || dup {
		t.Fatalf("with a gone store: dup=%v err=%v", dup, err)
	}
	if n := f.receiptCount(t, f.storeA); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	// Nothing placeable is still a 200, so the provider stops.
	if dup, err := f.svc.ReceivePlatform(ctx, "fakeplatform", delivery(gone), f.ip); err != nil || dup {
		t.Fatalf("only a gone store: dup=%v err=%v", dup, err)
	}
	if !strings.Contains(f.log.String(), "webhook part for no store dropped") {
		t.Error("dropped part not logged")
	}
}

// TestPlatformFailurePartWay: when a store's job cannot be enqueued, the
// delivery fails (the provider retries) and the retry accepts what is
// left, once.
func TestPlatformFailurePartWay(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.platform(t)
	req := delivery(
		webhooktest.Event{Store: f.storeA.String(), Connection: f.connA.String(), ID: "wamid.1"},
		webhooktest.Event{Store: f.storeB.String(), Connection: f.connB.String(), ID: "wamid.2"},
	)
	f.provider.FailAccept = errors.New("queue down")
	if _, err := f.svc.ReceivePlatform(t.Context(), "fakeplatform", req, f.ip); err == nil {
		t.Fatal("failure swallowed")
	}
	f.provider.FailAccept = nil
	if dup, err := f.svc.ReceivePlatform(t.Context(), "fakeplatform", req, f.ip); err != nil || dup {
		t.Fatalf("retry: dup=%v err=%v", dup, err)
	}
	if len(f.provider.Accepted()) != 2 {
		t.Fatalf("jobs = %d, want 2", len(f.provider.Accepted()))
	}
}

func TestPlatformRateLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, &app.Limits{
		PerIP:         ratelimit.Limit{Burst: 100, Period: time.Hour},
		PerConnection: ratelimit.Limit{Burst: 100, Period: time.Hour},
		PerPlatform:   ratelimit.Limit{Burst: 2, Period: time.Hour},
	})
	f.platform(t)
	for i := range 3 {
		req := delivery(webhooktest.Event{Store: f.storeA.String(), Connection: f.connA.String(), ID: fmt.Sprintf("wamid.%d", i)})
		_, err := f.svc.ReceivePlatform(t.Context(), "fakeplatform", req, f.ip)
		if i < 2 && err != nil {
			t.Fatal(err)
		}
		if i == 2 && errs.KindOf(err) != errs.KindRateLimited {
			t.Fatalf("third delivery: %v, want 429", err)
		}
	}
}

func TestRegisterPlatform(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.platform(t)
	if err := f.svc.RegisterPlatform(webhooktest.Platform{Provider: webhooktest.New()}); err == nil {
		t.Fatal("second platform provider of a name accepted")
	}
	if err := f.svc.RegisterPlatform(badPlatformName{webhooktest.Platform{Provider: webhooktest.New()}}); err == nil {
		t.Fatal("invalid name accepted")
	}
}

type badPlatformName struct{ webhooktest.Platform }

func (badPlatformName) Name() string { return "Bad" }
