// Package domain holds the analytics module's rules (ADR-0069): what an
// order earned the merchant, from its own facts, the store's cost
// estimates and its carriers' real charges. It imports only the standard
// library and internal/platform (CLAUDE.md rule 3).
//
// Amounts are int64 minor units of the store's currency (CLAUDE.md
// rule 7); a store sells in one currency.
package domain

import (
	"errors"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// State is where an order stands for profit.
type State string

// States.
const (
	Open      State = "open"      // not yet delivered or back: counted, not earned
	Delivered State = "delivered" // the customer took it
	Returned  State = "returned"  // it came back (or the courier could not deliver)
	Cancelled State = "cancelled" // cancelled before it shipped: nothing earned, nothing spent
)

// StateOf is the profit state of an order in the orders module's status.
func StateOf(status string) State {
	switch status {
	case "delivered":
		return Delivered
	case "returned", "failed_delivery":
		return Returned
	case "cancelled":
		return Cancelled
	}
	return Open
}

// Line is one order line as profit needs it.
type Line struct {
	ProductID string
	Title     string
	Qty       int
	Revenue   int64  // what the customer pays for the line, after its share of the discount
	Cost      *int64 // what the line cost the merchant; nil when not known
}

// Order is an order's facts: what the orders and shipping modules say of
// it, kept by analytics.
type Order struct {
	ID          string
	Day         string // the store-local day it was placed, YYYY-MM-DD
	State       State
	Governorate string
	Campaign    string // utm_campaign, "" without one
	Revenue     int64  // subtotal less the discount
	Shipping    int64  // the delivery rate the customer was charged
	CODFee      int64  // the COD fee the customer was charged
	OnDelivery  int64  // what the courier collects (the COD the carrier's fee is on)
	DepositPaid int64  // a prepaid delivery the merchant keeps (ADR-0067); 0 without one
	// CarrierCharge is what the carrier kept of the parcel, from its
	// payouts (ADR-0068); nil while not settled (then estimated).
	CarrierCharge *int64
	Lines         []Line
}

// Settings are the store's own estimates of delivery costs, used until a
// carrier's payout tells the real charge. Nil: the delivery rate the
// customer was charged.
type Settings struct {
	DeliveryCost *int64 // a delivered parcel
	ReturnCost   *int64 // bringing a parcel back
	CODFeeBP     int64  // the carrier's fee on cash collected, in basis points (1% = 100)
}

// Validate checks merchant input.
func (s Settings) Validate() error {
	var fields []errs.FieldError
	if s.DeliveryCost != nil && (*s.DeliveryCost < 0 || *s.DeliveryCost > maxMinor) {
		fields = append(fields, errs.FieldError{Field: "delivery_cost_minor", Code: "range", Message: "between 0 and 1000000000000"})
	}
	if s.ReturnCost != nil && (*s.ReturnCost < 0 || *s.ReturnCost > maxMinor) {
		fields = append(fields, errs.FieldError{Field: "return_cost_minor", Code: "range", Message: "between 0 and 1000000000000"})
	}
	if s.CODFeeBP < 0 || s.CODFeeBP > 10000 {
		fields = append(fields, errs.FieldError{Field: "cod_fee_bp", Code: "range", Message: "between 0 and 10000 (100%)"})
	}
	if len(fields) > 0 {
		return errs.Validation(fields...)
	}
	return nil
}

const maxMinor = 1_000_000_000_000

// Figures are what orders earned and cost, summed.
type Figures struct {
	Orders, Delivered, Returned, Cancelled, Open int
	Revenue                                      int64 // delivered orders' items, plus deposits kept on returns
	DeliveryPaid                                 int64 // what delivered orders' customers paid for delivery and the COD fee
	ProductCost                                  int64
	ShippingCost                                 int64 // delivering, by the carrier's charge or the estimate
	CODFees                                      int64 // the carrier's fee on cash collected (estimates only: a real charge includes it)
	ReturnCost                                   int64
	AdSpend                                      int64
	// CostMissing counts delivered orders with a line of unknown cost:
	// their profit is too high by that cost.
	CostMissing int
}

// Profit is what the figures leave the merchant.
func (f Figures) Profit() int64 {
	return f.Revenue + f.DeliveryPaid - f.ProductCost - f.ShippingCost - f.CODFees - f.ReturnCost - f.AdSpend
}

// Add sums g into f.
func (f *Figures) Add(g Figures) {
	f.Orders += g.Orders
	f.Delivered += g.Delivered
	f.Returned += g.Returned
	f.Cancelled += g.Cancelled
	f.Open += g.Open
	f.Revenue += g.Revenue
	f.DeliveryPaid += g.DeliveryPaid
	f.ProductCost += g.ProductCost
	f.ShippingCost += g.ShippingCost
	f.CODFees += g.CODFees
	f.ReturnCost += g.ReturnCost
	f.AdSpend += g.AdSpend
	f.CostMissing += g.CostMissing
}

// OfOrder is what one order earned and cost, under the store's
// settings:
//   - delivered: its items and what the customer paid for delivery, less
//     its product cost, the delivery (the carrier's real charge, or the
//     estimate plus the COD fee estimate);
//   - returned: the deposit kept, less the delivery both ways (the
//     carrier's charge, or both estimates); the goods come back;
//   - cancelled and open: counted, nothing earned or spent yet.
func OfOrder(o Order, s Settings) Figures {
	f := Figures{Orders: 1}
	switch o.State {
	case Open:
		f.Open = 1
	case Cancelled:
		f.Cancelled = 1
	case Delivered:
		f.Delivered = 1
		f.Revenue = o.Revenue
		f.DeliveryPaid = o.Shipping + o.CODFee
		for _, l := range o.Lines {
			if l.Cost == nil {
				f.CostMissing = 1
				continue
			}
			f.ProductCost += *l.Cost
		}
		if o.CarrierCharge != nil {
			f.ShippingCost = *o.CarrierCharge
		} else {
			f.ShippingCost = or(s.DeliveryCost, o.Shipping)
			f.CODFees = o.OnDelivery * s.CODFeeBP / 10000
		}
	case Returned:
		f.Returned = 1
		f.Revenue = o.DepositPaid
		if o.CarrierCharge != nil {
			f.ReturnCost = *o.CarrierCharge
		} else {
			f.ReturnCost = or(s.DeliveryCost, o.Shipping) + or(s.ReturnCost, o.Shipping)
		}
	}
	return f
}

func or(p *int64, def int64) int64 {
	if p != nil {
		return *p
	}
	return def
}

// Dimension is what a breakdown groups orders by.
type Dimension string

// Dimensions.
const (
	ByTotal       Dimension = "total"
	ByProduct     Dimension = "product"
	ByCampaign    Dimension = "campaign"
	ByGovernorate Dimension = "governorate"
)

// ParseDimension validates the `by` of a request.
func ParseDimension(s string) (Dimension, error) {
	switch d := Dimension(s); d {
	case ByProduct, ByCampaign, ByGovernorate:
		return d, nil
	}
	return "", errs.Validation(errs.FieldError{Field: "by", Code: "invalid", Message: fmt.Sprintf("%q: use product, campaign or governorate", s)})
}

// Row is one key's figures of one day in one dimension.
type Row struct {
	Dim   Dimension
	Key   string // a product ID, a campaign ("" for none) or a governorate code; "" for the total
	Label string // a product's title
	Figures
}

// DayRows breaks one day's orders down by every dimension: the total,
// each product (its lines' share of the order: revenue and cost; an
// order counts once per product), each campaign and each governorate.
// Ad spend goes to the total and to its campaign; adSpend maps campaign
// names to the day's spend.
func DayRows(orders []Order, s Settings, adSpend map[string]int64) []Row {
	total := Row{Dim: ByTotal}
	camps := map[string]*Row{}
	govs := map[string]*Row{}
	prods := map[string]*Row{}
	var keys struct{ camps, govs, prods []string }
	row := func(m map[string]*Row, order *[]string, dim Dimension, key string) *Row {
		r, ok := m[key]
		if !ok {
			r = &Row{Dim: dim, Key: key}
			m[key] = r
			*order = append(*order, key)
		}
		return r
	}
	for _, o := range orders {
		f := OfOrder(o, s)
		total.Add(f)
		row(camps, &keys.camps, ByCampaign, o.Campaign).Add(f)
		row(govs, &keys.govs, ByGovernorate, o.Governorate).Add(f)
		for _, pf := range byProduct(o, f) {
			r := row(prods, &keys.prods, ByProduct, pf.Key)
			r.Label = pf.Label
			r.Add(pf.Figures)
		}
	}
	for c, amount := range adSpend {
		total.AdSpend += amount
		row(camps, &keys.camps, ByCampaign, c).AdSpend += amount
	}
	out := []Row{total}
	for _, set := range []struct {
		m    map[string]*Row
		keys []string
	}{{camps, keys.camps}, {govs, keys.govs}, {prods, keys.prods}} {
		for _, k := range set.keys {
			out = append(out, *set.m[k])
		}
	}
	return out
}

// byProduct splits an order's figures over its products: revenue and
// cost by line; delivery, fees and returns by each product's share of
// the order's revenue (evenly when the order has none).
func byProduct(o Order, f Figures) []Row {
	type acc struct {
		label         string
		revenue, cost int64
		missing       bool
	}
	var order []string
	m := map[string]*acc{}
	for _, l := range o.Lines {
		a, ok := m[l.ProductID]
		if !ok {
			a = &acc{label: l.Title}
			m[l.ProductID] = a
			order = append(order, l.ProductID)
		}
		a.revenue += l.Revenue
		if l.Cost == nil {
			a.missing = true
		} else {
			a.cost += *l.Cost
		}
	}
	out := make([]Row, 0, len(order))
	n := int64(len(order))
	for _, id := range order {
		a := m[id]
		share := func(v int64) int64 {
			if o.Revenue > 0 {
				return v * a.revenue / o.Revenue
			}
			return v / n
		}
		r := Row{Dim: ByProduct, Key: id, Label: a.label, Figures: Figures{Orders: 1,
			Delivered: f.Delivered, Returned: f.Returned, Cancelled: f.Cancelled, Open: f.Open}}
		if f.Delivered == 1 {
			r.Revenue, r.ProductCost = a.revenue, a.cost
			if a.missing {
				r.CostMissing = 1
			}
		}
		r.DeliveryPaid, r.ShippingCost, r.CODFees, r.ReturnCost = share(f.DeliveryPaid), share(f.ShippingCost), share(f.CODFees), share(f.ReturnCost)
		if f.Returned == 1 {
			r.Revenue = share(f.Revenue)
		}
		out = append(out, r)
	}
	fixRemainders(out, f)
	return out
}

func sumOf(rs []Row, get func(Row) int64) int64 {
	var n int64
	for _, r := range rs {
		n += get(r)
	}
	return n
}

// fixRemainders gives the last product what integer division left, so
// products sum to the order exactly.
func fixRemainders(rs []Row, f Figures) {
	if len(rs) == 0 {
		return
	}
	last := &rs[len(rs)-1]
	last.DeliveryPaid += f.DeliveryPaid - sumOf(rs, func(r Row) int64 { return r.DeliveryPaid })
	last.ShippingCost += f.ShippingCost - sumOf(rs, func(r Row) int64 { return r.ShippingCost })
	last.CODFees += f.CODFees - sumOf(rs, func(r Row) int64 { return r.CODFees })
	last.ReturnCost += f.ReturnCost - sumOf(rs, func(r Row) int64 { return r.ReturnCost })
	if f.Returned == 1 {
		last.Revenue += f.Revenue - sumOf(rs, func(r Row) int64 { return r.Revenue })
	}
}

// ErrRange is a report range that is not 1–366 days.
var ErrRange = errors.New("analytics: range out of bounds")
