// Package retry runs an operation with capped exponential backoff and full
// jitter (CLAUDE.md rule 8). The operation must be idempotent.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Policy configures Do. The zero value is not useful; use Default.
type Policy struct {
	MaxAttempts int           // total tries, including the first; >= 1
	Initial     time.Duration // first backoff ceiling
	Max         time.Duration // backoff ceiling cap
}

// Default suits calls to external providers inside a job.
func Default() Policy {
	return Policy{MaxAttempts: 4, Initial: 200 * time.Millisecond, Max: 5 * time.Second}
}

type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent marks err as not worth retrying (e.g. HTTP 400 from a provider).
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// Do calls fn until it succeeds, returns a Permanent error, the attempts
// run out, or ctx ends. Between attempts it sleeps a random duration in
// [0, min(Max, Initial*2^n)) ("full jitter").
func Do(ctx context.Context, p Policy, fn func(context.Context) error) error {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	var err error
	for attempt := range p.MaxAttempts {
		if err = fn(ctx); err == nil {
			return nil
		}
		if perm, ok := errors.AsType[permanentError](err); ok {
			return perm.err
		}
		if attempt == p.MaxAttempts-1 {
			break
		}
		t := time.NewTimer(jitter(backoff(p, attempt)))
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("retry: %w (last error: %w)", ctx.Err(), err)
		case <-t.C:
		}
	}
	return fmt.Errorf("retry: gave up after %d attempts: %w", p.MaxAttempts, err)
}

func backoff(p Policy, attempt int) time.Duration {
	d := p.Initial
	for range attempt {
		if d >= p.Max/2 {
			return p.Max
		}
		d *= 2
	}
	return min(d, p.Max)
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return rand.N(d) //nolint:gosec // jitter is not security-sensitive.
}
