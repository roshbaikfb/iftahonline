package app_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
)

// fakeMail records what was sent; an address in fail fails.
type fakeMail struct {
	mu   sync.Mutex
	sent []mailer.Message
	fail map[string]bool
}

func (m *fakeMail) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail[msg.To] {
		return errors.New("smtp down")
	}
	m.sent = append(m.sent, msg)
	return nil
}

// to lists the subjects sent to addr, and forgets them.
func (m *fakeMail) to(addr string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	keep := m.sent[:0]
	for _, msg := range m.sent {
		if msg.To == addr {
			out = append(out, msg.Subject)
		} else {
			keep = append(keep, msg)
		}
	}
	m.sent = keep
	return out
}

// TestAlerts (ADR-0080) walks a red and an amber alert through two
// checks each way: who is told, once, of what; who reads them in the
// console; a failed email is tried again; an email turned off is not
// sent. The alert tables are global, so this test runs alone.
func TestAlerts(t *testing.T) {
	f := newFixture(t)
	mail := &fakeMail{fail: map[string]bool{}}
	d := f.deps
	d.Mailer, d.ConsoleURL = mail, "https://admin.iftahonline.com/console"
	svc := app.NewService(d)

	owner, ownerEmail := f.createAdminAs(t, domain.RoleOwner)
	_, financeEmail := f.createAdminAs(t, domain.RoleFinance)
	_, contentEmail := f.createAdminAs(t, domain.RoleContent)
	quiet, quietEmail := f.createAdminAs(t, domain.RoleOwner)
	ownerCtx := f.signedInWith(t, owner, ownerEmail)
	if err := svc.SetAlertEmail(f.signedInWith(t, quiet, quietEmail), false); err != nil {
		t.Fatal(err)
	}

	read := func(kind domain.AlertKind, r domain.Reading) map[domain.AlertKind]domain.Reading {
		out := map[domain.AlertKind]domain.Reading{}
		for _, k := range domain.AlertKinds() {
			out[k] = domain.Reading{Known: true}
		}
		out[kind] = r
		return out
	}
	step := func(m map[domain.AlertKind]domain.Reading) {
		t.Helper()
		if err := svc.StepAlerts(t.Context(), m); err != nil {
			t.Fatal(err)
		}
		if err := svc.DeliverAlerts(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	dup := domain.Reading{Known: true, Firing: true, Value: 2}

	// One bad check changes nothing; the second opens the alert.
	step(read(domain.AlertDuplicates, dup))
	if got := mail.to(ownerEmail); len(got) != 0 {
		t.Fatalf("after one check: %v", got)
	}
	mail.fail[financeEmail] = true
	step(read(domain.AlertDuplicates, dup))
	if got := mail.to(ownerEmail); !slices.Equal(got, []string{"[تنبيه] دفعات زائدة تحتاج استردادًا (2)"}) {
		t.Fatalf("owner = %v", got)
	}
	for name, addr := range map[string]string{"failed finance": financeEmail, "content": contentEmail, "email off": quietEmail} {
		if got := mail.to(addr); len(got) != 0 {
			t.Errorf("%s = %v", name, got)
		}
	}
	// The failed one is sent at the next check, the others not again.
	delete(mail.fail, financeEmail)
	step(read(domain.AlertDuplicates, domain.Reading{Known: true, Firing: true, Value: 3}))
	if got := mail.to(financeEmail); len(got) != 1 || len(mail.to(ownerEmail)) != 0 {
		t.Fatalf("retry: finance %v", got)
	}

	page, err := svc.Alerts(ownerCtx)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(page.Open, func(a app.AlertView) bool { return a.Kind == domain.AlertDuplicates })
	if i < 0 || page.Open[i].Value != 3 || page.Open[i].Path != "/billing?tab=payments&outcome=duplicate" || !page.Email {
		t.Fatalf("page = %+v", page)
	}
	contentPage, err := svc.Alerts(f.signedInWithRole(t, domain.RoleContent))
	if err != nil || len(contentPage.Open) != 0 {
		t.Fatalf("content sees %+v, %v", contentPage.Open, err)
	}

	// An unread section keeps it open; two clean checks resolve it, and a
	// red alert's resolution is told to those told of it.
	step(read(domain.AlertDuplicates, domain.Reading{}))
	step(read(domain.AlertDuplicates, domain.Reading{Known: true}))
	if got := mail.to(ownerEmail); len(got) != 0 {
		t.Fatalf("after one clean check: %v", got)
	}
	step(read(domain.AlertDuplicates, domain.Reading{Known: true}))
	if got := mail.to(ownerEmail); !slices.Equal(got, []string{"[حُلّ] دفعات زائدة تحتاج استردادًا (3)"}) {
		t.Fatalf("resolved = %v", got)
	}
	if got := mail.to(financeEmail); len(got) != 1 || len(mail.to(quietEmail)) != 0 {
		t.Fatalf("finance resolved = %v", got)
	}

	// An amber alert opens with a notice and resolves without one.
	overdue := domain.Reading{Known: true, Firing: true, Value: 6}
	step(read(domain.AlertOverdue, overdue))
	step(read(domain.AlertOverdue, overdue))
	if got := mail.to(ownerEmail); len(got) != 1 || !strings.Contains(got[0], "فواتير متأخرة (6)") {
		t.Fatalf("overdue opened = %v", got)
	}
	step(read(domain.AlertOverdue, domain.Reading{Known: true}))
	step(read(domain.AlertOverdue, domain.Reading{Known: true}))
	if got := mail.to(ownerEmail); len(got) != 0 {
		t.Fatalf("an amber resolution was sent: %v", got)
	}
	if page, err = svc.Alerts(ownerCtx); err != nil || !slices.ContainsFunc(page.Resolved, func(a app.AlertView) bool { return a.Kind == domain.AlertOverdue }) {
		t.Fatalf("resolved list = %+v, %v", page.Resolved, err)
	}
	if _, err = svc.Alerts(t.Context()); err == nil {
		t.Fatal("not signed in")
	}
	// A real check reads the platform and steps every kind.
	if err = svc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("check: %v", err)
	}
}
