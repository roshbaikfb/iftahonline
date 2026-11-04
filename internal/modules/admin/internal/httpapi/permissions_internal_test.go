package httpapi

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
)

// TestEveryOperationHasAPermission: each console operation names what it
// needs (ADR-0077), and the table names no operation that is gone.
func TestEveryOperationHasAPermission(t *testing.T) {
	t.Parallel()
	ops := map[string]bool{}
	it := reflect.TypeFor[StrictServerInterface]()
	for i := range it.NumMethod() {
		op := it.Method(i).Name
		ops[op] = true
		if _, ok := permissions[op]; !ok {
			t.Errorf("%s has no permission: add it to permissions", op)
		}
	}
	for op, p := range permissions {
		if !ops[op] {
			t.Errorf("permissions names %s, which is no operation", op)
		}
		if p != anyAdmin && !slices.Contains(domain.AllPermissions(), p) {
			t.Errorf("%s needs %q, which is no permission", op, p)
		}
	}
	// Only the session's own routes and the home are open to every admin;
	// anything that writes needs a manage permission.
	for op, p := range permissions {
		open := []string{"AdminLogin", "AdminLogout", "AdminMe", "AdminHealth", "AdminGetMetrics", "AdminLookupInvite", "AdminAcceptInvite", "AdminSearch", "AdminListAlerts", "AdminSetAlertEmail"}
		if p == anyAdmin && !slices.Contains(open, op) {
			t.Errorf("%s is open to every admin", op)
		}
		if strings.HasSuffix(string(p), ".read") && slices.ContainsFunc([]string{"Save", "Create", "Update", "Delete", "Set", "Remove", "Add",
			"Publish", "Retire", "Suspend", "Reactivate", "Impersonate", "Settle", "Mark", "Clear", "Export"}, func(verb string) bool {
			return strings.HasPrefix(op, "Admin"+verb)
		}) {
			t.Errorf("%s changes something but needs only %s", op, p)
		}
	}
}
