package retry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
)

var errTransient = errors.New("transient")

func TestDo(t *testing.T) {
	t.Parallel()
	p := retry.Policy{MaxAttempts: 4, Initial: time.Millisecond, Max: 4 * time.Millisecond}
	tests := []struct {
		name      string
		failUntil int // fn fails while calls < failUntil
		permanent bool
		wantCalls int
		wantErr   bool
	}{
		{"first try", 0, false, 1, false},
		{"succeeds on third", 2, false, 3, false},
		{"exhausts", 10, false, 4, true},
		{"permanent stops", 10, true, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			err := retry.Do(t.Context(), p, func(context.Context) error {
				calls++
				if calls <= tt.failUntil {
					if tt.permanent {
						return retry.Permanent(errTransient)
					}
					return errTransient
				}
				return nil
			})
			if (err != nil) != tt.wantErr || calls != tt.wantCalls {
				t.Fatalf("err = %v, calls = %d; want err %v, calls %d", err, calls, tt.wantErr, tt.wantCalls)
			}
			if err != nil && !errors.Is(err, errTransient) {
				t.Fatalf("err %v does not wrap cause", err)
			}
		})
	}
}

func TestDoContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	err := retry.Do(ctx, retry.Policy{MaxAttempts: 100, Initial: time.Hour, Max: time.Hour}, func(context.Context) error {
		calls++
		cancel()
		return errTransient
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errTransient) || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

func TestDefaultsAndEdges(t *testing.T) {
	t.Parallel()
	if d := retry.Default(); d.MaxAttempts < 2 || d.Initial <= 0 || d.Max < d.Initial {
		t.Fatalf("Default = %+v", d)
	}
	if retry.Permanent(nil) != nil {
		t.Fatal("Permanent(nil) must be nil")
	}
	calls := 0
	_ = retry.Do(t.Context(), retry.Policy{}, func(context.Context) error { calls++; return errTransient })
	if calls != 1 {
		t.Fatalf("zero policy calls = %d, want 1", calls)
	}
	// Large attempt counts must cap at Max without overflowing.
	start := time.Now()
	_ = retry.Do(t.Context(), retry.Policy{MaxAttempts: 70, Initial: time.Nanosecond, Max: 10 * time.Microsecond}, func(context.Context) error { return errTransient })
	if time.Since(start) > 2*time.Second {
		t.Fatal("backoff not capped")
	}
}
