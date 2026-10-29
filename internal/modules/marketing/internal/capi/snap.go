package capi

import (
	"context"
	"net/http"
	"net/url"
)

// SnapBase is Snapchat's Conversions API (v3).
const SnapBase = "https://tr.snapchat.com/v3"

// Snap sends to Snapchat's Conversions API: POST /{pixel}/events, with
// the token in the query string as Snapchat documents it (post scrubs it
// from errors).
type Snap struct {
	Base   string // SnapBase, or a test server
	Client *http.Client
}

// snapNames are Snapchat's standard events for ours.
var snapNames = map[string]string{AddToCart: "ADD_CART", InitiateCheckout: "START_CHECKOUT", Purchase: "PURCHASE"} //nolint:gochecknoglobals // constant table

// Send implements Sender.
func (s Snap) Send(ctx context.Context, p Pixel, e Event) error {
	u := e.User
	user := map[string]any{}
	set := func(k string, v any, ok bool) {
		if ok {
			user[k] = v
		}
	}
	set("ph", []string{hash(phoneDigits(u.Phone))}, u.Phone != "")
	set("external_id", []string{hash(u.ExternalID)}, u.ExternalID != "")
	set("country", []string{hash(lower(u.Country))}, u.Country != "")
	set("client_ip_address", u.IP, u.IP != "")
	set("client_user_agent", u.UserAgent, u.UserAgent != "")
	set("sc_click_id", u.ScCID, u.ScCID != "")
	ev := metaEvent{EventName: snapNames[e.Name], EventTime: e.Time.Unix(), EventID: e.ID, ActionSource: "WEB", EventSourceURL: e.URL,
		UserData: user, CustomData: custom(e)}
	q := url.Values{"access_token": {p.Token}}
	_, err := post(ctx, s.Client, s.Base+"/"+url.PathEscape(p.ID)+"/events?"+q.Encode(), nil, map[string]any{"data": []metaEvent{ev}})
	return err
}
