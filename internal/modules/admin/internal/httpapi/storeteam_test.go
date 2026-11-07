package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// teamMember is the one member testSupport knows.
const teamMember = "01M6V0000000000000000MEMBR"

// testSupport stands in for identity: one member, no requests.
type testSupport struct{}

func member(user ulid.ID) error {
	if user.String() != teamMember {
		return errs.NotFound("member_not_found", "not a member")
	}
	return nil
}

func (testSupport) Team(context.Context) (admin.StoreTeam, error) {
	id, err := ulid.Parse(teamMember)
	return admin.StoreTeam{Members: []admin.StoreMember{{UserID: id, Name: "كريم", Email: "karim@example.com", Phone: "+9665•••••412",
		Role: "manager", MFAEnabled: true, JoinedAt: time.Now()}}}, err
}

func (testSupport) RequestMFAReset(_ context.Context, by, user ulid.ID, reason string) (admin.SupportRequest, error) {
	now := time.Now()
	return admin.SupportRequest{ID: ulid.NewGenerator().New(), Kind: "mfa_reset", UserID: user, RequestedBy: by, Reason: reason, CreatedAt: now,
		EffectiveAt: now.Add(24 * time.Hour)}, member(user)
}

func (testSupport) RequestOwnershipTransfer(_ context.Context, by, user ulid.ID, reason string) (admin.SupportRequest, error) {
	now := time.Now()
	return admin.SupportRequest{ID: ulid.NewGenerator().New(), Kind: "ownership_transfer", UserID: user, RequestedBy: by, Reason: reason, CreatedAt: now,
		EffectiveAt: now.Add(24 * time.Hour)}, member(user)
}

func (testSupport) CancelRequest(context.Context, ulid.ID, ulid.ID, string) error { return nil }

func (testSupport) SetDisabled(_ context.Context, _, user ulid.ID, _ bool, _ string) error {
	return member(user)
}

func (testSupport) SignOut(_ context.Context, _, user ulid.ID, _ string) error { return member(user) }

func (testSupport) MemberStores(_ context.Context, user ulid.ID) ([]admin.MemberStore, error) {
	return nil, member(user)
}

// TestConsoleStoreTeam (C1): the team over HTTP; contacts only with
// contacts.read; actions need stores.manage, a reason and a same-origin
// request; an unknown store or a non-member is 404.
func TestConsoleStoreTeam(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sess, viewer, finance := f.login(t), f.loginAs(t, "viewer"), f.loginAs(t, "finance")
	storeID := f.store(t)
	base := "/admin/v1/stores/" + storeID + "/team"

	if r := f.do(t, http.MethodGet, adminHost, base, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", r.status)
	}
	r := f.do(t, http.MethodGet, adminHost, base, "", withCookie(sess))
	members, _ := r.body["members"].([]any)
	if r.status != http.StatusOK || len(members) != 1 || members[0].(map[string]any)["email"] != "karim@example.com" ||
		members[0].(map[string]any)["mfa_enabled"] != true {
		t.Fatalf("team = %d %v", r.status, r.body)
	}
	if _, has := members[0].(map[string]any)["last_seen_at"]; has {
		t.Fatalf("last_seen_at for never: %v", members[0])
	}
	r = f.do(t, http.MethodGet, adminHost, base, "", withCookie(finance))
	members, _ = r.body["members"].([]any)
	if r.status != http.StatusOK || len(members) != 1 || members[0].(map[string]any)["email"] != nil || members[0].(map[string]any)["phone"] != nil {
		t.Fatalf("finance's team = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodGet, adminHost, "/admin/v1/stores/"+f.ids.New().String()+"/team", "", withCookie(sess)); r.status != http.StatusNotFound {
		t.Fatalf("unknown store = %d", r.status)
	}

	reset := base + "/" + teamMember + "/mfa-reset"
	reason := `{"reason":"ticket 4821"}`
	if r = f.do(t, http.MethodPost, adminHost, reset, reason, withCookie(sess)); r.status != http.StatusForbidden {
		t.Fatalf("cross-site = %d", r.status)
	}
	if r = f.do(t, http.MethodPost, adminHost, reset, reason, withCookie(viewer), sameOrigin); r.status != http.StatusForbidden {
		t.Fatalf("viewer = %d", r.status)
	}
	if r = f.do(t, http.MethodPost, adminHost, reset, `{"reason":"x"}`, withCookie(sess), sameOrigin); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("short reason = %d %v", r.status, r.body)
	}
	r = f.do(t, http.MethodPost, adminHost, reset, reason, withCookie(sess), sameOrigin)
	if r.status != http.StatusAccepted || r.body["state"] != "open" || r.body["kind"] != "mfa_reset" || r.body["requested_by_name"] != "Ops" {
		t.Fatalf("reset = %d %v", r.status, r.body)
	}
	for _, path := range []string{base + "/" + f.ids.New().String() + "/disable", base + "/not-an-id/sign-out",
		"/admin/v1/stores/" + f.ids.New().String() + "/team/" + teamMember + "/disable"} {
		if r = f.do(t, http.MethodPost, adminHost, path, reason, withCookie(sess), sameOrigin); r.status != http.StatusNotFound {
			t.Fatalf("%s = %d %v", path, r.status, r.body)
		}
	}
	transfer := base + "/" + teamMember + "/ownership"
	if r = f.do(t, http.MethodPost, adminHost, transfer, reason, withCookie(viewer), sameOrigin); r.status != http.StatusForbidden {
		t.Fatalf("viewer transfer = %d", r.status)
	}
	if r = f.do(t, http.MethodPost, adminHost, transfer, reason, withCookie(sess), sameOrigin); r.status != http.StatusAccepted || r.body["kind"] != "ownership_transfer" {
		t.Fatalf("transfer = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodPost, adminHost, base+"/"+f.ids.New().String()+"/ownership", reason, withCookie(sess), sameOrigin); r.status != http.StatusNotFound {
		t.Fatalf("transfer to a non-member = %d", r.status)
	}
	for _, action := range []string{"disable", "enable", "sign-out"} {
		if r = f.do(t, http.MethodPost, adminHost, base+"/"+teamMember+"/"+action, reason, withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
			t.Fatalf("%s = %d %v", action, r.status, r.body)
		}
	}
	cancel := "/admin/v1/stores/" + storeID + "/support-requests/" + f.ids.New().String() + "/cancel"
	if r = f.do(t, http.MethodPost, adminHost, cancel, reason, withCookie(viewer), sameOrigin); r.status != http.StatusForbidden {
		t.Fatalf("viewer cancel = %d", r.status)
	}
	if r = f.do(t, http.MethodPost, adminHost, cancel, reason, withCookie(sess), sameOrigin); r.status != http.StatusNoContent {
		t.Fatalf("cancel = %d %v", r.status, r.body)
	}
	if r = f.do(t, http.MethodGet, adminHost, base+"/"+teamMember+"/stores", "", withCookie(viewer)); r.status != http.StatusForbidden {
		t.Fatalf("viewer member stores = %d", r.status)
	}
	if r = f.do(t, http.MethodGet, adminHost, base+"/"+teamMember+"/stores", "", withCookie(sess)); r.status != http.StatusNotFound {
		t.Fatalf("member stores for a store it is not in = %d %v", r.status, r.body)
	}
}
