package whatsapp_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/channeltest"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/whatsapp"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

const (
	phoneID   = "106540352242922"
	token     = "test-access-token"
	appSecret = "test-app-secret"
)

// meta is an in-process stand-in of the Cloud API's messages endpoint.
type meta struct {
	mu       sync.Mutex
	requests []map[string]any
	n        int
	// answer overrides the answer for a recipient: status and error code.
	answer map[string][2]int
}

func (m *meta) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fail := func(status, code int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"(#%d) refused","type":"OAuthException","code":%d,"fbtrace_id":"x"}}`, code, code)
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v26.0/"+phoneID+"/messages" {
		fail(http.StatusNotFound, 100)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+token {
		fail(http.StatusUnauthorized, 190)
		return
	}
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["messaging_product"] != "whatsapp" {
		fail(http.StatusBadRequest, 100)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	if a, ok := m.answer[fmt.Sprint(req["to"])]; ok {
		fail(a[0], a[1])
		return
	}
	m.n++
	_, _ = fmt.Fprintf(w, `{"messaging_product":"whatsapp","contacts":[{"input":%q,"wa_id":"x"}],"messages":[{"id":"wamid.HBgM%d"}]}`, req["to"], m.n)
}

func (m *meta) last() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests[len(m.requests)-1]
}

func sign(body []byte) map[string][]string {
	h := hmac.New(sha256.New, []byte(appSecret))
	h.Write(body)
	return map[string][]string{whatsapp.SignatureHeader: {"sha256=" + hex.EncodeToString(h.Sum(nil))}}
}

func webhook(values ...string) channel.Webhook {
	body := `{"object":"whatsapp_business_account","entry":[{"id":"WABA","changes":[` + strings.Join(values, ",") + `]}]}`
	return channel.Webhook{Header: sign([]byte(body)), Body: []byte(body)}
}

func change(phoneNumberID, inner string) string {
	return `{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"display_phone_number":"15550001111","phone_number_id":"` +
		phoneNumberID + `"},` + inner + `}}`
}

func setup(t *testing.T) (*whatsapp.Channel, *meta) {
	t.Helper()
	m := &meta{answer: map[string][2]int{
		"+201099999999": {http.StatusBadRequest, 131026},
		"+201088888888": {http.StatusBadRequest, 130429},
		"+201077777777": {http.StatusBadRequest, 132001},
		"+201066666666": {http.StatusInternalServerError, 1},
	}}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	c, err := whatsapp.New(whatsapp.Options{GraphURL: srv.URL + "/v26.0", PhoneNumberID: phoneID, AccessToken: token, AppSecret: appSecret})
	if err != nil {
		t.Fatal(err)
	}
	return c, m
}

func mustPhone(t *testing.T, s string) phone.E164 {
	t.Helper()
	p, err := phone.Parse(s, phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestContract(t *testing.T) {
	t.Parallel()
	c, _ := setup(t)
	channeltest.Run(t, channeltest.Harness{Channel: c, To: mustPhone(t, "01012345678"), Unreachable: mustPhone(t, "01099999999"), Templates: true,
		Delivered: func(_ *testing.T, sent channel.Sent, ref string) channel.Webhook {
			return webhook(change(phoneID, `"statuses":[{"id":"`+sent.ProviderID+`","status":"delivered","timestamp":"1761670000","recipient_id":"201012345678","biz_opaque_callback_data":"`+ref+`"}]`))
		}})
}

// TestTemplateRequest: what Meta receives for a template message.
func TestTemplateRequest(t *testing.T) {
	t.Parallel()
	c, m := setup(t)
	ref := channeltest.Ref()
	h := channeltest.Harness{Templates: true}
	if _, err := c.Send(t.Context(), h.Message(mustPhone(t, "01012345678"), ref)); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m.last()) //nolint:errcheck // a decoded map
	got := string(b)
	for _, want := range []string{
		`"to":"+201012345678"`, `"type":"template"`, `"recipient_type":"individual"`, `"biz_opaque_callback_data":"` + ref + `"`,
		`"name":"order_confirmation_v1"`, `"language":{"code":"ar"}`,
		`{"parameters":[{"text":"منى","type":"text"},{"text":"متجر سوق","type":"text"},{"text":"7P0R1S2T","type":"text"},{"text":"520.00 EGP","type":"text"}],"type":"body"}`,
		`{"index":"0","parameters":[{"payload":"b1.confirm","type":"payload"}],"sub_type":"quick_reply","type":"button"}`,
		`{"index":"1","parameters":[{"payload":"b1.cancel","type":"payload"}],"sub_type":"quick_reply","type":"button"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("request lacks %s:\n%s", want, got)
		}
	}
	// A text message (a reply inside the customer's window).
	if _, err := c.Send(t.Context(), channel.Message{To: mustPhone(t, "01012345678"), Text: "تم تأكيد طلبك", Ref: ref}); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(m.last()) //nolint:errcheck // a decoded map
	if !strings.Contains(string(b), `"text":{"body":"تم تأكيد طلبك","preview_url":false}`) || !strings.Contains(string(b), `"type":"text"`) {
		t.Fatalf("text request = %s", b)
	}
}

func TestSendErrors(t *testing.T) {
	t.Parallel()
	c, _ := setup(t)
	h := channeltest.Harness{Templates: true}
	tests := map[string]struct {
		to        string
		want      error
		transient bool
	}{
		"not on WhatsApp":  {"01099999999", channel.ErrUndeliverable, false},
		"throughput":       {"01088888888", nil, true},
		"no such template": {"01077777777", channel.ErrRejected, false},
		"server error":     {"01066666666", nil, true},
	}
	for name, tt := range tests {
		_, err := c.Send(t.Context(), h.Message(mustPhone(t, tt.to), channeltest.Ref()))
		if err == nil {
			t.Errorf("%s: sent", name)
			continue
		}
		permanent := errors.Is(err, channel.ErrUndeliverable) || errors.Is(err, channel.ErrRejected) || errors.Is(err, channel.ErrAuth)
		if tt.transient == permanent || (tt.want != nil && !errors.Is(err, tt.want)) {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad, err := whatsapp.New(whatsapp.Options{GraphURL: "http://127.0.0.1:1/v26.0", PhoneNumberID: phoneID, AccessToken: "x", AppSecret: appSecret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bad.Send(t.Context(), h.Message(mustPhone(t, "01012345678"), "r")); err == nil || errors.Is(err, channel.ErrRejected) {
		t.Fatalf("unreachable API: %v (want transient)", err)
	}
	if _, err = whatsapp.New(whatsapp.Options{PhoneNumberID: phoneID}); err == nil {
		t.Fatal("missing credentials accepted")
	}
}

func TestBadToken(t *testing.T) {
	t.Parallel()
	_, m := setup(t)
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	c, err := whatsapp.New(whatsapp.Options{GraphURL: srv.URL + "/v26.0", PhoneNumberID: phoneID, AccessToken: "expired", AppSecret: appSecret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Send(t.Context(), channel.Message{To: mustPhone(t, "01012345678"), Text: "x"}); !errors.Is(err, channel.ErrAuth) {
		t.Fatalf("expired token: %v", err)
	}
}

func TestWebhookEvents(t *testing.T) {
	t.Parallel()
	c, _ := setup(t)
	w := webhook(
		change(phoneID, `"statuses":[
			{"id":"wamid.A","status":"sent","timestamp":"1761670000","biz_opaque_callback_data":"m1.ref"},
			{"id":"wamid.A","status":"read","timestamp":"1761670060","biz_opaque_callback_data":"m1.ref"},
			{"id":"wamid.B","status":"failed","timestamp":"1761670001","biz_opaque_callback_data":"m1.refb","errors":[{"code":131026,"title":"Message undeliverable"}]},
			{"id":"wamid.C","status":"played","timestamp":"1761670002"}]`),
		change(phoneID, `"contacts":[{"profile":{"name":"Mona"},"wa_id":"201012345678"}],"messages":[
			{"context":{"from":"15550001111","id":"wamid.A"},"from":"201012345678","id":"wamid.IN1","timestamp":"1761670100","type":"button","button":{"payload":"b1.payload","text":"تأكيد الطلب"}},
			{"from":"201012345678","id":"wamid.IN2","timestamp":"1761670200","type":"text","text":{"body":"أكد طلبي K7Q2"}},
			{"from":"201012345678","id":"wamid.IN3","timestamp":"1761670300","type":"image","image":{"id":"x"}}]`),
		change("999999999999", `"statuses":[{"id":"wamid.OTHER","status":"delivered","timestamp":"1761670000"}]`),
		`{"field":"account_update","value":{}}`,
	)
	events, err := c.VerifyWebhook(w)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("events = %d: %+v", len(events), events)
	}
	if e := events[1]; e.ID != "wamid.A:read" || e.Status != channel.StatusRead || e.Ref != "m1.ref" || e.At.Unix() != 1761670060 {
		t.Fatalf("read = %+v", e)
	}
	if e := events[2]; e.Status != channel.StatusFailed || e.ErrorCode != "131026" || !c.Undeliverable(e.ErrorCode) || c.Undeliverable("131000") {
		t.Fatalf("failed = %+v", e)
	}
	if e := events[3]; e.Kind != channel.ButtonEvent || e.ID != "wamid.IN1" || e.ProviderID != "wamid.A" || e.Payload != "b1.payload" {
		t.Fatalf("button = %+v", e)
	}
	if e := events[4]; e.Kind != channel.TextEvent || e.From != "+201012345678" || e.Text != "أكد طلبي K7Q2" {
		t.Fatalf("text = %+v", e)
	}
	for _, bad := range []string{`{"object":"page","entry":[]}`, `not json`} {
		if _, err = c.VerifyWebhook(channel.Webhook{Header: sign([]byte(bad)), Body: []byte(bad)}); !errors.Is(err, channel.ErrBadWebhook) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err = c.VerifyWebhook(channel.Webhook{Header: map[string][]string{whatsapp.SignatureHeader: {"sha256=zz"}}, Body: w.Body}); !errors.Is(err, channel.ErrForged) {
		t.Errorf("not hex: %v", err)
	}
	if _, err = c.VerifyWebhook(channel.Webhook{Header: map[string][]string{whatsapp.SignatureHeader: {hex.EncodeToString([]byte("x"))}}, Body: w.Body}); !errors.Is(err, channel.ErrForged) {
		t.Errorf("no sha256= prefix: %v", err)
	}
}

// FuzzVerifyWebhook: any signed body is read without panicking, and
// every event it yields is of a known kind with an id.
func FuzzVerifyWebhook(f *testing.F) {
	f.Add([]byte(webhook(change(phoneID, `"statuses":[{"id":"wamid.A","status":"sent","timestamp":"1"}]`)).Body))
	f.Add([]byte(`{"object":"whatsapp_business_account","entry":[{"changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"` + phoneID + `"},"messages":[{"id":"x","type":"text","text":{"body":"` + strings.Repeat("ا", 1200) + `"}}]}}]}]}`))
	f.Add([]byte(`{}`))
	c, err := whatsapp.New(whatsapp.Options{PhoneNumberID: phoneID, AccessToken: token, AppSecret: appSecret})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		events, err := c.VerifyWebhook(channel.Webhook{Header: sign(body), Body: body})
		if err != nil {
			if !errors.Is(err, channel.ErrBadWebhook) {
				t.Fatalf("signed body refused as %v", err)
			}
			return
		}
		for _, e := range events {
			if e.ID == "" || (e.Kind != channel.StatusEvent && e.Kind != channel.ButtonEvent && e.Kind != channel.TextEvent) {
				t.Fatalf("event %+v", e)
			}
			if len([]rune(e.Text)) > 1000 {
				t.Fatalf("text of %d characters", len([]rune(e.Text)))
			}
		}
	})
}
