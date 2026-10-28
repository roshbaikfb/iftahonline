// Package whatsapp is the WhatsApp Business Cloud API adapter (ADR-0039),
// for the platform's own number (ADR-0038).
//
// It follows Meta's Cloud API documentation (developers.facebook.com,
// business-messaging/whatsapp): messages are POST
// /{phone-number-id}/messages with a Bearer token; a template message
// carries body parameters and quick-reply payloads, and
// biz_opaque_callback_data comes back with each of its statuses.
// Webhooks are signed with HMAC-SHA256 of the raw body under the app
// secret (X-Hub-Signature-256: sha256=<hex>) and batch up to 1,000
// updates. The contract suite runs against an in-process stand-in; until
// it has run against Meta's test number, treat the field details as
// unverified.
package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Name is the channel's name.
const Name = "whatsapp"

// DefaultGraphURL is Meta's Graph API at the version this adapter was
// written against.
const DefaultGraphURL = "https://graph.facebook.com/v26.0"

// SignatureHeader carries Meta's webhook signature.
const SignatureHeader = "X-Hub-Signature-256"

const (
	maxResponse = 1 << 20
	maxText     = 1000
)

// Meta's error codes this adapter tells apart.
const (
	codeAuth          = 190    // access token expired or invalid
	codeUndeliverable = 131026 // not on WhatsApp, old app, or not accepting
	codeRateLimit     = 130429 // throughput
	codePairRate      = 131056 // too many to one recipient
	codeSpamRate      = 131048 // quality limit
	codeTooMany       = 80007  // account rate limit
	codeTemporary     = 131016 // service unavailable
)

// Options configures New.
type Options struct {
	GraphURL      string // default DefaultGraphURL
	PhoneNumberID string
	AccessToken   string
	AppSecret     string
	Timeout       time.Duration // per request; default 10 s
	Client        *http.Client
}

// Channel is the adapter.
type Channel struct {
	base, phoneNumberID, token string
	secret                     []byte
	http                       *http.Client
}

var _ channel.Channel = (*Channel)(nil)

// New returns the adapter.
func New(o Options) (*Channel, error) {
	if o.PhoneNumberID == "" || o.AccessToken == "" || o.AppSecret == "" {
		return nil, errors.New("whatsapp: phone number ID, access token and app secret are required")
	}
	if o.GraphURL == "" {
		o.GraphURL = DefaultGraphURL
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.Timeout}
	}
	return &Channel{base: strings.TrimRight(o.GraphURL, "/"), phoneNumberID: o.PhoneNumberID, token: o.AccessToken,
		secret: []byte(o.AppSecret), http: o.Client}, nil
}

// Name implements channel.Channel.
func (*Channel) Name() string { return Name }

type param struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Payload string `json:"payload,omitempty"`
}

type component struct {
	Type       string  `json:"type"`
	SubType    string  `json:"sub_type,omitempty"`
	Index      string  `json:"index,omitempty"`
	Parameters []param `json:"parameters"`
}

type sendRequest struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Template         *struct {
		Name     string            `json:"name"`
		Language map[string]string `json:"language"`
		Comps    []component       `json:"components,omitempty"`
	} `json:"template,omitempty"`
	Text *struct {
		Body       string `json:"body"`
		PreviewURL bool   `json:"preview_url"`
	} `json:"text,omitempty"`
	BizOpaqueCallbackData string `json:"biz_opaque_callback_data,omitempty"`
}

type graphError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Send implements channel.Channel. It never retries: WhatsApp takes no
// idempotency key, so a lost answer is a transient error for the
// caller.
func (c *Channel) Send(ctx context.Context, m channel.Message) (channel.Sent, error) {
	req := sendRequest{MessagingProduct: "whatsapp", RecipientType: "individual", To: m.To.Unmasked(), BizOpaqueCallbackData: m.Ref}
	switch {
	case m.To.IsZero():
		return channel.Sent{}, fmt.Errorf("%w: no recipient", channel.ErrRejected)
	case m.Template != "" && m.Text == "":
		req.Type = "template"
		req.Template = &struct {
			Name     string            `json:"name"`
			Language map[string]string `json:"language"`
			Comps    []component       `json:"components,omitempty"`
		}{Name: m.Template, Language: map[string]string{"code": m.Language}}
		if len(m.Params) > 0 {
			body := component{Type: "body"}
			for _, p := range m.Params {
				body.Parameters = append(body.Parameters, param{Type: "text", Text: p})
			}
			req.Template.Comps = append(req.Template.Comps, body)
		}
		for i, b := range m.Buttons {
			req.Template.Comps = append(req.Template.Comps, component{Type: "button", SubType: "quick_reply", Index: strconv.Itoa(i),
				Parameters: []param{{Type: "payload", Payload: b.Payload}}})
		}
	case m.Text != "" && m.Template == "":
		req.Type = "text"
		req.Text = &struct {
			Body       string `json:"body"`
			PreviewURL bool   `json:"preview_url"`
		}{Body: m.Text}
	default:
		return channel.Sent{}, fmt.Errorf("%w: either a template or a text", channel.ErrRejected)
	}
	b, err := json.Marshal(req)
	if err != nil {
		return channel.Sent{}, fmt.Errorf("%w: %w", channel.ErrRejected, err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+c.phoneNumberID+"/messages", bytes.NewReader(b))
	if err != nil {
		return channel.Sent{}, fmt.Errorf("%w: %w", channel.ErrRejected, err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(hreq)
	if err != nil {
		return channel.Sent{}, fmt.Errorf("whatsapp: send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return channel.Sent{}, fmt.Errorf("whatsapp: read: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var out struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		}
		if err = json.Unmarshal(raw, &out); err != nil || len(out.Messages) == 0 || out.Messages[0].ID == "" {
			// Accepted, but we cannot tell its id: the caller must not
			// send again blindly; it sees a transient error.
			return channel.Sent{}, fmt.Errorf("whatsapp: unreadable answer (status %d)", resp.StatusCode)
		}
		return channel.Sent{ProviderID: out.Messages[0].ID}, nil
	}
	return channel.Sent{}, classify(resp.StatusCode, raw)
}

// classify maps a failed answer to the contract's errors.
func classify(status int, raw []byte) error {
	var ge graphError
	_ = json.Unmarshal(raw, &ge) //nolint:errcheck // an unreadable error body is classified by status
	code, msg := ge.Error.Code, text.Redact(text.Clip(ge.Error.Message, 200))
	switch {
	case status == http.StatusUnauthorized || code == codeAuth:
		return fmt.Errorf("%w: %s", channel.ErrAuth, msg)
	case code == codeUndeliverable:
		return fmt.Errorf("%w: %s", channel.ErrUndeliverable, msg)
	case status == http.StatusTooManyRequests || status >= 500 ||
		code == codeRateLimit || code == codePairRate || code == codeSpamRate || code == codeTooMany || code == codeTemporary:
		return fmt.Errorf("whatsapp: status %d, code %d: %s", status, code, msg)
	}
	return fmt.Errorf("%w: status %d, code %d: %s", channel.ErrRejected, status, code, msg)
}

// Undeliverable implements channel.Channel: Meta reports a recipient it
// cannot reach with 131026 in a failed status.
func (*Channel) Undeliverable(code string) bool { return code == strconv.Itoa(codeUndeliverable) }

type webhookBody struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				Metadata struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
				Statuses []struct {
					ID        string `json:"id"`
					Status    string `json:"status"`
					Timestamp string `json:"timestamp"`
					Callback  string `json:"biz_opaque_callback_data"`
					Errors    []struct {
						Code json.Number `json:"code"`
					} `json:"errors"`
				} `json:"statuses"`
				Messages []struct {
					From      string `json:"from"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Context   struct {
						ID string `json:"id"`
					} `json:"context"`
					Button struct {
						Payload string `json:"payload"`
					} `json:"button"`
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// VerifyWebhook implements channel.Channel. Events of other phone
// numbers, other fields and unknown kinds are left out.
func (c *Channel) VerifyWebhook(w channel.Webhook) ([]channel.Event, error) {
	sig, ok := strings.CutPrefix(http.Header(w.Header).Get(SignatureHeader), "sha256=")
	got, err := hex.DecodeString(sig)
	if !ok || err != nil {
		return nil, channel.ErrForged
	}
	m := hmac.New(sha256.New, c.secret)
	m.Write(w.Body)
	if !hmac.Equal(got, m.Sum(nil)) {
		return nil, channel.ErrForged
	}
	return c.parse(w.Body)
}

func (c *Channel) parse(body []byte) ([]channel.Event, error) {
	var wb webhookBody
	if err := json.Unmarshal(body, &wb); err != nil {
		return nil, fmt.Errorf("%w: %w", channel.ErrBadWebhook, err)
	}
	if wb.Object != "whatsapp_business_account" {
		return nil, fmt.Errorf("%w: object %q", channel.ErrBadWebhook, text.Clip(wb.Object, 40))
	}
	var events []channel.Event
	for _, e := range wb.Entry {
		for _, ch := range e.Changes {
			if ch.Field != "messages" || ch.Value.Metadata.PhoneNumberID != c.phoneNumberID {
				continue
			}
			for _, s := range ch.Value.Statuses {
				st := channel.Status(s.Status)
				if s.ID == "" || (st != channel.StatusSent && st != channel.StatusDelivered && st != channel.StatusRead && st != channel.StatusFailed) {
					continue
				}
				ev := channel.Event{Kind: channel.StatusEvent, ID: s.ID + ":" + s.Status, ProviderID: s.ID, Ref: s.Callback,
					Status: st, At: unix(s.Timestamp)}
				if len(s.Errors) > 0 {
					ev.ErrorCode = s.Errors[0].Code.String()
				}
				events = append(events, ev)
			}
			for _, msg := range ch.Value.Messages {
				if msg.ID == "" {
					continue
				}
				switch msg.Type {
				case "button":
					events = append(events, channel.Event{Kind: channel.ButtonEvent, ID: msg.ID, ProviderID: msg.Context.ID,
						Payload: msg.Button.Payload, At: unix(msg.Timestamp)})
				case "text":
					body := msg.Text.Body
					if utf8.RuneCountInString(body) > maxText {
						body = string([]rune(body)[:maxText])
					}
					events = append(events, channel.Event{Kind: channel.TextEvent, ID: msg.ID, From: "+" + strings.TrimPrefix(msg.From, "+"),
						Text: body, At: unix(msg.Timestamp)})
				}
			}
		}
	}
	return events, nil
}

// unix reads Meta's timestamps: Unix seconds as a string.
func unix(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}
