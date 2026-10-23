// Package bosta is the Bosta adapter (ADR-0026), for Egypt.
//
// Bosta publishes no API reference this code could be checked against.
// Paths, fields and state codes follow Bosta's own Node.js SDK (base
// URLs, the raw API key in Authorization) and the community .NET SDK
// (the v2 paths, the delivery and webhook shapes, the state codes). The
// shared contract suite runs against a sandbox account when
// BOSTA_SANDBOX_API_KEY is set; until it has, treat the mapping as
// unverified.
//
// Bosta has no API for cash settlements (they are in its dashboard), so
// CODSettlements is carrier.ErrUnsupported, and its webhooks carry no
// signature: the caller authenticates them with the connection's secret.
package bosta

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
)

// Name is Bosta's carrier name.
const Name = "bosta"

// Base URLs.
const (
	ProductionURL = "https://app.bosta.co/api/v2"
	StagingURL    = "https://stg-app.bosta.co/api/v2"
)

const (
	deliveryTypeSend = 10
	maxResponse      = 8 << 20 // labels are base64 PDFs
)

// Options configures New.
type Options struct {
	BaseURL string        // default ProductionURL
	Timeout time.Duration // per request; default 15 s
	Retry   *retry.Policy // default retry.Default()
	Client  *http.Client  // default a new client with Timeout
}

// Carrier is the Bosta adapter.
type Carrier struct {
	base  string
	http  *http.Client
	retry retry.Policy
}

var _ carrier.Carrier = (*Carrier)(nil)

// New returns the adapter.
func New(o Options) *Carrier {
	if o.BaseURL == "" {
		o.BaseURL = ProductionURL
	}
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.Timeout}
	}
	p := retry.Default()
	if o.Retry != nil {
		p = *o.Retry
	}
	return &Carrier{base: strings.TrimRight(o.BaseURL, "/"), http: o.Client, retry: p}
}

// Name implements carrier.Carrier.
func (*Carrier) Name() string { return Name }

// envelope is every Bosta response.
type envelope struct {
	Success *bool           `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// do sends one request and decodes data into out (when not nil). It
// classifies failures: auth, rejected and not found are permanent;
// rate limits, server errors and the network are retried by the caller.
func (c *Carrier) do(ctx context.Context, creds carrier.Credentials, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return retry.Permanent(fmt.Errorf("bosta: encode: %w", err))
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return retry.Permanent(fmt.Errorf("bosta: request: %w", err))
	}
	req.Header.Set("Authorization", creds.APIKey) // Bosta takes the key itself, no scheme
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bosta: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("bosta: read: %w", err)
	}
	var env envelope
	_ = json.Unmarshal(raw, &env) //nolint:errcheck // an unreadable body is classified by status below
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return retry.Permanent(carrier.ErrAuth)
	case resp.StatusCode == http.StatusNotFound:
		return retry.Permanent(carrier.ErrNotFound)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("bosta: %s %s: status %d", method, path, resp.StatusCode)
	case resp.StatusCode >= 400 || (env.Success != nil && !*env.Success):
		return retry.Permanent(fmt.Errorf("%w: %s (status %d)", carrier.ErrRejected, clip(env.Message), resp.StatusCode))
	}
	if out == nil {
		return nil
	}
	if err = json.Unmarshal(env.Data, out); err != nil {
		return retry.Permanent(fmt.Errorf("bosta: decode %s %s: %w", method, path, err))
	}
	return nil
}

func (c *Carrier) call(ctx context.Context, creds carrier.Credentials, method, path string, in, out any) error {
	return retry.Do(ctx, c.retry, func(ctx context.Context) error { return c.do(ctx, creds, method, path, in, out) })
}

// Verify implements carrier.Carrier with a one-result search.
func (c *Carrier) Verify(ctx context.Context, creds carrier.Credentials) error {
	if creds.APIKey == "" {
		return carrier.ErrAuth
	}
	return c.call(ctx, creds, http.MethodPost, "/deliveries/search", map[string]any{"pageId": 1, "pageLimit": 1}, nil)
}

type createRequest struct {
	Type               int            `json:"type"`
	COD                json.Number    `json:"cod"`
	Specs              specs          `json:"specs"`
	Notes              string         `json:"notes,omitempty"`
	DropOffAddress     dropOffAddress `json:"dropOffAddress"`
	Receiver           receiver       `json:"receiver"`
	BusinessReference  string         `json:"businessReference"`
	AllowToOpenPackage bool           `json:"allowToOpenPackage"`
}

type specs struct {
	PackageType    string         `json:"packageType"`
	Size           string         `json:"size"`
	PackageDetails packageDetails `json:"packageDetails"`
}

type packageDetails struct {
	ItemsCount  int    `json:"itemsCount"`
	Description string `json:"description,omitempty"`
}

type dropOffAddress struct {
	City       string `json:"city"`
	FirstLine  string `json:"firstLine"`
	SecondLine string `json:"secondLine,omitempty"`
}

type receiver struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Phone     string `json:"phone"`
}

type delivery struct {
	ID                string `json:"_id"`
	TrackingNumber    string `json:"trackingNumber"`
	BusinessReference string `json:"businessReference"`
	State             struct {
		Code  int    `json:"code"`
		Value string `json:"value"`
	} `json:"state"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CreateShipment implements carrier.Carrier. Bosta takes no idempotency
// key, so every create first looks the reference up: a create retried by
// its job, or whose answer was lost, finds the first delivery instead of
// making another. It costs one search per shipment.
func (c *Carrier) CreateShipment(ctx context.Context, creds carrier.Credentials, req carrier.ShipmentRequest) (carrier.Shipment, error) {
	body, err := createBody(req)
	if err != nil {
		return carrier.Shipment{}, err
	}
	var out carrier.Shipment
	err = retry.Do(ctx, c.retry, func(ctx context.Context) error {
		found, ok, findErr := c.byReference(ctx, creds, req.Reference)
		if findErr != nil {
			return findErr
		}
		if ok {
			out = found
			return nil
		}
		var d delivery
		if doErr := c.do(ctx, creds, http.MethodPost, "/deliveries?apiVersion=1", body, &d); doErr != nil {
			return doErr
		}
		if d.TrackingNumber == "" {
			return retry.Permanent(errors.New("bosta: create returned no tracking number"))
		}
		out = carrier.Shipment{TrackingNumber: d.TrackingNumber, ProviderID: d.ID}
		return nil
	})
	return out, err
}

func createBody(req carrier.ShipmentRequest) (createRequest, error) {
	city, ok := City(req.Address.Governorate)
	if req.Reference == "" || !ok || req.Receiver.Phone == "" || req.COD.IsNegative() || req.COD.Currency() != money.EGP {
		return createRequest{}, fmt.Errorf("%w: a reference, a governorate Bosta serves, a phone and a non-negative COD in EGP are required", carrier.ErrRejected)
	}
	first, last := splitName(req.Receiver.Name)
	return createRequest{
		Type: deliveryTypeSend, COD: majorUnits(req.COD), Notes: clip(req.Notes),
		Specs:             specs{PackageType: "Parcel", Size: "SMALL", PackageDetails: packageDetails{ItemsCount: max(req.Items, 1), Description: clip(req.Description)}},
		DropOffAddress:    dropOffAddress{City: city, FirstLine: req.Address.Line, SecondLine: req.Address.City},
		Receiver:          receiver{FirstName: first, LastName: last, Phone: localPhone(req.Receiver.Phone)},
		BusinessReference: req.Reference,
	}, nil
}

// byReference finds a delivery by our reference.
func (c *Carrier) byReference(ctx context.Context, creds carrier.Credentials, ref string) (carrier.Shipment, bool, error) {
	var raw json.RawMessage
	if err := c.do(ctx, creds, http.MethodPost, "/deliveries/search", map[string]any{"businessReference": ref, "pageId": 1}, &raw); err != nil {
		return carrier.Shipment{}, false, err
	}
	// The SDKs disagree on the shape: a list, or a page with deliveries.
	var list []delivery
	if err := json.Unmarshal(raw, &list); err != nil {
		var page struct {
			Deliveries []delivery `json:"deliveries"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			return carrier.Shipment{}, false, retry.Permanent(fmt.Errorf("bosta: decode search: %w", err))
		}
		list = page.Deliveries
	}
	for _, d := range list {
		if d.BusinessReference == ref && d.TrackingNumber != "" {
			return carrier.Shipment{TrackingNumber: d.TrackingNumber, ProviderID: d.ID}, true, nil
		}
	}
	return carrier.Shipment{}, false, nil
}

// Label implements carrier.Carrier: the A6 airway bill in Arabic.
func (c *Carrier) Label(ctx context.Context, creds carrier.Credentials, tracking string) ([]byte, error) {
	var raw json.RawMessage
	in := map[string]string{"trackingNumbers": tracking, "requestedAwbType": "A6", "lang": "ar"}
	if err := c.call(ctx, creds, http.MethodPost, "/deliveries/mass-awb", in, &raw); err != nil {
		return nil, err
	}
	var b64 string
	if err := json.Unmarshal(raw, &b64); err != nil {
		var obj struct {
			AWB string `json:"awb"`
		}
		if err = json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("bosta: decode label: %w", err)
		}
		b64 = obj.AWB
	}
	pdf, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		return nil, errors.New("bosta: the label is not a PDF")
	}
	return pdf, nil
}

// Track implements carrier.Carrier.
func (c *Carrier) Track(ctx context.Context, creds carrier.Credentials, tracking string) (carrier.Tracking, error) {
	var d delivery
	if err := c.call(ctx, creds, http.MethodGet, "/deliveries/business/"+url.PathEscape(tracking), nil, &d); err != nil {
		return carrier.Tracking{}, err
	}
	return carrier.Tracking{Status: MapState(d.State.Code), RawState: strconv.Itoa(d.State.Code) + " " + clip(d.State.Value), At: d.UpdatedAt}, nil
}

// Cancel implements carrier.Carrier ("terminate" in Bosta's words). The
// state is checked first, so cancelling twice succeeds and a parcel
// already picked up is ErrNotCancellable.
func (c *Carrier) Cancel(ctx context.Context, creds carrier.Credentials, tracking string) error {
	t, err := c.Track(ctx, creds, tracking)
	if err != nil {
		return err
	}
	switch t.Status {
	case carrier.StatusCancelled:
		return nil
	case carrier.StatusCreated:
	default:
		return carrier.ErrNotCancellable
	}
	err = c.call(ctx, creds, http.MethodDelete, "/deliveries/business/"+url.PathEscape(tracking)+"/terminate", nil, nil)
	if errors.Is(err, carrier.ErrRejected) {
		return carrier.ErrNotCancellable
	}
	return err
}

// CODSettlements implements carrier.Carrier: Bosta has no API for them.
func (*Carrier) CODSettlements(context.Context, carrier.Credentials, time.Time, time.Time) ([]carrier.Settlement, error) {
	return nil, carrier.ErrUnsupported
}

// webhook is Bosta's webhook body.
type webhook struct {
	ID             string      `json:"_id"`
	TrackingNumber string      `json:"trackingNumber"`
	State          json.Number `json:"state"`
	TimeStamp      json.Number `json:"timeStamp"`
}

// ParseWebhook implements carrier.Carrier. Bosta sends no event id; one
// change of one delivery is its tracking number, state and time, which
// Bosta repeats unchanged when it retries.
func (*Carrier) ParseWebhook(body []byte) (carrier.WebhookEvent, error) {
	var w webhook
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&w); err != nil || w.TrackingNumber == "" || w.State == "" || len(w.TrackingNumber) > 64 {
		return carrier.WebhookEvent{}, carrier.ErrBadWebhook
	}
	if _, err := w.State.Int64(); err != nil {
		return carrier.WebhookEvent{}, carrier.ErrBadWebhook
	}
	if strings.ContainsAny(w.TrackingNumber, " \t\r\n:") {
		return carrier.WebhookEvent{}, carrier.ErrBadWebhook
	}
	id := w.TrackingNumber + ":" + w.State.String()
	if ts := w.TimeStamp.String(); ts != "" {
		if _, err := w.TimeStamp.Int64(); err != nil {
			return carrier.WebhookEvent{}, carrier.ErrBadWebhook
		}
		id += ":" + ts
	}
	return carrier.WebhookEvent{EventID: id, TrackingNumber: w.TrackingNumber}, nil
}

// MapState maps Bosta's state codes onto our statuses. Codes not listed
// are StatusUnknown: they move nothing.
func MapState(code int) carrier.Status {
	switch code {
	case 10, 20, 21:
		return carrier.StatusCreated // created, pickup requested
	case 22, 23, 24, 25, 30:
		return carrier.StatusInTransit // picked up, at a hub, in transit
	case 41, 43:
		return carrier.StatusOutForDelivery
	case 45:
		return carrier.StatusDelivered
	case 46, 47:
		return carrier.StatusFailedAttempt // an exception or a failed attempt
	case 48:
		return carrier.StatusCancelled // terminated
	case 49:
		return carrier.StatusReturned // returned to the business
	}
	return carrier.StatusUnknown
}

// cities are Bosta's city names for Egypt's governorates, where they are
// spelled otherwise than platform/geo's English names.
var cities = map[string]string{ //nolint:gochecknoglobals // constant table
	"EG-AST": "Assuit", "EG-FYM": "Fayoum", "EG-KFS": "Kafr Alsheikh",
}

// City is Bosta's city for an Egyptian governorate; false outside Egypt.
func City(governorate string) (string, bool) {
	if c, ok := cities[governorate]; ok {
		return c, true
	}
	a, ok := geo.Lookup(geo.EG, governorate)
	if !ok {
		return "", false
	}
	return a.NameEN, true
}

// localPhone writes an Egyptian E.164 number the local way couriers dial
// it (+201012345678 → 01012345678); others stay E.164.
func localPhone(e164 string) string {
	if strings.HasPrefix(e164, "+20") {
		return "0" + e164[3:]
	}
	return e164
}

func splitName(name string) (first, last string) {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "-", "-"
	case 1:
		return parts[0], parts[0]
	}
	return parts[0], strings.Join(parts[1:], " ")
}

// majorUnits writes an EGP amount in pounds, Bosta's unit for COD.
func majorUnits(m money.Money) json.Number {
	a := m.Amount()
	return json.Number(strconv.FormatInt(a/100, 10) + "." + fmt.Sprintf("%02d", a%100))
}

func clip(s string) string {
	const maxLen = 300
	if r := []rune(s); len(r) > maxLen {
		return string(r[:maxLen])
	}
	return s
}
