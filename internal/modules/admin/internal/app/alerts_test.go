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
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
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

// fakePush records the notifications sent; a device whose endpoint ends
// in /gone is gone.
type fakePush struct {
	mu   sync.Mutex
	sent []string // endpoint + " " + payload
}

func (p *fakePush) Push(_ context.Context, d store.PushDevice, payload []byte, _ bool) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.HasSuffix(d.Endpoint, "/gone") {
		return true, nil
	}
	p.sent = append(p.sent, d.Endpoint+" "+string(payload))
	return false, nil
}

func (p *fakePush) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.sent
	p.sent = nil
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
	push := &fakePush{}
	d.Mailer, d.ConsoleURL, d.Pusher, d.PushPublicKey = mail, "https://admin.iftahonline.com/console", push, "BPublicKey"
	svc := app.NewService(d)

	owner, ownerEmail := f.createAdminAs(t, domain.RoleOwner)
	_, financeEmail := f.createAdminAs(t, domain.RoleFinance)
	_, contentEmail := f.createAdminAs(t, domain.RoleContent)
	quiet, quietEmail := f.createAdminAs(t, domain.RoleOwner)
	ownerCtx := f.signedInWith(t, owner, ownerEmail)
	quietCtx := f.signedInWith(t, quiet, quietEmail)
	if err := svc.SetAlertEmail(quietCtx, false); err != nil {
		t.Fatal(err)
	}
	// The owner turned notifications on in a phone and in a browser whose
	// subscription later expired; the quiet owner, with email off, in one.
	p256dh, auth := strings.Repeat("B", 87), strings.Repeat("A", 22)
	phone := "https://fcm.googleapis.com/fcm/send/" + owner.Admin.ID.String()
	for ctx, endpoint := range map[context.Context]string{ownerCtx: phone, quietCtx: "https://web.push.apple.com/" + quiet.Admin.ID.String()} {
		if err := svc.SubscribePush(ctx, endpoint, p256dh, auth); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.SubscribePush(ownerCtx, "https://updates.push.services.mozilla.com/"+owner.Admin.ID.String()+"/gone", p256dh, auth); err != nil {
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
	// Push: the owner's phone and the quiet owner's device, once each; the
	// gone browser is forgotten.
	pushed := push.take()
	mine := slices.DeleteFunc(slices.Clone(pushed), func(p string) bool {
		return !strings.Contains(p, owner.Admin.ID.String()) && !strings.Contains(p, quiet.Admin.ID.String())
	})
	if len(mine) != 2 || !strings.Contains(mine[0]+mine[1], `"path":"/billing?tab=payments\u0026outcome=duplicate"`) {
		t.Fatalf("pushed = %v", mine)
	}
	if st, err := svc.PushStatus(ownerCtx); err != nil || st.Devices != 1 || st.PublicKey != "BPublicKey" {
		t.Fatalf("owner's devices = %+v, %v", st, err)
	}
	for name, addr := range map[string]string{"failed finance": financeEmail, "content": contentEmail, "email off": quietEmail} {
		if got := mail.to(addr); len(got) != 0 {
			t.Errorf("%s = %v", name, got)
		}
	}
	// The failed one is sent at the next check, the others not again
	// (nor pushed again).
	delete(mail.fail, financeEmail)
	step(read(domain.AlertDuplicates, domain.Reading{Known: true, Firing: true, Value: 3}))
	if got := mail.to(financeEmail); len(got) != 1 || len(mail.to(ownerEmail)) != 0 {
		t.Fatalf("retry: finance %v", got)
	}
	if again := slices.DeleteFunc(push.take(), func(p string) bool { return !strings.Contains(p, owner.Admin.ID.String()) }); len(again) != 0 {
		t.Fatalf("pushed again: %v", again)
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
	if got := slices.DeleteFunc(push.take(), func(p string) bool { return !strings.Contains(p, phone) }); len(got) != 1 || !strings.Contains(got[0], "حُلّ: ") {
		t.Fatalf("resolved push = %v", got)
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
	if err = svc.UnsubscribePush(ownerCtx, phone); err != nil {
		t.Fatal(err)
	}
	if st, stErr := svc.PushStatus(ownerCtx); stErr != nil || st.Devices != 0 {
		t.Fatalf("after unsubscribing = %+v, %v", st, stErr)
	}
	// A real check reads the platform and steps every kind.
	if err = svc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("check: %v", err)
	}
}

// TestSubscribePush: a device is accepted only from a known push service
// with its keys, and only when this server sends notifications.
func TestSubscribePush(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, _ := f.signedIn(t)
	d := f.deps
	d.PushPublicKey = "BPublicKey"
	svc := app.NewService(d)
	p256dh, auth := strings.Repeat("B", 87), strings.Repeat("A", 22)
	for name, err := range map[string]error{
		"another host": svc.SubscribePush(ctx, "https://evil.example/push", p256dh, auth),
		"plain http":   svc.SubscribePush(ctx, "http://fcm.googleapis.com/x", p256dh, auth),
		"short key":    svc.SubscribePush(ctx, "https://fcm.googleapis.com/x", p256dh[:40], auth),
		"bad auth":     svc.SubscribePush(ctx, "https://fcm.googleapis.com/x", p256dh, "!!"),
	} {
		if !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.svc.SubscribePush(ctx, "https://fcm.googleapis.com/x", p256dh, auth); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("no keys on this server: %v", err)
	}
	if st, err := f.svc.PushStatus(ctx); err != nil || st.PublicKey != "" {
		t.Fatalf("status without keys = %+v, %v", st, err)
	}
	if err := svc.SubscribePush(t.Context(), "https://fcm.googleapis.com/x", p256dh, auth); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
}
