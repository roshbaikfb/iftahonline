package app

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/email"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's search (ADR-0079): one query across what the admin's
// role may read, each source capped and timed on its own, so a slow one
// leaves the others shown.

// OwnerStoresFunc lists the stores whose owner has exactly this email or
// one of these E.164 phones (identity, on the console's pool); store IDs
// only.
type OwnerStoresFunc func(ctx context.Context, email string, phones []string) ([]ulid.ID, error)

// Search limits.
const (
	searchPerGroup = 5
	searchTimeout  = 2 * time.Second
)

// SearchResult is what a query found, by kind; a kind the role may not
// read is left empty. Jump is the console path of the one thing the
// query names exactly (a full invoice number, an ID, a domain), if any;
// Failed names the kinds that could not be read in time.
type SearchResult struct {
	Jump        string
	Stores      []tenancy.DirectoryEntry
	OwnerStores []tenancy.DirectoryEntry
	Invoices    []billing.ConsoleInvoice
	Payments    []billing.ConsolePayment
	Agencies    []billing.Agency
	Admins      []store.Member
	Failed      []string
}

var (
	errSearchLength = errs.Validation(errs.FieldError{Field: "q", Code: "invalid", Message: "2 to 100 characters"})
	invoiceNumber   = regexp.MustCompile(`^INV-\d{4}-\d{6,}$`)
	hostLike        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// Search looks q up in every kind the signed-in admin's role reads. An
// exact email or phone also finds the stores its owner owns, for roles
// that read contacts; that lookup is recorded first in the admin log,
// the email or phone masked.
func (s *Service) Search(ctx context.Context, raw string) (SearchResult, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return SearchResult{}, errNoSession
	}
	q := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(q); n < 2 || n > 100 {
		return SearchResult{}, errSearchLength
	}
	var (
		res SearchResult
		mu  sync.Mutex
	)
	failed := func(kind string, err error) {
		if err == nil {
			return
		}
		s.d.Log.WarnContext(ctx, "admin: search source failed", "kind", kind, "err", err)
		mu.Lock()
		res.Failed = append(res.Failed, kind)
		mu.Unlock()
	}
	var g sync.WaitGroup
	run := func(g *sync.WaitGroup, kind string, fn func(context.Context) error) {
		g.Go(func() {
			tctx, cancel := context.WithTimeout(ctx, searchTimeout)
			defer cancel()
			failed(kind, fn(tctx))
		})
	}

	if who.Can(domain.PermStoresRead) && s.d.Directory != nil {
		run(&g, "stores", func(c context.Context) error {
			p, err := s.d.Directory.Search(c, q, "", "", searchPerGroup)
			res.Stores = p.Items
			return err
		})
	}
	if who.Can(domain.PermBillingRead) && s.d.Billing != nil {
		run(&g, "invoices", func(c context.Context) error {
			p, err := s.Invoices(c, billing.InvoiceQuery{Q: q}, "", searchPerGroup)
			res.Invoices = p.Items
			return err
		})
		if !strings.ContainsRune(q, ' ') {
			run(&g, "payments", func(c context.Context) error {
				ps, err := s.d.Billing.PaymentsByReference(c, q)
				res.Payments = ps
				return err
			})
		}
	}
	if who.Can(domain.PermCatalogRead) && s.d.Agencies != nil {
		run(&g, "agencies", func(c context.Context) error {
			all, err := s.d.Agencies.Agencies(c)
			res.Agencies = firstN(all, searchPerGroup, func(a billing.Agency) bool { return contains(a.Name, q) })
			return err
		})
	}
	if who.Can(domain.PermTeamManage) {
		run(&g, "admins", func(c context.Context) error {
			team, err := store.Team(c, s.d.DB)
			res.Admins = firstN(team, searchPerGroup, func(m store.Member) bool { return contains(m.Name, q) || contains(m.Email, q) })
			return err
		})
	}
	if who.Can(domain.PermContactsRead) && s.d.OwnerStores != nil && s.d.Directory != nil {
		if addr, phones, masked, ok := contactOf(q); ok {
			if err := s.audit(ctx, &who.AdminID, "admin.searched_owner", "search", "", map[string]any{"query": masked}); err != nil {
				return SearchResult{}, err
			}
			run(&g, "owners", func(c context.Context) error {
				ids, err := s.d.OwnerStores(c, addr, phones)
				if err != nil {
					return err
				}
				es, err := s.d.Directory.Entries(c, ids)
				res.OwnerStores = es
				return err
			})
		}
	}
	g.Wait()
	slices.Sort(res.Failed)
	res.Jump = s.jump(ctx, who, q, res)
	return res, nil
}

// jump is the path of the one thing q names exactly.
func (s *Service) jump(ctx context.Context, who Identity, q string, res SearchResult) string {
	if invoiceNumber.MatchString(strings.ToUpper(q)) {
		for _, inv := range res.Invoices {
			if strings.EqualFold(inv.Number, q) {
				return "/billing/invoices/" + inv.ID.String()
			}
		}
	}
	if host := strings.ToLower(q); hostLike.MatchString(host) {
		for _, e := range res.Stores {
			if e.PrimaryHost == host {
				return "/stores/" + e.Store.ID.String()
			}
		}
	}
	id, err := ulid.Parse(q)
	if err != nil {
		return ""
	}
	tctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	if who.Can(domain.PermStoresRead) && s.d.Directory != nil {
		if _, _, err = s.d.Directory.Store(tctx, tenant.StoreID{ID: id}); err == nil {
			return "/stores/" + id.String()
		}
	}
	if who.Can(domain.PermBillingRead) && s.d.Billing != nil {
		if _, err = s.d.Billing.Invoice(tctx, id); err == nil {
			return "/billing/invoices/" + id.String()
		}
	}
	return ""
}

// contactOf reads q as an exact email or phone: the normalized email, or
// the E.164 numbers it may be in the platform's countries, and how the
// admin log writes it (masked).
func contactOf(q string) (addr string, phones []string, masked string, ok bool) {
	if strings.ContainsRune(q, '@') {
		if a, valid := email.Normalize(q); valid {
			return a, nil, maskEmail(a), true
		}
		return "", nil, "", false
	}
	seen := map[string]bool{}
	for _, r := range phone.Regions() {
		n, err := phone.Parse(q, r)
		if err != nil || seen[n.Unmasked()] {
			continue
		}
		seen[n.Unmasked()] = true
		phones = append(phones, n.Unmasked())
		if masked == "" {
			masked = n.Masked()
		}
	}
	return "", phones, masked, len(phones) > 0
}

// maskEmail keeps an email's first letter and its domain.
func maskEmail(a string) string {
	local, dom, _ := strings.Cut(a, "@")
	if local == "" {
		return "•••@" + dom
	}
	r, _ := utf8.DecodeRuneInString(local)
	return string(r) + "•••@" + dom
}

func contains(s, q string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(q)) }

func firstN[T any](xs []T, n int, keep func(T) bool) []T {
	out := make([]T, 0, n)
	for _, x := range xs {
		if len(out) == n {
			break
		}
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}
