package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeSupport stands in for identity: it records each call with the
// store in ctx and answers a fixed team.
type fakeSupport struct {
	mu     sync.Mutex
	member ulid.ID
	stores []app.MemberStore
	reqs   []app.SupportRequest
	calls  []string
	in     []tenant.StoreID
	by     []ulid.ID
}

func (f *fakeSupport) record(ctx context.Context, call string, by ulid.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, _ := tenant.FromContext(ctx)
	f.calls, f.in, f.by = append(f.calls, call), append(f.in, id), append(f.by, by)
}

func (f *fakeSupport) Team(ctx context.Context) (app.StoreTeam, error) {
	f.record(ctx, "team", ulid.ID{})
	return app.StoreTeam{
		Members:  []app.StoreMember{{UserID: f.member, Name: "كريم", Email: "karim@example.com", Phone: "+9665•••••412", Role: "manager", MFAEnabled: true}},
		Invites:  []app.StoreInvite{{Email: "ship@example.com", Role: "shipping", ExpiresAt: time.Now().Add(time.Hour)}},
		Requests: f.reqs,
	}, nil
}

func (f *fakeSupport) RequestMFAReset(ctx context.Context, by, user ulid.ID, _ string) (app.SupportRequest, error) {
	f.record(ctx, "mfa_reset", by)
	if user != f.member {
		return app.SupportRequest{}, errs.NotFound("member_not_found", "not a member")
	}
	return app.SupportRequest{ID: ulid.NewGenerator().New(), Kind: "mfa_reset", UserID: user, RequestedBy: by}, nil
}

func (f *fakeSupport) CancelRequest(ctx context.Context, by, _ ulid.ID, _ string) error {
	f.record(ctx, "cancel", by)
	return nil
}

func (f *fakeSupport) SetDisabled(ctx context.Context, by, _ ulid.ID, disabled bool, _ string) error {
	call := "enable"
	if disabled {
		call = "disable"
	}
	f.record(ctx, call, by)
	return nil
}

func (f *fakeSupport) SignOut(ctx context.Context, by, _ ulid.ID, _ string) error {
	f.record(ctx, "sign_out", by)
	return nil
}

func (f *fakeSupport) MemberStores(ctx context.Context, _ ulid.ID) ([]app.MemberStore, error) {
	f.record(ctx, "stores", ulid.ID{})
	return f.stores, nil
}

// TestStoreTeam (C1): the console reads a store's team through identity
// with the store in ctx; contacts are left out for a role without
// contacts.read; requests name the admin who asked.
func TestStoreTeam(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	finance, _ := f.signedInAs(t, domain.RoleFinance)
	shop := f.createStore(t)
	fake := &fakeSupport{member: f.ids.New()}
	fake.reqs = []app.SupportRequest{{ID: f.ids.New(), Kind: "mfa_reset", UserID: fake.member, RequestedBy: me.Admin.ID},
		{ID: f.ids.New(), Kind: "mfa_reset", UserID: fake.member, RequestedBy: f.ids.New()}}
	d := f.deps
	d.Support = fake
	svc := app.NewService(d)

	team, err := svc.StoreTeam(ctx, shop)
	if err != nil || len(team.Members) != 1 || team.Members[0].Email == "" || team.Members[0].Phone == "" || team.Invites[0].Email == "" {
		t.Fatalf("team = %+v, %v", team, err)
	}
	if fake.in[0] != shop {
		t.Fatalf("asked in store %v", fake.in[0])
	}
	if team.Requests[0].RequesterName != "Ops" || team.Requests[0].RequesterRole != domain.RoleOwner || team.Requests[1].RequesterName != "" {
		t.Fatalf("requesters = %+v", team.Requests)
	}
	hidden, err := svc.StoreTeam(finance, shop)
	if err != nil || hidden.Members[0].Email != "" || hidden.Members[0].Phone != "" || hidden.Invites[0].Email != "" || hidden.Members[0].Name == "" {
		t.Fatalf("finance sees = %+v, %v", hidden, err)
	}
	if _, err = svc.StoreTeam(ctx, tenant.StoreID{ID: f.ids.New()}); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown store: %v", err)
	}
	if _, err = svc.StoreTeam(t.Context(), shop); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
	if _, err = f.svc.StoreTeam(ctx, shop); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("not wired: %v", err)
	}
}

// TestStoreTeamActions (C1): each action needs a reason, is recorded in
// the admin log first, and reaches identity in the store's ctx as the
// signed-in admin.
func TestStoreTeamActions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, me := f.signedIn(t)
	shop, other := f.createStore(t), f.createStore(t)
	fake := &fakeSupport{member: f.ids.New(), stores: []app.MemberStore{{StoreID: shop.ID, Role: "manager"}, {StoreID: other.ID, Role: "support"}}}
	d := f.deps
	d.Support = fake
	svc := app.NewService(d)

	if _, err := svc.RequestMFAReset(ctx, shop, fake.member, "x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a short reason: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("identity called without a reason: %v", fake.calls)
	}
	req, err := svc.RequestMFAReset(ctx, shop, fake.member, "ticket 4821")
	if err != nil || req.RequesterName != "Ops" || req.RequesterRole != domain.RoleOwner {
		t.Fatalf("request = %+v, %v", req, err)
	}
	if _, err = svc.RequestMFAReset(ctx, tenant.StoreID{ID: f.ids.New()}, fake.member, "ticket 4821"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown store: %v", err)
	}
	if _, err = svc.RequestMFAReset(ctx, shop, f.ids.New(), "ticket 4821"); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("not a member: %v", err)
	}
	for _, act := range []func() error{
		func() error { return svc.SetMemberDisabled(ctx, shop, fake.member, true, "stolen account") },
		func() error { return svc.SetMemberDisabled(ctx, shop, fake.member, false, "cleared") },
		func() error { return svc.SignOutMember(ctx, shop, fake.member, "suspicious device") },
		func() error { return svc.CancelSupportRequest(ctx, shop, req.ID, "wrong member") },
	} {
		if err = act(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"mfa_reset", "mfa_reset", "disable", "enable", "sign_out", "cancel"}
	if len(fake.calls) != len(want) {
		t.Fatalf("calls = %v", fake.calls)
	}
	for i, c := range want {
		if fake.calls[i] != c || fake.in[i] != shop || fake.by[i] != me.Admin.ID {
			t.Fatalf("call %d = %s in %v by %v", i, fake.calls[i], fake.in[i], fake.by[i])
		}
	}
	page, err := svc.Audit(ctx, app.AuditQuery{Family: "store"}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	logged := map[string]bool{}
	for _, it := range page.Items {
		logged[it.Action] = true
	}
	for _, a := range []string{"store.member_mfa_reset_requested", "store.member_disabled", "store.member_enabled", "store.member_signed_out",
		"store.support_request_cancelled"} {
		if !logged[a] {
			t.Errorf("admin log misses %s", a)
		}
	}

	// The other stores of a member, named; not for an account outside.
	stores, err := svc.MemberStores(ctx, shop, fake.member)
	if err != nil || len(stores) != 1 || stores[0].StoreID != other.ID || stores[0].Name == "" || stores[0].Role != "support" {
		t.Fatalf("member stores = %+v, %v", stores, err)
	}
	if _, err = svc.MemberStores(ctx, tenant.StoreID{ID: f.ids.New()}, fake.member); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown store: %v", err)
	}
	fake.stores = []app.MemberStore{{StoreID: other.ID, Role: "support"}}
	if _, err = svc.MemberStores(ctx, shop, fake.member); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("not this store's member: %v", err)
	}
}

type mailBox struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (m *mailBox) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

// TestNotifyAdmin (C1): an active admin is emailed with the console's
// link; an unknown one, or without a mailer, nothing.
func TestNotifyAdmin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	created, email := f.createAdmin(t)
	box := &mailBox{}
	d := f.deps
	d.Mailer, d.ConsoleURL = box, "https://admin.iftahonline.com/console"
	svc := app.NewService(d)

	if err := svc.NotifyAdmin(t.Context(), created.Admin.ID, "أُلغي طلب", "النص"); err != nil {
		t.Fatal(err)
	}
	if err := svc.NotifyAdmin(t.Context(), f.ids.New(), "x", "y"); err != nil {
		t.Fatalf("unknown admin: %v", err)
	}
	if err := f.svc.NotifyAdmin(t.Context(), created.Admin.ID, "x", "y"); err != nil {
		t.Fatalf("no mailer: %v", err)
	}
	if len(box.sent) != 1 || box.sent[0].To != email || box.sent[0].Subject != "أُلغي طلب" || box.sent[0].Text != "النص\n\nhttps://admin.iftahonline.com/console" {
		t.Fatalf("sent = %+v", box.sent)
	}
}
