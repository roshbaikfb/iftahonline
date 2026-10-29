package capi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// GA4Base is GA4's Measurement Protocol.
const GA4Base = "https://www.google-analytics.com"

// GA4 sends to GA4's Measurement Protocol: POST /mp/collect, with the
// measurement ID and the API secret in the query string, as Google
// documents it. GA4 counts a purchase once per transaction_id, so the
// browser's and the server's purchase are one.
type GA4 struct {
	Base   string // GA4Base, or a test server
	Client *http.Client
}

// ga4Names are GA4's recommended events for ours.
var ga4Names = map[string]string{AddToCart: "add_to_cart", InitiateCheckout: "begin_checkout", Purchase: "purchase"} //nolint:gochecknoglobals // constant table

// Send implements Sender. The client ID ties events to a visitor; we do
// not read GA's cookie, so it is our customer's (or the event's) ID: the
// server's events count, but do not join the browser's session.
func (s GA4) Send(ctx context.Context, p Pixel, e Event) error {
	items := make([]map[string]any, len(e.Items))
	for i, id := range e.Items {
		items[i] = map[string]any{"item_id": id}
	}
	params := map[string]any{"currency": e.Currency, "value": value(e.ValueMinor, e.Units), "items": items, "event_id": e.ID,
		"engagement_time_msec": 1}
	if e.OrderID != "" {
		params["transaction_id"] = e.OrderID
	}
	client := e.User.ExternalID
	if client == "" {
		client = e.ID
	}
	body := map[string]any{"client_id": "iftah." + client, "timestamp_micros": e.Time.UnixMicro(),
		"events": []map[string]any{{"name": ga4Names[e.Name], "params": params}}}
	q := url.Values{"measurement_id": {p.ID}, "api_secret": {p.Token}}
	_, err := post(ctx, s.Client, s.Base+"/mp/collect?"+q.Encode(), nil, body)
	return err
}

func lower(s string) string { return strings.ToLower(s) }
