// Package paymob is the Paymob adapter (ADR-0029), for Egypt, Saudi
// Arabia and the UAE.
//
// It follows Paymob's own API collections (PaymobAccept/API-Postman-
// Collections): payments are Intentions (POST /v1/intention/, with our
// payment ID as special_reference) paid on the Unified Checkout page;
// status inquiries go through an auth token (POST /api/auth/tokens with
// the API key) to POST /api/ecommerce/orders/transaction_inquiry by
// merchant_order_id; refunds are POST /api/acceptance/void_refund/refund.
// Transaction callbacks are verified with HMAC-SHA512 over Paymob's 20
// ordered fields, the hex digest in the hmac query parameter. The
// contract suite runs against Paymob's test mode when PAYMOB_SANDBOX_*
// keys are set; until it has, treat the field details as unverified.
package paymob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/paymob"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Name is Paymob's gateway name.
const Name = "paymob"

// Base URLs per region.
const (
	EgyptURL = paymob.EgyptURL
	SaudiURL = paymob.SaudiURL
	UAEURL   = paymob.UAEURL
)

// Options configures New.
type Options struct {
	// BaseURL overrides the region chosen by currency (tests).
	BaseURL string
	Timeout time.Duration // per request; default 10 s
	Retry   *retry.Policy // for inquiries and refunds; default retry.Default()
	Client  *http.Client
}

// Gateway is the Paymob adapter.
type Gateway struct {
	client *paymob.Client
	retry  retry.Policy
	tokens *tokens
}

var _ gateway.Gateway = (*Gateway)(nil)

// New returns the adapter.
func New(o Options) *Gateway {
	p := retry.Default()
	if o.Retry != nil {
		p = *o.Retry
	}
	return &Gateway{client: paymob.New(paymob.Options{BaseURL: o.BaseURL, Timeout: o.Timeout, Client: o.Client}), retry: p, tokens: newTokens()}
}

// Name implements gateway.Gateway.
func (*Gateway) Name() string { return Name }

// baseFor is the API of cur's region.
func (g *Gateway) baseFor(cur money.Currency) (string, error) {
	base, err := g.client.BaseFor(string(cur))
	return base, gatewayErr(err)
}

// do sends one JSON request; auth is the whole Authorization value.
func (g *Gateway) do(ctx context.Context, method, rawURL, auth string, in, out any) (int, error) {
	code, err := g.client.Do(ctx, method, rawURL, auth, in, out)
	return code, gatewayErr(err)
}

// gatewayErr gives the platform client's refusals this module's gateway
// errors, keeping them permanent.
func gatewayErr(err error) error {
	switch {
	case errors.Is(err, paymob.ErrAuth):
		return retry.Permanent(gateway.ErrAuth)
	case errors.Is(err, paymob.ErrRejected):
		return retry.Permanent(fmt.Errorf("%w: %w", gateway.ErrRejected, err))
	}
	return err
}

// token is an auth token from the API key, for inquiries.
func (g *Gateway) token(ctx context.Context, base string, creds gateway.Credentials) (string, error) {
	if creds.APIKey == "" {
		return "", gateway.ErrAuth
	}
	var out struct {
		Token string `json:"token"`
	}
	code, err := g.do(ctx, http.MethodPost, base+"/api/auth/tokens", "", map[string]string{"api_key": creds.APIKey}, &out)
	if err != nil {
		return "", err
	}
	if code == http.StatusNotFound || out.Token == "" {
		return "", retry.Permanent(gateway.ErrAuth)
	}
	return out.Token, nil
}

// inquiryToken is a cached auth token for inquiries, or a new one.
func (g *Gateway) inquiryToken(ctx context.Context, base string, creds gateway.Credentials) (string, tokenKey, error) {
	k := keyOf(base, creds.APIKey)
	if tok, ok := g.tokens.get(k); ok {
		return tok, k, nil
	}
	tok, err := g.token(ctx, base, creds)
	if err != nil {
		return "", k, err
	}
	g.tokens.put(k, tok)
	return tok, k, nil
}

// NeedsIntegrationIDs implements gateway.IntegrationIDsNeeded: Paymob
// routes card payments through the account's integration IDs.
func (*Gateway) NeedsIntegrationIDs() bool { return true }

var _ gateway.IntegrationIDsNeeded = (*Gateway)(nil)

// Verify implements gateway.Gateway: the API key gets an auth token, and
// every other key Paymob needs is present.
func (g *Gateway) Verify(ctx context.Context, creds gateway.Credentials) error {
	if creds.SecretKey == "" || creds.PublicKey == "" || creds.WebhookSecret == "" || len(creds.IntegrationIDs) == 0 {
		return gateway.ErrAuth
	}
	// The override, else Egypt: the key is checked where it was issued;
	// any region answers for its own keys.
	base, err := g.baseFor(money.EGP)
	if err != nil {
		return err
	}
	return retry.Do(ctx, g.retry, func(ctx context.Context) error {
		_, err := g.token(ctx, base, creds)
		return err
	})
}

// CreatePayment implements gateway.Gateway with an Intention. It is not
// retried: Paymob refuses a reused special_reference, so a failed attempt
// is a new payment the next time.
func (g *Gateway) CreatePayment(ctx context.Context, creds gateway.Credentials, req gateway.PaymentRequest) (gateway.Checkout, error) {
	base, err := g.baseFor(req.Amount.Currency())
	if err != nil {
		return gateway.Checkout{}, err
	}
	if req.Reference == "" || req.Amount.Amount() <= 0 || len(creds.IntegrationIDs) == 0 {
		return gateway.Checkout{}, fmt.Errorf("%w: a reference, a positive amount and a card integration are required", gateway.ErrRejected)
	}
	c := req.Customer
	email := c.Email
	if email == "" {
		email = "NA" // Paymob requires the field; shoppers here order by phone
	}
	body := map[string]any{
		"amount": req.Amount.Amount(), "currency": string(req.Amount.Currency()), "payment_methods": creds.IntegrationIDs,
		// One item for the whole amount: Paymob requires the items to add
		// up to it exactly.
		"items": []map[string]any{{"name": text.Clip(req.Description, 200), "amount": req.Amount.Amount(), "description": text.Clip(req.Description, 200), "quantity": 1}},
		"billing_data": map[string]string{"first_name": orNA(c.FirstName), "last_name": orNA(c.LastName), "phone_number": c.Phone, "email": email,
			"apartment": "NA", "floor": "NA", "street": "NA", "building": "NA", "city": "NA", "country": "NA", "state": "NA"},
		"special_reference": req.Reference, "notification_url": req.NotificationURL, "redirection_url": req.RedirectURL,
	}
	var out struct {
		ID           string `json:"id"`
		ClientSecret string `json:"client_secret"`
	}
	code, err := g.do(ctx, http.MethodPost, base+"/v1/intention/", "Token "+creds.SecretKey, body, &out)
	if err != nil {
		return gateway.Checkout{}, err
	}
	if code == http.StatusNotFound || out.ClientSecret == "" {
		return gateway.Checkout{}, errors.New("paymob: the intention has no client secret")
	}
	u := base + "/unifiedcheckout/?publicKey=" + url.QueryEscape(creds.PublicKey) + "&clientSecret=" + url.QueryEscape(out.ClientSecret)
	return gateway.Checkout{URL: u, ProviderRef: out.ID}, nil
}

// transaction is Paymob's transaction object (callbacks and inquiries).
type transaction struct {
	ID                  json.Number `json:"id"`
	Success             bool        `json:"success"`
	Pending             bool        `json:"pending"`
	AmountCents         int64       `json:"amount_cents"`
	RefundedAmountCents int64       `json:"refunded_amount_cents"`
	IsRefunded          bool        `json:"is_refunded"`
	IsVoided            bool        `json:"is_voided"`
	IsAuth              bool        `json:"is_auth"`
	IsCapture           bool        `json:"is_capture"`
	Currency            string      `json:"currency"`
	CreatedAt           string      `json:"created_at"`
	Order               struct {
		MerchantOrderID string `json:"merchant_order_id"`
	} `json:"order"`
}

// FetchStatus implements gateway.Gateway by transaction inquiry.
func (g *Gateway) FetchStatus(ctx context.Context, creds gateway.Credentials, reference string, cur money.Currency) (gateway.Status, error) {
	base, err := g.baseFor(cur)
	if err != nil {
		return gateway.Status{}, err
	}
	var t transaction
	var found bool
	err = retry.Do(ctx, g.retry, func(ctx context.Context) error {
		tok, k, tokErr := g.inquiryToken(ctx, base, creds)
		if tokErr != nil {
			return tokErr
		}
		inquire := func(tok string) (int, error) {
			return g.do(ctx, http.MethodPost, base+"/api/ecommerce/orders/transaction_inquiry", "Bearer "+tok,
				map[string]string{"merchant_order_id": reference}, &t)
		}
		code, doErr := inquire(tok)
		if errors.Is(doErr, gateway.ErrAuth) {
			// The cached token expired: a new one, once (a bad API key
			// fails at the token itself).
			g.tokens.drop(k)
			if tok, k, tokErr = g.inquiryToken(ctx, base, creds); tokErr != nil {
				return tokErr
			}
			if code, doErr = inquire(tok); errors.Is(doErr, gateway.ErrAuth) {
				g.tokens.drop(k)
			}
		}
		found = code != http.StatusNotFound
		return doErr
	})
	if err != nil {
		return gateway.Status{}, err
	}
	zero, err := money.Zero(cur)
	if err != nil {
		return gateway.Status{}, err
	}
	st := gateway.Status{State: gateway.StatePending, Amount: zero, Refunded: zero}
	if !found || t.ID == "" {
		return st, nil // no transaction yet: the shopper has not paid
	}
	// The answer is believed only for what was asked: this reference, in
	// this currency.
	if t.Order.MerchantOrderID != reference {
		return gateway.Status{}, fmt.Errorf("paymob: the inquiry for %s answered with order %q", reference, t.Order.MerchantOrderID)
	}
	if t.Currency != "" && t.Currency != string(cur) { // absent: nothing to compare (to check in the sandbox run)
		return gateway.Status{}, fmt.Errorf("paymob: the inquiry for %s answered in %q, not %s", reference, t.Currency, cur)
	}
	st.TransactionID = t.ID.String()
	st.At, _ = time.Parse(time.RFC3339Nano, t.CreatedAt) //nolint:errcheck // a time Paymob writes otherwise stays zero
	switch {
	case t.Pending, t.IsAuth && !t.IsCapture: // an authorization alone is not money taken
	case t.Success && !t.IsVoided:
		st.State = gateway.StatePaid
		if st.Amount, err = money.New(t.AmountCents, cur); err != nil {
			return gateway.Status{}, err
		}
		if st.Refunded, err = money.New(t.RefundedAmountCents, cur); err != nil {
			return gateway.Status{}, err
		}
	default:
		st.State = gateway.StateFailed
	}
	return st, nil
}

// Refund implements gateway.Gateway.
func (g *Gateway) Refund(ctx context.Context, creds gateway.Credentials, transactionID string, amount money.Money) (string, error) {
	base, err := g.baseFor(amount.Currency())
	if err != nil {
		return "", err
	}
	txn, err := strconv.ParseInt(transactionID, 10, 64)
	if err != nil || amount.Amount() <= 0 {
		return "", fmt.Errorf("%w: refund needs a Paymob transaction and a positive amount", gateway.ErrRejected)
	}
	// Sent once, never retried here: Paymob takes no idempotency key, and
	// a refund whose answer was lost may have gone through. The caller
	// asks the gateway what was refunded before trying again.
	var out transaction
	code, err := g.do(ctx, http.MethodPost, base+"/api/acceptance/void_refund/refund", "Token "+creds.SecretKey,
		map[string]int64{"transaction_id": txn, "amount_cents": amount.Amount()}, &out)
	if err == nil && code == http.StatusNotFound {
		err = fmt.Errorf("%w: no transaction %s", gateway.ErrRejected, transactionID)
	}
	if err != nil {
		return "", err
	}
	return out.ID.String(), nil
}

// VerifyWebhook implements gateway.Gateway for transaction callbacks.
func (*Gateway) VerifyWebhook(creds gateway.Credentials, req gateway.WebhookRequest) (gateway.WebhookEvent, error) {
	cb, err := paymob.ParseCallback(req.Body)
	if err != nil || !paymob.Verify(creds.WebhookSecret, req.Query.Get("hmac"), cb.Obj, paymob.TransactionFields()) {
		return gateway.WebhookEvent{}, gateway.ErrForged // cannot be what was signed
	}
	return (*Gateway)(nil).ParseWebhook(req.Body)
}

// ParseWebhook implements gateway.Gateway for transaction callbacks.
func (*Gateway) ParseWebhook(body []byte) (gateway.WebhookEvent, error) {
	var cb struct {
		Obj transaction `json:"obj"`
	}
	if err := json.Unmarshal(body, &cb); err != nil || cb.Obj.ID == "" || cb.Obj.Order.MerchantOrderID == "" {
		return gateway.WebhookEvent{}, gateway.ErrBadWebhook
	}
	t := cb.Obj
	// One change of one transaction: its id and the flags that change.
	id := fmt.Sprintf("txn:%s:%t:%t:%t:%t:%d", t.ID, t.Success, t.Pending, t.IsRefunded, t.IsVoided, t.RefundedAmountCents)
	return gateway.WebhookEvent{EventID: id, Reference: t.Order.MerchantOrderID}, nil
}

// Sign is the hmac a callback body carries (the stand-in and tests).
func Sign(secret string, body []byte) (string, error) {
	return paymob.Sign(secret, body, paymob.TransactionFields())
}

func orNA(s string) string {
	if strings.TrimSpace(s) == "" {
		return "NA"
	}
	return s
}
