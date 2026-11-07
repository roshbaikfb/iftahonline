package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/clientinfo"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/qr"
	"github.com/roshbaikfb/iftahonline/internal/platform/totp"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// An admin's own account (C3, the approved mockup): the name, the
// password (the current one proves it), moving two-step verification to
// a new phone (a code from the old one, then one from the new), and the
// devices signed in. A lost phone is still another owner's «إعادة تعيين».
// Password and phone changes are emailed through the admin_notices outbox:
// the console's role cannot insert River jobs.

// NewPhoneTTL is how long a started phone move waits for its confirmation.
const NewPhoneTTL = 10 * time.Minute

var (
	errWrongPassword = errs.Validation(errs.FieldError{Field: "current_password", Code: "wrong", Message: "the current password is not right"})
	errWrongCode     = errs.Validation(errs.FieldError{Field: "code", Code: "invalid", Message: "the code does not match the authenticator"})
	errNoNewPhone    = errs.Conflict("no_new_phone", "start again: scan the new code first")
	errThisSession   = errs.Validation(errs.FieldError{Field: "session_id", Code: "current", Message: "sign out of this device instead"})
)

func newPhoneKey(session ulid.ID) string { return "admin:new-phone:" + session.String() }

// UpdateName renames the signed-in admin.
func (s *Service) UpdateName(ctx context.Context, raw string) (domain.Admin, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return domain.Admin{}, errNoSession
	}
	name, err := domain.ValidateName(raw)
	if err != nil {
		return domain.Admin{}, err
	}
	a, err := store.SetName(ctx, s.d.DB, who.AdminID, name)
	if err != nil {
		return domain.Admin{}, err
	}
	return a, s.audit(ctx, &who.AdminID, "admin.name_changed", "admin", who.AdminID.String(), nil)
}

// proveSelf checks the signed-in admin's password (failures throttled
// like logins).
func (s *Service) proveSelf(ctx context.Context, who Identity, password string) (domain.Admin, error) {
	key := "me:password:" + who.AdminID.String()
	if err := s.allow(ctx, key, s.d.Limits.LoginFailPerKey, false); err != nil {
		return domain.Admin{}, err
	}
	a, err := store.AdminByID(ctx, s.d.DB, who.AdminID)
	if err != nil {
		return domain.Admin{}, err
	}
	match, _, err := s.d.Hasher.Verify(ctx, password, a.PasswordHash)
	if err != nil {
		return domain.Admin{}, err
	}
	if !match {
		if err = s.allow(ctx, key, s.d.Limits.LoginFailPerKey, true); err != nil {
			return domain.Admin{}, err
		}
		return domain.Admin{}, errWrongPassword
	}
	return a, nil
}

// ChangePassword sets a new password for the signed-in admin, who proves
// the current one; every other device is signed out and an email tells
// them.
func (s *Service) ChangePassword(ctx context.Context, current, next string) error {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	if _, err := s.proveSelf(ctx, who, current); err != nil {
		return err
	}
	if err := domain.ValidatePassword(next); err != nil {
		return errs.Validation(errs.FieldError{Field: "new_password", Code: "weak", Message: fmt.Sprintf("use %d to 128 characters", domain.MinPasswordLen)})
	}
	hash, err := s.d.Hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "admin.password_changed", "admin", who.AdminID.String(), nil); err != nil {
		return err
	}
	return store.InTx(ctx, s.d.DB, func(db store.DB) error {
		if txErr := store.SetPassword(ctx, db, who.AdminID, hash); txErr != nil {
			return txErr
		}
		if _, txErr := store.RevokeOtherSessions(ctx, db, who.AdminID, who.SessionID); txErr != nil {
			return txErr
		}
		return store.InsertNotice(ctx, db, s.d.IDs.New(), who.AdminID, store.NoticePasswordChanged, clientinfo.IPPrefix(metaFrom(ctx).IP))
	})
}

// NewPhone is a new phone's enrolment, shown once.
type NewPhone struct {
	Secret, URI, QR string // base32, otpauth://, an SVG QR code
}

// StartNewPhone begins moving two-step verification to a new phone: a
// current code from the old phone, then a new seed waits sealed in Redis
// (NewPhoneTTL) for ConfirmNewPhone.
func (s *Service) StartNewPhone(ctx context.Context, code string) (NewPhone, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return NewPhone{}, errNoSession
	}
	if s.d.Redis == nil {
		return NewPhone{}, errNoRedis
	}
	key := "me:code:" + who.AdminID.String()
	if err := s.allow(ctx, key, s.d.Limits.LoginFailPerKey, false); err != nil {
		return NewPhone{}, err
	}
	a, err := store.AdminByID(ctx, s.d.DB, who.AdminID)
	if err != nil {
		return NewPhone{}, err
	}
	if !s.totpOK(ctx, a, strings.TrimSpace(code)) {
		if err = s.allow(ctx, key, s.d.Limits.LoginFailPerKey, true); err != nil {
			return NewPhone{}, err
		}
		return NewPhone{}, errWrongCode
	}
	seed, err := totp.NewSecret()
	if err != nil {
		return NewPhone{}, err
	}
	sealed, err := s.d.Keyring.Seal(seed, totpAAD(a.ID))
	if err != nil {
		return NewPhone{}, err
	}
	if err = s.d.Redis.Set(ctx, newPhoneKey(who.SessionID), sealed, NewPhoneTTL).Err(); err != nil {
		return NewPhone{}, errs.Unavailable(5*time.Second, fmt.Errorf("admin: keep new phone: %w", err))
	}
	uri := totp.URI(seed, TOTPIssuer, a.Email)
	svg, err := qr.SVG(uri)
	if err != nil {
		return NewPhone{}, err
	}
	return NewPhone{Secret: totp.EncodeSecret(seed), URI: uri, QR: svg}, nil
}

// ConfirmNewPhone finishes the move with a code from the new phone: the
// old one stops at once, and an email tells the admin.
func (s *Service) ConfirmNewPhone(ctx context.Context, code string) error {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	if s.d.Redis == nil {
		return errNoRedis
	}
	sealed, err := s.d.Redis.Get(ctx, newPhoneKey(who.SessionID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return errNoNewPhone
	}
	if err != nil {
		return errs.Unavailable(5*time.Second, fmt.Errorf("admin: read new phone: %w", err))
	}
	seed, err := s.d.Keyring.Open(sealed, totpAAD(who.AdminID))
	if err != nil {
		return fmt.Errorf("admin: open new phone: %w", err)
	}
	step, ok := totp.Verify(seed, strings.TrimSpace(code), s.now(), 0)
	if !ok {
		return errWrongCode
	}
	if err = s.audit(ctx, &who.AdminID, "admin.totp_changed", "admin", who.AdminID.String(), nil); err != nil {
		return err
	}
	if err = store.InTx(ctx, s.d.DB, func(db store.DB) error {
		if txErr := store.SetTOTP(ctx, db, who.AdminID, sealed, step); txErr != nil {
			return txErr
		}
		return store.InsertNotice(ctx, db, s.d.IDs.New(), who.AdminID, store.NoticeTOTPChanged, clientinfo.IPPrefix(metaFrom(ctx).IP))
	}); err != nil {
		return err
	}
	if err = s.d.Redis.Del(ctx, newPhoneKey(who.SessionID)).Err(); err != nil {
		s.d.Log.WarnContext(ctx, "admin: drop new phone", "err", err)
	}
	return nil
}

// OwnSession is a device the signed-in admin is on; Current is this one.
type OwnSession struct {
	store.OwnSession
	Current bool
}

// Sessions lists the signed-in admin's devices.
func (s *Service) Sessions(ctx context.Context) ([]OwnSession, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return nil, errNoSession
	}
	rows, err := store.ListSessions(ctx, s.d.DB, who.AdminID)
	if err != nil {
		return nil, err
	}
	out := make([]OwnSession, len(rows))
	for i, r := range rows {
		out[i] = OwnSession{OwnSession: r, Current: r.ID == who.SessionID}
	}
	return out, nil
}

// RevokeSession signs the admin out of another of their devices.
func (s *Service) RevokeSession(ctx context.Context, id ulid.ID) error {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	if id == who.SessionID {
		return errThisSession
	}
	done, err := store.RevokeOwnSession(ctx, s.d.DB, who.AdminID, id)
	if err != nil {
		return err
	}
	if !done {
		return errs.NotFound("session_not_found", "no such session")
	}
	return s.audit(ctx, &who.AdminID, "admin.session_revoked", "session", id.String(), nil)
}

// RevokeOtherSessions signs the admin out of every other device.
func (s *Service) RevokeOtherSessions(ctx context.Context) error {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	n, err := store.RevokeOtherSessions(ctx, s.d.DB, who.AdminID, who.SessionID)
	if err != nil {
		return err
	}
	return s.audit(ctx, &who.AdminID, "admin.sessions_revoked", "admin", who.AdminID.String(), map[string]any{"count": n})
}

// NoticesArgs mails the waiting admin notices.
type NoticesArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (NoticesArgs) Kind() string { return "admin.notices" }

// NoticesWorker runs NoticesArgs.
type NoticesWorker struct {
	river.WorkerDefaults[NoticesArgs]
	Svc *Service
}

// Work implements river.Worker.
func (w *NoticesWorker) Work(ctx context.Context, _ *river.Job[NoticesArgs]) error {
	return w.Svc.DeliverNotices(ctx)
}

// Timeout bounds one delivery round.
func (*NoticesWorker) Timeout(*river.Job[NoticesArgs]) time.Duration { return time.Minute }

// NoticesPeriodic mails the outbox every minute.
func NoticesPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return NoticesArgs{}, nil }, nil)
}

// DeliverNotices mails the waiting notices (the worker). Without a mailer
// they wait.
func (s *Service) DeliverNotices(ctx context.Context) error {
	if s.d.Mailer == nil {
		return nil
	}
	return store.InTx(ctx, s.d.DB, func(db store.DB) error {
		ns, err := store.PendingNotices(ctx, db)
		if err != nil {
			return err
		}
		for _, n := range ns {
			if err = s.d.Mailer.Send(ctx, noticeMessage(n, s.d.ConsoleURL)); err != nil {
				return err
			}
			if err = store.MarkNoticeSent(ctx, db, n.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func noticeMessage(n store.Notice, console string) mailer.Message {
	when := n.CreatedAt.UTC().Format("2006-01-02 15:04")
	what, whatEn := "تغيّرت كلمة مرور حسابك في الكونسول", "Your console password changed"
	if n.Kind == store.NoticeTOTPChanged {
		what, whatEn = "انتقل التحقق بخطوتين لحسابك في الكونسول إلى موبايل جديد", "Your console two-step verification moved to a new phone"
	}
	return mailer.Message{To: n.Email, Subject: what,
		Text: fmt.Sprintf("مرحبًا %s،\n\n%s في %s UTC (الشبكة %s).\n\nإن لم تكن أنت، اطلب فورًا من مالك آخر «إعادة تعيين» حسابك من صفحة الفريق:\n%s/team\n\n"+
			"%s at %s UTC. Not you? Ask another owner to reset your account right away.",
			n.Name, what, when, n.IPPrefix, strings.TrimRight(console, "/"), whatEn, when)}
}
