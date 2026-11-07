package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestTransferOwnershipOverHTTP (C2): the owner moves the store to a
// member with the password; a manager cannot; another store's account is
// not found; a wrong password is a field error; afterwards the old owner
// is a manager and cannot move it again.
func TestTransferOwnershipOverHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, karim, other := f.signup(t, "mobile"), f.signup(t, "mobile"), f.signup(t, "mobile")
	f.addMember(t, owner.storeID, karim.userID, "manager")
	path := "/v1/stores/" + owner.storeID + "/ownership"
	body := func(to, pw string) string { return fmt.Sprintf(`{"user_id":%q,"password":%q}`, to, pw) }

	if res, m := f.do(t, newReq(t, http.MethodPost, path, body(owner.userID, "correct horse battery"), bearer(karim.token))); res.StatusCode != http.StatusForbidden {
		t.Fatalf("a manager = %d %v", res.StatusCode, m)
	}
	if res, m := f.do(t, newReq(t, http.MethodPost, path, body(karim.userID, "wrong"), bearer(owner.token))); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a wrong password = %d %v", res.StatusCode, m)
	}
	if res, m := f.do(t, newReq(t, http.MethodPost, path, body(other.userID, "correct horse battery"), bearer(owner.token))); res.StatusCode != http.StatusNotFound ||
		m["code"] != "member_not_found" {
		t.Fatalf("another store's account = %d %v", res.StatusCode, m)
	}
	if res, _ := f.do(t, newReq(t, http.MethodPost, path, `{"user_id":`, bearer(owner.token))); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed = %d", res.StatusCode)
	}
	if res, m := f.do(t, newReq(t, http.MethodPost, path, body(karim.userID, "correct horse battery"), bearer(owner.token))); res.StatusCode != http.StatusNoContent {
		t.Fatalf("transfer = %d %v", res.StatusCode, m)
	}
	// Now the old owner is a manager: no longer allowed.
	if res, m := f.do(t, newReq(t, http.MethodPost, path, body(karim.userID, "correct horse battery"), bearer(owner.token))); res.StatusCode != http.StatusForbidden {
		t.Fatalf("the old owner again = %d %v", res.StatusCode, m)
	}
	// Another store's owner cannot reach this store.
	if res, _ := f.do(t, newReq(t, http.MethodPost, path, body(other.userID, "correct horse battery"), bearer(other.token))); res.StatusCode != http.StatusNotFound &&
		res.StatusCode != http.StatusForbidden {
		t.Fatalf("another store's owner = %d", res.StatusCode)
	}
}
