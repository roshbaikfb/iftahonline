package app_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// addMember invites a new account to owner's store as a manager and
// accepts it: the member's session.
func (f fixture) addMember(t *testing.T, owner app.AuthResult) (app.AuthResult, string) {
	t.Helper()
	email := f.unique("member") + "@example.com"
	inv, err := f.svc.Invite(ownerCtx(f.device(t), owner), email, "manager")
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.AcceptInvitation(f.device(t), app.AcceptInput{Token: f.inviteToken(t, owner.StoreID, inv.ID), Name: "Karim", Password: goodPassword})
	if err != nil || res.Auth == nil {
		t.Fatalf("accept = %+v, %v", res, err)
	}
	return *res.Auth, email
}

// enableTOTP turns two-step verification on for acc.
func (f fixture) enableTOTP(t *testing.T, acc app.AuthResult) {
	t.Helper()
	me := as(t.Context(), acc)
	setup, err := f.svc.SetupTOTP(me)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ConfirmTOTP(me, totpNow(t, setup.Secret, time.Now())); err != nil {
		t.Fatal(err)
	}
}

// supportLinks runs the cancel-link emails of request id and returns
// each recipient's link secret (member is the account it is about), by email, with the subjects.
func (f fixture) supportLinks(t *testing.T, store tenant.StoreID, id, member ulid.ID) (map[string]string, []string) {
	t.Helper()
	rows, err := f.owner.Query(t.Context(), `SELECT id, user_id = $2 FROM user_tokens WHERE request_id = $1 ORDER BY created_at`, id, member)
	if err != nil {
		t.Fatal(err)
	}
	type link struct {
		id     ulid.ID
		member bool
	}
	var links []link
	for rows.Next() {
		var l link
		if err = rows.Scan(&l.id, &l.member); err != nil {
			t.Fatal(err)
		}
		links = append(links, l)
	}
	rows.Close()
	c := &capture{}
	w := &app.SupportLinkEmailWorker{D: f.workerDeps(c)}
	ctx := tenant.WithStore(t.Context(), store)
	for _, l := range links {
		if err = w.Work(ctx, &river.Job[app.SupportLinkEmailArgs]{Args: app.SupportLinkEmailArgs{TokenID: l.id, Owner: !l.member}}); err != nil {
			t.Fatal(err)
		}
	}
	out := map[string]string{}
	var subjects []string
	for _, m := range c.mail {
		_, after, ok := strings.Cut(m.Text, "/support-request#")
		if !ok {
			t.Fatalf("no link in %q", m.Text)
		}
		out[m.To] = strings.Fields(after)[0]
		subjects = append(subjects, m.Subject)
	}
	return out, subjects
}

// execute runs a request's job at the given time.
func (f fixture) execute(t *testing.T, store tenant.StoreID, id ulid.ID, at time.Time) error {
	t.Helper()
	d := f.workerDeps(&capture{})
	d.IDs, d.Jobs, d.Now = f.ids, f.ins, func() time.Time { return at }
	w := &app.SupportExecuteWorker{D: d}
	return w.Work(tenant.WithStore(t.Context(), store), &river.Job[app.SupportExecuteArgs]{Args: app.SupportExecuteArgs{RequestID: id}})
}

// auditActions lists the store's activity log actions, newest first.
func (f fixture) auditActions(t *testing.T, owner app.AuthResult) []string {
	t.Helper()
	page, err := f.svc.ListAudit(ownerCtx(t.Context(), owner), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range page.Items {
		out = append(out, e.Action)
	}
	return out
}

func containsAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// TestSupportMFAReset (C1): support asks to remove a member's two-step
// verification; nothing changes for 24 hours; the member and the owner
// are emailed a cancel link; then the job removes it and signs the member
// out everywhere.
func TestSupportMFAReset(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, ownerEmail := f.signup(t)
	member, memberEmail := f.addMember(t, owner)
	f.enableTOTP(t, member)
	stranger, _ := f.signup(t)
	admin := f.ids.New()
	sctx := tenant.WithStore(f.device(t), owner.StoreID)

	team, err := f.svc.SupportTeam(sctx)
	if err != nil || len(team.Members) != 2 || len(team.Requests) != 0 {
		t.Fatalf("team = %+v, %v", team, err)
	}
	for _, m := range team.Members {
		if m.UserID == member.User.ID && (!m.MFAEnabled || m.Disabled || m.Email != memberEmail || m.LastSeenAt.IsZero()) {
			t.Fatalf("member = %+v", m)
		}
	}

	// Refused: another store's account (cross-tenant), no 2FA, no reason.
	if _, err = f.svc.RequestMFAReset(sctx, admin, stranger.User.ID, "ticket 1"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("another store's account: %v", err)
	}
	if _, err = f.svc.RequestMFAReset(sctx, admin, owner.User.ID, "ticket 1"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("no two-step: %v", err)
	}
	if _, err = f.svc.RequestMFAReset(sctx, admin, member.User.ID, ""); err == nil {
		t.Fatal("no reason accepted")
	}

	before := time.Now()
	req, err := f.svc.RequestMFAReset(sctx, admin, member.User.ID, "ticket 4821")
	if err != nil || !req.Open() || req.RequestedBy != admin || req.EffectiveAt.Sub(before) < app.SupportDelay-time.Minute {
		t.Fatalf("request = %+v, %v", req, err)
	}
	if _, err = f.svc.RequestMFAReset(sctx, admin, member.User.ID, "again"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("a second open request: %v", err)
	}
	// Another store sees nothing of it.
	other, err := f.svc.SupportTeam(tenant.WithStore(t.Context(), stranger.StoreID))
	if err != nil || len(other.Requests) != 0 {
		t.Fatalf("another store's requests = %+v, %v", other.Requests, err)
	}

	links, subjects := f.supportLinks(t, owner.StoreID, req.ID, member.User.ID)
	if len(links) != 2 || links[memberEmail] == "" || links[ownerEmail] == "" {
		t.Fatalf("links = %v", links)
	}
	if !strings.Contains(strings.Join(subjects, "|"), "طلب إلغاء التحقق بخطوتين لحسابك") || !strings.Contains(strings.Join(subjects, "|"), "لـKarim") {
		t.Fatalf("subjects = %v", subjects)
	}
	got, err := f.svc.CheckSupportLink(f.device(t), links[memberEmail])
	if err != nil || got.Kind != "mfa_reset" || !got.EffectiveAt.Equal(req.EffectiveAt) || got.Email == memberEmail || !strings.Contains(got.Email, "@example.com") {
		t.Fatalf("check = %+v, %v", got, err)
	}

	// Too early: the job refuses and River retries; nothing changed.
	if err = f.execute(t, owner.StoreID, req.ID, time.Now()); err == nil {
		t.Fatal("ran before its time")
	}
	if _, err = f.svc.AuthenticateInStore(tenant.WithStore(t.Context(), owner.StoreID), string(member.Token)); err != nil {
		t.Fatalf("the member was signed out early: %v", err)
	}

	if err = f.execute(t, owner.StoreID, req.ID, req.EffectiveAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AuthenticateInStore(tenant.WithStore(t.Context(), owner.StoreID), string(member.Token)); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("the member's session survived: %v", err)
	}
	res, err := f.svc.Login(f.device(t), memberEmail, goodPassword)
	if err != nil || res.MFA != nil || res.Token == "" {
		t.Fatalf("login after the reset = %+v, %v", res, err)
	}
	// Done: it runs once, its links close, the team shows it done.
	if err = f.execute(t, owner.StoreID, req.ID, req.EffectiveAt.Add(time.Hour)); err != nil {
		t.Fatalf("a second run: %v", err)
	}
	if _, err = f.svc.CheckSupportLink(f.device(t), links[ownerEmail]); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("a done request's link: %v", err)
	}
	team, _ = f.svc.SupportTeam(sctx)
	if len(team.Requests) != 1 || team.Requests[0].DoneAt == nil {
		t.Fatalf("requests = %+v", team.Requests)
	}
	if acts := f.auditActions(t, owner); !containsAll(acts, "support.mfa_reset_requested", "support.mfa_reset_done") {
		t.Fatalf("activity = %v", acts)
	}
}

// adminMail records NotifyAdmin's emails.
type adminMail struct {
	mu   sync.Mutex
	sent []ulid.ID
	text []string
}

func (a *adminMail) notify(_ context.Context, id ulid.ID, subject, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent, a.text = append(a.sent, id), append(a.text, subject+"\n"+text)
	return nil
}

// TestSupportCancel (C1): the emailed link cancels a request (every link
// closes, the admin who asked is told) and the job then does nothing;
// the console cancels one too.
func TestSupportCancel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, ownerEmail := f.signup(t)
	member, memberEmail := f.addMember(t, owner)
	f.enableTOTP(t, member)
	admin := f.ids.New()
	sctx := tenant.WithStore(f.device(t), owner.StoreID)

	req, err := f.svc.RequestMFAReset(sctx, admin, member.User.ID, "ticket 4821")
	if err != nil {
		t.Fatal(err)
	}
	links, _ := f.supportLinks(t, owner.StoreID, req.ID, member.User.ID)
	if err = f.svc.CancelSupportLink(f.device(t), links[ownerEmail]); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{links[ownerEmail], links[memberEmail], "junk"} {
		if err = f.svc.CancelSupportLink(f.device(t), tok); !errs.Is(err, errs.KindNotFound) {
			t.Fatalf("cancel a closed link: %v", err)
		}
		if _, err = f.svc.CheckSupportLink(f.device(t), tok); !errs.Is(err, errs.KindNotFound) {
			t.Fatalf("check a closed link: %v", err)
		}
	}
	if err = f.execute(t, owner.StoreID, req.ID, req.EffectiveAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AuthenticateInStore(tenant.WithStore(t.Context(), owner.StoreID), string(member.Token)); err != nil {
		t.Fatalf("a cancelled request signed the member out: %v", err)
	}
	team, _ := f.svc.SupportTeam(sctx)
	if len(team.Requests) != 1 || team.Requests[0].CancelledBy != "link" {
		t.Fatalf("requests = %+v", team.Requests)
	}

	// The admin who asked is told.
	notes := &adminMail{}
	d := f.workerDeps(&capture{})
	d.NotifyAdmin = notes.notify
	w := &app.SupportCancelledWorker{D: d}
	if err = w.Work(tenant.WithStore(t.Context(), owner.StoreID),
		&river.Job[app.SupportCancelledArgs]{Args: app.SupportCancelledArgs{RequestID: req.ID}}); err != nil {
		t.Fatal(err)
	}
	if len(notes.sent) != 1 || notes.sent[0] != admin || !strings.Contains(notes.text[0], "ticket 4821") {
		t.Fatalf("admin told = %v %v", notes.sent, notes.text)
	}

	// The console cancels a waiting one; not twice, not another store's.
	req2, err := f.svc.RequestMFAReset(sctx, admin, member.User.ID, "ticket 4900")
	if err != nil {
		t.Fatal(err)
	}
	stranger, _ := f.signup(t)
	if err = f.svc.CancelSupportRequest(tenant.WithStore(t.Context(), stranger.StoreID), admin, req2.ID, "x"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("cancel from another store: %v", err)
	}
	if err = f.svc.CancelSupportRequest(sctx, admin, req2.ID, "duplicate ticket"); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CancelSupportRequest(sctx, admin, req2.ID, "duplicate ticket"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("cancel twice: %v", err)
	}
	if acts := f.auditActions(t, owner); !containsAll(acts, "support.mfa_reset_requested", "support.mfa_reset_cancelled") {
		t.Fatalf("activity = %v", acts)
	}
}

// TestSupportDisableAndSignOut (C1): disabling ends every session and
// refuses sign-in until enabled; signing out ends sessions only; another
// store's account is never touched; each is emailed.
func TestSupportDisableAndSignOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, _ := f.signup(t)
	member, memberEmail := f.addMember(t, owner)
	stranger, _ := f.signup(t)
	admin := f.ids.New()
	sctx := tenant.WithStore(f.device(t), owner.StoreID)
	inStore := func() error {
		_, err := f.svc.AuthenticateInStore(tenant.WithStore(t.Context(), owner.StoreID), string(member.Token))
		return err
	}

	if err := f.svc.SetMemberDisabled(sctx, admin, stranger.User.ID, true, "fraud"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("disable another store's account: %v", err)
	}
	if err := f.svc.SignOutMember(sctx, admin, stranger.User.ID, "fraud"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("sign out another store's account: %v", err)
	}
	if _, err := f.svc.AuthenticateInStore(tenant.WithStore(t.Context(), stranger.StoreID), string(stranger.Token)); err != nil {
		t.Fatalf("the stranger was touched: %v", err)
	}

	if err := f.svc.SetMemberDisabled(sctx, admin, member.User.ID, true, "stolen account"); err != nil {
		t.Fatal(err)
	}
	if err := inStore(); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("a disabled member's session: %v", err)
	}
	if _, err := f.svc.Login(f.device(t), memberEmail, goodPassword); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("a disabled member signs in: %v", err)
	}
	if err := f.svc.SetMemberDisabled(sctx, admin, member.User.ID, true, "again"); err != nil {
		t.Fatalf("disable twice: %v", err)
	}
	team, _ := f.svc.SupportTeam(sctx)
	for _, m := range team.Members {
		if m.UserID == member.User.ID && !m.Disabled {
			t.Fatal("the team does not show the member disabled")
		}
	}
	if err := f.svc.SetMemberDisabled(sctx, admin, member.User.ID, false, "cleared"); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.Login(f.device(t), memberEmail, goodPassword)
	if err != nil {
		t.Fatalf("an enabled member signs in: %v", err)
	}
	member.Token = res.Token
	if err = inStore(); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.SignOutMember(sctx, admin, member.User.ID, "suspicious device"); err != nil {
		t.Fatal(err)
	}
	if err = inStore(); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("signed out member's session: %v", err)
	}
	if _, err = f.svc.Login(f.device(t), memberEmail, goodPassword); err != nil {
		t.Fatalf("signed out, not disabled: %v", err)
	}
	if acts := f.auditActions(t, owner); !containsAll(acts, "member.disabled", "member.enabled", "member.signed_out") {
		t.Fatalf("activity = %v", acts)
	}

	// Each notice is emailed to the member.
	c := &capture{}
	w := &app.SupportNoticeWorker{D: f.workerDeps(c)}
	for _, ev := range []string{app.NoticeDisabled, app.NoticeEnabled, app.NoticeSignedOut, app.NoticeMFAReset} {
		if err = w.Work(tenant.WithStore(t.Context(), owner.StoreID), &river.Job[app.SupportNoticeArgs]{
			Args: app.SupportNoticeArgs{UserID: member.User.ID, Event: ev, At: time.Now()}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.mail) != 4 || c.mail[0].To != memberEmail || !strings.Contains(c.mail[0].Subject, "أُوقف") || !strings.Contains(c.mail[0].Text, "بتوقيت القاهرة") {
		t.Fatalf("notices = %+v", c.mail)
	}
}

func TestMaskPhone(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"+966512345412": "+9665•••••412",
		"+201012345678": "+2010•••••678",
		"":              "",
		"+2012":         "",
	} {
		if got := app.MaskPhone(in); got != want {
			t.Errorf("MaskPhone(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestSupportEdges (C1): what the support paths refuse or skip.
func TestSupportEdges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, _ := f.signup(t)
	member, _ := f.addMember(t, owner)
	f.enableTOTP(t, member)
	admin := f.ids.New()
	sctx := tenant.WithStore(f.device(t), owner.StoreID)

	// No admin or reason, an unknown request.
	if err := f.svc.SignOutMember(sctx, admin, member.User.ID, ""); err == nil {
		t.Fatal("sign out without a reason")
	}
	if err := f.svc.SetMemberDisabled(sctx, ulid.ID{}, member.User.ID, true, "x"); err == nil {
		t.Fatal("disable without an admin")
	}
	if err := f.svc.CancelSupportRequest(sctx, admin, f.ids.New(), ""); err == nil {
		t.Fatal("cancel without a reason")
	}
	if err := f.svc.CancelSupportRequest(sctx, admin, f.ids.New(), "x"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("cancel an unknown request: %v", err)
	}
	if err := f.execute(t, owner.StoreID, f.ids.New(), time.Now()); err != nil {
		t.Fatalf("an unknown request's job: %v", err)
	}

	// The service runs a request too, refusing one not due yet.
	req, err := f.svc.RequestMFAReset(sctx, admin, member.User.ID, "ticket 1")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.ExecuteSupportRequest(tenant.WithStore(t.Context(), owner.StoreID), req.ID); err == nil {
		t.Fatal("ran before its time")
	}

	// A link whose request closed is not sent; the cancelled job tells
	// nobody without a console, or for an unknown request.
	var tokenID ulid.ID
	if err = f.owner.QueryRow(t.Context(), `SELECT id FROM user_tokens WHERE request_id = $1 LIMIT 1`, req.ID).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CancelSupportRequest(sctx, admin, req.ID, "wrong member"); err != nil {
		t.Fatal(err)
	}
	c := &capture{}
	store := tenant.WithStore(t.Context(), owner.StoreID)
	if err = (&app.SupportLinkEmailWorker{D: f.workerDeps(c)}).Work(store,
		&river.Job[app.SupportLinkEmailArgs]{Args: app.SupportLinkEmailArgs{TokenID: tokenID}}); err != nil || len(c.mail) != 0 {
		t.Fatalf("a closed request's link = %v, %v", c.mail, err)
	}
	if err = (&app.SupportCancelledWorker{D: f.workerDeps(c)}).Work(store,
		&river.Job[app.SupportCancelledArgs]{Args: app.SupportCancelledArgs{RequestID: req.ID}}); err != nil {
		t.Fatalf("no console: %v", err)
	}
	notes := &adminMail{}
	d := f.workerDeps(c)
	d.NotifyAdmin = notes.notify
	if err = (&app.SupportCancelledWorker{D: d}).Work(store,
		&river.Job[app.SupportCancelledArgs]{Args: app.SupportCancelledArgs{RequestID: f.ids.New()}}); err != nil || len(notes.sent) != 0 {
		t.Fatalf("an unknown request = %v, %v", notes.sent, err)
	}

	// Notices: an unknown event is dropped, an unknown user ignored.
	w := &app.SupportNoticeWorker{D: f.workerDeps(c)}
	if err = w.Work(store, &river.Job[app.SupportNoticeArgs]{Args: app.SupportNoticeArgs{UserID: member.User.ID, Event: "nope"}}); err == nil {
		t.Fatal("an unknown notice was sent")
	}
	if err = w.Work(store, &river.Job[app.SupportNoticeArgs]{Args: app.SupportNoticeArgs{UserID: f.ids.New(), Event: app.NoticeDisabled}}); err != nil {
		t.Fatalf("an unknown user: %v", err)
	}
	if len(c.mail) != 0 {
		t.Fatalf("sent = %+v", c.mail)
	}
}
