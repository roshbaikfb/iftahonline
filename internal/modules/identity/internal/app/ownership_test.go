package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// roleOf is user's role in the store, read as the owner.
func (f fixture) roleOf(t *testing.T, owner app.AuthResult, user ulid.ID) authz.Role {
	t.Helper()
	members, err := f.svc.Members(ownerCtx(t.Context(), owner))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if m.UserID == user {
			return m.Role
		}
	}
	return ""
}

// memberCtx is a request of member res in store as role.
func memberCtx(ctx context.Context, store tenant.StoreID, res app.AuthResult, role authz.Role) context.Context {
	ctx = tenant.WithStore(ctx, store)
	ctx = authz.WithPrincipal(ctx, authz.Principal{UserID: res.User.ID, SessionID: res.Session.ID, StoreID: store, Role: role})
	return app.WithIdentity(ctx, app.Identity{UserID: res.User.ID, SessionID: res.Session.ID})
}

// TestTransferOwnership (C2): the owner moves the store to an active
// member at once, proving it with the password and the second factor;
// the old owner stays on as a manager; nobody else can, nor a support
// session, nor to someone outside the store.
func TestTransferOwnership(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, ownerEmail := f.signup(t)
	member, memberEmail := f.addMember(t, owner)
	stranger, _ := f.signup(t)
	octx := ownerCtx(f.device(t), owner)

	if err := f.svc.TransferOwnership(memberCtx(f.device(t), owner.StoreID, member, authz.RoleManager), owner.User.ID, goodPassword, ""); !errs.Is(err, errs.KindForbidden) {
		t.Fatalf("a manager moves it: %v", err)
	}
	support := app.WithIdentity(octx, app.Identity{UserID: owner.User.ID, SessionID: owner.Session.ID, Impersonation: &app.Impersonation{Admin: f.ids.New()}})
	if err := f.svc.TransferOwnership(support, member.User.ID, goodPassword, ""); !errs.Is(err, errs.KindForbidden) {
		t.Fatalf("a support session moves it: %v", err)
	}
	if err := f.svc.TransferOwnership(octx, member.User.ID, "wrong password", ""); fieldCodes(err)["password"] != "wrong" {
		t.Fatalf("a wrong password: %v", err)
	}
	for name, to := range map[string]ulid.ID{"another store's account": stranger.User.ID, "the owner": owner.User.ID} {
		if err := f.svc.TransferOwnership(octx, to, goodPassword, ""); !errs.Is(err, errs.KindNotFound) && !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// With two-step verification on, a code is needed too.
	setup, err := f.svc.SetupTOTP(as(t.Context(), owner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ConfirmTOTP(as(t.Context(), owner), totpNow(t, setup.Secret, time.Now())); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.TransferOwnership(octx, member.User.ID, goodPassword, ""); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("no code: %v", err)
	}
	if f.roleOf(t, owner, member.User.ID) != authz.RoleManager {
		t.Fatal("moved without the code")
	}
	if err = f.svc.TransferOwnership(octx, member.User.ID, goodPassword, totpNow(t, setup.Secret, time.Now().Add(30*time.Second))); err != nil {
		t.Fatal(err)
	}
	if f.roleOf(t, owner, member.User.ID) != authz.RoleOwner || f.roleOf(t, owner, owner.User.ID) != authz.RoleManager {
		t.Fatal("the roles did not swap")
	}
	if acts := f.auditActions(t, app.AuthResult{User: member.User, Session: member.Session, StoreID: owner.StoreID}); !containsAll(acts, "store.ownership_transferred") {
		t.Fatalf("activity = %v", acts)
	}

	// Both are told.
	c := &capture{}
	w := &app.SupportNoticeWorker{D: f.workerDeps(c)}
	store := tenant.WithStore(t.Context(), owner.StoreID)
	for _, n := range []app.SupportNoticeArgs{
		{UserID: owner.User.ID, Other: member.User.ID, Event: app.NoticeOwnershipGiven, At: time.Now()},
		{UserID: member.User.ID, Other: owner.User.ID, Event: app.NoticeOwnershipReceived, At: time.Now()},
	} {
		if err = w.Work(store, &river.Job[app.SupportNoticeArgs]{Args: n}); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.mail) != 2 || c.mail[0].To != ownerEmail || !strings.Contains(c.mail[0].Text, "Karim") || c.mail[1].To != memberEmail ||
		!strings.Contains(c.mail[1].Subject, "صرت صاحب") {
		t.Fatalf("notices = %+v", c.mail)
	}
}

// TestSupportOwnershipTransfer (C2): support asks to move a store's
// ownership; only the owner now gets a cancel link; it takes effect after
// the delay, or is skipped when the team changed meanwhile.
func TestSupportOwnershipTransfer(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, ownerEmail := f.signup(t)
	member, _ := f.addMember(t, owner)
	admin := f.ids.New()
	sctx := tenant.WithStore(f.device(t), owner.StoreID)

	if _, err := f.svc.RequestOwnershipTransfer(sctx, admin, owner.User.ID, "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("to the owner: %v", err)
	}
	req, err := f.svc.RequestOwnershipTransfer(sctx, admin, member.User.ID, "sale contract")
	if err != nil || req.FromUserID == nil || *req.FromUserID != owner.User.ID || req.Kind != "ownership_transfer" {
		t.Fatalf("request = %+v, %v", req, err)
	}
	if _, err = f.svc.RequestOwnershipTransfer(sctx, admin, member.User.ID, "again"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("a second transfer: %v", err)
	}
	links, subjects := f.supportLinks(t, owner.StoreID, req.ID, member.User.ID)
	if len(links) != 1 || links[ownerEmail] == "" || !strings.Contains(subjects[0], "طلب نقل ملكية") {
		t.Fatalf("links = %v %v", links, subjects)
	}
	got, err := f.svc.CheckSupportLink(f.device(t), links[ownerEmail])
	if err != nil || got.Kind != "ownership_transfer" || got.ForMember || got.MemberName != "Karim" {
		t.Fatalf("the owner's check = %+v, %v", got, err)
	}
	// The new owner hears of it.
	c := &capture{}
	if err = (&app.SupportNoticeWorker{D: f.workerDeps(c)}).Work(tenant.WithStore(t.Context(), owner.StoreID), &river.Job[app.SupportNoticeArgs]{
		Args: app.SupportNoticeArgs{UserID: member.User.ID, Other: owner.User.ID, Event: app.NoticeTransferAsked, At: req.EffectiveAt}}); err != nil ||
		len(c.mail) != 1 || !strings.Contains(c.mail[0].Subject, "إليك") {
		t.Fatalf("heads-up = %+v, %v", c.mail, err)
	}

	if err = f.execute(t, owner.StoreID, req.ID, req.EffectiveAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if f.roleOf(t, owner, member.User.ID) != authz.RoleOwner || f.roleOf(t, owner, owner.User.ID) != authz.RoleManager {
		t.Fatal("the roles did not swap")
	}

	// Skipped when the team changed: the new owner (Karim) moves it back
	// himself, so a second request from the old owner finds no owner to
	// move from.
	req2, err := f.svc.RequestOwnershipTransfer(sctx, admin, owner.User.ID, "move it back")
	if err != nil {
		t.Fatal(err)
	}
	kctx := memberCtx(f.device(t), owner.StoreID, member, authz.RoleOwner)
	if err = f.svc.TransferOwnership(kctx, owner.User.ID, goodPassword, ""); err != nil {
		t.Fatal(err)
	}
	if err = f.execute(t, owner.StoreID, req2.ID, req2.EffectiveAt.Add(time.Minute)); err != nil {
		t.Fatalf("a stale transfer: %v", err)
	}
	if f.roleOf(t, owner, owner.User.ID) != authz.RoleOwner {
		t.Fatal("a stale transfer changed the roles")
	}
	if acts := f.auditActions(t, owner); !containsAll(acts, "support.ownership_transfer_requested", "support.ownership_transfer_done",
		"support.ownership_transfer_skipped") {
		t.Fatalf("activity = %v", acts)
	}
}
