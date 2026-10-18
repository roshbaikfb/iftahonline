package postgres_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
)

// TestTenantTablesUseRLS is the catalog-wide second-wall check (ADR-0002):
// every table any migration creates with a store_id column must have RLS
// enabled and forced and the tenant_isolation policy. It needs no update
// when modules add tables. Extra permissive policies that apply to
// app_user must be listed here, each one reviewed.
func TestTenantTablesUseRLS(t *testing.T) {
	testkit.AssertTenantTablesUseRLS(t, pool(t, infra.OwnerURL, 2))
}
