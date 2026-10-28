package domain_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func TestSettings(t *testing.T) {
	t.Parallel()
	if d := domain.DefaultSettings(); !d.ConfirmCOD || d.AutoCancelHours != 24 || !d.Valid() {
		t.Fatalf("defaults = %+v", d)
	}
	for hours, want := range map[int]bool{0: false, 1: true, 72: true, 73: false, -5: false} {
		if got := (domain.Settings{AutoCancelHours: hours}).Valid(); got != want {
			t.Errorf("%d hours valid = %v", hours, got)
		}
	}
}

func TestToken(t *testing.T) {
	t.Parallel()
	a, ha, err := domain.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, hb, err := domain.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 22 || a == b || bytes.Equal(ha, hb) || len(ha) != 32 || !bytes.Equal(domain.HashToken(a), ha) || strings.ContainsAny(a, "+/=") {
		t.Fatalf("tokens %q %q", a, b)
	}
	if got := domain.ConfirmPath("ar", a); got != "/ar/o/"+a {
		t.Fatalf("path = %q", got)
	}
}

func TestNotices(t *testing.T) {
	t.Parallel()
	period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	q := domain.QuotaNotice(domain.QuotaReached, period, 500, 500)
	if q.DedupKey != "2026-10" || q.Data.Period != "2026-10" || q.Data.Used != 500 || !q.Kind.Emailed() {
		t.Fatalf("quota notice = %+v", q)
	}
	order := ulid.NewGenerator().New()
	o := domain.OrderNotice(domain.OrderAutoCancelled, order, "7P0R1S2T")
	if o.DedupKey != order.String() || o.Data.OrderRef != "7P0R1S2T" || o.Kind.Emailed() {
		t.Fatalf("order notice = %+v", o)
	}
	for _, k := range []domain.NoticeKind{domain.OrderConfirmedNotice, domain.OrderCancelledNotice, domain.ConfirmationUnreached} {
		if k.Emailed() {
			t.Errorf("%s emailed", k)
		}
	}
	nearly := domain.QuotaNotice(domain.QuotaNearly, period, 400, 500)
	tests := []struct {
		n                    domain.Notification
		locale, subj, inText string
	}{
		{q, "ar", "سوق: نفدت رسائل هذا الشهر", "500 رسالة"},
		{nearly, "ar", "سوق: استُخدم 80% من رسائل هذا الشهر", "400 من 500"},
		{q, "en", "سوق: monthly messages used up", "all 500 customer messages"},
		{nearly, "en", "سوق: 80% of monthly messages used", "400 of its 500"},
	}
	for _, tt := range tests {
		subj, text := tt.n.Email(tt.locale, "سوق", "https://app.example.com")
		if subj != tt.subj || !strings.Contains(text, tt.inText) || !strings.Contains(text, "https://app.example.com/messages") {
			t.Errorf("%s/%s: %q\n%s", tt.n.Kind, tt.locale, subj, text)
		}
	}
}
