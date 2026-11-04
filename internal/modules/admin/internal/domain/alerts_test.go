package domain_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
)

// TestStep (ADR-0080): two bad checks open, two clean ones resolve, one
// alone changes nothing, and an unread section changes nothing either.
func TestStep(t *testing.T) {
	t.Parallel()
	bad, good, unknown := domain.Reading{Known: true, Firing: true, Value: 3}, domain.Reading{Known: true}, domain.Reading{}
	type check struct {
		r    domain.Reading
		want domain.AlertStep
	}
	for name, run := range map[string][]check{
		"opens on the second bad check": {{bad, domain.AlertNone}, {bad, domain.AlertOpen}},
		"a blip changes nothing":        {{bad, domain.AlertNone}, {good, domain.AlertNone}, {bad, domain.AlertNone}, {good, domain.AlertNone}},
		"unread keeps the streak":       {{bad, domain.AlertNone}, {unknown, domain.AlertNone}, {bad, domain.AlertOpen}},
		"resolves on the second clean":  {{bad, domain.AlertNone}, {bad, domain.AlertOpen}, {good, domain.AlertNone}, {bad, domain.AlertNone}, {good, domain.AlertNone}, {good, domain.AlertResolve}},
		"a third bad check stays open":  {{bad, domain.AlertNone}, {bad, domain.AlertOpen}, {bad, domain.AlertNone}},
	} {
		var s domain.AlertStreak
		open := false
		for i, c := range run {
			var step domain.AlertStep
			s, step = domain.Step(s, c.r, open)
			if step != c.want {
				t.Errorf("%s: check %d = %v; want %v", name, i+1, step, c.want)
			}
			switch step {
			case domain.AlertOpen:
				open = true
			case domain.AlertResolve:
				open = false
			case domain.AlertNone:
			}
		}
	}
}

// TestSignals: each kind fires from its own section, an unread section
// is unknown, and the queue fires only past its limit.
func TestSignals(t *testing.T) {
	t.Parallel()
	got := domain.Signals{RedisKnown: true, RedisUp: false, JobsKnown: true, Discarded24h: 2, OldestWaitingSeconds: domain.SlowQueueSeconds,
		BillingKnown: true, Overdue: 6, DuplicatesKnown: true}.Read()
	for kind, want := range map[domain.AlertKind]domain.Reading{
		domain.AlertRedis:      {Known: true, Firing: true},
		domain.AlertDiscarded:  {Known: true, Firing: true, Value: 2},
		domain.AlertSlowQueue:  {Known: true, Value: domain.SlowQueueSeconds},
		domain.AlertOverdue:    {Known: true, Firing: true, Value: 6},
		domain.AlertPastDue:    {Known: true},
		domain.AlertDuplicates: {Known: true},
		domain.AlertHeld:       {},
	} {
		if got[kind] != want {
			t.Errorf("%s = %+v; want %+v", kind, got[kind], want)
		}
	}
	if len(got) != len(domain.AlertKinds()) {
		t.Fatalf("%d readings for %d kinds", len(got), len(domain.AlertKinds()))
	}
}

// TestAlertAudience: owners hear of everything, finance of money,
// operations of the jobs, content and viewers of nothing; only red
// alerts announce their resolution.
func TestAlertAudience(t *testing.T) {
	t.Parallel()
	for _, k := range domain.AlertKinds() {
		if !k.For(domain.RoleOwner) || k.For(domain.RoleContent) || k.For(domain.RoleViewer) {
			t.Errorf("%s: owner, content or viewer", k)
		}
		money := k == domain.AlertDuplicates || k == domain.AlertOverdue || k == domain.AlertHeld || k == domain.AlertPastDue
		if k.For(domain.RoleFinance) != money {
			t.Errorf("%s: finance", k)
		}
		if k.For(domain.RoleOperations) != (k == domain.AlertDiscarded || k == domain.AlertSlowQueue) {
			t.Errorf("%s: operations", k)
		}
		red := k == domain.AlertDuplicates || k == domain.AlertDiscarded || k == domain.AlertSlowQueue || k == domain.AlertRedis
		if (k.Severity() == domain.SeverityRed) != red || k.NotifyResolved() != red {
			t.Errorf("%s: severity", k)
		}
	}
}
