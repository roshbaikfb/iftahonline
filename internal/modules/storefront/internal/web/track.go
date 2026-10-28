package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Tracking is looked up by a code and a phone: these bound how many
// guesses a visitor, and anyone against one store, can make.
var (
	trackPerIP    = ratelimit.Limit{Burst: 10, Period: 10 * time.Minute}  //nolint:gochecknoglobals // constant policy
	trackPerStore = ratelimit.Limit{Burst: 300, Period: 10 * time.Minute} //nolint:gochecknoglobals // constant policy
)

// trackView is the tracking page: the form, and the order once found.
type trackView struct {
	code    string // as typed, shown back in the form (never the phone)
	problem Msg
	t       *Tracking
	steps   []trackStep
	alert   Msg // cancelled, not delivered, returned
}

// trackStep is one step of where an order stands.
type trackStep struct {
	label Msg
	state string // "done", "now" or ""
}

// trackRoute serves /track: the form (GET) and the lookup (POST). Both
// are private and never cached: the answer is one shopper's.
func (s *Shop) trackRoute(w http.ResponseWriter, r *http.Request, store tenant.StoreID, p *pageCtx) {
	if s.orders == nil {
		s.writeNotFoundPrivate(w, r, p)
		return
	}
	p.title, p.noindex, p.soft, p.cssURL = p.l.T(MsgTrack)+" | "+p.v.name, true, true, s.assets.checkoutURL
	if isRead(r) {
		s.writePrivate(w, r, p, http.StatusOK, trackPage(p, trackView{}))
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	v := trackView{code: r.PostForm.Get("order")}
	if ok, retry := s.allowTrack(r, store); !ok {
		if retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		}
		v.problem = MsgErrBusy
		s.writePrivate(w, r, p, http.StatusTooManyRequests, trackPage(p, v))
		return
	}
	t, err := s.orders.Track(r.Context(), v.code, r.PostForm.Get("phone"))
	switch {
	case err == nil:
		v.t = &t
		v.steps, v.alert = trackSteps(t)
	case errors.Is(err, ErrOrderNotFound):
		v.problem = MsgTrackNotFound
	default:
		s.unavailable(w, r, err)
		return
	}
	status := http.StatusOK
	if v.t == nil {
		status = http.StatusNotFound
	}
	s.writePrivate(w, r, p, status, trackPage(p, v))
}

// allowTrack applies the tracking limits; when limited it says how long
// to wait. A limiter outage refuses too (with no wait to give): the
// limits are what stop someone guessing codes for a known phone.
func (s *Shop) allowTrack(r *http.Request, store tenant.StoreID) (bool, time.Duration) {
	return s.allowGuarded(r, store, "track", trackPerIP, trackPerStore)
}

// allowGuarded applies a per-IP and a per-store limit to a page that
// guards a secret (an order code, a link's token): when limited it says
// how long to wait, and a limiter outage refuses (with no wait to give).
func (s *Shop) allowGuarded(r *http.Request, store tenant.StoreID, name string, perIP, perStore ratelimit.Limit) (bool, time.Duration) {
	if s.limiter == nil {
		return true, 0
	}
	ctx := r.Context()
	for _, c := range []struct {
		key   string
		limit ratelimit.Limit
	}{{name + ":ip:" + s.clientIP(r), perIP}, {name + ":store:" + store.String(), perStore}} {
		res, err := s.limiter.Allow(ctx, c.key, c.limit)
		if err != nil {
			s.log.WarnContext(ctx, "storefront: "+name+" limiter unavailable", "err", err)
			return false, 0
		}
		if !res.Allowed {
			return false, max(res.RetryAfter, time.Second) // > 0: limited, not an outage
		}
	}
	return true, 0
}

// trackSteps says where an order stands in five steps (placed,
// confirmed, shipped, out for delivery, delivered), and what went wrong
// when it did.
func trackSteps(t Tracking) ([]trackStep, Msg) {
	labels := []Msg{MsgStepPlaced, MsgStepConfirmed, MsgStepShipped, MsgStepOut, MsgStepDelivered}
	reached := 0 // the last step done
	var alert Msg
	switch t.Status {
	case "confirmed", "ready_to_ship":
		reached = 1
	case "shipped":
		reached = 2
	case "delivered":
		reached = 4
	case "cancelled":
		alert = MsgTrackCancelled
	case "failed_delivery":
		reached, alert = 2, MsgTrackFailed
	case "returned":
		reached, alert = 2, MsgTrackReturned
	}
	if t.Parcel != nil && reached == 2 && (t.Parcel.Status == "out_for_delivery" || t.Parcel.Status == "failed_attempt") {
		reached = 3
	}
	steps := make([]trackStep, len(labels))
	for i, m := range labels {
		steps[i].label = m
		switch {
		case i <= reached:
			steps[i].state = "done"
		case i == reached+1 && alert == 0:
			steps[i].state = "now"
		}
	}
	return steps, alert
}

// carrierName is a carrier as shoppers know it.
func (l Locale) carrierName(code string) string {
	switch code {
	case "bosta":
		return map[Locale]string{Arabic: "بوسطة", English: "Bosta"}[l]
	case "fake":
		return l.T(MsgCourier)
	}
	return code
}
