// Package stripe is the Stripe adapter (ADR-0092): a merchant outside
// Egypt takes its shoppers' cards on its own Stripe account, with its
// own keys, as Paymob's merchants do on theirs.
//
// It follows Stripe's API reference: a payment is a Checkout Session in
// payment mode (our payment ID as client_reference_id and in the
// metadata), paid on Stripe's page; its status is the session read back
// with its PaymentIntent and latest charge; refunds are POST /v1/refunds
// on the PaymentIntent. Webhooks are verified with the merchant's
// endpoint secret (Stripe-Signature, HMAC-SHA256). The contract suite
// runs against an in-process stand-in, and against Stripe's test mode
// when STRIPE_SANDBOX_* keys are set.
package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/stripe"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Name is Stripe's gateway name.
const Name = "stripe"

// SessionTTL is how long a payment's page stays open: an hour, as the
// order waits for its card payment (Stripe allows 30 minutes to a day).
const SessionTTL = time.Hour

// Options configures New.
type Options struct {
	BaseURL string        // overrides Stripe's API (tests)
	Timeout time.Duration // per request; default 10 s
	Retry   *retry.Policy // for reads; default retry.Default()
	Client  *http.Client
	Now     func() time.Time // sessions' expiry and webhooks' clock; time.Now when nil
}

// Gateway is the Stripe adapter. Stateless: each call brings the
// merchant's keys.
type Gateway struct {
	o     Options
	retry retry.Policy
	now   func() time.Time
}

var (
	_ gateway.Gateway    = (*Gateway)(nil)
	_ gateway.KeysNeeded = (*Gateway)(nil)
)

// New returns the adapter.
func New(o Options) *Gateway {
	p := retry.Default()
	if o.Retry != nil {
		p = *o.Retry
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Gateway{o: o, retry: p, now: o.Now}
}

// Name implements gateway.Gateway.
func (*Gateway) Name() string { return Name }

// NeedsKeys implements gateway.KeysNeeded: the secret (or restricted)
// key and the webhook endpoint's signing secret; Stripe has no API key
// of Paymob's kind.
func (*Gateway) NeedsKeys() gateway.Keys { return gateway.Keys{SecretKey: true, WebhookSecret: true} }

var (
	referenceRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	sessionRE   = regexp.MustCompile(`^cs_[A-Za-z0-9_]{1,250}$`)
	intentRE    = regexp.MustCompile(`^pi_[A-Za-z0-9_]{1,250}$`)
)

// keysOK reports whether creds are shaped as Stripe's keys.
func keysOK(c gateway.Credentials) bool {
	return (strings.HasPrefix(c.SecretKey, "sk_") || strings.HasPrefix(c.SecretKey, "rk_")) && strings.HasPrefix(c.WebhookSecret, "whsec_")
}

func (g *Gateway) client(c gateway.Credentials) *stripe.Client {
	return stripe.New(stripe.Options{SecretKey: c.SecretKey, BaseURL: g.o.BaseURL, Timeout: g.o.Timeout, Client: g.o.Client})
}

// gatewayErr gives the platform client's refusals this module's gateway
// errors, keeping them permanent. A decline is the shopper's, on
// Stripe's page: no call here gets one.
func gatewayErr(err error) error {
	var declined *stripe.CardError
	switch {
	case errors.Is(err, stripe.ErrAuth):
		return retry.Permanent(gateway.ErrAuth)
	case errors.Is(err, stripe.ErrRejected), errors.As(err, &declined):
		return retry.Permanent(fmt.Errorf("%w: %w", gateway.ErrRejected, err))
	}
	return err
}

// Verify implements gateway.Gateway: the keys are Stripe's, and the
// secret key lists Checkout Sessions (what a restricted key needs).
func (g *Gateway) Verify(ctx context.Context, creds gateway.Credentials) error {
	if !keysOK(creds) {
		return gateway.ErrAuth
	}
	return retry.Do(ctx, g.retry, func(ctx context.Context) error {
		_, err := g.client(creds).Do(ctx, http.MethodGet, "/v1/checkout/sessions", url.Values{"limit": {"1"}}, "", nil)
		return gatewayErr(err)
	})
}

// CreatePayment implements gateway.Gateway with a Checkout Session. Its
// idempotency key is our reference: a request whose answer was lost and
// is sent again gets the same session.
func (g *Gateway) CreatePayment(ctx context.Context, creds gateway.Credentials, req gateway.PaymentRequest) (gateway.Checkout, error) {
	if !referenceRE.MatchString(req.Reference) || req.Amount.Amount() <= 0 || req.RedirectURL == "" {
		return gateway.Checkout{}, fmt.Errorf("%w: a reference, a positive amount and a return URL are required", gateway.ErrRejected)
	}
	if !keysOK(creds) {
		return gateway.Checkout{}, gateway.ErrAuth
	}
	desc := text.Clip(req.Description, 200)
	if desc == "" {
		desc = req.Reference
	}
	form := url.Values{
		"mode": {"payment"}, "client_reference_id": {req.Reference},
		"success_url": {req.RedirectURL}, "cancel_url": {req.RedirectURL},
		"expires_at":                                    {strconv.FormatInt(g.now().Add(SessionTTL).Unix(), 10)},
		"payment_method_types[0]":                       {"card"},
		"line_items[0][quantity]":                       {"1"},
		"line_items[0][price_data][currency]":           {stripe.Currency(string(req.Amount.Currency()))},
		"line_items[0][price_data][unit_amount]":        {strconv.FormatInt(req.Amount.Amount(), 10)},
		"line_items[0][price_data][product_data][name]": {desc},
		"payment_intent_data[metadata][reference]":      {req.Reference},
		"metadata[reference]":                           {req.Reference},
	}
	if a, err := mail.ParseAddress(req.Customer.Email); err == nil && a.Name == "" && len(a.Address) <= 254 {
		form.Set("customer_email", a.Address)
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if _, err := g.client(creds).Do(ctx, http.MethodPost, "/v1/checkout/sessions", form, "payment:"+req.Reference, &out); err != nil {
		return gateway.Checkout{}, gatewayErr(err)
	}
	if !sessionRE.MatchString(out.ID) || !strings.HasPrefix(out.URL, "https://") {
		return gateway.Checkout{}, errors.New("stripe: the session has no ID or page")
	}
	return gateway.Checkout{URL: out.URL, ProviderRef: out.ID}, nil
}

// session is a Checkout Session read back with its PaymentIntent and
// that intent's latest charge.
type session struct {
	ClientReferenceID string `json:"client_reference_id"`
	Status            string `json:"status"`         // open, complete, expired
	PaymentStatus     string `json:"payment_status"` // unpaid, paid, no_payment_required
	Currency          string `json:"currency"`
	PaymentIntent     *struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		AmountReceived int64  `json:"amount_received"`
		LatestCharge   *struct {
			AmountRefunded int64 `json:"amount_refunded"`
			Created        int64 `json:"created"`
		} `json:"latest_charge"`
	} `json:"payment_intent"`
}

// FetchStatus implements gateway.Gateway: the payment's session, read
// back. A payment without a session (its creation's answer was lost, so
// no shopper has its page) is pending.
func (g *Gateway) FetchStatus(ctx context.Context, creds gateway.Credentials, ref gateway.Ref, cur money.Currency) (gateway.Status, error) {
	zero, err := money.Zero(cur)
	if err != nil {
		return gateway.Status{}, err
	}
	st := gateway.Status{State: gateway.StatePending, Amount: zero, Refunded: zero}
	if ref.ProviderRef == "" {
		return st, nil
	}
	if !sessionRE.MatchString(ref.ProviderRef) {
		return gateway.Status{}, fmt.Errorf("%w: %q is not a Stripe Checkout Session", gateway.ErrRejected, ref.ProviderRef)
	}
	var s session
	err = retry.Do(ctx, g.retry, func(ctx context.Context) error {
		code, doErr := g.client(creds).Do(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(ref.ProviderRef),
			url.Values{"expand[]": {"payment_intent.latest_charge"}}, "", &s)
		if doErr == nil && code == http.StatusNotFound {
			return retry.Permanent(fmt.Errorf("stripe: no session %s on this account", ref.ProviderRef))
		}
		return gatewayErr(doErr)
	})
	if err != nil {
		return gateway.Status{}, err
	}
	// The answer is believed only for what was asked: this reference, in
	// this currency.
	if s.ClientReferenceID != ref.Reference || s.Currency != stripe.Currency(string(cur)) {
		return gateway.Status{}, fmt.Errorf("stripe: the session of %s answered for %q in %q", ref.Reference, s.ClientReferenceID, s.Currency)
	}
	pi := s.PaymentIntent
	switch {
	case s.PaymentStatus == "paid" && pi != nil && pi.Status == "succeeded":
		st.State, st.TransactionID = gateway.StatePaid, pi.ID
		if st.Amount, err = money.New(pi.AmountReceived, cur); err != nil {
			return gateway.Status{}, err
		}
		if c := pi.LatestCharge; c != nil {
			if st.Refunded, err = money.New(c.AmountRefunded, cur); err != nil {
				return gateway.Status{}, err
			}
			st.At = time.Unix(c.Created, 0).UTC()
		}
	case s.Status == "expired", pi != nil && pi.Status == "canceled":
		// The page closed unpaid: a declined card stays on the page for
		// another try, so a payment fails when its page expires.
		st.State = gateway.StateFailed
	}
	return st, nil
}

// Refund implements gateway.Gateway on the PaymentIntent. Sent once,
// without an idempotency key: two refunds of one amount are two refunds,
// and the caller asks FetchStatus what was refunded before trying again.
func (g *Gateway) Refund(ctx context.Context, creds gateway.Credentials, transactionID string, amount money.Money) (string, error) {
	if !intentRE.MatchString(transactionID) || amount.Amount() <= 0 {
		return "", fmt.Errorf("%w: refund needs a Stripe PaymentIntent and a positive amount", gateway.ErrRejected)
	}
	var out struct {
		ID string `json:"id"`
	}
	code, err := g.client(creds).Do(ctx, http.MethodPost, "/v1/refunds", url.Values{
		"payment_intent": {transactionID}, "amount": {strconv.FormatInt(amount.Amount(), 10)},
	}, "", &out)
	if err == nil && code == http.StatusNotFound {
		err = fmt.Errorf("%w: no payment %s", gateway.ErrRejected, transactionID)
	}
	if err != nil {
		return "", gatewayErr(err)
	}
	return out.ID, nil
}

// VerifyWebhook implements gateway.Gateway: the merchant's endpoint
// secret over the body, recent.
func (g *Gateway) VerifyWebhook(creds gateway.Credentials, req gateway.WebhookRequest) (gateway.WebhookEvent, error) {
	if stripe.Verify(creds.WebhookSecret, req.Header.Get("Stripe-Signature"), req.Body, g.now()) != nil {
		return gateway.WebhookEvent{}, gateway.ErrForged
	}
	return g.ParseWebhook(req.Body)
}

// sessionEvent reports whether an event is about a payment's session;
// the merchant's endpoint subscribes to these only.
func sessionEvent(typ string) bool {
	switch typ {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded",
		"checkout.session.async_payment_failed", "checkout.session.expired":
		return true
	}
	return false
}

// ParseWebhook implements gateway.Gateway: an event about one of our
// payments' sessions. Stripe sends an event again with its ID.
func (*Gateway) ParseWebhook(body []byte) (gateway.WebhookEvent, error) {
	e, err := stripe.ParseEvent(body)
	if err != nil || !sessionEvent(e.Type) {
		return gateway.WebhookEvent{}, gateway.ErrBadWebhook
	}
	var s struct {
		Object            string `json:"object"`
		ClientReferenceID string `json:"client_reference_id"`
	}
	if err = json.Unmarshal(e.Data.Object, &s); err != nil || s.Object != "checkout.session" || !referenceRE.MatchString(s.ClientReferenceID) {
		return gateway.WebhookEvent{}, gateway.ErrBadWebhook
	}
	return gateway.WebhookEvent{EventID: e.ID, Reference: s.ClientReferenceID}, nil
}
