package identity_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// TestStaffEmails: the store's members of the roles asked, and no other
// store's.
func TestStaffEmails(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	mkStore := func(members map[string]authz.Role) tenant.StoreID {
		id := tenant.StoreID{ID: ids.New()}
		ctx := tenant.WithStore(t.Context(), id)
		if txErr := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
			if _, sErr := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "idc-"+strings.ToLower(id.String()[14:])); sErr != nil {
				return sErr
			}
			for email, role := range members {
				uid := ids.New()
				if _, uErr := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash) VALUES ($1, $2, 'N', 'x')`, uid, email); uErr != nil {
					return uErr
				}
				if _, mErr := tx.Exec(ctx, `INSERT INTO store_memberships (store_id, user_id, role) VALUES ($1, $2, $3)`, id.ID, uid, string(role)); mErr != nil {
					return mErr
				}
			}
			return nil
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return id
	}
	suffix := strings.ToLower(ids.New().String())
	a := mkStore(map[string]authz.Role{"owner-a-" + suffix + "@example.com": authz.RoleOwner, "manager-a-" + suffix + "@example.com": authz.RoleManager,
		"support-a-" + suffix + "@example.com": authz.RoleSupport})
	mkStore(map[string]authz.Role{"owner-b-" + suffix + "@example.com": authz.RoleOwner})

	got, err := identity.StaffEmails(pool, authz.RoleOwner, authz.RoleManager)(tenant.WithStore(t.Context(), a))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if len(got) != 2 || !strings.HasPrefix(got[0], "manager-a-") || !strings.HasPrefix(got[1], "owner-a-") {
		t.Fatalf("staff emails = %v", got)
	}
	if _, err = identity.StaffEmails(pool, authz.RoleOwner)(t.Context()); err == nil {
		t.Fatal("no store in ctx accepted")
	}
}

// TestStaffPhones: the verified phones of the store's members of the
// roles asked; an unverified phone, another role and another store's
// owner are left out.
func TestStaffPhones(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	n := ids.New().Time().UnixNano() % 1_000_000
	num := func(i int) string { return fmt.Sprintf("+2010%02d%06d", i, n) }
	type member struct {
		role     authz.Role
		phone    string
		verified bool
	}
	mkStore := func(members []member) tenant.StoreID {
		id := tenant.StoreID{ID: ids.New()}
		ctx := tenant.WithStore(t.Context(), id)
		if txErr := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
			if _, sErr := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "idp-"+strings.ToLower(id.String()[14:])); sErr != nil {
				return sErr
			}
			for _, m := range members {
				uid := ids.New()
				if _, uErr := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash, phone, phone_verified_at)
					VALUES ($1, $2, 'N', 'x', $3, CASE WHEN $4 THEN now() END)`, uid, strings.ToLower(uid.String())+"@example.com", m.phone, m.verified); uErr != nil {
					return uErr
				}
				if _, mErr := tx.Exec(ctx, `INSERT INTO store_memberships (store_id, user_id, role) VALUES ($1, $2, $3)`, id.ID, uid, string(m.role)); mErr != nil {
					return mErr
				}
			}
			return nil
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return id
	}
	a := mkStore([]member{{authz.RoleOwner, num(1), true}, {authz.RoleManager, num(2), true}})
	unverified := mkStore([]member{{authz.RoleOwner, num(3), false}})
	mkStore([]member{{authz.RoleOwner, num(4), true}})
	got, err := identity.StaffPhones(pool, authz.RoleOwner)(tenant.WithStore(t.Context(), a))
	if err != nil || len(got) != 1 || got[0] != num(1) {
		t.Fatalf("owner phones = %v, %v", got, err)
	}
	if got, err = identity.StaffPhones(pool, authz.RoleOwner)(tenant.WithStore(t.Context(), unverified)); err != nil || len(got) != 0 {
		t.Fatalf("an unverified phone = %v, %v", got, err)
	}
}
