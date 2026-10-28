package twilio_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/channeltest"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/twilio"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
)

const (
	accountSID = "AC00000000000000000000000000000001"
	authToken  = "test-auth-token"
	callback   = "https://app.example.com/v1/webhooks/twilio"
)

// api is an in-process stand-in of Twilio's Messages resource.
type api struct {
	mu    sync.Mutex
	forms []url.Values
	n     int
	state map[string]string // sid -> status
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(status, code int) {
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"code":%d,"message":"refused","more_info":"https://www.twilio.com/docs/errors/%d","status":%d}`, code, code, status)
	}
	if u, p, ok := r.BasicAuth(); !ok || u != accountSID || p != authToken {
		fail(http.StatusUnauthorized, 20003)
		return
	}
	base := "/2010-04-01/Accounts/" + accountSID + "/Messages"
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == base+".json":
		if err := r.ParseForm(); err != nil {
			fail(http.StatusBadRequest, 21602)
			return
		}
		a.forms = append(a.forms, r.PostForm)
		switch r.PostForm.Get("To") {
		case "+201099999999":
			fail(http.StatusBadRequest, 21614)
			return
		case "+201066666666":
			fail(http.StatusServiceUnavailable, 20503)
			return
		case "+201077777777":
			fail(http.StatusBadRequest, 21408)
			return
		}
		a.n++
		sid := fmt.Sprintf("SM%032x", a.n)
		a.state[sid] = "queued"
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"sid":%q,"status":"queued","error_code":null}`, sid)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, base+"/"):
		sid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, base+"/"), ".json")
		st, ok := a.state[sid]
		if !ok {
			fail(http.StatusNotFound, 20404)
			return
		}
		code := "null"
		if st == "undelivered" {
			code = "30003"
		}
		_, _ = fmt.Fprintf(w, `{"sid":%q,"status":%q,"error_code":%s}`, sid, st, code)
	default:
		fail(http.StatusNotFound, 20404)
	}
}

func setup(t *testing.T) (*twilio.Channel, *api) {
	t.Helper()
	a := &api{state: map[string]string{}}
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	p := retry.Policy{MaxAttempts: 2, Initial: time.Millisecond, Max: time.Millisecond}
	c, err := twilio.New(twilio.Options{BaseURL: srv.URL, AccountSID: accountSID, AuthToken: authToken, From: "MG00000000000000000000000000000001",
		CallbackURL: callback, Retry: &p})
	if err != nil {
		t.Fatal(err)
	}
	return c, a
}

func mustPhone(t *testing.T, s string) phone.E164 {
	t.Helper()
	p, err := phone.Parse(s, phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func statusWebhook(sid, status, ref, code string) channel.Webhook {
	form := url.Values{"MessageSid": {sid}, "MessageStatus": {status}, "AccountSid": {accountSID}, "To": {"+201012345678"}, "ApiVersion": {"2010-04-01"}}
	if code != "" {
		form.Set("ErrorCode", code)
	}
	q := url.Values{"m": {ref}}
	sig := twilio.Sign(authToken, callback+"?"+q.Encode(), form)
	return channel.Webhook{Header: map[string][]string{twilio.SignatureHeader: {sig}}, Query: q, Body: []byte(form.Encode())}
}

func TestContract(t *testing.T) {
	t.Parallel()
	c, _ := setup(t)
	channeltest.Run(t, channeltest.Harness{Channel: c, To: mustPhone(t, "01012345678"), Unreachable: mustPhone(t, "01099999999"),
		Delivered: func(_ *testing.T, sent channel.Sent, ref string) channel.Webhook {
			return statusWebhook(sent.ProviderID, "delivered", ref, "")
		}})
}

func TestSendRequest(t *testing.T) {
	t.Parallel()
	c, a := setup(t)
	ref := channeltest.Ref()
	if _, err := c.Send(t.Context(), channel.Message{To: mustPhone(t, "01012345678"), Text: "أكّد طلبك", Ref: ref}); err != nil {
		t.Fatal(err)
	}
	f := a.forms[len(a.forms)-1]
	if f.Get("To") != "+201012345678" || f.Get("Body") != "أكّد طلبك" || f.Get("MessagingServiceSid") == "" || f.Get("From") != "" ||
		f.Get("StatusCallback") != callback+"?m="+ref {
		t.Fatalf("form = %v", f)
	}
	if _, err := c.Send(t.Context(), channel.Message{To: mustPhone(t, "01012345678"), Template: "x"}); !errors.Is(err, channel.ErrRejected) {
		t.Fatalf("template over SMS: %v", err)
	}
}

func TestSendErrors(t *testing.T) {
	t.Parallel()
	c, _ := setup(t)
	if _, err := c.Send(t.Context(), channel.Message{To: mustPhone(t, "01066666666"), Text: "x"}); err == nil ||
		errors.Is(err, channel.ErrRejected) || errors.Is(err, channel.ErrUndeliverable) {
		t.Fatalf("503: %v, want transient", err)
	}
	if _, err := c.Send(t.Context(), channel.Message{To: mustPhone(t, "01077777777"), Text: "x"}); !errors.Is(err, channel.ErrRejected) {
		t.Fatalf("region not allowed: %v", err)
	}
	srv := httptest.NewServer(&api{state: map[string]string{}})
	t.Cleanup(srv.Close)
	bad, err := twilio.New(twilio.Options{BaseURL: srv.URL, AccountSID: accountSID, AuthToken: "wrong", From: "+15005550006", CallbackURL: callback})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bad.Send(t.Context(), channel.Message{To: mustPhone(t, "01012345678"), Text: "x"}); !errors.Is(err, channel.ErrAuth) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err = twilio.New(twilio.Options{AccountSID: accountSID}); err == nil {
		t.Fatal("missing options accepted")
	}
}

func TestFetch(t *testing.T) {
	t.Parallel()
	c, a := setup(t)
	sent, err := c.Send(t.Context(), channel.Message{To: mustPhone(t, "01012345678"), Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if st, _, fErr := c.Fetch(t.Context(), sent.ProviderID); fErr != nil || st != "" {
		t.Fatalf("queued = %q, %v", st, fErr)
	}
	a.mu.Lock()
	a.state[sent.ProviderID] = "undelivered"
	a.mu.Unlock()
	st, code, err := c.Fetch(t.Context(), sent.ProviderID)
	if err != nil || st != channel.StatusFailed || code != "30003" || !c.Undeliverable(code) || c.Undeliverable("30007") {
		t.Fatalf("undelivered = %q %q, %v", st, code, err)
	}
	if _, _, err = c.Fetch(t.Context(), "../../x"); !errors.Is(err, channel.ErrRejected) {
		t.Fatalf("bad SID: %v", err)
	}
	if _, _, err = c.Fetch(t.Context(), fmt.Sprintf("SM%032x", 999)); !errors.Is(err, channel.ErrRejected) {
		t.Fatalf("unknown SID: %v", err)
	}
}

func TestWebhook(t *testing.T) {
	t.Parallel()
	c, _ := setup(t)
	sid := fmt.Sprintf("SM%032x", 7)
	events, err := c.VerifyWebhook(statusWebhook(sid, "undelivered", "m1.ref", "30005"))
	if err != nil || len(events) != 1 || events[0].Status != channel.StatusFailed || events[0].ErrorCode != "30005" ||
		events[0].ID != sid+":undelivered" || events[0].Ref != "m1.ref" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if events, err = c.VerifyWebhook(statusWebhook(sid, "sending", "m1.ref", "")); err != nil || len(events) != 0 {
		t.Fatalf("sending = %+v, %v", events, err)
	}
	w := statusWebhook(sid, "delivered", "m1.ref", "")
	w.Query = url.Values{"m": {"m1.other"}}
	if _, err = c.VerifyWebhook(w); !errors.Is(err, channel.ErrForged) {
		t.Fatalf("query changed: %v", err)
	}
	other := statusWebhook("SMnope", "delivered", "m1.ref", "")
	if _, err = c.VerifyWebhook(other); !errors.Is(err, channel.ErrBadWebhook) {
		t.Fatalf("not a SID: %v", err)
	}
	if _, err = c.VerifyWebhook(channel.Webhook{Body: []byte("%zz")}); !errors.Is(err, channel.ErrForged) {
		t.Fatalf("bad form: %v", err)
	}
}

// FuzzVerifyWebhook: any correctly signed form is read without panicking.
func FuzzVerifyWebhook(f *testing.F) {
	f.Add(string(statusWebhook(fmt.Sprintf("SM%032x", 1), "delivered", "m1.ref", "").Body), "m1.ref")
	f.Add("MessageSid=x&MessageStatus=failed", "")
	c, err := twilio.New(twilio.Options{AccountSID: accountSID, AuthToken: authToken, From: "+15005550006", CallbackURL: callback})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, body, ref string) {
		form, err := url.ParseQuery(body)
		if err != nil {
			return
		}
		q := url.Values{"m": {ref}}
		w := channel.Webhook{Header: map[string][]string{twilio.SignatureHeader: {twilio.Sign(authToken, callback+"?"+q.Encode(), form)}}, Query: q, Body: []byte(body)}
		events, err := c.VerifyWebhook(w)
		if err != nil {
			if !errors.Is(err, channel.ErrBadWebhook) {
				t.Fatalf("signed form refused as %v", err)
			}
			return
		}
		for _, e := range events {
			if b, _ := json.Marshal(e); e.ID == "" || e.Status == "" || len(b) == 0 { //nolint:errcheck // plain struct
				t.Fatalf("event %+v", e)
			}
		}
	})
}
