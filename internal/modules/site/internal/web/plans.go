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

// Plans is billing's read of the plans on sale.
type Plans interface {
	PublicOffers(ctx context.Context, currency string) (billing.Offers, error)
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

// buildPricing turns billing's offers into what the pages show.
func buildPricing(o billing.Offers) pricing {
	p := pricing{Currency: o.Currency}
	recommended := false
	for _, of := range o.Plans {
		v := planView{Name: of.NameAr, Free: of.Kind == billing.KindFree, Pays: of.Kind == billing.KindPays, TrialDays: int(of.TrialDays), Grants: of.Grants}
		if of.Monthly != nil {
			v.Monthly, v.MonthlyAt = price(*of.Monthly, o.Currency), amount(*of.Monthly)
		}
		if of.Annual != nil {
			v.Annual = price(*of.Annual, o.Currency)
		}
		if of.Fee != nil {
			v.Fee = percent(of.Fee.RateBps)
		}
		v.Priced = v.Free || v.Monthly != "" || v.Fee != ""
		if v.Priced && !v.Free {
			p.AnyPrice = true
		}
		if of.Kind == billing.KindSubscription && !recommended {
			v.Recommended, recommended = true, true
		}
		v.Lines = lines(of.Grants)
		p.Plans = append(p.Plans, v)
	}
	for _, f := range o.Features {
		r := row{Name: f.NameAr}
		for _, pl := range p.Plans {
			r.Cells = append(r.Cells, cell{Text: grantText(pl.Grants[f.Key], string(f.Kind))})
		}
		p.Rows = append(p.Rows, r)
	}
	return p
}

// lines are a plan's card bullets, from its grants.
func lines(g billing.Grants) []string {
	var out []string
	count := func(key, one, unlimited string) {
		x, ok := g[key]
		switch {
		case !ok:
		case x.Unlimited:
			out = append(out, unlimited)
		case x.Value > 0:
			out = append(out, number(x.Value)+" "+one)
		}
	}
	count("products", "منتج", "منتجات بلا حد")
	count("orders_per_month", "طلب في الشهر", "طلبات بلا حد")
	count("messages", "رسالة واتساب في الشهر", "رسائل واتساب بلا حد")
	on := func(key, text string) {
		if x, ok := g[key]; ok && (x.Unlimited || x.Value > 0) {
			out = append(out, text)
		}
	}
	on("risk_rules", "الحماية من الطلبات الوهمية")
	on("profit", "لوحة الربح الصافي")
	on("card_payments", "الدفع بالبطاقة")
	on("custom_domains", "دومين خاص بك")
	count("staff", "من فريق العمل", "فريق عمل بلا حد")
	return out
}

// grantText is one cell of the comparison table.
func grantText(g billing.Grant, kind string) string {
	switch {
	case g.Unlimited:
		return "بلا حد"
	case g.Value <= 0:
		return "—"
	case kind == "flag":
		return "نعم"
	default:
		return number(g.Value)
	}
}

// price is an amount in minor units, as the site writes it: "299 ج.م",
// "$29", "$29.50".
func price(minor int64, currency string) string {
	whole, cents := minor/100, minor%100
	s := number(whole)
	if cents != 0 {
		s += "." + fmt.Sprintf("%02d", cents)
	}
	switch currency {
	case currencyEGP:
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

// percent writes basis points as a percentage: 250 → "2.5٪".
func percent(bps int32) string {
	s := strconv.FormatFloat(float64(bps)/100, 'f', -1, 64)
	return s + "٪"
}
