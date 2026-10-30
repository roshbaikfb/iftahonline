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
	"errors"
	"fmt"
	"net/http"
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

var _ app.Gateway = (*Gateway)(nil)

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
