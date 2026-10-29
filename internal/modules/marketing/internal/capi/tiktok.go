package capi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// TikTokBase is TikTok's Business API.
const TikTokBase = "https://business-api.tiktok.com/open_api/v1.3"

// TikTok sends to TikTok's Events API: POST /event/track/, with the
// token in the Access-Token header.
type TikTok struct {
	Base   string // TikTokBase, or a test server
	Client *http.Client
}

// tiktokNames are TikTok's standard events for ours.
var tiktokNames = map[string]string{AddToCart: "AddToCart", InitiateCheckout: "InitiateCheckout", Purchase: "CompletePayment"} //nolint:gochecknoglobals // constant table

type tiktokEvent struct {
	Event      string         `json:"event"`
	EventTime  int64          `json:"event_time"`
	EventID    string         `json:"event_id"`
	User       map[string]any `json:"user"`
	Properties map[string]any `json:"properties"`
	Page       map[string]any `json:"page,omitempty"`
}

type tiktokBody struct {
	EventSource   string        `json:"event_source"`
	EventSourceID string        `json:"event_source_id"`
	TestEventCode string        `json:"test_event_code,omitempty"`
	Data          []tiktokEvent `json:"data"`
}

// Send implements Sender.
func (s TikTok) Send(ctx context.Context, p Pixel, e Event) error {
	u := e.User
	user := map[string]any{}
	add := func(k, v string) {
		if v != "" {
			user[k] = v
		}
	}
	add("phone", hash(u.Phone)) // E.164, with "+"
	add("external_id", hash(u.ExternalID))
	add("ip", u.IP)
	add("user_agent", u.UserAgent)
	add("ttp", u.TTP)
	add("ttclid", u.TTCLID)
	contents := make([]map[string]any, len(e.Items))
	for i, id := range e.Items {
		contents[i] = map[string]any{"content_id": id}
	}
	props := map[string]any{"currency": e.Currency, "value": value(e.ValueMinor, e.Units), "content_type": "product", "contents": contents}
	if e.OrderID != "" {
		props["order_id"] = e.OrderID
	}
	ev := tiktokEvent{Event: tiktokNames[e.Name], EventTime: e.Time.Unix(), EventID: e.ID, User: user, Properties: props}
	if e.URL != "" {
		ev.Page = map[string]any{"url": e.URL}
	}
	out, err := post(ctx, s.Client, s.Base+"/event/track/", http.Header{"Access-Token": {p.Token}},
		tiktokBody{EventSource: "web", EventSourceID: p.ID, TestEventCode: p.TestCode, Data: []tiktokEvent{ev}})
	if err != nil {
		return err
	}
	// TikTok answers 200 with its own code: 0 is accepted.
	var res struct {
		Code int `json:"code"`
	}
	if err = json.Unmarshal(out, &res); err != nil {
		return fmt.Errorf("capi: tiktok answer: %w", err)
	}
	if res.Code != 0 {
		return fmt.Errorf("%w: tiktok code %d", ErrRejected, res.Code)
	}
	return nil
}
