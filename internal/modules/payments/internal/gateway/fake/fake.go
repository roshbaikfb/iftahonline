// Package fake is an in-memory payment gateway for tests and development
// (ADR-0029). Tests (and the dev shopper) pay or decline its payments
// with Pay and Decline, and build its signed webhooks with Webhook. Its
// webhooks are signed with HMAC-SHA256 of the body in SignatureHeader.
//
// Credentials: any API key but "" and InvalidKey works.
package fake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Name is the fake gateway's name. It is not "fake": the fake carrier
// (shipping) has that name, and webhook providers are named uniquely.
const Name = "fakepay"

// InvalidKey is the API key the fake refuses.
const InvalidKey = "invalid-key"

// SignatureHeader carries the fake's webhook signature.
const SignatureHeader = "X-Fake-Signature"

// DefaultCheckoutBase is where the fake's hosted pages are, unless
// NewWithCheckout says otherwise.
const DefaultCheckoutBase = "https://pay.fake.test/checkout/"

// Gateway is the fake gateway.
type Gateway struct {
	mu       sync.Mutex
	seq      int
	base     string
	payments map[string]*payment // by key|reference
	byTxn    map[string]*payment
}

type payment struct {
	key, ref  string
	redirect  string
	amount    money.Money
	state     gateway.State
	txn       string
	refunded  money.Money
	at        time.Time
	changes   int
	refundSeq int
}

// New returns an empty fake gateway.
func New() *Gateway { return NewWithCheckout(DefaultCheckoutBase) }

// NewWithCheckout returns an empty fake gateway whose hosted pages are
// base + the payment's reference (the development page, ADR-0030).
func NewWithCheckout(base string) *Gateway {
	return &Gateway{base: base, payments: map[string]*payment{}, byTxn: map[string]*payment{}}
}

var _ gateway.Gateway = (*Gateway)(nil)

// Name implements gateway.Gateway.
func (*Gateway) Name() string { return Name }

func auth(creds gateway.Credentials) error {
	if creds.APIKey == "" || creds.APIKey == InvalidKey {
		return gateway.ErrAuth
	}
	return nil
}

// Verify implements gateway.Gateway.
func (*Gateway) Verify(_ context.Context, creds gateway.Credentials) error { return auth(creds) }

// CreatePayment implements gateway.Gateway; creating twice with one
// reference returns the first checkout.
func (g *Gateway) CreatePayment(_ context.Context, creds gateway.Credentials, req gateway.PaymentRequest) (gateway.Checkout, error) {
	if err := auth(creds); err != nil {
		return gateway.Checkout{}, err
	}
	if req.Reference == "" || req.Amount.Amount() <= 0 || req.NotificationURL == "" {
		return gateway.Checkout{}, fmt.Errorf("%w: reference, a positive amount and a notification URL are required", gateway.ErrRejected)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := creds.APIKey + "|" + req.Reference
	if _, ok := g.payments[k]; !ok {
		zero, err := money.Zero(req.Amount.Currency())
		if err != nil {
			return gateway.Checkout{}, err
		}
		g.payments[k] = &payment{key: creds.APIKey, ref: req.Reference, redirect: req.RedirectURL, amount: req.Amount, state: gateway.StatePending,
			refunded: zero, at: time.Now().UTC()}
	}
	return gateway.Checkout{URL: g.base + req.Reference, ProviderRef: "fp_" + req.Reference}, nil
}

// VerifyWebhook implements gateway.Gateway.
func (*Gateway) VerifyWebhook(creds gateway.Credentials, req gateway.WebhookRequest) (gateway.WebhookEvent, error) {
	got, err := hex.DecodeString(req.Header.Get(SignatureHeader))
	if err != nil || creds.WebhookSecret == "" || !hmac.Equal(got, sign(creds.WebhookSecret, req.Body)) {
		return gateway.WebhookEvent{}, gateway.ErrForged
	}
	return (*Gateway)(nil).ParseWebhook(req.Body)
}

// ParseWebhook implements gateway.Gateway.
func (*Gateway) ParseWebhook(body []byte) (gateway.WebhookEvent, error) {
	var w webhook
	if err := json.Unmarshal(body, &w); err != nil || w.EventID == "" || w.Reference == "" {
		return gateway.WebhookEvent{}, gateway.ErrBadWebhook
	}
	return gateway.WebhookEvent{EventID: w.EventID, Reference: w.Reference}, nil
}

// FetchStatus implements gateway.Gateway.
func (g *Gateway) FetchStatus(_ context.Context, creds gateway.Credentials, reference string, cur money.Currency) (gateway.Status, error) {
	if err := auth(creds); err != nil {
		return gateway.Status{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.payments[creds.APIKey+"|"+reference]
	if !ok {
		zero, err := money.Zero(cur)
		return gateway.Status{State: gateway.StatePending, Amount: zero, Refunded: zero}, err
	}
	st := gateway.Status{State: p.state, Refunded: p.refunded, TransactionID: p.txn, At: p.at}
	var err error
	if st.Amount, err = money.Zero(cur); err != nil {
		return gateway.Status{}, err
	}
	if p.state == gateway.StatePaid {
		st.Amount = p.amount
	}
	return st, nil
}

// Refund implements gateway.Gateway.
func (g *Gateway) Refund(_ context.Context, creds gateway.Credentials, transactionID string, amount money.Money) (string, error) {
	if err := auth(creds); err != nil {
		return "", err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.byTxn[transactionID]
	if !ok || p.key != creds.APIKey || p.state != gateway.StatePaid {
		return "", fmt.Errorf("%w: no paid transaction %s", gateway.ErrRejected, transactionID)
	}
	left, err := p.amount.Sub(p.refunded)
	if err != nil {
		return "", err
	}
	if amount.Amount() <= 0 || amount.Amount() > left.Amount() {
		return "", fmt.Errorf("%w: refund %s of %s left", gateway.ErrRejected, amount, left)
	}
	if p.refunded, err = p.refunded.Add(amount); err != nil {
		return "", err
	}
	p.changes++
	p.refundSeq++
	return transactionID + "-r" + strconv.Itoa(p.refundSeq), nil
}

// Pay marks a created payment paid, as the shopper would on the hosted
// page, and returns its transaction ID.
func (g *Gateway) Pay(apiKey, reference string) (string, error) {
	return g.settle(apiKey, reference, gateway.StatePaid)
}

// Decline marks a created payment failed.
func (g *Gateway) Decline(apiKey, reference string) error {
	_, err := g.settle(apiKey, reference, gateway.StateFailed)
	return err
}

func (g *Gateway) settle(apiKey, reference string, st gateway.State) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.payments[apiKey+"|"+reference]
	if !ok {
		return "", fmt.Errorf("fake gateway: no payment %s", reference)
	}
	g.seq++
	p.state, p.at, p.txn = st, time.Now().UTC(), "ftx_"+strconv.Itoa(g.seq)
	p.changes++
	g.byTxn[p.txn] = p
	return p.txn, nil
}

// Page is what the fake's hosted page shows of a payment.
type Page struct {
	Amount   money.Money
	Pending  bool
	Redirect string // where the shopper goes back
}

// PageOf finds a payment by its reference alone, as the hosted page
// does.
func (g *Gateway) PageOf(reference string) (Page, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.byRef(reference)
	if p == nil {
		return Page{}, false
	}
	return Page{Amount: p.amount, Pending: p.state == gateway.StatePending, Redirect: p.redirect}, true
}

// Answer pays (or declines) a pending payment by its reference alone, as
// the shopper does on the hosted page.
func (g *Gateway) Answer(reference string, pay bool) error {
	g.mu.Lock()
	p := g.byRef(reference)
	pending := p != nil && p.state == gateway.StatePending
	g.mu.Unlock()
	if !pending {
		return fmt.Errorf("fake gateway: no pending payment %s", reference)
	}
	st := gateway.StateFailed
	if pay {
		st = gateway.StatePaid
	}
	_, err := g.settle(p.key, reference, st)
	return err
}

func (g *Gateway) byRef(reference string) *payment {
	for _, p := range g.payments {
		if p.ref == reference {
			return p
		}
	}
	return nil
}

type webhook struct {
	EventID     string `json:"event_id"`
	Reference   string `json:"reference"`
	AmountMinor int64  `json:"amount_minor"`
}

// Webhook returns the signed body and header value the fake sends for a
// payment's last change (the same on every retry).
func (g *Gateway) Webhook(apiKey, webhookSecret, reference string) (body []byte, signature string, err error) {
	g.mu.Lock()
	p, ok := g.payments[apiKey+"|"+reference]
	var changes int
	var amount int64
	if ok {
		changes, amount = p.changes, p.amount.Amount()
	}
	g.mu.Unlock()
	if !ok {
		return nil, "", fmt.Errorf("fake gateway: no payment %s", reference)
	}
	if body, err = json.Marshal(webhook{EventID: reference + "-" + strconv.Itoa(changes), Reference: reference, AmountMinor: amount}); err != nil {
		return nil, "", err
	}
	return body, hex.EncodeToString(sign(webhookSecret, body)), nil
}

func sign(secret string, body []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return m.Sum(nil)
}
