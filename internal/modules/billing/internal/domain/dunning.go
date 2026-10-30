package domain

import "time"

// retryAfter are when a failed invoice is charged again, after its first
// failure: 1, 3 and 5 days (approved 2026-10-30), all inside the 7-day
// grace.
func retryAfter() [3]time.Duration {
	return [3]time.Duration{24 * time.Hour, 72 * time.Hour, 120 * time.Hour}
}

// NextAttempt is when an invoice that failed failures times, first at
// firstFailed, is charged again; ok is false when no retry is left (the
// grace then runs out and the stores are suspended).
func NextAttempt(firstFailed time.Time, failures int) (time.Time, bool) {
	r := retryAfter()
	if failures < 1 || failures > len(r) {
		return time.Time{}, false
	}
	return firstFailed.Add(r[failures-1]), true
}
