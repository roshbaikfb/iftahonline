package errs_test

import (
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestKinds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		kind errs.Kind
		slug string
	}{
		{errs.NotFound("product_not_found", "product not found"), errs.KindNotFound, "not-found"},
		{errs.Conflict("slug_taken", "slug already used"), errs.KindConflict, "conflict"},
		{errs.BadRequest("bad json", io.ErrUnexpectedEOF), errs.KindBadRequest, "bad-request"},
		{errs.Validation(errs.FieldError{Field: "name", Code: "required", Message: "required"}), errs.KindValidation, "validation"},
		{errs.Unauthenticated("login required"), errs.KindUnauthenticated, "unauthenticated"},
		{errs.Forbidden("not your store"), errs.KindForbidden, "forbidden"},
		{errs.RateLimited(time.Second), errs.KindRateLimited, "rate-limited"},
		{errs.Unavailable(time.Second, io.EOF), errs.KindUnavailable, "unavailable"},
		{io.EOF, errs.KindInternal, "internal"},
		{fmt.Errorf("wrapped: %w", errs.NotFound("x", "y")), errs.KindNotFound, "not-found"},
	}
	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			t.Parallel()
			if got := errs.KindOf(tt.err); got != tt.kind {
				t.Fatalf("KindOf = %v, want %v", got, tt.kind)
			}
			if !errs.Is(tt.err, tt.kind) || tt.kind.String() != tt.slug {
				t.Fatalf("Is/String mismatch for %v", tt.err)
			}
		})
	}
	if errs.Is(nil, errs.KindInternal) {
		t.Fatal("nil must not be any kind")
	}
}

func TestErrorStringAndUnwrap(t *testing.T) {
	t.Parallel()
	err := errs.Unavailable(0, io.EOF)
	if !errors.Is(err, io.EOF) {
		t.Fatal("cause must unwrap")
	}
	if got := err.Error(); got != "unavailable (unavailable): service temporarily unavailable: EOF" {
		t.Fatalf("Error() = %q", got)
	}
	if got := (&errs.Error{}).Error(); got != "internal" {
		t.Fatalf("empty Error() = %q", got)
	}
}
