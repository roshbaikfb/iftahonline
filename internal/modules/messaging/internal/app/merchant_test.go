package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
)

// TestSendMerchant: a merchant notice is the platform's template on
// WhatsApp, its values made fit, and nothing of it is logged with the
// store's messages or counted in its quota (ADR-0054).
func TestSendMerchant(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	ctx := f.store(t)
	err := f.svc.SendMerchant(ctx, "+201012345678", domain.MerchantPaymentFailed, "ar",
		[]string{"متجر\nسوق", "INV-2026-000001", "682.86 EGP", "2026-11-08"})
	if err != nil {
		t.Fatal(err)
	}
	sent := f.wa.Sent()
	if len(sent) != 1 || sent[0].Template != "billing_payment_failed_v1" || sent[0].Language != "ar" || sent[0].To.Unmasked() != "+201012345678" ||
		sent[0].Params[0] != "متجر سوق" || !strings.HasPrefix(sent[0].Ref, "mn_") || len(f.sms.Sent()) != 0 {
		t.Fatalf("sent = %+v", sent)
	}
	if u, uErr := f.quota.Usage(ctx, "messages"); uErr != nil || u.Used != 0 {
		t.Fatalf("a merchant notice counted: %+v, %v", u, uErr)
	}
	for name, call := range map[string]func() error{
		"too few values": func() error {
			return f.svc.SendMerchant(ctx, "+201012345678", domain.MerchantPaid, "ar", []string{"x"})
		},
		"unknown kind": func() error { return f.svc.SendMerchant(ctx, "+201012345678", "x", "ar", nil) },
		"bad locale": func() error {
			return f.svc.SendMerchant(ctx, "+201012345678", domain.MerchantSuspended, "fr", []string{"s"})
		},
		"bad phone": func() error { return f.svc.SendMerchant(ctx, "0101", domain.MerchantSuspended, "en", []string{"s"}) },
	} {
		if err = call(); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Without WhatsApp, nothing is sent.
	if err = newFixture(t, options{noWA: true}).svc.SendMerchant(ctx, "+201012345678", domain.MerchantSuspended, "en", []string{"s"}); err != nil {
		t.Errorf("without WhatsApp: %v", err)
	}
	f.wa.FailWith(errors.New("down"))
	if err = f.svc.SendMerchant(ctx, "+201012345678", domain.MerchantSuspended, "en", []string{"s"}); err == nil {
		t.Error("a failed send: want an error")
	}
}
