// Package stripe bills merchants outside Egypt through the platform's own
// Stripe account (the UK company, ADR-0091):
//   - a merchant pays an invoice or tops up its balance on a Checkout
//     Session, which saves the card for later (setup_future_usage);
//   - the card on file is charged off session with a PaymentIntent;
//   - the billing webhook endpoint's events are believed only with its
//     signature, and Stripe is asked about the session before anything is
//     marked paid.
//
// The contract is tested against a stand-in; run it against Stripe's test
// mode before launch.
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

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/stripe"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Name is the gateway's name on cards on file and payment rules.
const Name = "stripe"

// Options configures New.
type Options struct {
	SecretKey     string
	WebhookSecret string // the billing endpoint's signing secret
	BaseURL       string // overrides Stripe's API (tests)
	Timeout       time.Duration
	Client        *http.Client
	Now           func() time.Time // the webhooks' clock; time.Now when nil
}

// Gateway charges cards on file and takes payments on Stripe's page.
type Gateway struct {
	client *stripe.Client
	secret string
	now    func() time.Time
}

var _ app.CheckoutGateway = (*Gateway)(nil)

// New returns the gateway.
func New(o Options) *Gateway {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Gateway{client: stripe.New(stripe.Options{SecretKey: o.SecretKey, BaseURL: o.BaseURL, Timeout: o.Timeout, Client: o.Client}),
		secret: o.WebhookSecret, now: o.Now}
}

var (
	// errUnknown: the charge's outcome is not known yet; the job retries
	// and asks Stripe first.
	errUnknown = errors.New("stripe: the charge is pending")
	// references are ours (an invoice's number and attempt, a checkout's):
	// safe inside a search query and a metadata value.
	referenceRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	idRE        = regexp.MustCompile(`^[a-z]{2,5}_[A-Za-z0-9_]{1,250}$`)
)

// Token is a card on file: the Stripe customer and its payment method.
func Token(customer, paymentMethod string) string { return customer + " " + paymentMethod }

func splitToken(t string) (customer, paymentMethod string, ok bool) {
	customer, paymentMethod, ok = strings.Cut(t, " ")
	return customer, paymentMethod, ok && strings.HasPrefix(customer, "cus_") && strings.HasPrefix(paymentMethod, "pm_") &&
		idRE.MatchString(customer) && idRE.MatchString(paymentMethod)
}

type paymentIntent struct {
	ID               string            `json:"id"`
	Status           string            `json:"status"`
	Amount           int64             `json:"amount"`
	AmountReceived   int64             `json:"amount_received"`
	Currency         string            `json:"currency"`
	Metadata         map[string]string `json:"metadata"`
	LastPaymentError *stripe.CardError `json:"last_payment_error"`
	PaymentMethod    json.RawMessage   `json:"payment_method"` // an ID, or the object when expanded
}

// Charge implements app.Gateway: an off-session PaymentIntent confirmed
// at once. Stripe is asked about the attempt's reference first, and the
// request carries it as its idempotency key: a charge whose answer was
// lost is found, never made twice.
func (g *Gateway) Charge(ctx context.Context, c app.Charge) (app.ChargeResult, error) {
	customer, pm, ok := splitToken(c.Token)
	if !ok || c.AmountMinor <= 0 || !referenceRE.MatchString(c.Reference) || len(c.Currency) != 3 {
		return app.ChargeResult{}, errors.New("stripe: a card on file, a positive amount, a currency and a reference are required")
	}
	var found struct {
		Data []paymentIntent `json:"data"`
	}
	if _, err := g.client.Do(ctx, http.MethodGet, "/v1/payment_intents/search", url.Values{
		"query": {"metadata['reference']:'" + c.Reference + "'"}, "limit": {"1"},
	}, "", &found); err != nil {
		return app.ChargeResult{}, err
	}
	if len(found.Data) > 0 {
		return resultOf(found.Data[0], c)
	}
	var pi paymentIntent
	_, err := g.client.Do(ctx, http.MethodPost, "/v1/payment_intents", url.Values{
		"amount": {strconv.FormatInt(c.AmountMinor, 10)}, "currency": {stripe.Currency(c.Currency)},
		"customer": {customer}, "payment_method": {pm}, "off_session": {"true"}, "confirm": {"true"},
		"description": {text.Clip("Iftah Online "+c.Reference, 200)}, "metadata[reference]": {c.Reference},
	}, "charge:"+c.Reference, &pi)
	var declined *stripe.CardError
	if errors.As(err, &declined) {
		res := app.ChargeResult{Reason: declined.Reason()}
		var id struct {
			ID string `json:"id"`
		}
		if len(declined.PaymentIntent) > 0 && json.Unmarshal(declined.PaymentIntent, &id) == nil {
			res.Reference = id.ID
		}
		return res, nil
	}
	if err != nil {
		return app.ChargeResult{}, err // a lost answer: the retry asks first
	}
	return resultOf(pi, c)
}

// resultOf reads a PaymentIntent of c's reference: approved, declined, or
// not known yet.
func resultOf(pi paymentIntent, c app.Charge) (app.ChargeResult, error) {
	if pi.Metadata["reference"] != c.Reference || pi.ID == "" {
		return app.ChargeResult{}, fmt.Errorf("stripe: the answer for %s is another payment", c.Reference)
	}
	res := app.ChargeResult{Reference: pi.ID}
	switch pi.Status {
	case "succeeded":
		if pi.AmountReceived != c.AmountMinor || pi.Currency != stripe.Currency(c.Currency) {
			return app.ChargeResult{}, fmt.Errorf("stripe: %s received %d %s, not %d %s", pi.ID, pi.AmountReceived, pi.Currency, c.AmountMinor, c.Currency)
		}
		res.Approved = true
	case "requires_payment_method", "canceled":
		res.Reason = "declined"
		if pi.LastPaymentError != nil {
			res.Reason = pi.LastPaymentError.Reason()
		}
	case "requires_action":
		// The bank asks the merchant to confirm: off session it cannot, so
		// the merchant pays on the page (which also saves a new card).
		res.Reason = "authentication_required"
	default: // processing, requires_confirmation, requires_capture
		return app.ChargeResult{}, errUnknown
	}
	return res, nil
}

// Checkout implements app.CheckoutGateway: a Checkout Session in payment
// mode for the amount, which creates a customer and saves the card for
// off-session charges. The session's ID is the checkout's: Stripe is
// asked by it (Paid). Stripe sends its events to the endpoint set in its
// dashboard, so NotificationURL is not sent.
func (g *Gateway) Checkout(ctx context.Context, c app.CheckoutRequest) (app.CheckoutPage, error) {
	if c.AmountMinor <= 0 || !referenceRE.MatchString(c.Reference) || len(c.Currency) != 3 || c.RedirectURL == "" {
		return app.CheckoutPage{}, errors.New("stripe: a positive amount, a currency, a reference and a return URL are required")
	}
	form := url.Values{
		"mode": {"payment"}, "client_reference_id": {c.Reference}, "customer_creation": {"always"},
		"success_url": {c.RedirectURL}, "cancel_url": {c.RedirectURL},
		"payment_method_types[0]":                       {"card"},
		"line_items[0][quantity]":                       {"1"},
		"line_items[0][price_data][currency]":           {stripe.Currency(c.Currency)},
		"line_items[0][price_data][unit_amount]":        {strconv.FormatInt(c.AmountMinor, 10)},
		"line_items[0][price_data][product_data][name]": {text.Clip("Iftah Online "+c.Description, 200)},
		"payment_intent_data[setup_future_usage]":       {"off_session"},
		"payment_intent_data[metadata][reference]":      {c.Reference},
		"metadata[reference]":                           {c.Reference},
	}
	if a, err := mail.ParseAddress(c.Email); err == nil && a.Name == "" && len(a.Address) <= 254 {
		form.Set("customer_email", a.Address)
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if _, err := g.client.Do(ctx, http.MethodPost, "/v1/checkout/sessions", form, "checkout:"+c.Reference, &out); err != nil {
		return app.CheckoutPage{}, err
	}
	if !strings.HasPrefix(out.ID, "cs_") || !idRE.MatchString(out.ID) || !strings.HasPrefix(out.URL, "https://") {
		return app.CheckoutPage{}, errors.New("stripe: the session has no ID or page")
	}
	return app.CheckoutPage{URL: out.URL, Session: out.ID}, nil
}

// sessionEvent reports whether an event is about a checkout's payment;
// the endpoint subscribes to these only.
func sessionEvent(typ string) bool {
	switch typ {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded",
		"checkout.session.async_payment_failed", "checkout.session.expired":
		return true
	}
	return false
}

// ParseCallback implements app.CheckoutGateway: an event is believed only
// with the endpoint's signature (Stripe-Signature, recent).
func (g *Gateway) ParseCallback(header http.Header, _ url.Values, body []byte) (app.Callback, error) {
	if stripe.Verify(g.secret, header.Get("Stripe-Signature"), body, g.now()) != nil {
		return app.Callback{}, app.ErrForgedCallback
	}
	return g.ReadCallback(body)
}

// ReadCallback implements app.CheckoutGateway: an event verified when it
// was received, read again by its processing job. Each is about one of
// our checkouts' sessions, by our reference.
func (*Gateway) ReadCallback(body []byte) (app.Callback, error) {
	e, err := stripe.ParseEvent(body)
	if err != nil || !sessionEvent(e.Type) {
		return app.Callback{}, app.ErrBadCallback
	}
	var s struct {
		Object            string `json:"object"`
		ID                string `json:"id"`
		ClientReferenceID string `json:"client_reference_id"`
	}
	if err = json.Unmarshal(e.Data.Object, &s); err != nil || s.Object != "checkout.session" || !strings.HasPrefix(s.ClientReferenceID, "bc_") ||
		!referenceRE.MatchString(s.ClientReferenceID) {
		return app.Callback{}, app.ErrBadCallback
	}
	return app.Callback{Kind: app.CallbackTransaction, EventID: e.ID, Reference: s.ClientReferenceID}, nil
}

// Paid implements app.CheckoutGateway: Stripe is asked about the
// checkout's session before anything is marked paid (CLAUDE.md). A paid
// session's card, saved for later, comes with the answer.
func (g *Gateway) Paid(ctx context.Context, q app.PaidQuery) (app.Payment, error) {
	if !strings.HasPrefix(q.Session, "cs_") || !idRE.MatchString(q.Session) {
		return app.Payment{}, fmt.Errorf("stripe: the checkout %s has no session", q.Reference)
	}
	var s struct {
		ClientReferenceID string        `json:"client_reference_id"`
		Status            string        `json:"status"`
		PaymentStatus     string        `json:"payment_status"`
		AmountTotal       int64         `json:"amount_total"`
		Currency          string        `json:"currency"`
		Customer          string        `json:"customer"`
		PaymentIntent     paymentIntent `json:"payment_intent"`
	}
	status, err := g.client.Do(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(q.Session),
		url.Values{"expand[]": {"payment_intent.payment_method"}}, "", &s)
	if err != nil {
		return app.Payment{}, err
	}
	if status == http.StatusNotFound {
		return app.Payment{}, nil
	}
	if s.ClientReferenceID != q.Reference || s.Currency != stripe.Currency(q.Currency) {
		return app.Payment{}, fmt.Errorf("stripe: the session of %s answered another payment", q.Reference)
	}
	pi := s.PaymentIntent
	paid := s.Status == "complete" && s.PaymentStatus == "paid" && s.AmountTotal == q.AmountMinor &&
		pi.Status == "succeeded" && pi.AmountReceived == q.AmountMinor
	out := app.Payment{TransactionID: pi.ID, Paid: paid}
	var m struct {
		ID   string `json:"id"`
		Card struct {
			Brand string `json:"brand"`
			Last4 string `json:"last4"` // nocarddata:allow Stripe's last four digits, shown as the card's label
		} `json:"card"`
	}
	if paid && len(pi.PaymentMethod) > 0 && json.Unmarshal(pi.PaymentMethod, &m) == nil {
		t := Token(s.Customer, m.ID)
		if _, _, ok := splitToken(t); ok {
			out.Token, out.Label = t, cardLabel(m.Card.Brand, m.Card.Last4)
		}
	}
	return out, nil
}

// cardLabel is what the merchant sees of a saved card: its brand and
// last four digits.
func cardLabel(brand, last4 string) string {
	brand = strings.ToUpper(text.Clip(strings.ToValidUTF8(brand, ""), 20))
	last := []rune(strings.ToValidUTF8(last4, ""))
	if len(last) > 4 {
		last = last[len(last)-4:]
	}
	return text.Clip(strings.TrimSpace(brand+" •••• "+string(last)), 60)
}
