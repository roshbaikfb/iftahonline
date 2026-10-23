// Package bostatest is an in-process stand-in for Bosta's API, built
// from the same shapes the adapter follows (package bosta): the
// adapter's tests and the contract suite run against it without a
// network. It can fail requests on demand, to prove retries and the
// lost-create lookup.
package bostatest

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Key is the API key the server accepts.
const Key = "bosta-test-key"

// Server is the stand-in.
type Server struct {
	URL string

	mu          sync.Mutex
	seq         int
	deliveries  map[string]*delivery // by tracking number
	failNext    int                  // answer this many requests with 503
	loseCreates int                  // create, then answer 503, this many times
	creates     []map[string]any
	requests    int
}

type delivery struct {
	ID, Tracking, Reference string
	State                   int
	Updated                 time.Time
}

// New starts a server closed at the end of the test.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{deliveries: map[string]*delivery{}}
	ts := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

// FailNext answers the next n requests with 503.
func (s *Server) FailNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = n
}

// LoseCreates makes the next n creates succeed at Bosta but answer 503,
// as when the response is lost on the way back.
func (s *Server) LoseCreates(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loseCreates = n
}

// SetState moves a delivery to a Bosta state code.
func (s *Server) SetState(tracking string, code int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.deliveries[tracking]
	if ok {
		d.State, d.Updated = code, time.Now().UTC()
	}
	return ok
}

// Creates returns the bodies of every accepted create.
func (s *Server) Creates() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.creates...)
}

// Deliveries is how many deliveries exist.
func (s *Server) Deliveries() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deliveries)
}

// Requests is how many requests arrived.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	if s.failNext > 0 {
		s.failNext--
		reply(w, http.StatusServiceUnavailable, false, "busy", nil)
		return
	}
	if r.Header.Get("Authorization") != Key {
		reply(w, http.StatusUnauthorized, false, "Unauthorized", nil)
		return
	}
	var in map[string]any
	if r.Body != nil {
		if b, err := io.ReadAll(r.Body); err == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &in) //nolint:errcheck // a bad body fails the checks below
		}
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2")
	switch {
	case r.Method == http.MethodPost && path == "/deliveries" && r.URL.Query().Get("apiVersion") == "1":
		s.create(w, in)
	case r.Method == http.MethodPost && path == "/deliveries/search":
		s.search(w, in)
	case r.Method == http.MethodPost && path == "/deliveries/mass-awb":
		d, ok := s.deliveries[str(in["trackingNumbers"])]
		if !ok {
			reply(w, http.StatusNotFound, false, "not found", nil)
			return
		}
		reply(w, http.StatusOK, true, "", map[string]string{"awb": base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n% AWB " + d.Tracking + "\n%%EOF"))})
	case strings.HasPrefix(path, "/deliveries/business/"):
		rest := strings.TrimPrefix(path, "/deliveries/business/")
		tracking, action, _ := strings.Cut(rest, "/")
		d, ok := s.deliveries[tracking]
		switch {
		case !ok:
			reply(w, http.StatusNotFound, false, "Delivery not found", nil)
		case r.Method == http.MethodGet && action == "":
			reply(w, http.StatusOK, true, "", view(d))
		case r.Method == http.MethodDelete && action == "terminate":
			if d.State > 21 && d.State != 48 {
				reply(w, http.StatusBadRequest, false, "Delivery cannot be terminated", nil)
				return
			}
			d.State, d.Updated = 48, time.Now().UTC()
			reply(w, http.StatusOK, true, "terminated", nil)
		default:
			reply(w, http.StatusNotFound, false, "no route", nil)
		}
	default:
		reply(w, http.StatusNotFound, false, "no route", nil)
	}
}

func (s *Server) create(w http.ResponseWriter, in map[string]any) {
	recv, okRecv := in["receiver"].(map[string]any)
	addr, okAddr := in["dropOffAddress"].(map[string]any)
	if !okRecv || !okAddr || in["type"] != float64(10) || str(in["businessReference"]) == "" || str(recv["phone"]) == "" || str(addr["city"]) == "" {
		reply(w, http.StatusBadRequest, false, "Validation failed", nil)
		return
	}
	s.seq++
	d := &delivery{ID: "bid" + strconv.Itoa(s.seq), Tracking: strconv.Itoa(7000000 + s.seq), Reference: str(in["businessReference"]),
		State: 10, Updated: time.Now().UTC()}
	s.deliveries[d.Tracking] = d
	s.creates = append(s.creates, in)
	if s.loseCreates > 0 {
		s.loseCreates--
		reply(w, http.StatusBadGateway, false, "", nil)
		return
	}
	reply(w, http.StatusOK, true, "", map[string]string{"_id": d.ID, "trackingNumber": d.Tracking, "businessReference": d.Reference})
}

func (s *Server) search(w http.ResponseWriter, in map[string]any) {
	ref := str(in["businessReference"])
	out := []map[string]any{}
	for _, d := range s.deliveries {
		if ref == "" || d.Reference == ref {
			out = append(out, view(d))
		}
	}
	reply(w, http.StatusOK, true, "", map[string]any{"deliveries": out})
}

func view(d *delivery) map[string]any {
	return map[string]any{"_id": d.ID, "trackingNumber": d.Tracking, "businessReference": d.Reference,
		"state": map[string]any{"code": d.State, "value": "state " + strconv.Itoa(d.State)}, "updatedAt": d.Updated.Format(time.RFC3339)}
}

func reply(w http.ResponseWriter, code int, ok bool, msg string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": ok, "message": msg, "data": data}) //nolint:errcheck // test server
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
