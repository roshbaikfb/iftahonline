// Package twilio is the SMS adapter on Twilio's Programmable Messaging
// API (ADR-0039), one platform-wide account.
//
// It follows Twilio's documentation (twilio.com/docs/messaging): a
// message is POST /2010-04-01/Accounts/{AccountSid}/Messages.json, form
// encoded, with HTTP Basic auth (account SID and auth token); its
// statuses are POSTed to the StatusCallback we give it, signed in
// X-Twilio-Signature: base64 of HMAC-SHA1 under the auth token over the
// callback URL followed by each form parameter's name and value, sorted
// by name. A message's status is read back with GET
// .../Messages/{Sid}.json. Until it has run against a Twilio test
// account, treat the details as unverified.
package twilio

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // Twilio signs webhooks with HMAC-SHA1
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Name is the channel's name.
const Name = "twilio"

// DefaultBaseURL is Twilio's REST API.
const DefaultBaseURL = "https://api.twilio.com"

// SignatureHeader carries Twilio's webhook signature.
const SignatureHeader = "X-Twilio-Signature"

const maxResponse = 1 << 20

var sidRE = regexp.MustCompile(`^(SM|MM)[0-9a-fA-F]{32}$`)

// undeliverableNow reports whether an API error code means the number
// cannot take SMS: invalid, unreachable route, not mobile, unsubscribed.
func undeliverableNow(code int) bool {
	switch code {
	case 21211, 21612, 21614, 21610:
		return true
	}
	return false
}

// Options configures New.
type Options struct {
	BaseURL    string // default DefaultBaseURL
	AccountSID string
	AuthToken  string
	// From is the sender: a number in E.164, an alphanumeric sender ID,
	// or a Messaging Service SID (MG…).
	From string
	// CallbackURL is our status webhook URL without a query (the
	// platform route, webhooks.PlatformURL): signatures are computed over
	// it plus the query we add.
	CallbackURL string
	Timeout     time.Duration // per request; default 10 s
	Retry       *retry.Policy // for Fetch; default retry.Default()
	Client      *http.Client
}

// Channel is the adapter.
type Channel struct {
	base, sid, token, from, callback string
	http                             *http.Client
	retry                            retry.Policy
}

var (
	_ channel.Channel = (*Channel)(nil)
	_ channel.Fetcher = (*Channel)(nil)
)

// New returns the adapter.
func New(o Options) (*Channel, error) {
	if o.AccountSID == "" || o.AuthToken == "" || o.From == "" || o.CallbackURL == "" {
		return nil, errors.New("twilio: account SID, auth token, sender and callback URL are required")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
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
	return &Channel{base: strings.TrimRight(o.BaseURL, "/"), sid: o.AccountSID, token: o.AuthToken, from: o.From,
		callback: o.CallbackURL, http: o.Client, retry: p}, nil
}

// Name implements channel.Channel.
func (*Channel) Name() string { return Name }

// StatusURL is the callback URL for the message with reference ref.
func (c *Channel) StatusURL(ref string) string {
	return c.callback + "?" + url.Values{"m": {ref}}.Encode()
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type apiMessage struct {
	SID       string `json:"sid"`
	Status    string `json:"status"`
	ErrorCode *int   `json:"error_code"`
}

func (c *Channel) messagesURL() string {
	return c.base + "/2010-04-01/Accounts/" + url.PathEscape(c.sid) + "/Messages"
}

// do sends one request and decodes a 2xx answer into out.
func (c *Channel) do(req *http.Request, out any) error {
	req.SetBasicAuth(c.sid, c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("twilio: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("twilio: read: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err = json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("twilio: unreadable answer (status %d)", resp.StatusCode)
		}
		return nil
	}
	var ae apiError
	_ = json.Unmarshal(raw, &ae) //nolint:errcheck // an unreadable error body is classified by status
	msg := text.Redact(text.Clip(ae.Message, 200))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || ae.Code == 20003:
		return retry.Permanent(fmt.Errorf("%w: %s", channel.ErrAuth, msg))
	case undeliverableNow(ae.Code):
		return retry.Permanent(fmt.Errorf("%w: code %d: %s", channel.ErrUndeliverable, ae.Code, msg))
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("twilio: status %d, code %d: %s", resp.StatusCode, ae.Code, msg)
	}
	return retry.Permanent(fmt.Errorf("%w: status %d, code %d: %s", channel.ErrRejected, resp.StatusCode, ae.Code, msg))
}

// Send implements channel.Channel: a text message only. It never
// retries (Twilio takes no idempotency key for messages).
func (c *Channel) Send(ctx context.Context, m channel.Message) (channel.Sent, error) {
	if m.To.IsZero() || m.Text == "" || m.Template != "" {
		return channel.Sent{}, fmt.Errorf("%w: SMS takes a recipient and a text", channel.ErrRejected)
	}
	form := url.Values{"To": {m.To.Unmasked()}, "Body": {m.Text}}
	if strings.HasPrefix(c.from, "MG") {
		form.Set("MessagingServiceSid", c.from)
	} else {
		form.Set("From", c.from)
	}
	if m.Ref != "" {
		form.Set("StatusCallback", c.StatusURL(m.Ref))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.messagesURL()+".json", strings.NewReader(form.Encode()))
	if err != nil {
		return channel.Sent{}, fmt.Errorf("%w: %w", channel.ErrRejected, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out apiMessage
	if err = c.do(req, &out); err != nil {
		return channel.Sent{}, err
	}
	if !sidRE.MatchString(out.SID) {
		return channel.Sent{}, errors.New("twilio: answer without a message SID")
	}
	return channel.Sent{ProviderID: out.SID}, nil
}

// Fetch implements channel.Fetcher.
func (c *Channel) Fetch(ctx context.Context, providerID string) (channel.Status, string, error) {
	if !sidRE.MatchString(providerID) {
		return "", "", fmt.Errorf("%w: not a message SID", channel.ErrRejected)
	}
	var out apiMessage
	err := retry.Do(ctx, c.retry, func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.messagesURL()+"/"+providerID+".json", nil)
		if err != nil {
			return retry.Permanent(err)
		}
		return c.do(req, &out)
	})
	if err != nil {
		return "", "", err
	}
	code := ""
	if out.ErrorCode != nil && *out.ErrorCode != 0 {
		code = strconv.Itoa(*out.ErrorCode)
	}
	return status(out.Status), code, nil
}

// status maps Twilio's message status; "" while on its way.
func status(s string) channel.Status {
	switch s {
	case "sent":
		return channel.StatusSent
	case "delivered":
		return channel.StatusDelivered
	case "read":
		return channel.StatusRead
	case "undelivered", "failed", "canceled":
		return channel.StatusFailed
	}
	return ""
}

// Sign is Twilio's signature of a POST to rawURL with form.
func Sign(token, rawURL string, form url.Values) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString(rawURL)
	for _, k := range keys {
		for _, v := range form[k] {
			b.WriteString(k)
			b.WriteString(v)
		}
	}
	m := hmac.New(sha1.New, []byte(token))
	m.Write([]byte(b.String()))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// VerifyWebhook implements channel.Channel: one status callback, signed
// over our callback URL with its query. A status still on its way
// yields no event.
func (c *Channel) VerifyWebhook(w channel.Webhook) ([]channel.Event, error) {
	form, err := url.ParseQuery(string(w.Body))
	if err != nil {
		return nil, channel.ErrForged
	}
	full := c.callback
	if len(w.Query) > 0 {
		full += "?" + w.Query.Encode()
	}
	want := Sign(c.token, full, form)
	if !hmac.Equal([]byte(http.Header(w.Header).Get(SignatureHeader)), []byte(want)) {
		return nil, channel.ErrForged
	}
	sid := form.Get("MessageSid")
	if !sidRE.MatchString(sid) || form.Get("AccountSid") != c.sid {
		return nil, fmt.Errorf("%w: not a message status of this account", channel.ErrBadWebhook)
	}
	raw := form.Get("MessageStatus")
	st := status(raw)
	if st == "" {
		return nil, nil
	}
	return []channel.Event{{Kind: channel.StatusEvent, ID: sid + ":" + raw, ProviderID: sid, Ref: w.Query.Get("m"), Status: st,
		ErrorCode: form.Get("ErrorCode"), At: time.Now().UTC().Truncate(time.Second)}}, nil
}

// Undeliverable implements channel.Channel: a failed status's code for
// an unreachable handset, an unknown number, or a landline or carrier
// that takes no SMS.
func (*Channel) Undeliverable(code string) bool {
	switch code {
	case "30003", "30005", "30006":
		return true
	}
	return false
}
