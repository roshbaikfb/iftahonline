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
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Name is Paymob's gateway name.
const Name = "paymob"

// Base URLs per region.
const (
	EgyptURL = "https://accept.paymob.com"
	SaudiURL = "https://ksa.paymob.com"
	UAEURL   = "https://uae.paymob.com"
)

const maxResponse = 1 << 20

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
	base  string
	http  *http.Client
	retry retry.Policy
}

var _ gateway.Gateway = (*Gateway)(nil)

// New returns the adapter.
func New(o Options) *Gateway {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.Timeout}
	}
	p := retry.Default()
	if o.Retry != nil {
		p = *o.Retry
	}
	return &Gateway{base: strings.TrimRight(o.BaseURL, "/"), http: o.Client, retry: p}
}

// Name implements gateway.Gateway.
func (*Gateway) Name() string { return Name }

// baseFor is the API of cur's region.
func (g *Gateway) baseFor(cur money.Currency) (string, error) {
	if g.base != "" {
		return g.base, nil
	}
	switch cur {
	case money.EGP:
		return EgyptURL, nil
	case money.SAR:
		return SaudiURL, nil
	case money.AED:
		return UAEURL, nil
	}
	return "", fmt.Errorf("%w: Paymob does not take %s", gateway.ErrRejected, cur)
}

// do sends one JSON request; auth is the whole Authorization value.
func (g *Gateway) do(ctx context.Context, method, rawURL, auth string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, retry.Permanent(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return 0, retry.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("paymob: %s: %w", req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("paymob: read: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, retry.Permanent(gateway.ErrAuth)
	case resp.StatusCode == http.StatusNotFound:
		return resp.StatusCode, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return resp.StatusCode, fmt.Errorf("paymob: %s: status %d", req.URL.Path, resp.StatusCode)
	case resp.StatusCode >= 400:
		return resp.StatusCode, retry.Permanent(fmt.Errorf("%w: %s (status %d)", gateway.ErrRejected, text.Redact(text.Clip(string(raw), 200)), resp.StatusCode))
	}
	if out != nil {
		if err = json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, retry.Permanent(fmt.Errorf("paymob: decode %s: %w", req.URL.Path, err))
		}
	}
	return resp.StatusCode, nil
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
	base := g.base
	if base == "" {
		base = EgyptURL // the key is checked where it was issued; any region answers for its own keys
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
		tok, tokErr := g.token(ctx, base, creds)
		if tokErr != nil {
			return tokErr
		}
		code, doErr := g.do(ctx, http.MethodPost, base+"/api/ecommerce/orders/transaction_inquiry", "Bearer "+tok,
			map[string]string{"merchant_order_id": reference}, &t)
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

// hmacFields are the transaction callback's signed fields, in Paymob's
// order. One of the source_data fields is the card's masked last four
// digits, as Paymob sends them: it is only read to check the signature.
var hmacFields = []string{ //nolint:gochecknoglobals // constant list
	"amount_cents", "created_at", "currency", "error_occured", "has_parent_transaction", "id", "integration_id",
	"is_3d_secure", "is_auth", "is_capture", "is_refunded", "is_standalone_payment", "is_voided", "order.id", "owner",
	"pending", "source_data.pan", "source_data.sub_type", "source_data.type", "success", // nocarddata:allow masked last four digits, part of Paymob's signed field list
}

// VerifyWebhook implements gateway.Gateway for transaction callbacks.
func (*Gateway) VerifyWebhook(creds gateway.Credentials, req gateway.WebhookRequest) (gateway.WebhookEvent, error) {
	got, err := hex.DecodeString(strings.ToLower(req.Query.Get("hmac")))
	if err != nil || len(got) != sha512.Size || creds.WebhookSecret == "" {
		return gateway.WebhookEvent{}, gateway.ErrForged
	}
	var cb struct {
		Type string                     `json:"type"`
		Obj  map[string]json.RawMessage `json:"obj"`
	}
	d := json.NewDecoder(bytes.NewReader(req.Body))
	d.UseNumber()
	if err = d.Decode(&cb); err != nil || cb.Obj == nil {
		return gateway.WebhookEvent{}, gateway.ErrForged // cannot be what was signed
	}
	m := hmac.New(sha512.New, []byte(creds.WebhookSecret))
	m.Write([]byte(signedString(cb.Obj)))
	if !hmac.Equal(got, m.Sum(nil)) {
		return gateway.WebhookEvent{}, gateway.ErrForged
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

// signedString concatenates the signed fields' values: strings as they
// are, numbers as sent, booleans as true/false, missing and null as
// nothing.
func signedString(obj map[string]json.RawMessage) string {
	var b strings.Builder
	for _, f := range hmacFields {
		raw := lookup(obj, f)
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			b.WriteString(s)
			continue
		}
		b.Write(bytes.TrimSpace(raw)) // numbers and booleans as written
	}
	return b.String()
}

func lookup(obj map[string]json.RawMessage, path string) json.RawMessage {
	parent, child, nested := strings.Cut(path, ".")
	if !nested {
		return obj[parent]
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(obj[parent], &inner); err != nil {
		return nil
	}
	return inner[child]
}

// Sign is the hmac a callback body carries (the stand-in and tests).
func Sign(secret string, body []byte) (string, error) {
	var cb struct {
		Obj map[string]json.RawMessage `json:"obj"`
	}
	if err := json.Unmarshal(body, &cb); err != nil {
		return "", err
	}
	m := hmac.New(sha512.New, []byte(secret))
	m.Write([]byte(signedString(cb.Obj)))
	return hex.EncodeToString(m.Sum(nil)), nil
}

func orNA(s string) string {
	if strings.TrimSpace(s) == "" {
		return "NA"
	}
	return s
}
