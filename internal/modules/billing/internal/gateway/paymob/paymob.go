// Package paymob charges merchants' cards on file through the
// platform's own Paymob accounts, one per region (ADR-0052). It follows
// Paymob's "Pay with saved card" collection: a merchant-initiated
// charge is an Intention on the MOTO integration, then
// POST /api/acceptance/payments/pay with the card token. The contract
// is tested against a stand-in; run it against Paymob's test mode before
// launch.
package paymob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/paymob"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Name is the gateway's name on cards on file.
const Name = "paymob"

// Region is the platform's Paymob account of one currency.
type Region struct {
	SecretKey         string // the Intention API's
	APIKey            string // for inquiries
	MotoIntegrationID int64  // merchant-initiated charges: Paymob enables it
	// The checkout where a merchant pays and saves the card (ADR-0053):
	// the public key, the 3DS card integration, and the callbacks' HMAC
	// secret. All three, or no checkout in this region.
	PublicKey         string
	CardIntegrationID int64
	HMACSecret        string
}

func (r Region) checkout() bool {
	return r.PublicKey != "" && r.CardIntegrationID != 0 && r.HMACSecret != ""
}

// Options configures New.
type Options struct {
	Regions map[string]Region // by currency: EGP, SAR, AED
	BaseURL string            // overrides the region's API (tests)
	Timeout time.Duration
	Client  *http.Client
}

// Gateway charges cards on file.
type Gateway struct {
	client  *paymob.Client
	regions map[string]Region
}

var _ app.CheckoutGateway = (*Gateway)(nil)

// New returns the gateway.
func New(o Options) *Gateway {
	return &Gateway{client: paymob.New(paymob.Options{BaseURL: o.BaseURL, Timeout: o.Timeout, Client: o.Client}), regions: o.Regions}
}

// errUnknown: the charge's outcome is not known yet; the job retries and
// asks Paymob first.
var errUnknown = errors.New("paymob: the charge is pending")

// Charge implements app.Gateway. Each attempt has its own reference, and
// Paymob is asked about it first: a charge whose answer was lost is
// found, never made twice.
func (g *Gateway) Charge(ctx context.Context, c app.Charge) (app.ChargeResult, error) {
	r, ok := g.regions[c.Currency]
	if !ok || r.SecretKey == "" || r.APIKey == "" || r.MotoIntegrationID == 0 {
		return app.ChargeResult{}, fmt.Errorf("paymob: no billing account for %s", c.Currency)
	}
	if c.Token == "" || c.AmountMinor <= 0 || c.Reference == "" {
		return app.ChargeResult{}, errors.New("paymob: a token, a positive amount and a reference are required")
	}
	base, err := g.client.BaseFor(c.Currency)
	if err != nil {
		return app.ChargeResult{}, err
	}
	tok, err := g.client.AuthToken(ctx, base, r.APIKey)
	if err != nil {
		return app.ChargeResult{}, err
	}
	if t, found, iErr := g.client.Inquire(ctx, base, tok, c.Reference); iErr != nil || found {
		if iErr != nil {
			return app.ChargeResult{}, iErr
		}
		return resultOf(t, c)
	}
	key, err := g.paymentKey(ctx, base, r, c)
	if err != nil {
		return app.ChargeResult{}, err
	}
	var t paymob.Transaction
	if _, err = g.client.Do(ctx, http.MethodPost, base+"/api/acceptance/payments/pay", "", map[string]any{
		"source": map[string]string{"identifier": c.Token, "subtype": "TOKEN"}, "payment_token": key,
	}, &t); err != nil {
		return app.ChargeResult{}, err // a lost answer: the retry asks first
	}
	return resultOf(t, c)
}

// paymentKey creates the charge's Intention on the MOTO integration.
func (g *Gateway) paymentKey(ctx context.Context, base string, r Region, c app.Charge) (string, error) {
	desc := text.Clip("Iftah Online "+c.Reference, 200)
	var out struct {
		PaymentKeys []struct {
			Key string `json:"key"`
		} `json:"payment_keys"`
	}
	_, err := g.client.Do(ctx, http.MethodPost, base+"/v1/intention/", "Token "+r.SecretKey, map[string]any{
		"amount": c.AmountMinor, "currency": c.Currency, "payment_methods": []int64{r.MotoIntegrationID},
		"items": []map[string]any{{"name": desc, "amount": c.AmountMinor, "description": desc, "quantity": 1}},
		// Paymob requires these; a merchant's charge has no shipping.
		"billing_data": map[string]string{"first_name": "Iftah", "last_name": "Merchant", "phone_number": "NA", "email": "NA",
			"apartment": "NA", "floor": "NA", "street": "NA", "building": "NA", "city": "NA", "country": "NA", "state": "NA"},
		"special_reference": c.Reference,
	}, &out)
	if err != nil {
		return "", err
	}
	if len(out.PaymentKeys) == 0 || out.PaymentKeys[0].Key == "" {
		return "", errors.New("paymob: the intention has no payment key")
	}
	return out.PaymentKeys[0].Key, nil
}

// resultOf reads a transaction of c's reference: approved, declined, or
// not known yet (pending).
func resultOf(t paymob.Transaction, c app.Charge) (app.ChargeResult, error) {
	if t.Order.MerchantOrderID != "" && t.Order.MerchantOrderID != c.Reference {
		return app.ChargeResult{}, fmt.Errorf("paymob: the answer for %s is order %q", c.Reference, t.Order.MerchantOrderID)
	}
	if t.Pending {
		return app.ChargeResult{}, errUnknown
	}
	res := app.ChargeResult{Reference: t.ID.String()}
	if t.Success && !t.IsVoided && (t.TxnResponseCode == "" || t.TxnResponseCode == "APPROVED") {
		res.Approved = true
		return res, nil
	}
	res.Reason = text.Clip(t.Data.Message, 200)
	if res.Reason == "" {
		res.Reason = text.Clip(t.TxnResponseCode, 200)
	}
	if res.Reason == "" {
		res.Reason = "declined"
	}
	return res, nil
}

// Checkout implements app.CheckoutGateway: a 3DS Intention for the
// invoice on the region's card integration, paid on the Unified
// Checkout, where the merchant's card is saved ("save card" pre-checked
// at Paymob's on request). The callbacks go to notificationURL.
func (g *Gateway) Checkout(ctx context.Context, c app.CheckoutRequest) (app.CheckoutPage, error) {
	r, ok := g.regions[c.Currency]
	if !ok || !r.checkout() {
		return app.CheckoutPage{}, fmt.Errorf("%w: %s", app.ErrNoCheckout, c.Currency)
	}
	base, err := g.client.BaseFor(c.Currency)
	if err != nil {
		return app.CheckoutPage{}, err
	}
	desc := text.Clip("Iftah Online "+c.Description, 200)
	var out struct {
		ID               string      `json:"id"`
		ClientSecret     string      `json:"client_secret"`
		IntentionOrderID json.Number `json:"intention_order_id"`
	}
	if _, err = g.client.Do(ctx, http.MethodPost, base+"/v1/intention/", "Token "+r.SecretKey, map[string]any{
		"amount": c.AmountMinor, "currency": c.Currency, "payment_methods": []int64{r.CardIntegrationID},
		"items": []map[string]any{{"name": desc, "amount": c.AmountMinor, "description": desc, "quantity": 1}},
		"billing_data": map[string]string{"first_name": orNA(c.Name), "last_name": "NA", "phone_number": "NA", "email": orNA(c.Email),
			"apartment": "NA", "floor": "NA", "street": "NA", "building": "NA", "city": "NA", "country": "NA", "state": "NA"},
		"special_reference": c.Reference, "notification_url": c.NotificationURL, "redirection_url": c.RedirectURL,
	}, &out); err != nil {
		return app.CheckoutPage{}, err
	}
	if out.ClientSecret == "" {
		return app.CheckoutPage{}, errors.New("paymob: the intention has no client secret")
	}
	u := base + "/unifiedcheckout/?publicKey=" + url.QueryEscape(r.PublicKey) + "&clientSecret=" + url.QueryEscape(out.ClientSecret)
	return app.CheckoutPage{URL: u, ProviderOrderID: out.IntentionOrderID.String()}, nil
}

func orNA(s string) string {
	if s == "" {
		return "NA"
	}
	return s
}

// ParseCallback implements app.CheckoutGateway: a callback is believed
// only with a region's HMAC secret over its type's fields (transaction:
// Paymob's 20; card token: its 8).
func (g *Gateway) ParseCallback(query url.Values, body []byte) (app.Callback, error) {
	cb, err := paymob.ParseCallback(body)
	if err != nil {
		return app.Callback{}, app.ErrForgedCallback
	}
	fields := paymob.TransactionFields()
	if cb.Type == "TOKEN" {
		fields = paymob.TokenFields()
	}
	signed := false
	for _, r := range g.regions {
		if r.HMACSecret != "" && paymob.Verify(r.HMACSecret, query.Get("hmac"), cb.Obj, fields) {
			signed = true
			break
		}
	}
	if !signed {
		return app.Callback{}, app.ErrForgedCallback
	}
	return g.ReadCallback(body)
}

// ReadCallback implements app.CheckoutGateway: a callback verified when
// it was received, read again by its processing job.
func (*Gateway) ReadCallback(body []byte) (app.Callback, error) {
	cb, err := paymob.ParseCallback(body)
	if err != nil {
		return app.Callback{}, app.ErrBadCallback
	}
	if cb.Type == "TOKEN" {
		var w struct {
			Obj struct {
				ID          json.Number `json:"id"`
				Token       string      `json:"token"`
				MaskedPan   string      `json:"masked_pan"` // nocarddata:allow Paymob's masked card (last four), shown as the card's label
				CardSubtype string      `json:"card_subtype"`
				OrderID     json.Number `json:"order_id"`
			} `json:"obj"`
		}
		if err = json.Unmarshal(body, &w); err != nil || w.Obj.Token == "" || w.Obj.OrderID == "" {
			return app.Callback{}, app.ErrBadCallback
		}
		tok := w.Obj
		return app.Callback{Kind: app.CallbackToken, EventID: "token:" + tok.ID.String(), ProviderOrderID: tok.OrderID.String(),
			Token: tok.Token, Label: cardLabel(tok.CardSubtype, tok.MaskedPan)}, nil
	}
	var t struct {
		Obj struct {
			paymob.Transaction
			Order struct {
				ID              json.Number `json:"id"`
				MerchantOrderID string      `json:"merchant_order_id"`
			} `json:"order"`
		} `json:"obj"`
	}
	if err = json.Unmarshal(body, &t); err != nil || t.Obj.ID == "" || t.Obj.Order.MerchantOrderID == "" {
		return app.Callback{}, app.ErrBadCallback
	}
	o := t.Obj
	return app.Callback{Kind: app.CallbackTransaction, EventID: fmt.Sprintf("txn:%s:%t:%t", o.ID, o.Success, o.Pending),
		Reference: o.Order.MerchantOrderID, ProviderOrderID: o.Order.ID.String(), TransactionID: o.ID.String()}, nil
}

// cardLabel is what the merchant sees of a saved card: its brand and
// last four digits.
func cardLabel(brand, masked string) string {
	last := masked
	if len(last) > 4 {
		last = last[len(last)-4:]
	}
	return text.Clip(strings.TrimSpace(brand+" •••• "+last), 60)
}

// Paid implements app.CheckoutGateway: Paymob is asked about the
// checkout's reference before anything is marked paid (CLAUDE.md).
func (g *Gateway) Paid(ctx context.Context, currency, reference string, amountMinor int64) (string, bool, error) {
	r, ok := g.regions[currency]
	if !ok || r.APIKey == "" {
		return "", false, fmt.Errorf("paymob: no billing account for %s", currency)
	}
	base, err := g.client.BaseFor(currency)
	if err != nil {
		return "", false, err
	}
	tok, err := g.client.AuthToken(ctx, base, r.APIKey)
	if err != nil {
		return "", false, err
	}
	t, found, err := g.client.Inquire(ctx, base, tok, reference)
	if err != nil || !found {
		return "", false, err
	}
	if t.Order.MerchantOrderID != reference || (t.Currency != "" && t.Currency != currency) {
		return "", false, fmt.Errorf("paymob: the inquiry for %s answered another payment", reference)
	}
	paid := t.Success && !t.Pending && !t.IsVoided && t.AmountCents == amountMinor
	return t.ID.String(), paid, nil
}
