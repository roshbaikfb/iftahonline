package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's alerts (ADR-0080): a check every five minutes in the
// worker reads the platform, opens and resolves alerts (two checks in a
// row each way), and tells each admin whose role covers one, once per
// event and channel.

// Alerts timing.
const (
	AlertsEvery     = 5 * time.Minute
	alertsDeliverIn = 24 * time.Hour      // a notice not sent by then is dropped
	alertsShownFor  = 30 * 24 * time.Hour // resolved alerts the page lists
	alertSendTime   = 10 * time.Second
)

// AlertsCheckArgs runs the check.
type AlertsCheckArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (AlertsCheckArgs) Kind() string { return "admin.alerts_check" }

// AlertsCheckWorker runs AlertsCheckArgs.
type AlertsCheckWorker struct {
	river.WorkerDefaults[AlertsCheckArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *AlertsCheckWorker) Work(ctx context.Context, _ *river.Job[AlertsCheckArgs]) error {
	return w.Svc.CheckAlerts(ctx)
}

// Timeout bounds one check: the reads and the notices.
func (*AlertsCheckWorker) Timeout(*river.Job[AlertsCheckArgs]) time.Duration { return 2 * time.Minute }

// AlertsPeriodic schedules the check.
func AlertsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(AlertsEvery),
		func() (river.JobArgs, *river.InsertOpts) { return AlertsCheckArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

// signals reads what the alerts watch; a section that cannot be read is
// unknown, never clean.
func (s *Service) signals(ctx context.Context) domain.Signals {
	h := s.readHealth(ctx)
	sig := domain.Signals{RedisKnown: s.d.Redis != nil, RedisUp: h.Redis}
	if j := h.Jobs; j != nil {
		sig.JobsKnown, sig.Discarded24h, sig.OldestWaitingSeconds = true, j.Discarded24h, j.OldestAvailableSeconds
	}
	if b := h.Billing; b != nil {
		sig.BillingKnown, sig.Overdue, sig.PastDue = true, b.InvoicesOverdue, b.PastDue
	}
	if c := h.Stores; c != nil {
		sig.StoresKnown, sig.Held = true, c.Held
	}
	if s.d.Billing != nil {
		cctx, cancel := context.WithTimeout(ctx, healthTimeout)
		defer cancel()
		if ds, err := s.d.Billing.DuplicatePayments(cctx); err == nil {
			sig.DuplicatesKnown = true
			for _, d := range ds {
				if d.RefundedAt == nil {
					sig.Duplicates++
				}
			}
		} else {
			s.d.Log.WarnContext(ctx, "admin: alerts: duplicates", "err", err)
		}
	}
	return sig
}

// CheckAlerts is one check: it advances every kind and sends what is
// owed.
func (s *Service) CheckAlerts(ctx context.Context) error {
	if err := s.stepAlerts(ctx, s.signals(ctx).Read()); err != nil {
		return err
	}
	return s.deliverAlerts(ctx)
}

// stepAlerts applies one check's readings in one transaction, the open
// alerts locked.
func (s *Service) stepAlerts(ctx context.Context, readings map[domain.AlertKind]domain.Reading) error {
	now := s.now().UTC()
	return store.InTx(ctx, s.d.DB, func(db store.DB) error {
		open, err := store.LockOpenAlerts(ctx, db)
		if err != nil {
			return err
		}
		byKind := map[domain.AlertKind]store.Alert{}
		for _, a := range open {
			byKind[a.Kind] = a
		}
		streaks, err := store.AlertStreaks(ctx, db)
		if err != nil {
			return err
		}
		for _, kind := range domain.AlertKinds() {
			r := readings[kind]
			cur, isOpen := byKind[kind]
			next, step := domain.Step(streaks[kind], r, isOpen)
			switch step {
			case domain.AlertOpen:
				err = store.OpenAlert(ctx, db, store.Alert{ID: s.d.IDs.New(), Kind: kind, Severity: kind.Severity(), Value: r.Value, OpenedAt: now})
			case domain.AlertResolve:
				err = store.ResolveAlert(ctx, db, cur.ID, now)
			case domain.AlertNone:
				if isOpen && r.Known && r.Firing {
					err = store.TouchAlert(ctx, db, cur.ID, r.Value, now)
				}
			}
			if err != nil {
				return err
			}
			if err = store.SaveAlertStreak(ctx, db, kind, next, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// deliverAlerts sends each notice owed: an opening to every admin whose
// role covers it, a red alert's resolution to those told of its opening.
// A notice that fails is tried again at the next check; one not sent in
// a day is dropped.
func (s *Service) deliverAlerts(ctx context.Context) error {
	if s.d.Mailer == nil {
		return nil
	}
	alerts, err := store.AlertsToDeliver(ctx, s.d.DB, s.now().Add(-alertsDeliverIn))
	if err != nil || len(alerts) == 0 {
		return err
	}
	people, err := store.Recipients(ctx, s.d.DB)
	if err != nil {
		return err
	}
	ids := make([]ulid.ID, len(alerts))
	for i, a := range alerts {
		ids[i] = a.ID
	}
	sent, err := store.Deliveries(ctx, s.d.DB, ids)
	if err != nil {
		return err
	}
	for _, a := range alerts {
		for _, p := range people {
			if !p.WantsEmail || !a.Kind.For(p.Role) {
				continue
			}
			opened := store.Delivery{Alert: a.ID, Admin: p.ID, Event: "opened", Channel: "email"}
			d := opened
			switch {
			case a.ResolvedAt == nil:
			case sent[opened]: // a red alert resolved: tell those told it opened
				d.Event = "resolved"
			default:
				continue
			}
			if sent[d] {
				continue
			}
			if err = s.sendAlert(ctx, p, a, d.Event); err != nil {
				s.d.Log.WarnContext(ctx, "admin: alert email failed", "alert", a.ID.String(), "admin", p.ID.String(), "err", err)
				continue
			}
			if err = store.RecordDelivery(ctx, s.d.DB, d); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) sendAlert(ctx context.Context, p store.Recipient, a store.Alert, event string) error {
	cctx, cancel := context.WithTimeout(ctx, alertSendTime)
	defer cancel()
	text := alertText(a)
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		return fmt.Errorf("admin: zone: %w", err)
	}
	when := func(t time.Time) string { return t.In(cairo).Format("2006-01-02 15:04") }
	var b strings.Builder
	subject := "[تنبيه] " + text.Title
	fmt.Fprintf(&b, "مرحبًا %s،\n\n", p.Name)
	if event == "resolved" && a.ResolvedAt != nil {
		subject = "[حُلّ] " + text.Title
		fmt.Fprintf(&b, "حُلّ التنبيه: %s\nفُتح %s، وحُلّ %s (بتوقيت القاهرة).\n", text.Title, when(a.OpenedAt), when(*a.ResolvedAt))
	} else {
		fmt.Fprintf(&b, "فُتح تنبيه في كونسول افتح أونلاين:\n%s\n%s\n\nمنذ: %s (بتوقيت القاهرة)\nافتحه: %s\n\nلن يصلك عنه إيميل آخر حتى يُحلّ.\n",
			text.Title, text.Body, when(a.OpenedAt), strings.TrimSuffix(s.d.ConsoleURL, "/")+text.Path)
	}
	b.WriteString("لإيقاف هذه الرسائل: الكونسول ← التنبيهات ← بالإيميل.\n")
	return s.d.Mailer.Send(cctx, mailer.Message{To: p.Email, Subject: subject, Text: b.String()})
}

// AlertText is an alert in words, and the console page that acts on it.
type AlertText struct {
	Title, Body, Path string
}

// alertText words an alert: the console's home said the same.
func alertText(a store.Alert) AlertText {
	n := func(title string) string { return fmt.Sprintf("%s (%s)", title, groupThousands(a.Value)) }
	switch a.Kind {
	case domain.AlertDuplicates:
		return AlertText{n("دفعات زائدة تحتاج استردادًا"), "تجار دفعوا فاتورة مدفوعة بالفعل. استردّ المبلغ من بوابة الدفع ثم سجّله.",
			"/billing?tab=payments&outcome=duplicate"}
	case domain.AlertDiscarded:
		return AlertText{n("مهام فشلت نهائيًا"), "فشلت خلال آخر 24 ساعة ولن تُعاد. راجع سجلات العامل (worker).", "/"}
	case domain.AlertSlowQueue:
		return AlertText{"طابور المهام متأخر", fmt.Sprintf("أقدم مهمة تنتظر منذ %d دقيقة. تأكد أن العامل (worker) يعمل.", a.Value/60), "/"}
	case domain.AlertRedis:
		return AlertText{"Redis لا يرد", "السلات وحدود الطلبات تعتمد عليه، فقد تتعطل الإضافة إلى السلة في المتاجر حتى يعود.", "/"}
	case domain.AlertOverdue:
		return AlertText{n("فواتير متأخرة"), "تجاوزت موعد الدفع. يتوقف المتجر إن انتهت فترة السماح دون دفع.", "/billing?status=overdue"}
	case domain.AlertHeld:
		return AlertText{n("متاجر موقوفة لفاتورة"), "فاتورة متأخرة أوقفتها. إن وصلك تحويل من أحدها فسجّل الفاتورة مدفوعة.", "/billing?status=overdue"}
	case domain.AlertPastDue:
		return AlertText{n("اشتراكات متأخرة الدفع"), "في فترة السماح الآن، ولم تُدفع فاتورتها بعد.", "/billing?status=overdue"}
	}
	return AlertText{string(a.Kind), "", "/"}
}

func groupThousands(v int64) string {
	s := fmt.Sprint(v)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// AlertView is an alert as the console shows it.
type AlertView struct {
	store.Alert
	AlertText
}

// AlertsPage is the alerts page: what the admin's role hears of, open and
// resolved in the last 30 days, and whether the admin wants them by email.
type AlertsPage struct {
	Open, Resolved []AlertView
	Email          bool
}

// Alerts lists the alerts the signed-in admin's role covers.
func (s *Service) Alerts(ctx context.Context) (AlertsPage, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return AlertsPage{}, errNoSession
	}
	all, err := store.Alerts(ctx, s.d.DB, s.now().Add(-alertsShownFor))
	if err != nil {
		return AlertsPage{}, err
	}
	email, err := store.AlertEmail(ctx, s.d.DB, who.AdminID)
	if err != nil {
		return AlertsPage{}, err
	}
	page := AlertsPage{Open: []AlertView{}, Resolved: []AlertView{}, Email: email}
	for _, a := range all {
		if !a.Kind.For(who.Role) {
			continue
		}
		v := AlertView{Alert: a, AlertText: alertText(a)}
		if a.ResolvedAt == nil {
			page.Open = append(page.Open, v)
		} else {
			page.Resolved = append(page.Resolved, v)
		}
	}
	return page, nil
}

// SetAlertEmail turns the signed-in admin's alert emails on or off.
func (s *Service) SetAlertEmail(ctx context.Context, on bool) error {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	return store.SetAlertEmail(ctx, s.d.DB, who.AdminID, on)
}
