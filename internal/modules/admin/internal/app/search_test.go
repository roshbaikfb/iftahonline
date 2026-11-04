package app_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestSearch (ADR-0079): one query across what the role reads; a full
// invoice number or an ID jumps to its page; a kind the role may not
// read stays empty.
func TestSearch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, _ := f.signedIn(t)
	content, _ := f.signedInAs(t, domain.RoleContent)
	shop := f.createStore(t)
	invoice, number := f.seedInvoice(t, &shop, "Victim")
	agencyInvoice, _ := f.seedInvoice(t, nil, "Searchable Agency")

	r, err := f.svc.Search(owner, number)
	if err != nil || len(r.Invoices) != 1 || r.Invoices[0].ID != invoice || r.Jump != "/billing/invoices/"+invoice.String() {
		t.Fatalf("by number = %+v, %v", r, err)
	}
	if r, err = f.svc.Search(owner, shop.String()); err != nil || r.Jump != "/stores/"+shop.String() {
		t.Fatalf("by store id = %+v, %v", r.Jump, err)
	}
	if r, err = f.svc.Search(owner, agencyInvoice.String()); err != nil || r.Jump != "/billing/invoices/"+agencyInvoice.String() {
		t.Fatalf("by invoice id = %+v, %v", r.Jump, err)
	}
	if r, err = f.svc.Search(owner, "Searchable Agency"); err != nil || len(r.Invoices) != 1 || r.Jump != "" {
		t.Fatalf("by agency name = %+v, %v", r, err)
	}
	if r, err = f.svc.Search(owner, "Ops"); err != nil || len(r.Admins) == 0 {
		t.Fatalf("admins = %+v, %v", r.Admins, err)
	}
	if r, err = f.svc.Search(owner, "Victim"); err != nil || len(r.Stores) == 0 || len(r.Failed) != 0 {
		t.Fatalf("stores = %+v, %v", r, err)
	}

	// Content reads none of these.
	r, err = f.svc.Search(content, number)
	if err != nil || len(r.Invoices)+len(r.Stores)+len(r.Admins)+len(r.Payments)+len(r.Agencies) != 0 || r.Jump != "" {
		t.Fatalf("content = %+v, %v", r, err)
	}
	for _, q := range []string{"x", " a ", strings.Repeat("y", 101)} {
		if _, err = f.svc.Search(owner, q); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%q: %v", q, err)
		}
	}
	if _, err = f.svc.Search(t.Context(), "Victim"); !errs.Is(err, errs.KindUnauthenticated) {
		t.Fatalf("not signed in: %v", err)
	}
}

// TestSearchOwner: an exact phone or email finds its owner's stores for
// owners and operations, recorded masked in the admin log; other roles
// never look it up.
func TestSearchOwner(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	shop := f.createStore(t)
	var asked []string
	d := f.deps
	d.OwnerStores = func(_ context.Context, addr string, phones []string) ([]ulid.ID, error) {
		asked = append(asked, addr+"|"+strings.Join(phones, ","))
		return []ulid.ID{shop.ID}, nil
	}
	svc := app.NewService(d)
	ctx, me := f.signedIn(t)
	finance, _ := f.signedInAs(t, domain.RoleFinance)

	r, err := svc.Search(ctx, "0101 234 5678")
	if err != nil || len(r.OwnerStores) != 1 || r.OwnerStores[0].Store.ID != shop {
		t.Fatalf("by phone = %+v, %v", r.OwnerStores, err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "+201012345678") {
		t.Fatalf("asked = %v", asked)
	}
	if r, err = svc.Search(ctx, " Mona@Example.COM "); err != nil || len(r.OwnerStores) != 1 || !strings.HasPrefix(asked[1], "mona@example.com|") {
		t.Fatalf("by email = %+v, %v (%v)", r.OwnerStores, err, asked)
	}
	page, err := f.svc.Audit(ctx, app.AuditQuery{Admin: &me.Admin.ID}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	for _, e := range page.Items {
		if e.Action == "admin.searched_owner" {
			logged = append(logged, string(e.Metadata))
		}
	}
	if len(logged) != 2 || !slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, "m•••@example.com") }) ||
		slices.ContainsFunc(logged, func(m string) bool { return strings.Contains(m, "12345678") || strings.Contains(m, "mona@") }) {
		t.Fatalf("logged = %v", logged)
	}

	// Finance does not read contacts: no lookup, nothing logged.
	if r, err = svc.Search(finance, "01012345678"); err != nil || len(r.OwnerStores) != 0 || len(asked) != 2 {
		t.Fatalf("finance = %+v, %v (%v)", r.OwnerStores, err, asked)
	}
	// A name is no contact.
	if _, err = svc.Search(ctx, "Victim"); err != nil || len(asked) != 2 {
		t.Fatalf("a name looked an owner up: %v %v", err, asked)
	}
}
