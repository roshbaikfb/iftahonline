package domain_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestStatusAdvances(t *testing.T) {
	t.Parallel()
	all := []domain.Status{domain.Queued, domain.Sent, domain.Delivered, domain.Read, domain.Failed, domain.Skipped}
	allowed := map[[2]domain.Status]bool{
		{domain.Queued, domain.Sent}: true, {domain.Queued, domain.Delivered}: true, {domain.Queued, domain.Read}: true,
		{domain.Queued, domain.Failed}: true, {domain.Sent, domain.Delivered}: true, {domain.Sent, domain.Read}: true,
		{domain.Sent, domain.Failed}: true, {domain.Delivered, domain.Read}: true,
	}
	for _, from := range all {
		if !from.Valid() {
			t.Errorf("%q not valid", from)
		}
		for _, to := range all {
			if got := from.Advances(to); got != allowed[[2]domain.Status{from, to}] {
				t.Errorf("%s -> %s = %v", from, to, got)
			}
		}
	}
	if domain.Status("nope").Valid() || domain.Kind("nope").Valid() || !domain.OrderConfirmation.Valid() {
		t.Error("validity")
	}
}

func TestRef(t *testing.T) {
	t.Parallel()
	ids := ulid.NewGenerator()
	r := domain.Ref{Store: tenant.StoreID{ID: ids.New()}, Message: ids.New()}
	s := r.String()
	if len(s) != 56 {
		t.Fatalf("len = %d", len(s))
	}
	got, ok := domain.ParseRef(s)
	if !ok || got != r {
		t.Fatalf("round trip = %+v %v", got, ok)
	}
	for _, bad := range []string{"", "m1.", "m2." + s[3:], s[:40], s + "x", "m1." + r.Store.String(), strings.Replace(s, ".", "-", 2),
		"m1.00000000000000000000000000." + r.Message.String()} {
		if _, ok := domain.ParseRef(bad); ok {
			t.Errorf("ParseRef(%q) accepted", bad)
		}
	}
}

func TestButtonPayload(t *testing.T) {
	t.Parallel()
	ids := ulid.NewGenerator()
	key := domain.ButtonKey("0123456789abcdef0123456789abcdef")
	r := domain.Ref{Store: tenant.StoreID{ID: ids.New()}, Message: ids.New()}
	for _, reply := range []domain.Reply{domain.Confirm, domain.Cancel} {
		p := key.Payload(r, reply)
		if len(p) > 100 {
			t.Fatalf("payload of %d characters", len(p))
		}
		got, gotReply, ok := key.ParsePayload(p)
		if !ok || got != r || gotReply != reply {
			t.Fatalf("%s: %+v %q %v", reply, got, gotReply, ok)
		}
		// Another key, a flipped answer, or a changed message: refused.
		if _, _, ok = domain.ButtonKey("another key, another platform....").ParsePayload(p); ok {
			t.Error("payload verifies under another key")
		}
		other := domain.Ref{Store: r.Store, Message: ids.New()}
		forged := strings.Replace(p, r.Message.String(), other.Message.String(), 1)
		if _, _, ok = key.ParsePayload(forged); ok {
			t.Error("payload for another message accepted")
		}
	}
	flipped := strings.Replace(key.Payload(r, domain.Confirm), ".c.", ".x.", 1)
	if _, _, ok := key.ParsePayload(flipped); ok {
		t.Error("confirm turned cancel accepted")
	}
	for _, bad := range []string{"", "b1.", "CONFIRM", "b1.a.b.c.d", r.String()} {
		if _, _, ok := key.ParsePayload(bad); ok {
			t.Errorf("ParsePayload(%q) accepted", bad)
		}
	}
}

func TestTemplate(t *testing.T) {
	t.Parallel()
	tpl, ok := domain.WhatsAppTemplate(domain.OrderConfirmation)
	if !ok || tpl.Name != "order_confirmation_v1" || len(tpl.Buttons) != 2 || tpl.Buttons[0] != domain.Confirm || tpl.Buttons[1] != domain.Cancel {
		t.Fatalf("template = %+v %v", tpl, ok)
	}
	if _, ok = domain.WhatsAppTemplate("other"); ok || domain.TemplateParams("other", domain.Vars{}) != nil {
		t.Fatal("unknown kind has a template")
	}
	params := domain.TemplateParams(domain.OrderConfirmation, domain.Vars{
		CustomerName: "  منى\n\tأحمد     علي ", StoreName: "متجر سوق", OrderRef: "7P0R1S2T", Total: "520.00 EGP",
	})
	want := []string{"منى أحمد علي", "متجر سوق", "7P0R1S2T", "520.00 EGP"}
	for i := range want {
		if params[i] != want[i] {
			t.Errorf("param %d = %q, want %q", i+1, params[i], want[i])
		}
	}
	long := domain.TemplateParams(domain.OrderConfirmation, domain.Vars{CustomerName: strings.Repeat("ن", 100)})
	if utf8.RuneCountInString(long[0]) != 60 || long[1] != "-" {
		t.Fatalf("long/empty = %q", long)
	}
}

func TestSMSTexts(t *testing.T) {
	t.Parallel()
	for _, l := range []string{domain.Arabic, domain.English} {
		body := domain.DefaultSMS(domain.OrderConfirmation, l)
		if !domain.ValidSMS(domain.OrderConfirmation, body) {
			t.Fatalf("%s default invalid: %q", l, body)
		}
	}
	if !domain.ValidLocale("ar") || domain.ValidLocale("fr") || domain.DefaultSMS("other", "ar") != "" {
		t.Fatal("locales")
	}
	tests := map[string]bool{
		"{{store}}: {{url}}":                  true,
		"":                                    false,
		"no link":                             false,
		"{{url}} {{phone}}":                   false,
		"{{url}} {{name}":                     false,
		strings.Repeat("ا", 395) + "{{url}}":  false,
		strings.Repeat("ا", 390) + " {{url}}": true,
		"{{url}}\xff":                         false,
	}
	for body, want := range tests {
		if got := domain.ValidSMS(domain.OrderConfirmation, body); got != want {
			t.Errorf("ValidSMS(%.20q) = %v", body, got)
		}
	}
	got := domain.RenderSMS(domain.DefaultSMS(domain.OrderConfirmation, domain.English), domain.Vars{
		CustomerName: "Mona", StoreName: "Souq", OrderRef: "7P0R", Total: "520.00 EGP", ConfirmURL: "https://souq.example.com/en/o/tok",
	})
	if got != "Souq: Hi Mona, please confirm your order 7P0R (520.00 EGP): https://souq.example.com/en/o/tok" {
		t.Fatalf("rendered %q", got)
	}
}

func FuzzParseRef(f *testing.F) {
	ids := ulid.NewGenerator()
	f.Add(domain.Ref{Store: tenant.StoreID{ID: ids.New()}, Message: ids.New()}.String())
	f.Add("m1..")
	f.Fuzz(func(t *testing.T, s string) {
		r, ok := domain.ParseRef(s)
		if ok && r.String() != s {
			// Parsing is case-insensitive for ULIDs; the canonical form must parse to the same ref.
			if again, ok2 := domain.ParseRef(r.String()); !ok2 || again != r {
				t.Fatalf("%q -> %+v does not round-trip", s, r)
			}
		}
	})
}

func FuzzParsePayload(f *testing.F) {
	ids := ulid.NewGenerator()
	key := domain.ButtonKey("0123456789abcdef0123456789abcdef")
	f.Add(key.Payload(domain.Ref{Store: tenant.StoreID{ID: ids.New()}, Message: ids.New()}, domain.Confirm))
	f.Add("b1.x.y.c.zzz")
	f.Fuzz(func(t *testing.T, s string) {
		r, reply, ok := key.ParsePayload(s)
		if !ok {
			return
		}
		if reply != domain.Confirm && reply != domain.Cancel {
			t.Fatalf("reply %q", reply)
		}
		if key.Payload(r, reply) != s {
			t.Fatalf("accepted %q, which we would not have issued", s)
		}
	})
}
