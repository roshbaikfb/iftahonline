package bosta_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/bosta"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/bosta/bostatest"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/carriertest"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
)

var fast = retry.Policy{MaxAttempts: 4, Initial: time.Millisecond, Max: 2 * time.Millisecond}

var codes = map[carrier.Status]int{carrier.StatusInTransit: 24, carrier.StatusOutForDelivery: 41, carrier.StatusDelivered: 45} //nolint:gochecknoglobals // test table

func standIn(t *testing.T) (*bosta.Carrier, *bostatest.Server) {
	t.Helper()
	srv := bostatest.New(t)
	return bosta.New(bosta.Options{BaseURL: srv.URL + "/api/v2", Retry: &fast}), srv
}

// TestContract runs the shared suite against the stand-in: the adapter
// speaks the shapes it was written for.
func TestContract(t *testing.T) {
	t.Parallel()
	c, srv := standIn(t)
	var mu sync.Mutex
	last := map[string]int{} // the state each delivery was last moved to
	carriertest.Run(t, carriertest.Harness{
		Carrier: c, Creds: carrier.Credentials{APIKey: bostatest.Key}, BadCreds: carrier.Credentials{APIKey: "wrong"},
		Advance: func(t *testing.T, tracking string, to carrier.Status) {
			t.Helper()
			if !srv.SetState(tracking, codes[to]) {
				t.Fatalf("no delivery %s", tracking)
			}
			mu.Lock()
			defer mu.Unlock()
			last[tracking] = codes[to]
		},
		Webhook: func(t *testing.T, tracking string) []byte {
			t.Helper()
			mu.Lock()
			defer mu.Unlock()
			return webhookBody(tracking, last[tracking], time.UnixMilli(1792750000000))
		},
	})
}

// TestSandboxContract runs the suite against Bosta's staging API when a
// key is given (BOSTA_SANDBOX_API_KEY): the check that the shapes are
// Bosta's own. Parcels cannot be moved on a sandbox, so those cases are
// left out. Without a key there is nothing to check against.
func TestSandboxContract(t *testing.T) {
	key := os.Getenv("BOSTA_SANDBOX_API_KEY")
	if key == "" {
		t.Skip("BOSTA_SANDBOX_API_KEY not set: Bosta's sandbox contract is not run")
	}
	carriertest.Run(t, carriertest.Harness{
		Carrier: bosta.New(bosta.Options{BaseURL: bosta.StagingURL}),
		Creds:   carrier.Credentials{APIKey: key}, BadCreds: carrier.Credentials{APIKey: "invalid-" + key},
	})
}

func webhookBody(tracking string, state int, at time.Time) []byte {
	return []byte(`{"_id":"bid1","trackingNumber":"` + tracking + `","state":` + strconv.Itoa(state) +
		`,"type":"SEND","timeStamp":` + strconv.FormatInt(at.UnixMilli(), 10) + `,"cod":520,"businessReference":"ref"}`)
}

// TestCreateBody: what Bosta receives for an Egyptian COD order.
func TestCreateBody(t *testing.T) {
	t.Parallel()
	c, srv := standIn(t)
	req := carriertest.Request(t)
	if _, err := c.CreateShipment(t.Context(), carrier.Credentials{APIKey: bostatest.Key}, req); err != nil {
		t.Fatal(err)
	}
	b := srv.Creates()[0]
	recv := b["receiver"].(map[string]any)
	addr := b["dropOffAddress"].(map[string]any)
	spec := b["specs"].(map[string]any)["packageDetails"].(map[string]any)
	if b["cod"] != 520.0 || b["type"] != 10.0 || b["businessReference"] != req.Reference || b["allowToOpenPackage"] != false ||
		recv["firstName"] != "منى" || recv["lastName"] != "أحمد علي" || recv["phone"] != "01012345678" ||
		addr["city"] != "Cairo" || addr["firstLine"] != req.Address.Line || addr["secondLine"] != "مدينة نصر" ||
		spec["itemsCount"] != 2.0 || b["notes"] != "اتصل قبل الوصول" {
		t.Fatalf("create body: %v", b)
	}
	for _, gov := range []string{"SA-01", "", "EG-XX"} {
		bad := carriertest.Request(t)
		bad.Address.Governorate = gov
		if _, err := c.CreateShipment(t.Context(), carrier.Credentials{APIKey: bostatest.Key}, bad); !errors.Is(err, carrier.ErrRejected) {
			t.Errorf("governorate %q: %v", gov, err)
		}
	}
}

// TestLostCreateIsNotRepeated: when Bosta creates the delivery but the
// answer is lost, the retry finds it by reference instead of creating a
// second one.
func TestLostCreateIsNotRepeated(t *testing.T) {
	t.Parallel()
	c, srv := standIn(t)
	srv.LoseCreates(1)
	s, err := c.CreateShipment(t.Context(), carrier.Credentials{APIKey: bostatest.Key}, carriertest.Request(t))
	if err != nil || s.TrackingNumber == "" || srv.Deliveries() != 1 {
		t.Fatalf("lost create: %+v %v, %d deliveries", s, err, srv.Deliveries())
	}
}

func TestRetriesAndFailures(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	creds := carrier.Credentials{APIKey: bostatest.Key}
	c, srv := standIn(t)
	s, err := c.CreateShipment(ctx, creds, carriertest.Request(t))
	if err != nil {
		t.Fatal(err)
	}
	srv.FailNext(2)
	if tr, trErr := c.Track(ctx, creds, s.TrackingNumber); trErr != nil || tr.Status != carrier.StatusCreated || tr.RawState != "10 state 10" {
		t.Fatalf("track after two 503s: %+v %v", tr, trErr)
	}
	srv.FailNext(10)
	if _, err = c.Track(ctx, creds, s.TrackingNumber); err == nil || errors.Is(err, carrier.ErrNotFound) {
		t.Fatalf("Bosta down: %v, want a retryable error", err)
	}
	srv.FailNext(0)
	before := srv.Requests()
	if err = c.Verify(ctx, carrier.Credentials{APIKey: "wrong"}); !errors.Is(err, carrier.ErrAuth) || srv.Requests() != before+1 {
		t.Fatalf("bad key: %v after %d requests (auth must not be retried)", err, srv.Requests()-before)
	}
	if err = c.Verify(ctx, carrier.Credentials{}); !errors.Is(err, carrier.ErrAuth) {
		t.Fatalf("empty key: %v", err)
	}
	srv.SetState(s.TrackingNumber, 24)
	if err = c.Cancel(ctx, creds, s.TrackingNumber); !errors.Is(err, carrier.ErrNotCancellable) {
		t.Fatalf("cancel in transit: %v", err)
	}
	if _, err = c.Label(ctx, creds, "0000000"); !errors.Is(err, carrier.ErrNotFound) {
		t.Fatalf("label of unknown: %v", err)
	}
	if bosta.New(bosta.Options{}).Name() != bosta.Name {
		t.Fatal("name")
	}
}

func TestMapState(t *testing.T) {
	t.Parallel()
	for code, want := range map[int]carrier.Status{
		10: carrier.StatusCreated, 21: carrier.StatusCreated, 22: carrier.StatusInTransit, 24: carrier.StatusInTransit,
		41: carrier.StatusOutForDelivery, 43: carrier.StatusOutForDelivery, 45: carrier.StatusDelivered,
		46: carrier.StatusFailedAttempt, 47: carrier.StatusFailedAttempt, 48: carrier.StatusCancelled,
		49: carrier.StatusReturned, 0: carrier.StatusUnknown, 99: carrier.StatusUnknown,
	} {
		if got := bosta.MapState(code); got != want {
			t.Errorf("MapState(%d) = %s, want %s", code, got, want)
		}
	}
	if !carrier.StatusDelivered.Final() || carrier.StatusFailedAttempt.Final() {
		t.Fatal("Final")
	}
}

func TestCity(t *testing.T) {
	t.Parallel()
	for gov, want := range map[string]string{"EG-C": "Cairo", "EG-GZ": "Giza", "EG-AST": "Assuit", "EG-KFS": "Kafr Alsheikh"} {
		if got, ok := bosta.City(gov); !ok || got != want {
			t.Errorf("City(%s) = %q %v, want %q", gov, got, ok, want)
		}
	}
	if _, ok := bosta.City("SA-01"); ok {
		t.Fatal("a Saudi region is not a Bosta city")
	}
}

func TestParseWebhook(t *testing.T) {
	t.Parallel()
	c := bosta.New(bosta.Options{})
	at := time.UnixMilli(1792750000000)
	ev, err := c.ParseWebhook(webhookBody("7000001", 45, at))
	if err != nil || ev.TrackingNumber != "7000001" || ev.EventID != "7000001:45:1792750000000" {
		t.Fatalf("event %+v %v", ev, err)
	}
	if next, _ := c.ParseWebhook(webhookBody("7000001", 49, at.Add(time.Hour))); next.EventID == ev.EventID {
		t.Fatal("two changes share an event id")
	}
	for _, body := range []string{`{"trackingNumber":"1"}`, `{"state":45}`, `{"trackingNumber":"a b","state":45}`,
		`{"trackingNumber":"1","state":"x"}`, `{"trackingNumber":"1","state":45,"timeStamp":"soon"}`, `[`} {
		if _, err = c.ParseWebhook([]byte(body)); !errors.Is(err, carrier.ErrBadWebhook) {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func FuzzParseWebhook(f *testing.F) {
	c := bosta.New(bosta.Options{})
	f.Add(webhookBody("7000001", 45, time.UnixMilli(1)))
	f.Add([]byte(`{"trackingNumber":"1","state":45}`))
	f.Add([]byte(`{"trackingNumber":"","state":1e400}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		ev, err := c.ParseWebhook(body)
		if err == nil && (ev.TrackingNumber == "" || ev.EventID == "" || len(ev.EventID) > 200) {
			t.Fatalf("accepted %q as %+v", body, ev)
		}
	})
}

// TestRefusalsCarryNoPersonalData: Bosta's messages may echo the
// receiver's phone or email; what the adapter keeps hides both.
func TestRefusalsCarryNoPersonalData(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"message":"Receiver phone 01012345678 or email mona@example.com is invalid"}`))
	}))
	t.Cleanup(srv.Close)
	c := bosta.New(bosta.Options{BaseURL: srv.URL})
	err := c.Verify(t.Context(), carrier.Credentials{APIKey: "key"})
	if !errors.Is(err, carrier.ErrRejected) {
		t.Fatalf("want a refusal, got %v", err)
	}
	for _, leak := range []string{"01012345678", "mona@example.com"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the refusal keeps %q: %v", leak, err)
		}
	}
}
