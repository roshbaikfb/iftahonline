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
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
)

// WorkerDeps are what the identity job workers need.
type WorkerDeps struct {
	DB           DB
	Redis        *redis.Client
	Keyring      *secretbox.Keyring
	Mailer       notify.Mailer
	OTP          notify.OTPSender
	Stores       tenancy.Service
	DashboardURL string
	Log          *slog.Logger
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
	msg := notify.Message{
		To:      inv.Email,
		Subject: fmt.Sprintf("دعوة للانضمام إلى %s على iftahonline", st.Name),
		Text: fmt.Sprintf("تمت دعوتك للانضمام إلى فريق متجر \"%s\" بدور: %s.\n\nلقبول الدعوة افتح الرابط التالي (صالح حتى %s):\n%s\n\n"+
			"You have been invited to join \"%s\" on iftahonline as %s.\nAccept: %s\n\nإذا لم تكن تتوقع هذه الدعوة فتجاهل هذه الرسالة.",
			st.Name, inv.Role, inv.ExpiresAt.UTC().Format("2006-01-02"), link, st.Name, inv.Role, link),
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
	security := strings.TrimRight(w.D.DashboardURL, "/") + "/settings/security"
	return w.D.Mailer.Send(ctx, notify.Message{
		To:      u.Email,
		Subject: "تسجيل دخول جديد إلى حسابك على iftahonline",
		Text: fmt.Sprintf("تم تسجيل الدخول إلى حسابك من جهاز جديد.\n\nالوقت: %s UTC\nالجهاز: %s\nالشبكة: %s\n\n"+
			"إذا لم تكن أنت، أنهِ الجلسة وغيّر كلمة المرور فورًا: %s\n\n"+
			"New sign-in to your account from a new device at %s UTC (%s, network %s). Not you? %s",
			a.At.Format("2006-01-02 15:04"), device, a.IPPrefix, security, a.At.Format("2006-01-02 15:04"), device, a.IPPrefix, security),
	})
}

// RegisterWorkers adds every identity worker.
func RegisterWorkers(workers *river.Workers, d WorkerDeps) error {
	for _, add := range []func() error{
		func() error { return river.AddWorkerSafely(workers, &AuditPartitionsWorker{DB: d.DB}) },
		func() error { return river.AddWorkerSafely(workers, &SendOTPWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &InviteEmailWorker{D: d}) },
		func() error { return river.AddWorkerSafely(workers, &DeviceAlertWorker{D: d}) },
	} {
		if err := add(); err != nil {
			return fmt.Errorf("identity: %w", err)
		}
	}
	return nil
}
