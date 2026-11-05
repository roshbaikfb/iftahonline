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
		RetryAt: retry, GraceUntil: retry.AddDate(0, 0, 6), ClosesAt: retry.AddDate(0, 1, 6)}
	tests := []struct {
		n             domain.Notice
		locale        string
		subject, text []string
	}{
		{failed, "ar", []string{"تعذّر الدفع"}, []string{"INV-2026-000007", "682.86 EGP", "2026-11-02", "الباقة المجانية يوم 2026-11-08", "يُغلق المتجر يوم 2026-12-08"}},
		{failed, "en", []string{"payment failed"}, []string{"682.86 EGP", "try again on 2026-11-02", "free plan on 2026-11-08", "closes on 2026-12-08"}},
		{domain.Notice{Kind: domain.NoticePaymentFailed, Invoice: "INV-2026-000008", TotalMinor: 100, Currency: "SAR"}, "en",
			[]string{"payment failed"}, []string{"1.00 SAR"}},
		{domain.Notice{Kind: domain.NoticeSuspended, ClosesAt: retry.AddDate(0, 1, 6)}, "ar", []string{"الباقة المجانية"}, []string{"إعلانات المنصة", "يُغلق المتجر يوم 2026-12-08"}},
		{domain.Notice{Kind: domain.NoticeSuspended, ClosesAt: retry.AddDate(0, 1, 6)}, "en", []string{"free plan"}, []string{"stays open", "closes on 2026-12-08"}},
		{domain.Notice{Kind: domain.NoticeHeld}, "ar", []string{"أُغلق المتجر"}, []string{"صفحة صيانة"}},
		{domain.Notice{Kind: domain.NoticeHeld}, "en", []string{"store closed"}, []string{"maintenance page"}},
		{domain.Notice{Kind: domain.NoticeBalanceLow, TotalMinor: 9640, Currency: "EGP"}, "ar", []string{"رصيدك منخفض"}, []string{"96.40 EGP", "اشحن"}},
		{domain.Notice{Kind: domain.NoticeBalanceLow, TotalMinor: 9640, Currency: "EGP"}, "en", []string{"balance is low"}, []string{"96.40 EGP", "Top up"}},
		{domain.Notice{Kind: domain.NoticeBalanceEmpty, TotalMinor: 4230, Currency: "EGP", GraceUntil: retry, ClosesAt: retry.AddDate(0, 0, 30)}, "ar",
			[]string{"نفد رصيدك"}, []string{"42.30 EGP", "الباقة المجانية يوم 2026-11-02", "يُغلق المتجر يوم 2026-12-02"}},
		{domain.Notice{Kind: domain.NoticeBalanceEmpty, TotalMinor: 4230, Currency: "EGP"}, "en", []string{"balance ran out"}, []string{"owes 42.30 EGP"}},
		{domain.Notice{Kind: domain.NoticeBalanceRestored, TotalMinor: 45770, Currency: "EGP"}, "ar", []string{"رجع متجرك"}, []string{"457.70 EGP"}},
		{domain.Notice{Kind: domain.NoticeBalanceRestored, TotalMinor: 45770, Currency: "EGP"}, "en", []string{"store is back"}, []string{"457.70 EGP"}},
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
