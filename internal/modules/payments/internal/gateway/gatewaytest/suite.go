// Package gatewaytest is the contract every payment gateway adapter
// passes (ADR-0029): against the fake gateway and Paymob's in-process
// stand-in in every build, and against Paymob's test mode when its keys
// are set. Cases that need a shopper to pay (only a person can, on a
// sandbox's hosted page) run when the harness can play one.
package gatewaytest

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Harness is one adapter under test.
type Harness struct {
	Gateway  gateway.Gateway
	Creds    gateway.Credentials
	BadCreds gateway.Credentials
	Currency money.Currency
	// Pay and Decline play the shopper on the hosted page; nil when the
	// harness cannot (a sandbox), which skips the cases that need them.
	Pay     func(t *testing.T, reference string)
	Decline func(t *testing.T, reference string)
	// Webhook is the signed delivery the gateway sends for a reference's
	// last change.
	Webhook func(t *testing.T, reference string) gateway.WebhookRequest
}

// Request is a valid payment request with a fresh reference.
func Request(t *testing.T, cur money.Currency) gateway.PaymentRequest {
	t.Helper()
	amount, err := money.New(52000, cur)
	if err != nil {
		t.Fatal(err)
	}
	return gateway.PaymentRequest{Reference: ulid.NewGenerator().New().String(), Amount: amount,
		Customer:    gateway.Customer{FirstName: "منى", LastName: "أحمد", Phone: "+201012345678"},
		Description: "Order 7P0R1S2T", NotificationURL: "https://app.example.com/v1/webhooks/x/y/z", RedirectURL: "https://shop.example.com/thank-you/x"}
}

func amountOf(t *testing.T, minor int64, cur money.Currency) money.Money {
	t.Helper()
	m, err := money.New(minor, cur)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Run runs the contract.
func Run(t *testing.T, h Harness) {
	ctx := t.Context()
	g := h.Gateway

	t.Run("verify", func(t *testing.T) {
		if err := g.Verify(ctx, h.Creds); err != nil {
			t.Fatalf("valid credentials: %v", err)
		}
		if err := g.Verify(ctx, h.BadCreds); !errors.Is(err, gateway.ErrAuth) {
			t.Fatalf("bad credentials: %v, want ErrAuth", err)
		}
	})

	t.Run("create and inquire", func(t *testing.T) {
		req := Request(t, h.Currency)
		c, err := g.CreatePayment(ctx, h.Creds, req)
		if err != nil || !strings.HasPrefix(c.URL, "http") {
			t.Fatalf("create: %+v %v", c, err)
		}
		st, err := g.FetchStatus(ctx, h.Creds, req.Reference, h.Currency)
		if err != nil || st.State != gateway.StatePending || !st.Amount.IsZero() {
			t.Fatalf("status before paying: %+v %v", st, err)
		}
		if _, err = g.CreatePayment(ctx, h.BadCreds, Request(t, h.Currency)); !errors.Is(err, gateway.ErrAuth) {
			t.Fatalf("create with bad credentials: %v", err)
		}
		bad := Request(t, h.Currency)
		bad.Reference = ""
		if _, err = g.CreatePayment(ctx, h.Creds, bad); !errors.Is(err, gateway.ErrRejected) {
			t.Fatalf("no reference: %v", err)
		}
	})

	if h.Pay == nil {
		t.Log("the harness cannot pay: payment, webhook and refund cases skipped")
		return
	}
	t.Run("pay, webhook, refund", func(t *testing.T) {
		req := Request(t, h.Currency)
		if _, err := g.CreatePayment(ctx, h.Creds, req); err != nil {
			t.Fatal(err)
		}
		h.Pay(t, req.Reference)
		st, err := g.FetchStatus(ctx, h.Creds, req.Reference, h.Currency)
		if err != nil || st.State != gateway.StatePaid || st.Amount.Amount() != 52000 || st.TransactionID == "" || !st.Refunded.IsZero() {
			t.Fatalf("status after paying: %+v %v", st, err)
		}

		w := h.Webhook(t, req.Reference)
		ev, err := g.VerifyWebhook(h.Creds, w)
		if err != nil || ev.Reference != req.Reference || ev.EventID == "" {
			t.Fatalf("webhook: %+v %v", ev, err)
		}
		if again, againErr := g.VerifyWebhook(h.Creds, w); againErr != nil || again.EventID != ev.EventID {
			t.Fatalf("a retried webhook changed its event id: %+v %v", again, againErr)
		}
		tampered := w
		tampered.Body = bytes.Replace(w.Body, []byte("52000"), []byte("1"), 1)
		if _, err = g.VerifyWebhook(h.Creds, tampered); !errors.Is(err, gateway.ErrForged) {
			t.Fatalf("tampered body: %v, want ErrForged", err)
		}
		other := h.Creds
		other.WebhookSecret = "another-secret"
		if _, err = g.VerifyWebhook(other, w); !errors.Is(err, gateway.ErrForged) {
			t.Fatalf("another secret: %v, want ErrForged", err)
		}

		if _, err = g.Refund(ctx, h.Creds, st.TransactionID, amountOf(t, 52001, h.Currency)); !errors.Is(err, gateway.ErrRejected) {
			t.Fatalf("refund over the amount: %v", err)
		}
		if _, err = g.Refund(ctx, h.Creds, st.TransactionID, amountOf(t, 20000, h.Currency)); err != nil {
			t.Fatalf("partial refund: %v", err)
		}
		if _, err = g.Refund(ctx, h.Creds, st.TransactionID, amountOf(t, 32000, h.Currency)); err != nil {
			t.Fatalf("refund the rest: %v", err)
		}
		if st, err = g.FetchStatus(ctx, h.Creds, req.Reference, h.Currency); err != nil || st.Refunded.Amount() != 52000 {
			t.Fatalf("after refunds: %+v %v", st, err)
		}
		if _, err = g.Refund(ctx, h.Creds, st.TransactionID, amountOf(t, 1, h.Currency)); !errors.Is(err, gateway.ErrRejected) {
			t.Fatalf("refund past the amount: %v", err)
		}
	})

	t.Run("declined", func(t *testing.T) {
		req := Request(t, h.Currency)
		if _, err := g.CreatePayment(ctx, h.Creds, req); err != nil {
			t.Fatal(err)
		}
		h.Decline(t, req.Reference)
		if st, err := g.FetchStatus(ctx, h.Creds, req.Reference, h.Currency); err != nil || st.State != gateway.StateFailed || !st.Amount.IsZero() {
			t.Fatalf("declined: %+v %v", st, err)
		}
	})
}
