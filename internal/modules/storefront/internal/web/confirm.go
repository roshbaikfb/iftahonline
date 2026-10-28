package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Confirmations backs the SMS link's page (ADR-0040): the customer
// confirms or cancels a cash-on-delivery order. Every method acts on the
// store in ctx; a token that names nothing there is
// ErrConfirmationNotFound.
type Confirmations interface {
	Confirmation(ctx context.Context, token string) (ConfirmView, error)
	AnswerConfirmation(ctx context.Context, token string, confirm bool) (ConfirmView, error)
}

// ErrConfirmationNotFound means there is no confirmation behind a link.
var ErrConfirmationNotFound = errors.New("storefront: no such confirmation")

// Confirmation states as the page shows them.
const (
	ConfirmWaiting   = "waiting"   // the customer may answer
	ConfirmDone      = "confirmed" // the customer confirmed
	ConfirmCancelled = "cancelled" // the customer cancelled
	ConfirmClosed    = "closed"    // settled otherwise (deadline, merchant)
)

// ConfirmView is the order the link names, and where it stands.
type ConfirmView struct {
	Code      string
	Total     money.Money
	FirstName string
	State     string
}

// A link's token is a secret of 128 bits; these bound guessing anyway,
// and answering twice by accident.
var (
	confirmPerIP    = ratelimit.Limit{Burst: 20, Period: 10 * time.Minute}  //nolint:gochecknoglobals // constant policy
	confirmPerStore = ratelimit.Limit{Burst: 600, Period: 10 * time.Minute} //nolint:gochecknoglobals // constant policy
)

// confirmRoute serves /o/<token>: the question (GET) and the answer
// (POST answer=confirm|cancel, same origin only). Private, never
// cached, never indexed.
func (s *Shop) confirmRoute(w http.ResponseWriter, r *http.Request, store tenant.StoreID, p *pageCtx, token string) {
	if s.confirms == nil || token == "" || len(token) > 64 {
		s.writeNotFoundPrivate(w, r, p)
		return
	}
	p.title, p.noindex, p.soft, p.cssURL = p.l.T(MsgConfirmTitle)+" | "+p.v.name, true, true, s.assets.checkoutURL
	var (
		v   ConfirmView
		err error
	)
	switch {
	case isRead(r):
		if ok, retry := s.allowConfirm(r, store); !ok {
			confirmBusy(w, retry)
			return
		}
		v, err = s.confirms.Confirmation(r.Context(), token)
	case r.Method == http.MethodPost:
		if !sameOrigin(r) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		if err = r.ParseForm(); err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		answer := r.PostForm.Get("answer")
		if answer != "confirm" && answer != "cancel" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		if ok, retry := s.allowConfirm(r, store); !ok {
			confirmBusy(w, retry)
			return
		}
		v, err = s.confirms.AnswerConfirmation(r.Context(), token, answer == "confirm")
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	switch {
	case errors.Is(err, ErrConfirmationNotFound):
		s.writeNotFoundPrivate(w, r, p)
	case err != nil:
		s.unavailable(w, r, err)
	default:
		s.writePrivate(w, r, p, http.StatusOK, confirmPage(p, v, p.URL("/o/"+token)))
	}
}

func confirmBusy(w http.ResponseWriter, retry time.Duration) {
	if retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	}
	http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
}

// allowConfirm applies the link's limits.
func (s *Shop) allowConfirm(r *http.Request, store tenant.StoreID) (bool, time.Duration) {
	return s.allowGuarded(r, store, "confirm", confirmPerIP, confirmPerStore)
}
