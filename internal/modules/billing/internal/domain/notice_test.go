package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
)

func TestNoticeEmails(t *testing.T) {
	t.Parallel()
	retry := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	failed := domain.Notice{Kind: domain.NoticePaymentFailed, Invoice: "INV-2026-000007", TotalMinor: 68286, Currency: "EGP",
		RetryAt: retry, GraceUntil: retry.AddDate(0, 0, 6)}
	tests := []struct {
		n             domain.Notice
		locale        string
		subject, text []string
	}{
		{failed, "ar", []string{"تعذّر الدفع"}, []string{"INV-2026-000007", "682.86 EGP", "2026-11-02", "2026-11-08"}},
		{failed, "en", []string{"payment failed"}, []string{"682.86 EGP", "try again on 2026-11-02", "closes on 2026-11-08"}},
		{domain.Notice{Kind: domain.NoticePaymentFailed, Invoice: "INV-2026-000008", TotalMinor: 100, Currency: "SAR"}, "en",
			[]string{"payment failed"}, []string{"1.00 SAR"}},
		{domain.Notice{Kind: domain.NoticeSuspended}, "ar", []string{"أُغلق المتجر"}, []string{"صفحة صيانة"}},
		{domain.Notice{Kind: domain.NoticeSuspended}, "en", []string{"store closed"}, []string{"maintenance page"}},
		{domain.Notice{Kind: domain.NoticePaid, Invoice: "INV-2026-000007", TotalMinor: 68286, Currency: "EGP"}, "ar", []string{"تم استلام الدفع"}, []string{"682.86 EGP"}},
		{domain.Notice{Kind: domain.NoticePaid, Invoice: "INV-2026-000007", TotalMinor: 68286, Currency: "EGP"}, "en", []string{"payment received"}, []string{"is paid"}},
	}
	for _, tt := range tests {
		subject, text := tt.n.Email(tt.locale, "سوق", "https://app.example.com/billing")
		for _, w := range tt.subject {
			if !strings.Contains(subject, w) || !strings.HasPrefix(subject, "سوق") {
				t.Errorf("%s/%s subject %q lacks %q", tt.n.Kind, tt.locale, subject, w)
			}
		}
		for _, w := range append(tt.text, "https://app.example.com/billing") {
			if !strings.Contains(text, w) {
				t.Errorf("%s/%s text lacks %q:\n%s", tt.n.Kind, tt.locale, w, text)
			}
		}
	}
	if s, _ := (domain.Notice{Kind: domain.NoticePaymentFailed, Invoice: "X", TotalMinor: 1, Currency: "EGP"}).Email("ar", "s", "l"); strings.Contains(s, "2026") {
		t.Error("no dates without them")
	}
}
