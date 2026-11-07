package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"

	"github.com/jackc/pgx/v5"
)

// AdminNotifier emails a platform admin (the console's mailer, wired in
// cmd/platform): identity cannot read the admins' table.
type AdminNotifier func(ctx context.Context, admin ulid.ID, subject, text string) error

// zoneNames name the time zones the platform's stores use, as the
// emails say them.
var zoneNames = map[string]string{ //nolint:gochecknoglobals // constant table
	"Africa/Cairo": "القاهرة", "Asia/Riyadh": "مكة", "Asia/Dubai": "الإمارات", "Asia/Kuwait": "الكويت",
	"Asia/Qatar": "قطر", "Asia/Bahrain": "البحرين", "Asia/Muscat": "مسقط",
}

// storeMoment is t as the store's merchant reads it: "الأحد 8 نوفمبر 2:30
// م بتوقيت مكة".
func storeMoment(st tenancy.Store, t time.Time) string {
	loc, err := time.LoadLocation(st.Timezone)
	if err != nil || st.Timezone == "" {
		return arabic.Moment(t.UTC()) + " بتوقيت غرينتش"
	}
	name, ok := zoneNames[st.Timezone]
	if !ok {
		name = st.Timezone
	}
	return arabic.Moment(t.In(loc)) + " بتوقيت " + name
}

// supportRequest reads a request of the job's store.
func supportRequest(ctx context.Context, db DB, id ulid.ID) (store.SupportRequest, error) {
	var req store.SupportRequest
	err := postgres.InTenantTx(ctx, db, func(tx pgx.Tx) error {
		var txErr error
		req, txErr = store.GetSupportRequest(ctx, tx, id, false)
		return txErr
	})
	return req, err
}

// SupportLinkEmailWorker sends a support request's cancel link (C1, the
// approved mockup): the link is in the URL fragment, so it never reaches
// a server log; a request closed meanwhile sends nothing.
type SupportLinkEmailWorker struct {
	river.WorkerDefaults[SupportLinkEmailArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*SupportLinkEmailWorker) Timeout(*river.Job[SupportLinkEmailArgs]) time.Duration {
	return 30 * time.Second
}

// Work implements river.Worker.
func (w *SupportLinkEmailWorker) Work(ctx context.Context, job *river.Job[SupportLinkEmailArgs]) error {
	id := job.Args.TokenID
	key := supportLinkKey(id)
	d, found, err := readDelivery(ctx, w.D, key, supportLinkAAD(id))
	if err != nil || !found {
		return err
	}
	t, err := store.SupportTokenForSend(ctx, w.D.DB, id)
	if errors.Is(err, domain.ErrNotFound) {
		return w.D.Redis.Del(ctx, key).Err()
	}
	if err != nil {
		return err
	}
	req, err := supportRequest(ctx, w.D.DB, t.RequestID)
	if errors.Is(err, domain.ErrNotFound) {
		return w.D.Redis.Del(ctx, key).Err()
	}
	if err != nil {
		return err
	}
	st, err := w.D.Stores.GetStore(ctx)
	if err != nil {
		return err
	}
	link := strings.TrimRight(w.D.DashboardURL, "/") + "/support-request#" + d.Text
	when := storeMoment(st, req.EffectiveAt)
	var msg mailer.Message
	if job.Args.Owner {
		member, memberErr := store.UserByID(ctx, w.D.DB, req.UserID)
		if memberErr != nil {
			return memberErr
		}
		msg = mailer.Message{To: t.Email, Subject: fmt.Sprintf("طلب إلغاء التحقق بخطوتين لـ%s في %s", member.Name, st.Name),
			Text: fmt.Sprintf("مرحبًا %s،\n\nطلب فريق دعم المنصة إلغاء التحقق بخطوتين لحساب %s في متجر \"%s\"، بعد أن أخبرنا أحد أنه فقد موبايله. "+
				"سيدخل بعدها بكلمة المرور وحدها.\n\nيُنفَّذ الطلب %s.\n\nإن لم يطلب %s هذا، ألغِ الطلب الآن (الرابط يعمل حتى تنفيذه):\n%s\n\n"+
				"Platform support asked to remove two-step verification from %s's account in \"%s\". It takes effect in 24 hours. "+
				"If they did not ask for it, cancel it: %s",
				t.Name, member.Name, st.Name, when, member.Name, link, member.Name, st.Name, link)}
	} else {
		msg = mailer.Message{To: t.Email, Subject: "طلب إلغاء التحقق بخطوتين لحسابك",
			Text: fmt.Sprintf("مرحبًا %s،\n\nطلب فريق دعم المنصة إلغاء التحقق بخطوتين لحسابك، بعد أن أخبرنا أحد أنه فقد موبايله. "+
				"ستدخل بعدها بكلمة المرور وحدها.\n\nيُنفَّذ الطلب %s.\n\nإن لم تطلب هذا، ألغِه الآن. حسابك يبقى كما هو:\n%s\n\n"+
				"الرابط يعمل حتى تنفيذ الطلب. إن كان الطلب منك فلا تفعل شيئًا.\n\n"+
				"Platform support asked to remove two-step verification from your account; it takes effect in 24 hours. "+
				"If you did not ask for it, cancel it: %s",
				t.Name, when, link, link)}
	}
	if err = w.D.Mailer.Send(ctx, msg); err != nil {
		return err
	}
	return w.D.Redis.Del(ctx, key).Err()
}

// SupportExecuteWorker carries out a support request when its delay
// ends.
type SupportExecuteWorker struct {
	river.WorkerDefaults[SupportExecuteArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*SupportExecuteWorker) Timeout(*river.Job[SupportExecuteArgs]) time.Duration {
	return 30 * time.Second
}

// Work implements river.Worker.
func (w *SupportExecuteWorker) Work(ctx context.Context, job *river.Job[SupportExecuteArgs]) error {
	now := w.D.Now
	if now == nil {
		now = time.Now
	}
	return supportRunner{db: w.D.DB, jobs: w.D.Jobs, audit: NewRecorder(w.D.IDs), now: now}.execute(ctx, job.Args.RequestID)
}

// SupportNoticeWorker tells a user what platform support did to their
// account.
type SupportNoticeWorker struct {
	river.WorkerDefaults[SupportNoticeArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*SupportNoticeWorker) Timeout(*river.Job[SupportNoticeArgs]) time.Duration {
	return 30 * time.Second
}

// Work implements river.Worker.
func (w *SupportNoticeWorker) Work(ctx context.Context, job *river.Job[SupportNoticeArgs]) error {
	u, err := store.UserByID(ctx, w.D.DB, job.Args.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	st, err := w.D.Stores.GetStore(ctx)
	if err != nil {
		return err
	}
	base := strings.TrimRight(w.D.DashboardURL, "/")
	at := storeMoment(st, job.Args.At)
	var subject, text string
	switch job.Args.Event {
	case NoticeMFAReset:
		subject = "أُلغي التحقق بخطوتين لحسابك على iftahonline"
		text = fmt.Sprintf("مرحبًا %s،\n\nنفّذنا طلب إلغاء التحقق بخطوتين لحسابك (%s)، وخرجنا من كل أجهزتك. ادخل الآن بكلمة المرور وحدها، "+
			"ثم فعّله من جديد من «حسابي»:\n%s/account\n\nإن نسيت كلمة المرور أيضًا: %s/forgot-password\n\n"+
			"Two-step verification was removed from your account and every device was signed out. Sign in with your password and turn it on again: %s/account",
			u.Name, at, base, base, base)
	case NoticeDisabled:
		subject = "أُوقف حسابك على iftahonline"
		text = fmt.Sprintf("مرحبًا %s،\n\nأوقف فريق دعم المنصة حسابك (%s)، وخرجنا من كل أجهزتك. لن تستطيع الدخول حتى يُعاد تفعيله.\n\n"+
			"إن ظننت هذا خطأ، رُدّ على هذه الرسالة وسيتواصل معك الدعم.\n\n"+
			"Platform support disabled your account and signed out every device. Think this is a mistake? Reply to this email.",
			u.Name, at)
	case NoticeEnabled:
		subject = "أُعيد تفعيل حسابك على iftahonline"
		text = fmt.Sprintf("مرحبًا %s،\n\nأعاد فريق دعم المنصة تفعيل حسابك (%s). تستطيع الدخول من جديد:\n%s/login\n\n"+
			"Your account was enabled again. Sign in: %s/login", u.Name, at, base, base)
	case NoticeSignedOut:
		subject = "خرجنا من كل أجهزتك على iftahonline"
		text = fmt.Sprintf("مرحبًا %s،\n\nأنهى فريق دعم المنصة كل جلساتك (%s)، فادخل من جديد. إن لم تطلب هذا، غيّر كلمة المرور:\n%s/forgot-password\n\n"+
			"Platform support signed you out of every device. Did not ask for it? Change your password: %s/forgot-password",
			u.Name, at, base, base)
	default:
		return river.JobCancel(fmt.Errorf("identity: unknown support notice %q", job.Args.Event))
	}
	return w.D.Mailer.Send(ctx, mailer.Message{To: u.Email, Subject: subject, Text: text})
}

// SupportCancelledWorker tells the admin who asked that the merchant
// cancelled the request from the email link.
type SupportCancelledWorker struct {
	river.WorkerDefaults[SupportCancelledArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*SupportCancelledWorker) Timeout(*river.Job[SupportCancelledArgs]) time.Duration {
	return 30 * time.Second
}

// Work implements river.Worker. Without a notifier (no console in this
// deployment) there is nobody to tell.
func (w *SupportCancelledWorker) Work(ctx context.Context, job *river.Job[SupportCancelledArgs]) error {
	if w.D.NotifyAdmin == nil {
		return nil
	}
	req, err := supportRequest(ctx, w.D.DB, job.Args.RequestID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	member, err := store.UserByID(ctx, w.D.DB, req.UserID)
	if err != nil {
		return err
	}
	st, err := w.D.Stores.GetStore(ctx)
	if err != nil {
		return err
	}
	what := "إلغاء التحقق بخطوتين"
	if req.Kind == store.KindOwnershipTransfer {
		what = "نقل الملكية"
	}
	subject := fmt.Sprintf("أُلغي طلب %s في %s من رابط الإيميل", what, st.Name)
	text := fmt.Sprintf("ألغى صاحب الحساب أو صاحب المتجر طلب %s لـ%s في متجر \"%s\" من رابط الإيميل.\n\n"+
		"السبب الذي كُتب عند الطلب: %s\n\nإن كان من تواصل مع الدعم منتحلًا، فقد يحاول من جديد: راجع التذكرة قبل أي طلب آخر.",
		what, member.Name, st.Name, req.Reason)
	return w.D.NotifyAdmin(ctx, req.RequestedBy, subject, text)
}
