// Package problem writes RFC 9457 application/problem+json responses. It is
// the single place where errors become HTTP statuses (CLAUDE.md rule 10).
//
// Client-facing text comes only from errs.Error.Message; causes and
// unclassified errors are logged with the request ID and replaced by a
// generic message, so internals never leak.
package problem

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/logging"
)

// ContentType is the RFC 9457 media type.
const ContentType = "application/problem+json"

// typePrefix namespaces problem type URIs without assuming a docs domain.
const typePrefix = "urn:iftahonline:problem:"

// Problem is the response body.
type Problem struct {
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Status    int               `json:"status"`
	Detail    string            `json:"detail,omitempty"`
	Instance  string            `json:"instance,omitempty"`
	Code      string            `json:"code,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Errors    []errs.FieldError `json:"errors,omitempty"`
}

// Responder writes problems and logs server-side failures.
type Responder struct{ log *slog.Logger }

// NewResponder returns a Responder that logs to log.
func NewResponder(log *slog.Logger) *Responder { return &Responder{log: log} }

// Write maps err to a problem response.
func (p *Responder) Write(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	pr, retryAfter := From(ctx, err)
	pr.Instance = r.URL.Path

	switch {
	case pr.Status >= http.StatusInternalServerError && !errors.Is(err, context.Canceled):
		p.log.ErrorContext(ctx, "request failed", "status", pr.Status, "err", err)
	case pr.Status >= http.StatusBadRequest:
		p.log.DebugContext(ctx, "request rejected", "status", pr.Status, "err", err)
	}

	h := w.Header()
	h.Set("Content-Type", ContentType)
	h.Set("Cache-Control", "no-store")
	if retryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(int((retryAfter+time.Second-1)/time.Second)))
	}
	w.WriteHeader(pr.Status)
	if encErr := json.NewEncoder(w).Encode(pr); encErr != nil {
		p.log.DebugContext(ctx, "problem: write body", "err", encErr)
	}
}

// From builds the Problem for err without writing it. Exposed for tests
// and for non-HTTP transports that want the same mapping.
func From(ctx context.Context, err error) (Problem, time.Duration) {
	pr := Problem{RequestID: logging.RequestID(ctx)}

	var maxBytes *http.MaxBytesError
	e, isErr := errors.AsType[*errs.Error](err)
	switch {
	case isErr:
		pr.Status = statusOf(e.Kind)
		pr.Code = e.Code
		pr.Errors = e.Fields
		if e.Kind != errs.KindInternal {
			pr.Detail = e.Message
		}
		pr.Type = typePrefix + e.Kind.String()
		pr.Title = http.StatusText(pr.Status)
		if pr.Status == http.StatusInternalServerError {
			pr.Code = ""
		}
		return pr, e.RetryAfter
	case errors.As(err, &maxBytes):
		return fill(pr, http.StatusRequestEntityTooLarge, "too-large", "request body too large"), 0
	case errors.Is(err, context.DeadlineExceeded):
		return fill(pr, http.StatusServiceUnavailable, "timeout", "the request timed out"), time.Second
	case errors.Is(err, context.Canceled):
		// The client went away; the body is never read. 503 keeps proxies honest.
		return fill(pr, http.StatusServiceUnavailable, "canceled", "request canceled"), 0
	default:
		return fill(pr, http.StatusInternalServerError, "internal", "an unexpected error occurred"), 0
	}
}

func fill(pr Problem, status int, slug, detail string) Problem {
	pr.Status = status
	pr.Type = typePrefix + slug
	pr.Title = http.StatusText(status)
	pr.Detail = detail
	return pr
}

func statusOf(k errs.Kind) int {
	switch k {
	case errs.KindBadRequest:
		return http.StatusBadRequest
	case errs.KindValidation:
		return http.StatusUnprocessableEntity
	case errs.KindUnauthenticated:
		return http.StatusUnauthorized
	case errs.KindForbidden:
		return http.StatusForbidden
	case errs.KindNotFound:
		return http.StatusNotFound
	case errs.KindConflict:
		return http.StatusConflict
	case errs.KindRateLimited:
		return http.StatusTooManyRequests
	case errs.KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
