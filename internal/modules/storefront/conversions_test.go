package storefront_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// laterHandler is an event handler set once the module it calls is
// built (cmd/platform's lateHandler).
type laterHandler struct {
	mu sync.Mutex
	h  eventbus.Handler
}

func (l *laterHandler) set(h eventbus.Handler) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.h = h
}

func (l *laterHandler) handle(ctx context.Context, d eventbus.Delivery) error {
	l.mu.Lock()
	h := l.h
	l.mu.Unlock()
	if h == nil {
		return nil
	}
	return h(ctx, d)
}

// metaEvent is what the stand-in Meta received.
type metaEvent struct {
	Pixel, Token, Name, ID string
	User                   map[string]any
	Custom                 map[string]any
}

type sink struct {
	*httptest.Server
	mu  sync.Mutex
	got []metaEvent
}

// metaSink is one stand-in for Meta's Conversions API shared by every
// test in the package: the tests share River's queue, so any worker may
// send another test's event; each test finds its own by event_id.
var metaSink = sync.OnceValue(func() *sink {
	s := &sink{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Token string `json:"access_token"`
			Data  []struct {
				Name   string         `json:"event_name"`
				ID     string         `json:"event_id"`
				User   map[string]any `json:"user_data"`
				Custom map[string]any `json:"custom_data"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || len(body.Data) != 1 {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		d := body.Data[0]
		s.mu.Lock()
		s.got = append(s.got, metaEvent{Pixel: strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/events"), Token: body.Token,
			Name: d.Name, ID: d.ID, User: d.User, Custom: d.Custom})
		s.mu.Unlock()
		_, _ = w.Write([]byte(`{"events_received":1}`))
	}))
	return s
})

// event waits for the event the stand-in received with id.
func (s *sink) event(t *testing.T, id string) metaEvent {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		i := slices.IndexFunc(s.got, func(e metaEvent) bool { return e.ID == id })
		var e metaEvent
		if i >= 0 {
			e = s.got[i]
		}
		s.mu.Unlock()
		if i >= 0 {
			return e
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Meta never received event %q", id)
	return metaEvent{}
}

func (s *sink) count(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.got {
		if e.ID == id {
			n++
		}
	}
	return n
}

var pxRE = regexp.MustCompile(`<script type="application/json" id="px">(.*?)</script>`)

// pageEventID is the event_id a page's pixel reports for name.
func pageEventID(t *testing.T, body, name string) string {
	t.Helper()
	m := pxRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no pixel configuration in the page")
	}
	var cfg struct {
		Ev []struct{ N, ID string } `json:"ev"`
	}
	if err := json.Unmarshal([]byte(m[1]), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, e := range cfg.Ev {
		if e.N == name {
			return e.ID
		}
	}
	t.Fatalf("the page reports no %s", name)
	return ""
}

// TestBrowserAndServerShareEventIDs is P6's acceptance test (ADR-0043):
// for each conversion, the event_id the page's pixel reports is the one
// the server sends to the provider, so the provider counts it once.
// Real modules and River; only Meta is a stand-in.
func TestBrowserAndServerShareEventIDs(t *testing.T) {
	t.Parallel()
	p, members := newPlatform(t)
	tag := strings.ToLower(ulid.NewGenerator().New().String()[18:])
	s := p.createStore(t, members, "px-"+tag, "متجر البكسل", "ar", "EGP")
	prod := p.call(t, s, http.MethodPost, "/products", `{"title":"عطر","slug":"perfume","status":"active","variants":[{"price_minor":25000,"on_hand":10}]}`, http.StatusCreated)
	variant := prod["variants"].([]any)[0].(map[string]any)["id"].(string)
	p.call(t, s, http.MethodPost, "/shipping/zones", `{"name":"القاهرة","areas":["EG-C"],"rate_minor":5000}`, http.StatusCreated)
	pixel := "1" + strings.Repeat("0", 9) + fmt.Sprint(time.Now().UnixNano()%100000) // unique per run
	p.call(t, s, http.MethodPut, "/marketing/pixels/meta", `{"pixel_id":"`+pixel+`","enabled":true,"server_token":"EAAB-acceptance","version":0}`, http.StatusOK)

	shopper := func(method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		rq := httptest.NewRequestWithContext(t.Context(), method, path, body)
		rq.Host, rq.RemoteAddr = s.host, "203.0.113.9:40000"
		rq.Header.Set("User-Agent", "Mozilla/5.0 (acceptance)")
		if form != nil {
			rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rq.Header.Set("Origin", "https://"+s.host)
		}
		for _, c := range append(cookies, &http.Cookie{Name: "_fbp", Value: "fb.1.1730000000000.77"}) {
			rq.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		p.shop.ServeHTTP(rec, rq)
		return rec
	}
	cookie := func(rec *httptest.ResponseRecorder, name string) *http.Cookie {
		for _, c := range rec.Result().Cookies() {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("no %s cookie", name)
		return nil
	}

	// AddToCart: the cart page after the add.
	add := shopper(http.MethodPost, "/cart", url.Values{"variant": {variant}, "qty": {"2"}})
	cart := cookie(add, "__Host-cart")
	cartPage := shopper(http.MethodGet, "/cart", nil, cart, cookie(add, "__Host-px_add")).Body.String()
	added := pageEventID(t, cartPage, marketing.AddToCart)
	if e := p.metaEvent(t, added); e.Name != "AddToCart" || e.Pixel != pixel || e.Token != "EAAB-acceptance" || e.User["fbp"] != "fb.1.1730000000000.77" ||
		e.User["client_ip_address"] != "203.0.113.9" {
		t.Fatalf("server AddToCart %+v", e)
	}

	// InitiateCheckout: the checkout page.
	started := pageEventID(t, shopper(http.MethodGet, "/checkout", nil, cart).Body.String(), marketing.InitiateCheckout)
	if e := p.metaEvent(t, started); e.Name != "InitiateCheckout" || e.Custom["value"] != 500.0 {
		t.Fatalf("server InitiateCheckout %+v", e)
	}

	// Purchase: the thank-you page.
	order := shopper(http.MethodPost, "/checkout", url.Values{"key": {"px-" + tag}, "name": {"هدى"}, "phone": {"01012345678"},
		"governorate": {"EG-C"}, "city": {"المعادي"}, "address": {"5 شارع 9"}}, cart)
	loc := order.Header().Get("Location")
	if order.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/thank-you/") {
		t.Fatalf("order: %d %s", order.Code, order.Body.String())
	}
	bought := pageEventID(t, shopper(http.MethodGet, loc, nil).Body.String(), marketing.Purchase)
	e := p.metaEvent(t, bought)
	if bought != "purchase:"+strings.TrimPrefix(loc, "/thank-you/") || e.Name != "Purchase" || e.Custom["order_id"] != strings.TrimPrefix(loc, "/thank-you/") ||
		e.Custom["value"] != 550.0 || e.User["ph"] == nil || e.User["fbp"] != "fb.1.1730000000000.77" {
		t.Fatalf("server Purchase %+v (page %q)", e, bought)
	}
	// One of each: the provider counts each conversion once.
	for _, id := range []string{added, started, bought} {
		if n := metaSink().count(id); n != 1 {
			t.Errorf("Meta got %s %d times", id, n)
		}
	}
}

func (p platform) metaEvent(t *testing.T, id string) metaEvent {
	t.Helper()
	return metaSink().event(t, id)
}
