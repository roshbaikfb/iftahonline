package cdn_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/cdn"
)

type fakeAPI struct {
	mu      sync.Mutex
	batches [][]string
	fails   int // answer 429 this many times first
	status  int // then answer this (0: 200)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != "/zones/zone-1/purge_cache" || r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if f.fails > 0 {
		f.fails--
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
		return
	}
	var body struct{ Tags []string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.batches = append(f.batches, body.Tags)
	_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"zone-1"}}`))
}

func newPurger(t *testing.T, api *fakeAPI) *cdn.Cloudflare {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	p, err := cdn.NewCloudflare(cdn.CloudflareOptions{ZoneID: "zone-1", Token: "tok", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCloudflareBatchesAndRetries(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{fails: 2}
	p := newPurger(t, api)
	tags := make([]string, 65)
	for i := range tags {
		tags[i] = fmt.Sprintf("p-%02d", i)
	}
	if err := p.PurgeTags(t.Context(), tags); err != nil {
		t.Fatal(err)
	}
	if len(api.batches) != 3 || len(api.batches[0]) != 30 || len(api.batches[2]) != 5 || api.batches[2][4] != "p-64" {
		t.Fatalf("batches %v", api.batches)
	}
	if err := p.PurgeTags(t.Context(), nil); err != nil || len(api.batches) != 3 {
		t.Fatalf("nothing to purge: %v", err)
	}
}

func TestCloudflareRejection(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{status: http.StatusForbidden}
	err := newPurger(t, api).PurgeTags(t.Context(), []string{"s-1"})
	if err == nil || !strings.Contains(err.Error(), "Authentication error") || strings.Contains(err.Error(), "gave up") {
		t.Fatalf("a rejected purge must fail at once, not retry: %v", err)
	}
	if strings.Contains(err.Error(), "tok") {
		t.Fatal("the token leaked into the error")
	}
}

func TestCloudflareGivesUp(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{fails: 100}
	if err := newPurger(t, api).PurgeTags(t.Context(), []string{"s-1"}); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v", err)
	}
}

func TestOptionsAndNop(t *testing.T) {
	t.Parallel()
	if _, err := cdn.NewCloudflare(cdn.CloudflareOptions{ZoneID: "z"}); err == nil {
		t.Fatal("missing token accepted")
	}
	if err := (cdn.Nop{}).PurgeTags(t.Context(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
}
