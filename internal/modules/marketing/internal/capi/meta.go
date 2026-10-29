package capi

import (
	"context"
	"net/http"
	"net/url"
)

// MetaBase is Meta's Graph API.
const MetaBase = "https://graph.facebook.com/v26.0"

// Meta sends to Meta's Conversions API: POST /{pixel}/events.
type Meta struct {
	Base   string // MetaBase, or a test server
	Client *http.Client
}

type metaEvent struct {
	EventName      string         `json:"event_name"`
	EventTime      int64          `json:"event_time"`
	EventID        string         `json:"event_id"`
	ActionSource   string         `json:"action_source"`
	EventSourceURL string         `json:"event_source_url,omitempty"`
	UserData       map[string]any `json:"user_data"`
	CustomData     map[string]any `json:"custom_data"`
}

type metaBody struct {
	Data          []metaEvent `json:"data"`
	AccessToken   string      `json:"access_token"` // in the body, never in a URL that may be logged
	TestEventCode string      `json:"test_event_code,omitempty"`
}

// metaUser is Meta's user_data: normalized, hashed identifiers; the IP,
// user agent and its cookies as they are.
func metaUser(u User) map[string]any {
	m := map[string]any{}
	set := func(k string, v any, ok bool) {
		if ok {
			m[k] = v
		}
	}
	set("ph", []string{hash(phoneDigits(u.Phone))}, u.Phone != "")
	set("external_id", []string{hash(u.ExternalID)}, u.ExternalID != "")
	set("country", []string{hash(lower(u.Country))}, u.Country != "")
	set("client_ip_address", u.IP, u.IP != "")
	set("client_user_agent", u.UserAgent, u.UserAgent != "")
	set("fbp", u.FBP, u.FBP != "")
	set("fbc", u.FBC, u.FBC != "")
	return m
}

func custom(e Event) map[string]any {
	m := map[string]any{"currency": e.Currency, "value": value(e.ValueMinor, e.Units), "content_type": "product", "content_ids": e.Items}
	if e.OrderID != "" {
		m["order_id"] = e.OrderID
	}
	return m
}

// Send implements Sender.
func (s Meta) Send(ctx context.Context, p Pixel, e Event) error {
	body := metaBody{AccessToken: p.Token, TestEventCode: p.TestCode, Data: []metaEvent{{
		EventName: e.Name, EventTime: e.Time.Unix(), EventID: e.ID, ActionSource: "website", EventSourceURL: e.URL,
		UserData: metaUser(e.User), CustomData: custom(e),
	}}}
	_, err := post(ctx, s.Client, s.Base+"/"+url.PathEscape(p.ID)+"/events", nil, body)
	return err
}
