package problem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/logging"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
)

func TestWrite(t *testing.T) {
	t.Parallel()
	secret := errors.New("pq: password authentication failed for user app")
	tests := []struct {
		name       string
		err        error
		status     int
		typ        string
		code       string
		detail     string
		retryAfter string
		fields     int
		logged     bool
	}{
		{"not found", errs.NotFound("product_not_found", "product not found"), 404, "urn:iftahonline:problem:not-found", "product_not_found", "product not found", "", 0, false},
		{"validation", errs.Validation(errs.FieldError{Field: "name", Code: "required", Message: "name is required"}), 422, "urn:iftahonline:problem:validation", "validation_failed", "one or more fields are invalid", "", 1, false},
		{"bad request", errs.BadRequest("invalid JSON", secret), 400, "urn:iftahonline:problem:bad-request", "bad_request", "invalid JSON", "", 0, false},
		{"unauth", errs.Unauthenticated("login required"), 401, "urn:iftahonline:problem:unauthenticated", "unauthenticated", "login required", "", 0, false},
		{"forbidden", errs.Forbidden("no access"), 403, "urn:iftahonline:problem:forbidden", "forbidden", "no access", "", 0, false},
		{"conflict", errs.Conflict("slug_taken", "slug taken"), 409, "urn:iftahonline:problem:conflict", "slug_taken", "slug taken", "", 0, false},
		{"rate limited", errs.RateLimited(1500 * time.Millisecond), 429, "urn:iftahonline:problem:rate-limited", "rate_limited", "too many requests", "2", 0, false},
		{"unavailable", errs.Unavailable(3*time.Second, secret), 503, "urn:iftahonline:problem:unavailable", "unavailable", "service temporarily unavailable", "3", 0, true},
		{"classified internal", &errs.Error{Kind: errs.KindInternal, Code: "x", Message: "leaky", Cause: secret}, 500, "urn:iftahonline:problem:internal", "", "", "", 0, true},
		{"unclassified", fmt.Errorf("store: %w", secret), 500, "urn:iftahonline:problem:internal", "", "an unexpected error occurred", "", 0, true},
		{"timeout", fmt.Errorf("q: %w", context.DeadlineExceeded), 503, "urn:iftahonline:problem:timeout", "", "the request timed out", "1", 0, true},
		{"canceled", context.Canceled, 503, "urn:iftahonline:problem:canceled", "", "request canceled", "", 0, false},
		{"too large", &http.MaxBytesError{Limit: 10}, 413, "urn:iftahonline:problem:too-large", "", "request body too large", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			resp := problem.NewResponder(logging.New(logging.Options{Writer: &logs, Level: slog.LevelInfo}))
			ctx := logging.WithRequestID(t.Context(), "req-42")
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/products/123?token=abc", nil)
			rec := httptest.NewRecorder()

			resp.Write(rec, req, tt.err)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != problem.ContentType {
				t.Fatalf("content-type = %q", ct)
			}
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Retry-After") != tt.retryAfter {
				t.Fatalf("headers = %v", rec.Header())
			}
			var p problem.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if p.Status != tt.status || p.Type != tt.typ || p.Code != tt.code || p.Detail != tt.detail ||
				p.RequestID != "req-42" || p.Instance != "/v1/products/123" || len(p.Errors) != tt.fields || p.Title == "" {
				t.Fatalf("problem = %+v", p)
			}
			if strings.Contains(rec.Body.String(), "password") || strings.Contains(rec.Body.String(), "leaky") {
				t.Fatalf("internal detail leaked: %s", rec.Body.String())
			}
			if logged := strings.Contains(logs.String(), `"request failed"`); logged != tt.logged {
				t.Fatalf("logged = %v, want %v: %s", logged, tt.logged, logs.String())
			}
		})
	}
}
