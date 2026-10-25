package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestTrackPage: the form, then the order found by its number and phone:
// where it stands and its parcel; the phone is never shown back, and a
// wrong pair gets one answer whichever part was wrong.
func TestTrackPage(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	form := f.get(t, "/track")
	if form.Code != http.StatusOK || form.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("form: %d %v", form.Code, form.Header())
	}
	wantContains(t, form.Body.String(), "<h1>تتبّع طلبك</h1>", `<label for="t-order">رقم الطلب</label>`, `name="phone"`, `action="/track"`)

	rec := f.post(t, "/track", url.Values{"order": {"#" + trackCode}, "phone": {trackPhone}}, nil, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("found: %d %v", rec.Code, rec.Header())
	}
	wantContains(t, body,
		`<h2>طلب رقم <bdi dir="ltr">#P0R1S2T3</bdi></h2>`, "قطعتان · 360 ج.م",
		`<li class="done"><span class="n">3</span><div><strong>تم الشحن</strong>`,
		`<li class="done"><span class="n">4</span><div><strong>خرج للتوصيل</strong>`,
		`<li class="now"><span class="n">5</span><div><strong>تم التوصيل</strong>`,
		`<p class="parcel">مع بوسطة · رقم التتبع: <bdi dir="ltr">7788123</bdi></p>`,
	)
	if strings.Contains(body, trackPhone) || strings.Contains(body, "1234 5678") {
		t.Fatal("the phone is shown back")
	}
	for _, bad := range []url.Values{
		{"order": {trackCode}, "phone": {"01099998888"}},
		{"order": {"ABCDEFGH"}, "phone": {trackPhone}},
		{"order": {""}, "phone": {""}},
	} {
		rec = f.post(t, "/track", bad, nil, nil)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "لم نجد طلبًا بهذه البيانات") {
			t.Errorf("%v: %d", bad, rec.Code)
		}
	}
	if rec = f.post(t, "/track", url.Values{"order": {trackCode}, "phone": {trackPhone}}, nil, map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", rec.Code)
	}
}

func TestTrackLimits(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	f.limiter.allow["track:ip:203.0.113.7"] = 2
	try := url.Values{"order": {"ABCDEFGH"}, "phone": {trackPhone}}
	for range 2 {
		if rec := f.post(t, "/track", try, nil, nil); rec.Code != http.StatusNotFound {
			t.Fatal(rec.Code)
		}
	}
	rec := f.post(t, "/track", url.Values{"order": {trackCode}, "phone": {trackPhone}}, nil, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || strings.Contains(rec.Body.String(), "7788123") {
		t.Fatalf("over the limit: %d", rec.Code)
	}
	// The limits stop guessing: when they cannot be checked, nobody guesses.
	g := newCheckoutFixture(t)
	g.limiter.fail = true
	if rec = g.post(t, "/track", url.Values{"order": {trackCode}, "phone": {trackPhone}}, nil, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limiter down: %d", rec.Code)
	}
}

func TestTrackLinks(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	home := f.get(t, "/").Body.String()
	wantContains(t, home, `<a class="track-link" href="/track">تتبّع طلبك</a>`, `<li><a href="/track">تتبّع طلبك</a></li>`)
	wantContains(t, f.get(t, "/thank-you/IFT-1001").Body.String(), `<a class="btn-ghost" href="/track">تتبّع طلبك</a>`, "احتفظ برقم الطلب")
	// A store that takes no orders links no tracking.
	g := newFixture(t)
	if strings.Contains(g.get(t, "/").Body.String(), `href="/track"`) || g.get(t, "/track").Code != http.StatusNotFound {
		t.Fatal("tracking without orders")
	}
}
