package app_test

import (
	"context"
	"encoding/base32"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/notify"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// capture records what the senders would deliver.
type capture struct {
	mu   sync.Mutex
	sms  map[string]string // E.164 -> last text
	mail []notify.Message
}

func (c *capture) SendOTP(_ context.Context, to phone.E164, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sms == nil {
		c.sms = map[string]string{}
	}
	c.sms[to.Unmasked()] = text
	return nil
}

func (c *capture) Send(_ context.Context, m notify.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mail = append(c.mail, m)
	return nil
}

func (f fixture) workerDeps(c *capture) app.WorkerDeps {
	return app.WorkerDeps{DB: f.app, Redis: f.rdb, Keyring: f.keyring, Mailer: c, OTP: c, Stores: f.ten.Service(),
		DashboardURL: "https://app.iftahonline.com", Log: f.log}
}

var sixDigits = regexp.MustCompile(`\b(\d{6})\b`)

// deliverOTP runs the send job for a challenge and returns the code.
func (f fixture) deliverOTP(t *testing.T, ch app.Challenge, num string) string {
	t.Helper()
	c := &capture{}
	w := &app.SendOTPWorker{D: f.workerDeps(c)}
	if err := w.Work(t.Context(), &river.Job[app.SendOTPArgs]{Args: app.SendOTPArgs{ChallengeID: ch.ID}}); err != nil {
		t.Fatal(err)
	}
	text := c.sms[num]
	m := sixDigits.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no code delivered to %s: %q", num, text)
	}
	return m[1]
}

func (f fixture) phoneNumber() (string, string) {
	b := f.ids.New()
	national := "010" + strings.Repeat("0", 0) + digits(b[10:], 8)
	return national, "+20" + national[1:]
}

func digits(b []byte, n int) string {
	var s strings.Builder
	for i := range n {
		s.WriteByte('0' + b[i%len(b)]%10)
	}
	return s.String()
}

func as(ctx context.Context, res app.AuthResult) context.Context {
	return app.WithIdentity(ctx, app.Identity{UserID: res.User.ID, SessionID: res.Session.ID})
}

func TestPhoneVerificationAndOTPLogin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, _ := f.signup(t)
	national, e164 := f.phoneNumber()
	me := as(f.device(t), acc)

	ch, err := f.svc.StartPhoneVerification(me, national, "EG")
	if err != nil {
		t.Fatal(err)
	}
	code := f.deliverOTP(t, ch, e164)
	// Wrong code, then a code for another purpose, are refused.
	if err = f.svc.VerifyPhone(me, ch.ID, "000000"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("wrong code: %v", err)
	}
	other, _ := f.signup(t)
	if err = f.svc.VerifyPhone(as(t.Context(), other), ch.ID, code); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("someone else's challenge: %v", err)
	}
	if err = f.svc.VerifyPhone(me, ch.ID, code); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.VerifyPhone(me, ch.ID, code); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("reuse: %v", err)
	}
	u, _, _ := f.svc.Me(me)
	if u.Phone != e164 {
		t.Fatalf("phone = %q", u.Phone)
	}

	// The number now signs in by OTP.
	login, err := f.svc.StartOTPLogin(f.device(t), e164, "")
	if err != nil {
		t.Fatal(err)
	}
	code = f.deliverOTP(t, login, e164)
	res, err := f.svc.VerifyOTPLogin(f.device(t), login.ID, code)
	if err != nil || res.User.ID != acc.User.ID || res.Token == "" || res.MFA != nil {
		t.Fatalf("otp login = %+v, %v", res, err)
	}

	// Another account cannot claim the same number.
	och, err := f.svc.StartPhoneVerification(as(f.device(t), other), e164, "")
	if err != nil {
		t.Fatal(err)
	}
	ocode := f.deliverOTP(t, och, e164)
	if err := f.svc.VerifyPhone(as(t.Context(), other), och.ID, ocode); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("taken number: %v", err)
	}
}

func TestOTPLoginGuards(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Unknown numbers get an indistinguishable challenge and nothing is sent.
	_, unknown := f.phoneNumber()
	ch, err := f.svc.StartOTPLogin(f.device(t), unknown, "")
	if err != nil || len(ch.ID) != 26 {
		t.Fatalf("unknown number: %+v, %v", ch, err)
	}
	if n, _ := f.rdb.Exists(t.Context(), "identity:delivery:"+ch.ID).Result(); n != 0 {
		t.Fatal("a code must not be queued for an unknown number")
	}
	if _, err = f.svc.VerifyOTPLogin(t.Context(), ch.ID, "123456"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("decoy: %v", err)
	}

	// Resend budget per number (3 in tests): the fourth request is refused,
	// for unknown numbers too, so limits do not reveal accounts.
	for i := range 2 {
		if _, err = f.svc.StartOTPLogin(f.device(t), unknown, ""); err != nil {
			t.Fatalf("code %d: %v", i+2, err)
		}
	}
	if _, err = f.svc.StartOTPLogin(f.device(t), unknown, ""); !errs.Is(err, errs.KindRateLimited) {
		t.Fatalf("fourth code: %v", err)
	}

	// Bad input.
	for _, bad := range []string{"", "123", "+12025550123", "not a phone"} {
		if _, err = f.svc.StartOTPLogin(f.device(t), bad, ""); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if _, err = f.svc.VerifyOTPLogin(t.Context(), "not-a-ulid", "123456"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatal(err)
	}

	// Five wrong guesses burn the challenge, even for the right code after.
	acc, _ := f.signup(t)
	national, e164 := f.phoneNumber()
	me := as(f.device(t), acc)
	vch, _ := f.svc.StartPhoneVerification(me, national, "")
	vcode := f.deliverOTP(t, vch, e164)
	if err = f.svc.VerifyPhone(me, vch.ID, vcode); err != nil {
		t.Fatal(err)
	}
	lch, err := f.svc.StartOTPLogin(f.device(t), e164, "")
	if err != nil {
		t.Fatal(err)
	}
	code := f.deliverOTP(t, lch, e164)
	for range 5 {
		_, _ = f.svc.VerifyOTPLogin(t.Context(), lch.ID, "999999")
	}
	if _, err := f.svc.VerifyOTPLogin(t.Context(), lch.ID, code); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("after 5 wrong guesses: %v", err)
	}
}

func TestOTPDisabled(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := app.NewService(app.Deps{DB: f.app, Limiter: nil, Stores: f.ten.Service(), IDs: f.ids, Log: f.log,
		Redis: f.rdb, Keyring: f.keyring, Pepper: []byte("p"), Jobs: f.ins, OTPEnabled: false})
	if _, err := svc.StartOTPLogin(t.Context(), "+201012345678", ""); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("otp disabled: %v", err)
	}
	ctx := app.WithIdentity(t.Context(), app.Identity{UserID: f.ids.New()})
	if _, err := svc.StartPhoneVerification(ctx, "+201012345678", ""); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("verification disabled: %v", err)
	}
}

func totpNow(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return domain.HOTP(raw, domain.TOTPStep(at), 6)
}

func TestTOTPLifecycle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, email := f.signup(t)
	me := as(t.Context(), acc)

	if _, err := f.svc.ConfirmTOTP(me, "123456"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("confirm before setup: %v", err)
	}
	setup, err := f.svc.SetupTOTP(me)
	if err != nil || !strings.HasPrefix(setup.URI, "otpauth://totp/iftahonline:") {
		t.Fatalf("setup = %+v, %v", setup, err)
	}
	if _, err = f.svc.ConfirmTOTP(me, "000000"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("wrong confirm: %v", err)
	}
	now := time.Now()
	codes, err := f.svc.ConfirmTOTP(me, totpNow(t, setup.Secret, now))
	if err != nil || len(codes) != 10 {
		t.Fatalf("confirm = %v, %v", codes, err)
	}
	if _, err = f.svc.SetupTOTP(me); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("setup while enabled: %v", err)
	}
	var sealed []byte
	if err = f.owner.QueryRow(t.Context(), "SELECT totp_secret FROM users WHERE id = $1", acc.User.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), setup.Secret) || len(sealed) < 40 {
		t.Fatal("seed must be stored sealed")
	}

	// Password now yields an MFA challenge, not a session.
	res, err := f.svc.Login(f.device(t), email, goodPassword)
	if err != nil || res.MFA == nil || res.Token != "" {
		t.Fatalf("login with 2FA = %+v, %v", res, err)
	}
	// The code that confirmed enrolment cannot be replayed; the next step's can.
	if _, err = f.svc.VerifyMFA(t.Context(), res.MFA.ID, totpNow(t, setup.Secret, now)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("replayed code: %v", err)
	}
	next := totpNow(t, setup.Secret, now.Add(domain.TOTPPeriod))
	done, err := f.svc.VerifyMFA(f.device(t), res.MFA.ID, next)
	if err != nil || done.Token == "" {
		t.Fatalf("mfa = %+v, %v", done, err)
	}
	if _, err := f.svc.VerifyMFA(t.Context(), res.MFA.ID, next); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("challenge reuse: %v", err)
	}

	// A recovery code works once.
	res2, _ := f.svc.Login(f.device(t), email, goodPassword)
	if _, err := f.svc.VerifyMFA(f.device(t), res2.MFA.ID, strings.ToUpper(codes[0])); err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	res3, _ := f.svc.Login(f.device(t), email, goodPassword)
	if _, err := f.svc.VerifyMFA(t.Context(), res3.MFA.ID, codes[0]); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("recovery code reuse: %v", err)
	}

	// Disabling needs the password and a valid second factor.
	if err := f.svc.DisableTOTP(me, "wrong password!", codes[1]); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("disable with wrong password: %v", err)
	}
	if err := f.svc.DisableTOTP(me, goodPassword, "000000"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("disable with wrong code: %v", err)
	}
	if err := f.svc.DisableTOTP(me, goodPassword, codes[1]); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DisableTOTP(me, goodPassword, codes[2]); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("disable twice: %v", err)
	}
	if res, err := f.svc.Login(f.device(t), email, goodPassword); err != nil || res.MFA != nil {
		t.Fatalf("login after disable = %+v, %v", res, err)
	}
	// Without an identity, every MFA call is 401.
	if _, err := f.svc.SetupTOTP(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatal(err)
	}
}

func TestNewDeviceAlert(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	acc, email := f.signup(t) // no device ID: not tracked
	withDevice := func(id string) context.Context {
		return app.WithRequestMeta(t.Context(), app.RequestMeta{UserAgent: "Browser/1", IP: netip.MustParseAddr("198.51.100.7"), DeviceID: id})
	}
	alerts := func() int {
		var n int
		if err := f.owner.QueryRow(t.Context(), "SELECT count(*) FROM river_job WHERE kind = 'identity.device.alert' AND args->>'user_id' = $1",
			acc.User.ID.String()).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	devA, devB := "device-a-"+f.ids.New().String(), "device-b-"+f.ids.New().String()
	for range 2 { // first device ever, then the same device: no alert
		if _, err := f.svc.Login(withDevice(devA), email, goodPassword); err != nil {
			t.Fatal(err)
		}
	}
	if alerts() != 0 {
		t.Fatal("first and known devices must not alert")
	}
	if _, err := f.svc.Login(withDevice(devB), email, goodPassword); err != nil {
		t.Fatal(err)
	}
	if alerts() != 1 {
		t.Fatalf("new device alerts = %d, want 1", alerts())
	}

	c := &capture{}
	w := &app.DeviceAlertWorker{D: f.workerDeps(c)}
	err := w.Work(t.Context(), &river.Job[app.NewDeviceAlertArgs]{Args: app.NewDeviceAlertArgs{
		UserID: acc.User.ID, UserAgent: "Browser/1", IPPrefix: "198.51.100.0/24", At: time.Now()}})
	if err != nil || len(c.mail) != 1 || c.mail[0].To != email || !strings.Contains(c.mail[0].Text, "198.51.100.0/24") ||
		strings.Contains(c.mail[0].Text, "198.51.100.7") {
		t.Fatalf("alert mail = %+v, %v", c.mail, err)
	}
	// A deleted user is skipped quietly.
	if err := w.Work(t.Context(), &river.Job[app.NewDeviceAlertArgs]{Args: app.NewDeviceAlertArgs{UserID: f.ids.New()}}); err != nil {
		t.Fatal(err)
	}
}

func ownerCtx(ctx context.Context, res app.AuthResult) context.Context {
	ctx = tenant.WithStore(ctx, res.StoreID)
	ctx = authz.WithPrincipal(ctx, authz.Principal{UserID: res.User.ID, SessionID: res.Session.ID, StoreID: res.StoreID, Role: authz.RoleOwner})
	return app.WithIdentity(ctx, app.Identity{UserID: res.User.ID, SessionID: res.Session.ID})
}

// inviteToken runs the email job and extracts the token from the link.
func (f fixture) inviteToken(t *testing.T, store tenant.StoreID, id ulid.ID) string {
	t.Helper()
	c := &capture{}
	w := &app.InviteEmailWorker{D: f.workerDeps(c)}
	ctx := tenant.WithStore(t.Context(), store)
	if err := w.Work(ctx, &river.Job[app.InviteEmailArgs]{Args: app.InviteEmailArgs{InvitationID: id}}); err != nil {
		t.Fatal(err)
	}
	if len(c.mail) != 1 {
		t.Fatalf("invitation mails = %d", len(c.mail))
	}
	_, after, ok := strings.Cut(c.mail[0].Text, "/invite#")
	if !ok {
		t.Fatalf("no link in %q", c.mail[0].Text)
	}
	return strings.Fields(after)[0]
}

func TestInvitations(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, _ := f.signup(t)
	octx := ownerCtx(f.device(t), owner)

	newEmail := f.unique("new") + "@example.com"
	inv, err := f.svc.Invite(octx, strings.ToUpper(newEmail), "support")
	if err != nil || inv.Email != newEmail || inv.Role != authz.RoleSupport {
		t.Fatalf("invite = %+v, %v", inv, err)
	}
	if _, err = f.svc.Invite(octx, newEmail, "manager"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("duplicate open invitation: %v", err)
	}
	if _, err = f.svc.Invite(octx, "x"+newEmail, "owner"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("owner role: %v", err)
	}
	if list, listErr := f.svc.Invitations(octx); listErr != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, listErr)
	}

	token := f.inviteToken(t, owner.StoreID, inv.ID)
	if !strings.HasPrefix(token, owner.StoreID.String()+".") {
		t.Fatalf("token = %q", token)
	}

	// New account: name and password are validated, then the account,
	// membership and session are created together.
	if _, err = f.svc.AcceptInvitation(f.device(t), app.AcceptInput{Token: token, Name: "", Password: "short"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("weak new account: %v", err)
	}
	res, err := f.svc.AcceptInvitation(f.device(t), app.AcceptInput{Token: token, Name: "Sara", Password: goodPassword})
	if err != nil || res.Role != authz.RoleSupport || res.Auth == nil || res.Auth.Token == "" || res.User.Email != newEmail {
		t.Fatalf("accept = %+v, %v", res, err)
	}
	p, err := f.svc.AuthenticateInStore(tenant.WithStore(t.Context(), owner.StoreID), string(res.Auth.Token))
	if err != nil || p.Role != authz.RoleSupport {
		t.Fatalf("new member principal = %+v, %v", p, err)
	}
	if _, err = f.svc.AcceptInvitation(t.Context(), app.AcceptInput{Token: token, Name: "x", Password: goodPassword}); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("token reuse: %v", err)
	}
	if _, err = f.svc.Invite(octx, newEmail, "manager"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("invite an existing member: %v", err)
	}

	// Existing account: must be signed in as the invited email.
	other, otherEmail := f.signup(t)
	inv2, err := f.svc.Invite(octx, otherEmail, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	tok2 := f.inviteToken(t, owner.StoreID, inv2.ID)
	if _, err = f.svc.AcceptInvitation(t.Context(), app.AcceptInput{Token: tok2}); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("existing account, anonymous: %v", err)
	}
	stranger, _ := f.signup(t)
	if _, err = f.svc.AcceptInvitation(as(t.Context(), stranger), app.AcceptInput{Token: tok2}); !errs.Is(err, errs.KindForbidden) {
		t.Fatalf("wrong account: %v", err)
	}
	joined, err := f.svc.AcceptInvitation(as(t.Context(), other), app.AcceptInput{Token: tok2})
	if err != nil || joined.Auth != nil || joined.Role != authz.RoleShipping {
		t.Fatalf("existing account accept = %+v, %v", joined, err)
	}

	// Revoked and tampered tokens are not found.
	inv3, _ := f.svc.Invite(octx, f.unique("r")+"@example.com", "manager")
	tok3 := f.inviteToken(t, owner.StoreID, inv3.ID)
	if err := f.svc.RevokeInvitation(octx, inv3.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeInvitation(octx, inv3.ID); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
	for name, tok := range map[string]string{
		"revoked":      tok3,
		"other store":  other.StoreID.String() + tok3[26:],
		"garbage":      "nope",
		"truncated":    tok3[:40],
		"bad store id": "x" + tok3[1:],
	} {
		if _, err := f.svc.AcceptInvitation(t.Context(), app.AcceptInput{Token: tok, Name: "x", Password: goodPassword}); !errs.Is(err, errs.KindNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// The email job skips revoked invitations without sending.
	inv4, _ := f.svc.Invite(octx, f.unique("s")+"@example.com", "manager")
	_ = f.svc.RevokeInvitation(octx, inv4.ID)
	c := &capture{}
	w := &app.InviteEmailWorker{D: f.workerDeps(c)}
	if err := w.Work(tenant.WithStore(t.Context(), owner.StoreID), &river.Job[app.InviteEmailArgs]{Args: app.InviteEmailArgs{InvitationID: inv4.ID}}); err != nil || len(c.mail) != 0 {
		t.Fatalf("revoked invitation mailed: %v %+v", err, c.mail)
	}
}

func TestMembers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, _ := f.signup(t)
	octx := ownerCtx(f.device(t), owner)
	member, memberEmail := f.signup(t)
	inv, _ := f.svc.Invite(octx, memberEmail, "support")
	tok := f.inviteToken(t, owner.StoreID, inv.ID)
	if _, err := f.svc.AcceptInvitation(as(t.Context(), member), app.AcceptInput{Token: tok}); err != nil {
		t.Fatal(err)
	}

	f.qc.Reset()
	ms, err := f.svc.Members(octx)
	if err != nil || len(ms) != 2 || ms[0].Role != authz.RoleOwner || ms[1].Email != memberEmail {
		t.Fatalf("members = %+v, %v", ms, err)
	}
	if f.qc.Count() != 4 { // BEGIN, set tenant, one joined query, COMMIT: no N+1
		t.Fatalf("statements = %d, want 4", f.qc.Count())
	}

	if err = f.svc.SetMemberRole(octx, member.User.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.SetMemberRole(octx, owner.User.ID, "manager"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("demote owner: %v", err)
	}
	if err = f.svc.SetMemberRole(octx, member.User.ID, "owner"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("promote to owner: %v", err)
	}
	if err = f.svc.SetMemberRole(octx, f.ids.New(), "manager"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown member: %v", err)
	}
	if err = f.svc.RemoveMember(octx, owner.User.ID); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("remove owner: %v", err)
	}
	if err = f.svc.RemoveMember(octx, member.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RemoveMember(octx, member.User.ID); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
	// Removal is immediate: the member's session no longer opens the store.
	if _, err = f.svc.AuthenticateInStore(tenant.WithStore(t.Context(), owner.StoreID), string(member.Token)); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("removed member: %v", err)
	}

	page, err := f.svc.ListAudit(octx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range page.Items {
		actions = append(actions, e.Action)
	}
	if got := strings.Join(actions, ","); got != "member.removed,member.role_changed,member.joined,member.invited,store.created" {
		t.Fatalf("audit = %s", got)
	}
}
