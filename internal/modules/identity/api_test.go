package identity_test

import (
	"crypto/sha256"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

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

// TestOwnerContactTx: the store's owner with their phone and their last
// use of the dashboard, support sessions left out; another store's owner
// never; a store without an owner says so.
func TestOwnerContactTx(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	suffix := strings.ToLower(ids.New().String())
	now := time.Now().UTC().Truncate(time.Second)
	phone := fmt.Sprintf("+2010%08d", rand.IntN(100_000_000)) //nolint:gosec // a test's unique number

	type member struct {
		role  authz.Role
		phone string
		seen  []time.Duration // sessions last seen this long ago; negative: a support session
	}
	mkStore := func(members map[string]member) tenant.StoreID {
		t.Helper()
		id := tenant.StoreID{ID: ids.New()}
		ctx := tenant.WithStore(t.Context(), id)
		if txErr := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
			if _, sErr := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "oc-"+strings.ToLower(id.String()[14:])); sErr != nil {
				return sErr
			}
			for email, m := range members {
				uid := ids.New()
				var phone any
				if m.phone != "" {
					phone = m.phone
				}
				if _, uErr := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash, phone, email_verified_at) VALUES ($1, $2, 'منى', 'x', $3, now())`,
					uid, email, phone); uErr != nil {
					return uErr
				}
				if _, mErr := tx.Exec(ctx, `INSERT INTO store_memberships (store_id, user_id, role) VALUES ($1, $2, $3)`, id.ID, uid, string(m.role)); mErr != nil {
					return mErr
				}
				for _, ago := range m.seen {
					var support, scope any
					if ago < 0 {
						support, scope, ago = ids.New(), id.ID, -ago
					}
					hash := sha256.Sum256([]byte(ids.New().String()))
					if _, sErr := tx.Exec(ctx, `INSERT INTO user_sessions (id, user_id, token_hash, expires_at, last_seen_at, impersonator_id, scope_store_id)
						VALUES ($1, $2, $3, $4, $5, $6, $7)`, ids.New(), uid, hash[:], now.Add(time.Hour), now.Add(-ago), support, scope); sErr != nil {
						return sErr
					}
				}
			}
			return nil
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return id
	}
	read := func(id tenant.StoreID) (identity.OwnerContact, bool) {
		t.Helper()
		ctx := tenant.WithStore(t.Context(), id)
		var c identity.OwnerContact
		var ok bool
		if err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) (txErr error) {
			c, ok, txErr = identity.OwnerContactTx(ctx, tx)
			return txErr
		}); err != nil {
			t.Fatal(err)
		}
		return c, ok
	}

	a := mkStore(map[string]member{
		"owner-a-" + suffix + "@example.com":   {role: authz.RoleOwner, phone: phone, seen: []time.Duration{48 * time.Hour, -time.Minute}},
		"manager-a-" + suffix + "@example.com": {role: authz.RoleManager, seen: []time.Duration{time.Minute}},
	})
	b := mkStore(map[string]member{"owner-b-" + suffix + "@example.com": {role: authz.RoleOwner}})
	c := mkStore(map[string]member{"manager-c-" + suffix + "@example.com": {role: authz.RoleManager}})

	got, ok := read(a)
	if !ok || got.Email != "owner-a-"+suffix+"@example.com" || got.Name != "منى" || got.Phone != phone || !got.EmailVerified {
		t.Fatalf("store A's owner = %+v, %v", got, ok)
	}
	// The support session a minute ago and the manager's are not the owner's own use.
	if !got.LastSeenAt.Equal(now.Add(-48 * time.Hour)) {
		t.Fatalf("last seen = %v; want %v", got.LastSeenAt, now.Add(-48*time.Hour))
	}
	if got, ok = read(b); !ok || got.Email != "owner-b-"+suffix+"@example.com" || got.Phone != "" || !got.LastSeenAt.IsZero() {
		t.Fatalf("store B's owner = %+v, %v", got, ok)
	}
	if _, ok = read(c); ok {
		t.Fatal("a store without an owner gave one")
	}
}
