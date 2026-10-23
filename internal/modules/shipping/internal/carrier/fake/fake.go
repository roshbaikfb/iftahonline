// Package fake is an in-memory carrier for tests and development
// (ADR-0026). It keeps shipments in the process, so in development the
// shipments it creates are tracked by the same worker that created them.
// Tests drive it with Advance and Settle, and build its webhooks with
// Webhook.
//
// Credentials: any API key but "" and InvalidKey works.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
)

// Name is the fake carrier's name.
const Name = "fake"

// InvalidKey is the API key the fake refuses.
const InvalidKey = "invalid-key"

// Carrier is the fake carrier. The zero value is not usable; use New.
type Carrier struct {
	mu          sync.Mutex
	now         func() time.Time
	seq         int
	byTracking  map[string]*shipment
	byReference map[string]string // key|reference -> tracking
	settled     map[string][]carrier.Settlement
	calls       map[string]int
}

type shipment struct {
	key    string
	req    carrier.ShipmentRequest
	status carrier.Status
	at     time.Time
	events int
}

// New returns an empty fake carrier.
func New() *Carrier {
	return &Carrier{now: time.Now, byTracking: map[string]*shipment{}, byReference: map[string]string{},
		settled: map[string][]carrier.Settlement{}, calls: map[string]int{}}
}

var _ carrier.Carrier = (*Carrier)(nil)

// Name implements carrier.Carrier.
func (*Carrier) Name() string { return Name }

func (c *Carrier) auth(creds carrier.Credentials, op string) error {
	c.calls[op]++
	if creds.APIKey == "" || creds.APIKey == InvalidKey {
		return carrier.ErrAuth
	}
	return nil
}

// Verify implements carrier.Carrier.
func (c *Carrier) Verify(_ context.Context, creds carrier.Credentials) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.auth(creds, "verify")
}

// CreateShipment implements carrier.Carrier.
func (c *Carrier) CreateShipment(_ context.Context, creds carrier.Credentials, req carrier.ShipmentRequest) (carrier.Shipment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.auth(creds, "create"); err != nil {
		return carrier.Shipment{}, err
	}
	if req.Reference == "" || req.Receiver.Phone == "" || req.Address.Governorate == "" || req.COD.IsNegative() {
		return carrier.Shipment{}, fmt.Errorf("%w: reference, phone and governorate are required", carrier.ErrRejected)
	}
	if t, ok := c.byReference[creds.APIKey+"|"+req.Reference]; ok {
		return carrier.Shipment{TrackingNumber: t, ProviderID: "fake-" + t}, nil
	}
	c.seq++
	t := "FK" + strconv.Itoa(100000+c.seq)
	c.byTracking[t] = &shipment{key: creds.APIKey, req: req, status: carrier.StatusCreated, at: c.now().UTC()}
	c.byReference[creds.APIKey+"|"+req.Reference] = t
	return carrier.Shipment{TrackingNumber: t, ProviderID: "fake-" + t}, nil
}

func (c *Carrier) get(creds carrier.Credentials, tracking, op string) (*shipment, error) {
	if err := c.auth(creds, op); err != nil {
		return nil, err
	}
	s, ok := c.byTracking[tracking]
	if !ok || s.key != creds.APIKey { // another account's shipment is not found
		return nil, carrier.ErrNotFound
	}
	return s, nil
}

// Label implements carrier.Carrier: a one-page PDF naming the tracking
// number.
func (c *Carrier) Label(_ context.Context, creds carrier.Credentials, tracking string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.get(creds, tracking, "label"); err != nil {
		return nil, err
	}
	return []byte("%PDF-1.4\n% fake airway bill " + tracking + "\n%%EOF\n"), nil
}

// Track implements carrier.Carrier.
func (c *Carrier) Track(_ context.Context, creds carrier.Credentials, tracking string) (carrier.Tracking, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.get(creds, tracking, "track")
	if err != nil {
		return carrier.Tracking{}, err
	}
	return carrier.Tracking{Status: s.status, RawState: string(s.status), At: s.at}, nil
}

// Cancel implements carrier.Carrier.
func (c *Carrier) Cancel(_ context.Context, creds carrier.Credentials, tracking string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.get(creds, tracking, "cancel")
	if err != nil {
		return err
	}
	switch s.status {
	case carrier.StatusCancelled:
		return nil
	case carrier.StatusCreated:
		s.status, s.at = carrier.StatusCancelled, c.now().UTC()
		return nil
	default:
		return carrier.ErrNotCancellable
	}
}

// CODSettlements implements carrier.Carrier.
func (c *Carrier) CODSettlements(_ context.Context, creds carrier.Credentials, from, to time.Time) ([]carrier.Settlement, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.auth(creds, "settlements"); err != nil {
		return nil, err
	}
	var out []carrier.Settlement
	for _, s := range c.settled[creds.APIKey] {
		if !s.SettledAt.Before(from) && s.SettledAt.Before(to) {
			out = append(out, s)
		}
	}
	return out, nil
}

// webhook is the fake's webhook body.
type webhook struct {
	EventID        string `json:"event_id"`
	TrackingNumber string `json:"tracking_number"`
	Status         string `json:"status"`
}

// ParseWebhook implements carrier.Carrier.
func (*Carrier) ParseWebhook(body []byte) (carrier.WebhookEvent, error) {
	var w webhook
	if err := json.Unmarshal(body, &w); err != nil || w.EventID == "" || w.TrackingNumber == "" {
		return carrier.WebhookEvent{}, carrier.ErrBadWebhook
	}
	return carrier.WebhookEvent{EventID: w.EventID, TrackingNumber: w.TrackingNumber}, nil
}

// Advance moves a shipment to status, as the courier would.
func (c *Carrier) Advance(tracking string, status carrier.Status) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.byTracking[tracking]
	if !ok {
		return carrier.ErrNotFound
	}
	s.status, s.at = status, c.now().UTC()
	s.events++
	return nil
}

// Webhook returns the body the fake would send for the shipment's last
// change: the same bytes on every retry of that change.
func (c *Carrier) Webhook(tracking string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.byTracking[tracking]
	if !ok {
		return nil, carrier.ErrNotFound
	}
	return json.Marshal(webhook{EventID: tracking + "-" + strconv.Itoa(s.events), TrackingNumber: tracking, Status: string(s.status)})
}

// Settle records that the carrier paid out amount for a shipment.
func (c *Carrier) Settle(tracking string, st carrier.Settlement) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.byTracking[tracking]
	if !ok {
		return carrier.ErrNotFound
	}
	st.TrackingNumber = tracking
	c.settled[s.key] = append(c.settled[s.key], st)
	return nil
}

// Request returns what was asked when the shipment was created.
func (c *Carrier) Request(tracking string) (carrier.ShipmentRequest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.byTracking[tracking]
	if !ok {
		return carrier.ShipmentRequest{}, false
	}
	return s.req, true
}

// Calls returns how many times op ("create", "track", …) was called.
func (c *Carrier) Calls(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}
