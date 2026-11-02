package web

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
)

// Currencies the site prices in: pounds in Egypt, dollars elsewhere.
const (
	currencyEGP = "EGP"
	currencyUSD = "USD"
)

// currencyFor is the currency of a visitor from country (ISO 3166-1
// alpha-2, as the CDN says it): pounds for Egypt and for a visitor whose
// country is not known (the main market), dollars for any other.
func currencyFor(country string) string {
	c := strings.ToUpper(strings.TrimSpace(country))
	if c == "" || c == "EG" || c == "XX" || c == "T1" { // XX: unknown, T1: Tor (Cloudflare)
		return currencyEGP
	}
	return currencyUSD
}

// plansTTL is how long the plans read from billing are kept: the console
// changes them rarely, and a change shows within this.
const plansTTL = 5 * time.Minute

// planCache keeps the plans on sale per currency (two entries at most).
type planCache struct {
	src Plans
	now func() time.Time
	mu  sync.Mutex
	got map[string]cachedOffers
}

type cachedOffers struct {
	offers billing.Offers
	at     time.Time
}

// Plans is billing's read of the plans on sale, and of the founding
// program's seats.
type Plans interface {
	PublicOffers(ctx context.Context, currency string) (billing.Offers, error)
	FoundingSeats(ctx context.Context) (billing.FoundingSeats, error)
}

// seatsCache keeps the founding program's seats a minute (the counter is
// the edge's lifetime behind anyway).
type seatsCache struct {
	src   Plans
	now   func() time.Time
	mu    sync.Mutex
	got   billing.FoundingSeats
	until time.Time
}

func (c *seatsCache) get(ctx context.Context) (billing.FoundingSeats, error) {
	c.mu.Lock()
	if c.now().Before(c.until) {
		got := c.got
		c.mu.Unlock()
		return got, nil
	}
	c.mu.Unlock()
	s, err := c.src.FoundingSeats(ctx)
	if err != nil {
		return billing.FoundingSeats{}, fmt.Errorf("site: founding seats: %w", err)
	}
	c.mu.Lock()
	c.got, c.until = s, c.now().Add(time.Minute)
	c.mu.Unlock()
	return s, nil
}

func (c *planCache) get(ctx context.Context, currency string) (billing.Offers, error) {
	c.mu.Lock()
	if e, ok := c.got[currency]; ok && c.now().Sub(e.at) < plansTTL {
		c.mu.Unlock()
		return e.offers, nil
	}
	c.mu.Unlock()
	o, err := c.src.PublicOffers(ctx, currency)
	if err != nil {
		return billing.Offers{}, fmt.Errorf("site: plans: %w", err)
	}
	c.mu.Lock()
	c.got[currency] = cachedOffers{offers: o, at: c.now()}
	c.mu.Unlock()
	return o, nil
}

// planView is a plan as the site shows it.
type planView struct {
	Name        string
	Free        bool
	Pays        bool
	Recommended bool // the first subscription plan: «موصى بها», never a claim of popularity
	Priced      bool // sold in the visitor's currency (or free)
	Monthly     string
	Annual      string
	MonthlyAt   string // the monthly price as schema.org writes it: "299", "29.50"
	Fee         string // pay as you sell: the rate, "2.5٪"
	FreeText    string // nothing, in the currency: "0 ج.م", "$0"
	TrialDays   int
	Lines       []string
	Grants      billing.Grants
}

// cell is one plan's value of a feature in the comparison table.
type cell struct{ Text string }

// row is one feature across the plans.
type row struct {
	Name  string
	Cells []cell
}

// pricing is the plans in one currency, with the comparison table.
type pricing struct {
	Currency string
	Plans    []planView
	Rows     []row
	AnyPrice bool // at least one paid plan is priced in Currency
}

// buildPricing turns billing's offers into what the pages show, in
// language l.
func buildPricing(o billing.Offers, l *language) pricing {
	p := pricing{Currency: o.Currency}
	en := l == english
	recommended := false
	for _, of := range o.Plans {
		v := planView{Name: of.NameAr, Free: of.Kind == billing.KindFree, Pays: of.Kind == billing.KindPays, TrialDays: int(of.TrialDays), Grants: of.Grants,
			FreeText: price(0, o.Currency, en)}
		if en && of.NameEn != "" {
			v.Name = of.NameEn
		}
		if of.Monthly != nil {
			v.Monthly, v.MonthlyAt = price(*of.Monthly, o.Currency, en), amount(*of.Monthly)
		}
		if of.Annual != nil {
			v.Annual = price(*of.Annual, o.Currency, en)
		}
		if of.Fee != nil {
			v.Fee = percent(of.Fee.RateBps, en)
		}
		v.Priced = v.Free || v.Monthly != "" || v.Fee != ""
		if v.Priced && !v.Free {
			p.AnyPrice = true
		}
		if of.Kind == billing.KindSubscription && !recommended {
			v.Recommended, recommended = true, true
		}
		v.Lines = lines(of.Grants, l.t)
		p.Plans = append(p.Plans, v)
	}
	for _, f := range o.Features {
		r := row{Name: f.NameAr}
		if en && f.NameEn != "" {
			r.Name = f.NameEn
		}
		for _, pl := range p.Plans {
			r.Cells = append(r.Cells, cell{Text: grantText(pl.Grants[f.Key], string(f.Kind), l.t)})
		}
		p.Rows = append(p.Rows, r)
	}
	return p
}

// lines are a plan's card bullets, from its grants.
func lines(g billing.Grants, t *texts) []string {
	var out []string
	count := func(key, many, unlimited string) {
		x, ok := g[key]
		switch {
		case !ok:
		case x.Unlimited:
			out = append(out, unlimited)
		case x.Value == 1 && t.Singular[many] != "":
			out = append(out, "1 "+t.Singular[many])
		case x.Value > 0:
			out = append(out, number(x.Value)+" "+many)
		}
	}
	count("products", t.Products, t.ProductsAll)
	count("orders_per_month", t.Orders, t.OrdersAll)
	count("messages", t.Messages, t.MessagesAll)
	on := func(key, text string) {
		if x, ok := g[key]; ok && (x.Unlimited || x.Value > 0) {
			out = append(out, text)
		}
	}
	on("risk_rules", t.Protection)
	on("profit", t.Profit)
	on("card_payments", t.Cards)
	on("custom_domains", t.Domain)
	count("staff", t.Staff, t.StaffAll)
	return out
}

// grantText is one cell of the comparison table.
func grantText(g billing.Grant, kind string, t *texts) string {
	switch {
	case g.Unlimited:
		return t.Unlimited
	case g.Value <= 0:
		return "—"
	case kind == "flag":
		return t.Yes
	default:
		return number(g.Value)
	}
}

// price is an amount in minor units, as the site writes it: "299 ج.م"
// (in English "EGP 299"), "$29", "$29.50".
func price(minor int64, currency string, en bool) string {
	whole, cents := minor/100, minor%100
	s := number(whole)
	if cents != 0 {
		s += "." + fmt.Sprintf("%02d", cents)
	}
	switch currency {
	case currencyEGP:
		if en {
			return "EGP " + s
		}
		return s + " ج.م"
	case currencyUSD:
		return "$" + s
	default:
		return s + " " + currency
	}
}

// amount is minor units as a plain decimal: 29900 → "299", 2950 → "29.50".
func amount(minor int64) string {
	s := strconv.FormatInt(minor/100, 10)
	if c := minor % 100; c != 0 {
		s += "." + fmt.Sprintf("%02d", c)
	}
	return s
}

// number writes n with thousands separators: 1,500.
func number(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// percent writes basis points as a percentage: 250 → "2.5٪" ("2.5%"
// in English).
func percent(bps int32, en bool) string {
	s := strconv.FormatFloat(float64(bps)/100, 'f', -1, 64)
	if en {
		return s + "%"
	}
	return s + "٪"
}
