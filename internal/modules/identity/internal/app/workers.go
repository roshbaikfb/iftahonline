package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/notify"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// WorkerDeps are what the identity job workers need.
type WorkerDeps struct {
	DB           DB
	Redis        *redis.Client
	Keyring      *secretbox.Keyring
	Mailer       mailer.Mailer
	OTP          notify.OTPSender
	Stores       tenancy.Service
	DashboardURL string
	Log          *slog.Logger
	// IDs and Jobs let support requests be carried out (C1); NotifyAdmin
	// tells the admin who asked when a merchant cancels one (nil: no
	// console here, nobody to tell); Now is the clock (tests).
	IDs         *ulid.Generator
	Jobs        jobs.Inserter
	NotifyAdmin AdminNotifier
	Now         func() time.Time
}

// readDelivery opens a sealed delivery; found is false when it expired
// (the user must ask again) — not an error worth retrying.
func readDelivery(ctx context.Context, d WorkerDeps, key, aad string) (delivery, bool, error) {
	sealed, err := d.Redis.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return delivery{}, false, nil
	}
	if err != nil {
		return delivery{}, false, fmt.Errorf("identity: read delivery: %w", err)
	}
	raw, err := d.Keyring.Open(sealed, []byte(aad))
	if err != nil {
		return delivery{}, false, river.JobCancel(err)
	}
	var out delivery
	if err := json.Unmarshal(raw, &out); err != nil {
		return delivery{}, false, river.JobCancel(err)
	}
	return out, true, nil
}

// SendOTPWorker delivers phone codes.
type SendOTPWorker struct {
	river.WorkerDefaults[SendOTPArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*SendOTPWorker) Timeout(*river.Job[SendOTPArgs]) time.Duration { return 20 * time.Second }

// Work implements river.Worker. A code is useless after its TTL, so a
// missing delivery ends the job, and retries stop with it.
func (w *SendOTPWorker) Work(ctx context.Context, job *river.Job[SendOTPArgs]) error {
	key := deliveryKey(job.Args.ChallengeID)
	d, found, err := readDelivery(ctx, w.D, key, "delivery:"+job.Args.ChallengeID)
	if err != nil || !found {
		return err
	}
	to, err := phone.ParseE164(d.Phone)
	if err != nil {
		return river.JobCancel(err)
	}
	if err := w.D.OTP.SendOTP(ctx, to, d.Text); err != nil {
		return err
	}
	return w.D.Redis.Del(ctx, key).Err()
}

// InviteEmailWorker mails invitation links.
type InviteEmailWorker struct {
	river.WorkerDefaults[InviteEmailArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*InviteEmailWorker) Timeout(*river.Job[InviteEmailArgs]) time.Duration { return 30 * time.Second }

// Work implements river.Worker. It runs in the invitation's store (the
// jobs middleware restored it from the args).
func (w *InviteEmailWorker) Work(ctx context.Context, job *river.Job[InviteEmailArgs]) error {
	key := inviteDeliveryKey(job.Args.InvitationID)
	d, found, err := readDelivery(ctx, w.D, key, "invite:"+job.Args.InvitationID.String())
	if err != nil || !found {
		return err
	}
	var inv store.Invitation
	err = postgres.InTenantTx(ctx, w.D.DB, func(tx pgx.Tx) error {
		var txErr error
		inv, txErr = store.GetInvitation(ctx, tx, job.Args.InvitationID)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if inv.AcceptedAt != nil || inv.RevokedAt != nil {
		return w.D.Redis.Del(ctx, key).Err()
	}
	st, err := w.D.Stores.GetStore(ctx)
	if err != nil {
		return err
	}
	// The token is in the URL fragment: browsers never send it to a
	// server, so it stays out of access logs and Referer headers.
	link := strings.TrimRight(w.D.DashboardURL, "/") + "/invite#" + d.Text
	msg := mailer.Message{
		To:      inv.Email,
		Subject: fmt.Sprintf("دعوة للانضمام إلى %s على iftahonline", st.Name),
		Text: fmt.Sprintf("تمت دعوتك للانضمام إلى فريق متجر \"%s\" بدور: %s.\n\nلقبول الدعوة افتح الرابط التالي (صالح حتى %s):\n%s\n\n"+
			"You have been invited to join \"%s\" on iftahonline as %s.\nAccept: %s\n\nإذا لم تكن تتوقع هذه الدعوة فتجاهل هذه الرسالة.",
			st.Name, roleArabic(inv.Role), inv.ExpiresAt.UTC().Format("2006-01-02"), link, st.Name, inv.Role, link),
	}
	if err := w.D.Mailer.Send(ctx, msg); err != nil {
		return err
	}
	return w.D.Redis.Del(ctx, key).Err()
}

// DeviceAlertWorker emails new-device sign-in alerts.
type DeviceAlertWorker struct {
	river.WorkerDefaults[NewDeviceAlertArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*DeviceAlertWorker) Timeout(*river.Job[NewDeviceAlertArgs]) time.Duration {
	return 30 * time.Second
}

// Work implements river.Worker.
func (w *DeviceAlertWorker) Work(ctx context.Context, job *river.Job[NewDeviceAlertArgs]) error {
	u, err := store.UserByID(ctx, w.D.DB, job.Args.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	a := job.Args
	device := a.UserAgent
	if device == "" {
		device = "unknown device"
	}
	security := strings.TrimRight(w.D.DashboardURL, "/") + "/account"
	return w.D.Mailer.Send(ctx, mailer.Message{
		To:      u.Email,
		Subject: "تسجيل دخول جديد إلى حسابك على iftahonline",
		Text: fmt.Sprintf("تم تسجيل الدخول إلى حسابك من جهاز جديد.\n\nالوقت: %s UTC\nالجهاز: %s\nالشبكة: %s\n\n"+
			"إذا لم تكن أنت، أنهِ الجلسة وغيّر كلمة المرور فورًا: %s\n\n"+
			"New sign-in to your account from a new device at %s UTC (%s, network %s). Not you? %s",
			a.At.Format("2006-01-02 15:04"), device, a.IPPrefix, security, a.At.Format("2006-01-02 15:04"), device, a.IPPrefix, security),
	})
}

// LinkEmailWorker sends a one-time link (D3): a password reset or an
// email confirmation. The link is in the URL fragment, so it never
// reaches a server log; a link used, replaced or expired meanwhile is not
// sent.
type LinkEmailWorker struct {
	river.WorkerDefaults[LinkEmailArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*LinkEmailWorker) Timeout(*river.Job[LinkEmailArgs]) time.Duration { return 30 * time.Second }

// Work implements river.Worker.
func (w *LinkEmailWorker) Work(ctx context.Context, job *river.Job[LinkEmailArgs]) error {
	id := job.Args.TokenID
	key := linkDeliveryKey(id)
	d, found, err := readDelivery(ctx, w.D, key, linkAAD(id))
	if err != nil || !found {
		return err
	}
	t, err := store.TokenForSend(ctx, w.D.DB, id)
	if errors.Is(err, domain.ErrNotFound) {
		return w.D.Redis.Del(ctx, key).Err()
	}
	if err != nil {
		return err
	}
	base := strings.TrimRight(w.D.DashboardURL, "/")
	var msg mailer.Message
	switch t.Purpose {
	case store.PurposePasswordReset:
		link := base + "/reset-password#" + d.Text
		msg = mailer.Message{To: t.Email, Subject: "تعيين كلمة مرور جديدة لحسابك على iftahonline",
			Text: fmt.Sprintf("مرحبًا %s،\n\nلتعيين كلمة مرور جديدة افتح الرابط التالي (يعمل ساعة واحدة ومرة واحدة):\n%s\n\n"+
				"إذا لم تطلب ذلك فتجاهل هذه الرسالة؛ كلمة مرورك لم تتغير.\n\n"+
				"Reset your iftahonline password (the link works once, for an hour): %s\nDidn't ask? Ignore this email.",
				t.Name, link, link)}
	case store.PurposeEmailVerify:
		link := base + "/verify-email#" + d.Text
		msg = mailer.Message{To: t.Email, Subject: "أكّد بريدك على iftahonline",
			Text: fmt.Sprintf("مرحبًا %s،\n\nأكّد أن هذا بريدك لتصلك الفواتير وتنبيهات الطلبات، ولتستطيع استعادة حسابك. افتح الرابط التالي (يعمل يومين):\n%s\n\n"+
				"Confirm your email on iftahonline (the link works for two days): %s",
				t.Name, link, link)}
	default:
		return w.D.Redis.Del(ctx, key).Err()
	}
	if err = w.D.Mailer.Send(ctx, msg); err != nil {
		return err
	}
	return w.D.Redis.Del(ctx, key).Err()
}

// PasswordChangedWorker tells a user their password changed, so a change
// they did not make is seen.
type PasswordChangedWorker struct {
	river.WorkerDefaults[PasswordChangedArgs]
	D WorkerDeps
}

// Timeout implements river.Worker.
func (*PasswordChangedWorker) Timeout(*river.Job[PasswordChangedArgs]) time.Duration {
	return 30 * time.Second
}

// Work implements river.Worker.
func (w *PasswordChangedWorker) Work(ctx context.Context, job *river.Job[PasswordChangedArgs]) error {
	u, err := store.UserByID(ctx, w.D.DB, job.Args.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	at := job.Args.At.Format("2006-01-02 15:04")
	forgot := strings.TrimRight(w.D.DashboardURL, "/") + "/forgot-password"
	return w.D.Mailer.Send(ctx, mailer.Message{To: u.Email, Subject: "تغيّرت كلمة مرور حسابك على iftahonline",
		Text: fmt.Sprintf("تغيّرت كلمة مرور حسابك في %s UTC، وخرجنا من الأجهزة الأخرى.\n\n"+
			"إذا لم تكن أنت، عيّن كلمة مرور جديدة فورًا: %s\n\n"+
			"Your iftahonline password changed at %s UTC; other devices were signed out. Not you? %s",
			at, forgot, at, forgot)})
}

// RegisterWorkers adds every identity worker.
func RegisterWorkers(workers *river.Workers, d WorkerDeps) error {
	for _, add := range []func() error{
		func() error { return river.AddWorkerSafely(workers, &SendOTPWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &InviteEmailWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &DeviceAlertWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &LinkEmailWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &PasswordChangedWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &SupportLinkEmailWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &SupportExecuteWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &SupportNoticeWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &SupportCancelledWorker{D: d}) },
	} {
		if err := add(); err != nil {
			return fmt.Errorf("identity: %w", err)
		}
	}
	return nil
}

// roleArabic names an invited role in the email's Arabic part, as the
// dashboard does; the English part keeps the role's key.
func roleArabic(r authz.Role) string {
	switch r {
	case authz.RoleManager:
		return "مدير"
	case authz.RoleSupport:
		return "خدمة العملاء"
	case authz.RoleShipping:
		return "الشحن"
	case authz.RoleOwner:
		return "المالك"
	}
	return string(r)
}
