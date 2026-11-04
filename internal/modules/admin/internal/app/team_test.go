package app_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

const newPW = "a brand new admin passphrase"

// accept looks an invite link up and accepts it with a current code.
func (f fixture) accept(t *testing.T, token string) app.LoginResult {
	t.Helper()
	v, err := f.svc.LookupInvite(f.device(t), token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	res, err := f.svc.AcceptInvite(f.device(t), token, newPW, code(t, v.Secret, time.Now()))
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	return res
}

// TestInviteAdmin (ADR-0078): an owner invites; the invitee sees who
// invited them and the seed, sets a password, confirms a code and is
// signed in, in the invite's role; the link works once; every step is in
// the admin log.
func TestInviteAdmin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	addr := "new-" + strings.ToLower(f.ids.New().String()[16:]) + "@iftahonline.com"

	link, err := f.svc.InviteAdmin(ctx, " "+strings.ToUpper(addr)+" ", "سارة", domain.RoleFinance, "joins finance")
	if err != nil || link.Token == "" || link.Invite.Email != addr || !link.Invite.ExpiresAt.After(time.Now().Add(71*time.Hour)) {
		t.Fatalf("invite = %+v, %v", link, err)
	}
	team, err := f.svc.Team(ctx)
	if err != nil || !slices.ContainsFunc(team.Invites, func(i app.TeamInvite) bool { return i.ID == link.Invite.ID }) {
		t.Fatalf("team = %+v, %v", team.Invites, err)
	}
	for name, err := range map[string]error{
		"existing admin": func() error {
			_, e := f.svc.InviteAdmin(ctx, me.Admin.Email, "Again", domain.RoleViewer, "again")
			return e
		}(),
		"open invite": func() error { _, e := f.svc.InviteAdmin(ctx, addr, "Again", domain.RoleViewer, "again"); return e }(),
	} {
		if !errs.Is(err, errs.KindConflict) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"bad email": func() error { _, e := f.svc.InviteAdmin(ctx, "nope", "A", domain.RoleViewer, "reason"); return e }(),
		"bad role":  func() error { _, e := f.svc.InviteAdmin(ctx, "x"+addr, "A", domain.Role("root"), "reason"); return e }(),
		"no reason": func() error { _, e := f.svc.InviteAdmin(ctx, "x"+addr, "A", domain.RoleViewer, ""); return e }(),
	} {
		if !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}

	v, err := f.svc.LookupInvite(f.device(t), link.Token)
	if err != nil || v.Purpose != "join" || v.Name != "سارة" || v.Role != domain.RoleFinance || v.InvitedByName != "Ops" ||
		len(v.Secret) != 32 || !strings.HasPrefix(v.URI, "otpauth://totp/") || !strings.HasPrefix(v.QR, "<svg") {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if _, err = f.svc.AcceptInvite(f.device(t), link.Token, newPW, "000000"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("wrong code: %v", err)
	}
	if _, err = f.svc.AcceptInvite(f.device(t), link.Token, "short", code(t, v.Secret, time.Now())); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("short password: %v", err)
	}
	res := f.accept(t, link.Token)
	if res.Admin.Email != addr || res.Admin.Role != domain.RoleFinance || res.Token == "" {
		t.Fatalf("accepted = %+v", res.Admin)
	}
	id, err := f.svc.Authenticate(t.Context(), string(res.Token))
	if err != nil || id.Role != domain.RoleFinance {
		t.Fatalf("session = %+v, %v", id, err)
	}
	// Once only; the code just used signs nobody in again.
	if _, err = f.svc.LookupInvite(f.device(t), link.Token); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("used link: %v", err)
	}
	if _, err = f.svc.Login(f.device(t), addr, newPW, code(t, v.Secret, time.Now())); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("replayed code: %v", err)
	}

	page, err := f.svc.Audit(ctx, app.AuditQuery{}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"admin.invited", "admin.joined"} {
		if !slices.ContainsFunc(page.Items, func(e app.AuditItem) bool { return e.Action == action && e.TargetID == res.Admin.ID.String() }) {
			t.Errorf("%s not in the admin log", action)
		}
	}
}

// TestRenewAndCancelInvite: a renewed link replaces the old one; a
// cancelled or unknown one is the same 404.
func TestRenewAndCancelInvite(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, _ := f.signedIn(t)
	addr := "renew-" + strings.ToLower(f.ids.New().String()[16:]) + "@iftahonline.com"
	first, err := f.svc.InviteAdmin(ctx, addr, "Nour", domain.RoleContent, "joins content")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.RenewInvite(ctx, first.Invite.ID, "lost the link")
	if err != nil || second.Token == first.Token {
		t.Fatalf("renew = %+v, %v", second, err)
	}
	if _, err = f.svc.LookupInvite(f.device(t), first.Token); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("old link: %v", err)
	}
	if _, err = f.svc.RenewInvite(ctx, first.Invite.ID, "again"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("renew a closed invite: %v", err)
	}
	if err = f.svc.CancelInvite(ctx, second.Invite.ID, "changed our mind"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{second.Token, "nope", strings.Repeat("A", 43)} {
		if _, err = f.svc.LookupInvite(f.device(t), token); !errs.Is(err, errs.KindNotFound) {
			t.Errorf("%q: %v", token, err)
		}
	}
	// Cancelled, the email may be invited again.
	if _, err = f.svc.InviteAdmin(ctx, addr, "Nour", domain.RoleContent, "second try"); err != nil {
		t.Fatal(err)
	}
}

// TestTeamChanges: an owner changes another admin's role, disables it
// (its sessions end) and enables it, resets it (locked out until the
// link sets a new password), and never acts on themselves.
func TestTeamChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	otherCreated, otherEmail := f.createAdminAs(t, domain.RoleOperations)
	them := otherCreated.Admin.ID
	signIn, err := f.svc.Login(f.device(t), otherEmail, adminPW, code(t, otherCreated.Secret, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	session := string(signIn.Token)

	for name, err := range map[string]error{
		"own role":   f.svc.ChangeRole(ctx, me.Admin.ID, domain.RoleViewer, "step down"),
		"own access": f.svc.SetAdminActive(ctx, me.Admin.ID, false, "leave"),
		"own reset":  func() error { _, e := f.svc.ResetAdmin(ctx, me.Admin.ID, "lost phone"); return e }(),
	} {
		if !errs.Is(err, errs.KindConflict) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err = f.svc.ChangeRole(ctx, f.ids.New(), domain.RoleViewer, "who"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown admin: %v", err)
	}

	if err = f.svc.ChangeRole(ctx, them, domain.RoleFinance, "moved to finance"); err != nil {
		t.Fatal(err)
	}
	id, err := f.svc.Authenticate(t.Context(), session)
	if err != nil || id.Role != domain.RoleFinance {
		t.Fatalf("after the change: %+v, %v", id, err)
	}

	if err = f.svc.SetAdminActive(ctx, them, false, "left the company"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Authenticate(t.Context(), session); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("a disabled admin's session: %v", err)
	}
	team, err := f.svc.Team(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(team.Members, func(m app.TeamMember) bool { return m.ID == them }); i < 0 || team.Members[i].Active || team.Members[i].Role != domain.RoleFinance {
		t.Fatalf("team member = %+v", team.Members)
	}
	if err = f.svc.SetAdminActive(ctx, them, true, "came back"); err != nil {
		t.Fatal(err)
	}

	link, err := f.svc.ResetAdmin(ctx, them, "lost phone")
	if err != nil || link.Invite.Purpose != "reset" || link.Invite.AdminID != them {
		t.Fatalf("reset = %+v, %v", link, err)
	}
	if _, err = f.svc.Login(f.device(t), otherEmail, adminPW, code(t, otherCreated.Secret, time.Now())); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("old password while resetting: %v", err)
	}
	if team, err = f.svc.Team(ctx); err != nil || !slices.ContainsFunc(team.Members, func(m app.TeamMember) bool { return m.ID == them && m.Resetting }) {
		t.Fatalf("resetting not shown: %v", err)
	}
	res := f.accept(t, link.Token)
	if res.Admin.ID != them || res.Admin.Role != domain.RoleFinance || !res.Admin.Active {
		t.Fatalf("reset done = %+v", res.Admin)
	}

	page, err := f.svc.Audit(ctx, app.AuditQuery{Admin: &me.Admin.ID}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"admin.role_changed", "admin.disabled", "admin.enabled", "admin.reset"} {
		if !slices.ContainsFunc(page.Items, func(e app.AuditItem) bool { return e.Action == action && e.TargetID == them.String() }) {
			t.Errorf("%s not in the admin log", action)
		}
	}
	if _, err = f.cli.Team(ctx); err != nil {
		t.Fatalf("cli team: %v", err)
	}
	if _, err = f.svc.Team(t.Context()); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
}

// TestExpiredInvite: past its 72 hours a link shows nothing and accepts
// nothing; renewing gives a working one.
func TestExpiredInvite(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, _ := f.signedIn(t)
	link, err := f.svc.InviteAdmin(ctx, "late-"+strings.ToLower(f.ids.New().String()[16:])+"@iftahonline.com", "Late", domain.RoleViewer, "joins")
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.svc.LookupInvite(f.device(t), link.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(t.Context(), `UPDATE admin_invites SET expires_at = now() - interval '1 minute' WHERE id = $1`, link.Invite.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.LookupInvite(f.device(t), link.Token); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("expired lookup: %v", err)
	}
	if _, err = f.svc.AcceptInvite(f.device(t), link.Token, newPW, code(t, v.Secret, time.Now())); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("expired accept: %v", err)
	}
	renewed, err := f.svc.RenewInvite(ctx, link.Invite.ID, "expired")
	if err != nil {
		t.Fatal(err)
	}
	f.accept(t, renewed.Token)
}
