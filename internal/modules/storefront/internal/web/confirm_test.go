package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

const confirmToken = "aaaabbbbccccddddeeeeff"

// fakeConfirms has one link, waiting for an answer.
type fakeConfirms struct {
	mu      sync.Mutex
	state   string
	answers []bool
	err     error
}

func (f *fakeConfirms) view() web.ConfirmView {
	total, _ := money.New(36000, money.EGP) //nolint:errcheck // a valid amount
	return web.ConfirmView{Code: "P0R1S2T3", Total: total, FirstName: "منى", State: f.state}
}

func (f *fakeConfirms) Confirmation(_ context.Context, token string) (web.ConfirmView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return web.ConfirmView{}, f.err
	}
	if token != confirmToken {
		return web.ConfirmView{}, web.ErrConfirmationNotFound
	}
	return f.view(), nil
}

func (f *fakeConfirms) AnswerConfirmation(_ context.Context, token string, confirm bool) (web.ConfirmView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if token != confirmToken {
		return web.ConfirmView{}, web.ErrConfirmationNotFound
	}
	f.answers = append(f.answers, confirm)
	if f.state == web.ConfirmWaiting {
		f.state = web.ConfirmCancelled
		if confirm {
			f.state = web.ConfirmDone
		}
	}
	return f.view(), nil
}

func (f *fakeConfirms) WhatsAppLink(_ context.Context, orderRef, locale string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil || f.state != web.ConfirmWaiting {
		return "", f.err
	}
	return "https://wa.me/201000000000?text=" + url.QueryEscape("order "+orderRef+" "+locale), nil
}

func newConfirmFixture(t *testing.T) (checkoutFixture, *fakeConfirms) {
	t.Helper()
	c := &fakeConfirms{state: web.ConfirmWaiting}
	return newCheckoutFixture(t, func(d *web.Deps) { d.Confirmations = c }), c
}

// TestConfirmPage: the SMS link asks, private and unindexed; the answer
// is a same-origin post; once settled, the page says so.
func TestConfirmPage(t *testing.T) {
	t.Parallel()
	f, c := newConfirmFixture(t)
	rec := f.get(t, "/o/"+confirmToken)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("ask: %d %v", rec.Code, rec.Header())
	}
	wantContains(t, body, "<h1>تأكيد الطلب</h1>", `<bdi dir="ltr">#P0R1S2T3</bdi>`, "360 ج.م", "أهلاً منى، هل تؤكد طلبك؟",
		`action="/o/`+confirmToken+`"`, `name="answer" value="confirm"`, `name="answer" value="cancel"`, `noindex`)
	if strings.Contains(body, "<script>") {
		t.Fatal("inline script on the page")
	}

	rec = f.post(t, "/o/"+confirmToken, url.Values{"answer": {"confirm"}}, nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "تم تأكيد طلبك") || strings.Contains(rec.Body.String(), `value="confirm"`) {
		t.Fatalf("confirmed: %d %s", rec.Code, rec.Body)
	}
	// Answering again shows how it settled.
	if rec = f.post(t, "/o/"+confirmToken, url.Values{"answer": {"cancel"}}, nil, nil); !strings.Contains(rec.Body.String(), "تم تأكيد طلبك") {
		t.Fatalf("second answer: %s", rec.Body)
	}
	if len(c.answers) != 2 || !c.answers[0] || c.answers[1] {
		t.Fatalf("answers = %v", c.answers)
	}
	if en := f.get(t, "/en/o/"+confirmToken).Body.String(); !strings.Contains(en, "Your order is confirmed") {
		t.Fatal("English page")
	}
}

func TestConfirmPageRefusals(t *testing.T) {
	t.Parallel()
	f, c := newConfirmFixture(t)
	tests := []struct {
		name string
		rec  func() int
		want int
	}{
		{"unknown token", func() int { return f.get(t, "/o/nope").Code }, http.StatusNotFound},
		{"long token", func() int { return f.get(t, "/o/"+strings.Repeat("a", 65)).Code }, http.StatusNotFound},
		{"cross-site post", func() int {
			return f.post(t, "/o/"+confirmToken, url.Values{"answer": {"cancel"}}, nil, map[string]string{"Origin": "https://evil.example"}).Code
		}, http.StatusForbidden},
		{"no answer", func() int { return f.post(t, "/o/"+confirmToken, url.Values{}, nil, nil).Code }, http.StatusBadRequest},
		{"odd answer", func() int { return f.post(t, "/o/"+confirmToken, url.Values{"answer": {"maybe"}}, nil, nil).Code }, http.StatusBadRequest},
	}
	for _, tt := range tests {
		if got := tt.rec(); got != tt.want {
			t.Errorf("%s: %d, want %d", tt.name, got, tt.want)
		}
	}
	if len(c.answers) != 0 {
		t.Fatalf("refused requests answered: %v", c.answers)
	}
	c.state = web.ConfirmClosed
	if body := f.get(t, "/o/"+confirmToken).Body.String(); !strings.Contains(body, "لم يعد ينتظر ردك") || strings.Contains(body, `value="confirm"`) {
		t.Fatalf("closed: %s", body)
	}
	c.err = errors.New("db down")
	if rec := f.get(t, "/o/"+confirmToken); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("outage: %d", rec.Code)
	}
	// Without the confirmations port, the page does not exist.
	if rec := newCheckoutFixture(t).get(t, "/o/"+confirmToken); rec.Code != http.StatusNotFound {
		t.Fatalf("no port: %d", rec.Code)
	}
}

func TestConfirmLimits(t *testing.T) {
	t.Parallel()
	f, _ := newConfirmFixture(t)
	f.limiter.allow["confirm:ip:203.0.113.7"] = 2
	for range 2 {
		if rec := f.get(t, "/o/nope"); rec.Code != http.StatusNotFound {
			t.Fatal(rec.Code)
		}
	}
	if rec := f.get(t, "/o/"+confirmToken); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d", rec.Code)
	}
	g, _ := newConfirmFixture(t)
	g.limiter.fail = true
	if rec := g.post(t, "/o/"+confirmToken, url.Values{"answer": {"confirm"}}, nil, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limiter down: %d", rec.Code)
	}
}

// TestThankYouConfirmOnWhatsApp: a cash-on-delivery order waiting for
// its answer offers the "confirm on WhatsApp" link; once answered, or
// without the port, it does not.
func TestThankYouConfirmOnWhatsApp(t *testing.T) {
	t.Parallel()
	f, c := newConfirmFixture(t)
	body := f.get(t, "/thank-you/IFT-1001").Body.String()
	wantContains(t, body, `class="card-box confirm-wa"`, "أكّد طلبك الآن على واتساب", `class="btn btn-block btn-wa" href="https://wa.me/201000000000?text=order+IFT-1001+ar"`,
		`rel="nofollow noopener"`)
	if en := f.get(t, "/en/thank-you/IFT-1001").Body.String(); !strings.Contains(en, "text=order+IFT-1001+en") || !strings.Contains(en, "Confirm on WhatsApp") {
		t.Fatal("English link")
	}
	c.state = web.ConfirmDone
	if strings.Contains(f.get(t, "/thank-you/IFT-1001").Body.String(), "confirm-wa") {
		t.Fatal("link offered after the answer")
	}
	c.state, c.err = web.ConfirmWaiting, errors.New("db down")
	if rec := f.get(t, "/thank-you/IFT-1001"); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "confirm-wa") {
		t.Fatalf("outage: %d", rec.Code)
	}
	if strings.Contains(newCheckoutFixture(t).get(t, "/thank-you/IFT-1001").Body.String(), "confirm-wa") {
		t.Fatal("link without the port")
	}
}
