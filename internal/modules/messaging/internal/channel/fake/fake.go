// Package fake is an in-memory messaging channel for development, demos
// and tests (MESSAGING_FAKE_CHANNEL; refused in production). It records
// what it sends, refuses the numbers it is told cannot be reached, and
// signs the webhooks it is asked to produce with HMAC-SHA256.
package fake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
)

// SignatureHeader carries a fake webhook's signature.
const SignatureHeader = "X-Fake-Channel-Signature"

// UndeliverableCode is the failed status code that means unreachable.
const UndeliverableCode = "fake_unreachable"

// Channel is the fake channel.
type Channel struct {
	name   string
	secret []byte
	seq    atomic.Int64

	mu          sync.Mutex
	sent        []channel.Message
	unreachable map[string]bool
	fail        error
}

var _ channel.Channel = (*Channel)(nil)

// New returns a fake channel called name that signs with secret.
func New(name string, secret []byte) *Channel {
	return &Channel{name: name, secret: secret, unreachable: map[string]bool{}}
}

// Name implements channel.Channel.
func (c *Channel) Name() string { return c.name }

// Unreachable makes sends to e164 fail with ErrUndeliverable.
func (c *Channel) Unreachable(e164 string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unreachable[e164] = true
}

// FailWith makes every send fail with err (nil: succeed again).
func (c *Channel) FailWith(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = err
}

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, m channel.Message) (channel.Sent, error) {
	if err := ctx.Err(); err != nil {
		return channel.Sent{}, err
	}
	if m.To.IsZero() || (m.Template == "") == (m.Text == "") {
		return channel.Sent{}, fmt.Errorf("%w: a recipient and either a template or a text", channel.ErrRejected)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return channel.Sent{}, c.fail
	}
	if c.unreachable[m.To.Unmasked()] {
		return channel.Sent{}, channel.ErrUndeliverable
	}
	c.sent = append(c.sent, m)
	return channel.Sent{ProviderID: fmt.Sprintf("%s.%d", c.name, c.seq.Add(1))}, nil
}

// Sent returns what was sent, in order.
func (c *Channel) Sent() []channel.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]channel.Message(nil), c.sent...)
}

// Webhook is a signed delivery of events, as the channel would send it.
func (c *Channel) Webhook(events ...channel.Event) channel.Webhook {
	for i := range events {
		if events[i].At.IsZero() {
			events[i].At = time.Now().UTC().Truncate(time.Second)
		}
	}
	body, _ := json.Marshal(events) //nolint:errcheck // plain structs
	return channel.Webhook{Header: http.Header{SignatureHeader: {Sign(c.secret, body)}}, Body: body}
}

// Sign is the signature header value for body.
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// VerifyWebhook implements channel.Channel.
func (c *Channel) VerifyWebhook(w channel.Webhook) ([]channel.Event, error) {
	got, err := hex.DecodeString(http.Header(w.Header).Get(SignatureHeader))
	if err != nil {
		return nil, channel.ErrForged
	}
	m := hmac.New(sha256.New, c.secret)
	m.Write(w.Body)
	if !hmac.Equal(got, m.Sum(nil)) {
		return nil, channel.ErrForged
	}
	var events []channel.Event
	if err = json.Unmarshal(w.Body, &events); err != nil {
		return nil, fmt.Errorf("%w: %w", channel.ErrBadWebhook, err)
	}
	return events, nil
}

// Undeliverable implements channel.Channel.
func (*Channel) Undeliverable(code string) bool { return code == UndeliverableCode }
