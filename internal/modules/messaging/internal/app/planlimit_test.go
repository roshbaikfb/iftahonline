package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
)

// TestPlanLimitWarnings: a plan's monthly limit near and reached is one
// notice each per month, emailed to the staff; a repeat adds nothing.
func TestPlanLimitWarnings(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{})
	ctx := c.store(t)
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, ev := range []entitlements.UsageCrossed{
		{Feature: entitlements.OrdersPerMonth, Threshold: entitlements.Nearly, Period: october, Used: 240, Limit: 300},
		{Feature: entitlements.OrdersPerMonth, Threshold: entitlements.Reached, Period: october, Used: 300, Limit: 300},
		{Feature: entitlements.OrdersPerMonth, Threshold: entitlements.Reached, Period: october, Used: 300, Limit: 300}, // a retried job
		{Feature: entitlements.OrdersPerMonth, Threshold: entitlements.NoThreshold, Period: october},
	} {
		if err := c.svc.PlanLimitCrossed(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if kinds := c.noticeKinds(ctx, t); kinds != "quota_reached,quota_nearly" {
		t.Fatalf("notices = %s", kinds)
	}
	emails := c.jobsOf("messaging.email_notice")
	if len(emails) != 2 {
		t.Fatalf("email jobs = %d", len(emails))
	}
	for _, e := range emails {
		if err := c.svc.EmailNotice(ctx, e.(app.EmailNoticeArgs).NotificationID); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.mail.got) != 4 || !strings.Contains(c.mail.got[0].Subject, "80%") || !strings.Contains(c.mail.got[2].Subject, "used up") ||
		!strings.Contains(c.mail.got[2].Text, "https://app.example.com/billing") {
		t.Fatalf("emails = %+v", c.mail.got)
	}
	p, err := c.svc.Notifications(ctx, nil, false, 10)
	if err != nil || p.Items[0].Data.Feature != "orders_per_month" || p.Items[0].Data.Limit != 300 {
		t.Fatalf("notice = %+v, %v", p.Items, err)
	}
	// Next month warns again.
	if err = c.svc.PlanLimitCrossed(ctx, entitlements.UsageCrossed{Feature: entitlements.OrdersPerMonth, Threshold: entitlements.Nearly,
		Period: october.AddDate(0, 1, 0), Used: 240, Limit: 300}); err != nil {
		t.Fatal(err)
	}
	if kinds := c.noticeKinds(ctx, t); !strings.HasPrefix(kinds, "quota_nearly,") {
		t.Fatalf("next month = %s", kinds)
	}
}
