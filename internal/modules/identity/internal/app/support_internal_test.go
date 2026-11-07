package app

import (
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
)

// TestStoreMoment: the emails say a time in the store's zone, named as
// its merchant says it; an unknown zone falls back to Greenwich.
func TestStoreMoment(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 8, 11, 30, 0, 0, time.UTC)
	for zone, want := range map[string]string{
		"Asia/Riyadh":    "الأحد 8 نوفمبر 2:30 م بتوقيت مكة",
		"Africa/Cairo":   "الأحد 8 نوفمبر 1:30 م بتوقيت القاهرة",
		"Europe/Berlin":  "الأحد 8 نوفمبر 12:30 م بتوقيت Europe/Berlin",
		"":               "الأحد 8 نوفمبر 11:30 ص بتوقيت غرينتش",
		"Not/A_Timezone": "الأحد 8 نوفمبر 11:30 ص بتوقيت غرينتش",
	} {
		if got := storeMoment(tenancy.Store{Timezone: zone}, at); got != want {
			t.Errorf("%q: %q; want %q", zone, got, want)
		}
	}
}

// TestSupportTimeouts: every support job has a bound.
func TestSupportTimeouts(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{
		(&SupportLinkEmailWorker{}).Timeout(&river.Job[SupportLinkEmailArgs]{}),
		(&SupportExecuteWorker{}).Timeout(&river.Job[SupportExecuteArgs]{}),
		(&SupportNoticeWorker{}).Timeout(&river.Job[SupportNoticeArgs]{}),
		(&SupportCancelledWorker{}).Timeout(&river.Job[SupportCancelledArgs]{}),
	} {
		if d <= 0 || d > time.Minute {
			t.Errorf("timeout %v", d)
		}
	}
}
