package domain_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func p(v int64) *int64 { return &v }

// watch is a delivered order: 450 of items (a 300 watch costing 120 and
// a 150 strap of unknown cost), 50 delivery and a 15 COD fee paid.
func watch() domain.Order {
	return domain.Order{ID: "o1", Day: "2026-11-01", State: domain.Delivered, Governorate: "EG-C", Campaign: "eid",
		Revenue: 45000, Shipping: 5000, CODFee: 1500, OnDelivery: 51500,
		Lines: []domain.Line{
			{ProductID: "watch", Title: "ساعة", Qty: 1, Revenue: 30000, Cost: p(12000)},
			{ProductID: "strap", Title: "سوار", Qty: 1, Revenue: 15000},
		}}
}

func TestOfOrder(t *testing.T) {
	t.Parallel()
	returned := watch()
	returned.State, returned.DepositPaid = domain.Returned, 6500
	settled := watch()
	settled.CarrierCharge = p(6200)
	tests := []struct {
		name   string
		o      domain.Order
		s      domain.Settings
		want   domain.Figures
		profit int64
	}{
		{"delivered, estimated from what the customer paid", watch(), domain.Settings{CODFeeBP: 100},
			domain.Figures{Orders: 1, Delivered: 1, Revenue: 45000, DeliveryPaid: 6500, ProductCost: 12000, ShippingCost: 5000, CODFees: 515, CostMissing: 1},
			45000 + 6500 - 12000 - 5000 - 515},
		{"delivered, the store's own estimate", watch(), domain.Settings{DeliveryCost: p(4000)},
			domain.Figures{Orders: 1, Delivered: 1, Revenue: 45000, DeliveryPaid: 6500, ProductCost: 12000, ShippingCost: 4000, CostMissing: 1},
			45000 + 6500 - 12000 - 4000},
		{"delivered, the carrier's real charge (fees included)", settled, domain.Settings{DeliveryCost: p(4000), CODFeeBP: 100},
			domain.Figures{Orders: 1, Delivered: 1, Revenue: 45000, DeliveryPaid: 6500, ProductCost: 12000, ShippingCost: 6200, CostMissing: 1},
			45000 + 6500 - 12000 - 6200},
		{"returned, both ways estimated, the deposit kept", returned, domain.Settings{ReturnCost: p(3000)},
			domain.Figures{Orders: 1, Returned: 1, Revenue: 6500, ReturnCost: 5000 + 3000},
			6500 - 8000},
		{"cancelled", domain.Order{State: domain.Cancelled, Revenue: 45000}, domain.Settings{},
			domain.Figures{Orders: 1, Cancelled: 1}, 0},
		{"open", domain.Order{State: domain.Open, Revenue: 45000}, domain.Settings{},
			domain.Figures{Orders: 1, Open: 1}, 0},
	}
	for _, tt := range tests {
		got := domain.OfOrder(tt.o, tt.s)
		if got != tt.want || got.Profit() != tt.profit {
			t.Errorf("%s:\n got %+v (profit %d)\nwant %+v (profit %d)", tt.name, got, got.Profit(), tt.want, tt.profit)
		}
	}
}

func TestStateOf(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]domain.State{
		"pending": domain.Open, "confirmed": domain.Open, "ready_to_ship": domain.Open, "shipped": domain.Open,
		"delivered": domain.Delivered, "returned": domain.Returned, "cancelled": domain.Cancelled,
		// The courier tries again: not a return until it comes back (P8 review).
		"failed_delivery": domain.Open,
	} {
		if got := domain.StateOf(status); got != want {
			t.Errorf("%s = %s, want %s", status, got, want)
		}
	}
}

// TestDayRows: one day's orders broken down by every dimension; products
// sum to the order exactly; ad spend lands on its campaign and the total.
func TestDayRows(t *testing.T) {
	t.Parallel()
	other := watch()
	other.ID, other.Campaign, other.Governorate = "o2", "", "EG-ALX"
	other.Lines = other.Lines[:1]
	other.Revenue = 30000
	rows := domain.DayRows([]domain.Order{watch(), other}, domain.Settings{CODFeeBP: 100}, map[string]int64{"eid": 20000, "summer": 5000})
	byKey := map[domain.Dimension]map[string]domain.Row{}
	for _, r := range rows {
		if byKey[r.Dim] == nil {
			byKey[r.Dim] = map[string]domain.Row{}
		}
		byKey[r.Dim][r.Key] = r
	}
	total := byKey[domain.ByTotal][""]
	if total.Orders != 2 || total.Delivered != 2 || total.AdSpend != 25000 || total.ProductCost != 24000 || total.CostMissing != 1 {
		t.Fatalf("total = %+v", total)
	}
	if eid := byKey[domain.ByCampaign]["eid"]; eid.Orders != 1 || eid.AdSpend != 20000 || eid.Profit() != domain.OfOrder(watch(), domain.Settings{CODFeeBP: 100}).Profit()-20000 {
		t.Fatalf("eid = %+v", eid)
	}
	if summer := byKey[domain.ByCampaign]["summer"]; summer.Orders != 0 || summer.AdSpend != 5000 || summer.Profit() != -5000 {
		t.Fatalf("a campaign with spend and no orders = %+v", summer)
	}
	if none := byKey[domain.ByCampaign][""]; none.Orders != 1 {
		t.Fatalf("no campaign = %+v", none)
	}
	if g := byKey[domain.ByGovernorate]; g["EG-C"].Orders != 1 || g["EG-ALX"].Orders != 1 {
		t.Fatalf("governorates = %+v", g)
	}
	w, s := byKey[domain.ByProduct]["watch"], byKey[domain.ByProduct]["strap"]
	if w.Orders != 2 || w.Label != "ساعة" || w.Revenue != 60000 || w.ProductCost != 24000 || s.CostMissing != 1 || w.CostMissing != 0 {
		t.Fatalf("products: %+v / %+v", w, s)
	}
	// Products split each order's delivery exactly: 2/3 and 1/3 of the
	// first order's, all of the second's.
	var shipping, paid int64
	for _, r := range byKey[domain.ByProduct] {
		shipping += r.ShippingCost
		paid += r.DeliveryPaid
	}
	if shipping != total.ShippingCost || paid != total.DeliveryPaid {
		t.Fatalf("product shares %d/%d, want the total's %d/%d", shipping, paid, total.ShippingCost, total.DeliveryPaid)
	}
	var profit int64
	for _, r := range byKey[domain.ByProduct] {
		profit += r.Profit()
	}
	if profit != total.Profit()+total.AdSpend { // ad spend is not a product's
		t.Fatalf("products' profit %d, want %d", profit, total.Profit()+total.AdSpend)
	}
}

// TestDayRowsOddShares: integer shares never lose a minor unit.
func TestDayRowsOddShares(t *testing.T) {
	t.Parallel()
	o := domain.Order{State: domain.Delivered, Revenue: 3, Shipping: 100, Lines: []domain.Line{
		{ProductID: "a", Revenue: 1, Cost: p(0)}, {ProductID: "b", Revenue: 1, Cost: p(0)}, {ProductID: "c", Revenue: 1, Cost: p(0)},
	}}
	var sum int64
	for _, r := range domain.DayRows([]domain.Order{o}, domain.Settings{}, nil) {
		if r.Dim == domain.ByProduct {
			sum += r.ShippingCost
		}
	}
	if sum != 100 {
		t.Fatalf("shares sum to %d, want 100", sum)
	}
	free := domain.Order{State: domain.Returned, Shipping: 99, Lines: []domain.Line{{ProductID: "a"}, {ProductID: "b"}}}
	sum = 0
	for _, r := range domain.DayRows([]domain.Order{free}, domain.Settings{}, nil) {
		if r.Dim == domain.ByProduct {
			sum += r.ReturnCost
		}
	}
	if sum != 198 {
		t.Fatalf("an order without revenue: shares sum to %d, want 198", sum)
	}
}

func TestSettingsAndDimension(t *testing.T) {
	t.Parallel()
	if err := (domain.Settings{DeliveryCost: p(5000), ReturnCost: p(0), CODFeeBP: 250}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []domain.Settings{{DeliveryCost: p(-1)}, {ReturnCost: p(-1)}, {CODFeeBP: 10001}, {CODFeeBP: -1}} {
		if err := bad.Validate(); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	for _, ok := range []string{"product", "campaign", "governorate"} {
		if _, err := domain.ParseDimension(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	if _, err := domain.ParseDimension("total"); !errs.Is(err, errs.KindValidation) {
		t.Errorf("total is not a breakdown: %v", err)
	}
}
