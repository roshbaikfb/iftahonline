package app_test

import (
	"net/netip"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// signalsOf reads an order's signals row straight from its table; ok is
// false when there is none.
func (f fixture) signalsOf(t *testing.T, o domain.Order) (s domain.Signals, rows int) {
	t.Helper()
	if err := postgres.InTenantTx(f.shopperA, f.pool, func(tx pgx.Tx) error {
		r, err := tx.Query(f.shopperA, `SELECT fbp, fbc, ttp, ttclid, sccid, gclid, ip, user_agent FROM order_signals
			WHERE order_created_at = $1 AND order_id = $2`, o.CreatedAt, o.ID)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var ip *netip.Addr
			if err = r.Scan(&s.FBP, &s.FBC, &s.TTP, &s.TTCLID, &s.ScCID, &s.GCLID, &ip, &s.UserAgent); err != nil {
				return err
			}
			if ip != nil {
				s.IP = ip.String()
			}
			rows++
		}
		return r.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return s, rows
}

// TestPlaceOrderAttribution (ADR-0043): an order keeps the campaign that
// brought it, shown on the order, and its signals, written once with it;
// a retry writes none again; an order without signals has no row.
func TestPlaceOrderAttribution(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tee := f.product(t, f.storeA, "تيشيرت قطن", 15000, 10)
	in := newOrder("k-attr", domain.LineInput{VariantID: tee, Qty: 1})
	in.Attribution = domain.Attribution{Source: " facebook ", Medium: "paid_social", Campaign: "رمضان", Content: "ad-7", Term: ""}
	in.Signals = domain.Signals{FBP: "fb.1.1730000000000.123", FBC: "fb.1.1730000000000.IwAR0", TTP: "ttp-1", TTCLID: "E.C.P.1",
		ScCID: "sc-1", GCLID: "Cj0", IP: "::ffff:203.0.113.7", UserAgent: "Mozilla/5.0"}
	p, err := f.svc.PlaceOrder(f.shopperA, in)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Attribution{Source: "facebook", Medium: "paid_social", Campaign: "رمضان", Content: "ad-7"}
	if p.Order.Attribution != want {
		t.Fatalf("placed attribution %+v", p.Order.Attribution)
	}
	d, err := f.svc.GetDetail(f.ctxA, p.Order.ID)
	if err != nil || d.Order.Attribution != want {
		t.Fatalf("read back %+v %v", d.Order.Attribution, err)
	}
	s, n := f.signalsOf(t, p.Order)
	if n != 1 || s.FBP != in.Signals.FBP || s.GCLID != "Cj0" || s.IP != "203.0.113.7" || s.UserAgent != "Mozilla/5.0" || s.ScCID != "sc-1" {
		t.Fatalf("signals %+v (%d rows)", s, n)
	}

	// The same submit again returns the order and writes nothing more.
	again, err := f.svc.PlaceOrder(f.shopperA, in)
	if err != nil || !again.Replayed || again.Order.ID != p.Order.ID {
		t.Fatalf("replay %+v %v", again, err)
	}
	if _, n = f.signalsOf(t, p.Order); n != 1 {
		t.Fatalf("%d signal rows after a retry", n)
	}

	plain, err := f.svc.PlaceOrder(f.shopperA, newOrder("k-plain", domain.LineInput{VariantID: tee, Qty: 3}))
	if err != nil {
		t.Fatal(err)
	}
	if _, n = f.signalsOf(t, plain.Order); n != 0 || !plain.Order.Attribution.IsZero() {
		t.Fatalf("an order without signals: %d rows, attribution %+v", n, plain.Order.Attribution)
	}
	if _, n = f.signalsOf(t, domain.Order{ID: ulid.NewGenerator().New(), CreatedAt: p.Order.CreatedAt}); n != 0 {
		t.Fatal("signals for an order that does not exist")
	}
}
