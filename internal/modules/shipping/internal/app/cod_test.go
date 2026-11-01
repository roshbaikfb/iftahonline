package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// deliver ships an order and has the carrier deliver it.
func (f shipFixture) deliver(t *testing.T) domain.Shipment {
	t.Helper()
	sh := f.ship(t, f.orders.Add(t, "ready_to_ship"))
	if err := f.carrier.Advance(sh.TrackingNumber, carrier.StatusDelivered); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.TrackShipment(f.ctxA, sh.ID); err != nil {
		t.Fatal(err)
	}
	got, _, err := f.svc.Shipment(f.ctxA, sh.ID)
	if err != nil || got.Status != domain.ShipmentDelivered {
		t.Fatalf("delivered: %+v %v", got, err)
	}
	return got
}

func (f shipFixture) report(t *testing.T) app.CODReport {
	t.Helper()
	r, err := f.svc.Report(f.ctxA, time.Now().Add(-30*24*time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f shipFixture) importCSV(t *testing.T, csv string) app.Imported {
	t.Helper()
	got, err := f.svc.ImportStatement(f.ctxA, f.conn.Connection.ID, strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestCODReconciliation: delivered parcels are expected; statement lines
// settle them, partly, fully or more; the rest is held by the carrier
// and aged; lines of unknown parcels are reported; a statement imported
// twice changes nothing.
func TestCODReconciliation(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	p1, p2, p3 := f.deliver(t), f.deliver(t), f.deliver(t)

	r := f.report(t)
	if len(r.Carriers) != 1 {
		t.Fatalf("carriers %+v", r.Carriers)
	}
	c := r.Carriers[0]
	if c.Carrier != fake.Name || c.Delivered != 3 || c.Expected.Amount() != 156000 || !c.Settled.IsZero() ||
		c.Held.Amount() != 156000 || c.Held0to7.Amount() != 156000 || r.UnmatchedLines != 0 {
		t.Fatalf("before any statement: %+v", r)
	}

	stmt := "tracking_number,amount,settled_at,reference\n" +
		p1.TrackingNumber + ",520,2026-10-20,CO-1\n" +
		p2.TrackingNumber + ",300,2026-10-20,CO-1\n" +
		"9999999,100,2026-10-20,CO-1\n" +
		"bad line,,,\n"
	got := f.importCSV(t, stmt)
	if got.Lines != 4 || got.Added.Imported != 3 || got.Added.Matched != 2 || got.Added.Duplicates != 0 || len(got.Errors) != 1 {
		t.Fatalf("import: %+v", got)
	}
	if again := f.importCSV(t, stmt); again.Added.Imported != 0 || again.Added.Duplicates != 3 {
		t.Fatalf("second import of the statement: %+v", again)
	}

	r = f.report(t)
	c = r.Carriers[0]
	if c.Settled.Amount() != 82000 || c.Held.Amount() != 74000 || c.OverSettled != 0 || r.UnmatchedLines != 1 || r.UnmatchedAmount.Amount() != 10000 {
		t.Fatalf("after the statement: %+v", r)
	}
	held, err := f.svc.Held(f.ctxA, nil, 1)
	if err != nil || len(held.Items) != 1 || held.Items[0].ShipmentID != p2.ID || held.Items[0].Settled.Amount() != 30000 || held.Next == nil {
		t.Fatalf("held page 1: %+v %v", held, err)
	}
	if held, err = f.svc.Held(f.ctxA, held.Next, 10); err != nil || len(held.Items) != 1 || held.Items[0].ShipmentID != p3.ID || held.Next != nil {
		t.Fatalf("held page 2: %+v %v", held, err)
	}

	// The carrier pays p1 twice (another payout), and p3 was delivered
	// three weeks ago.
	f.importCSV(t, "tracking,amount,reference\n"+p1.TrackingNumber+",520,CO-2\n")
	if err = postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(f.ctxA, `UPDATE shipments SET status_at = now() - interval '21 days' WHERE id = $1`, p3.ID)
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
	c = f.report(t).Carriers[0]
	if c.OverSettled != 1 || c.Held15Plus.Amount() != 52000 || c.Held0to7.Amount() != 22000 || c.Held.Amount() != 74000 {
		t.Fatalf("over-settled and aged: %+v", c)
	}

	// Store B sees nothing of it and cannot import into A's connection.
	if rb, rbErr := f.svc.Report(f.ctxB, time.Now().Add(-time.Hour), time.Now().Add(time.Hour)); rbErr != nil || len(rb.Carriers) != 0 || rb.UnmatchedLines != 0 {
		t.Fatalf("B's report: %+v %v", rb, rbErr)
	}
	if _, err = f.svc.ImportStatement(f.ctxB, f.conn.Connection.ID, strings.NewReader(stmt)); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Fatalf("B imports into A's connection: %v", err)
	}
	if heldB, heldErr := f.svc.Held(f.ctxB, nil, 0); heldErr != nil || len(heldB.Items) != 0 {
		t.Fatalf("B's held list: %+v %v", heldB, heldErr)
	}
}

func TestCODRefusals(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	if _, err := f.svc.ImportStatement(f.ctxA, f.conn.Connection.ID, strings.NewReader("no,columns\n1,2\n")); !fieldIs(err, "file", "unreadable") {
		t.Errorf("unreadable statement: %v", err)
	}
	if _, err := f.svc.ImportStatement(f.ctxA, ulid.NewGenerator().New(), strings.NewReader("tracking,amount\n1,1\n")); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Errorf("unknown connection: %v", err)
	}
	now := time.Now()
	for name, period := range map[string][2]time.Time{"backwards": {now, now.Add(-time.Hour)}, "empty": {now, now},
		"over a year": {now.Add(-400 * 24 * time.Hour), now}} {
		if _, err := f.svc.Report(f.ctxA, period[0], period[1]); !fieldIs(err, "from", "range") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.svc.Held(f.ctxA, nil, 500); !fieldIs(err, "limit", "range") {
		t.Errorf("held limit: %v", err)
	}
	// A statement for a disconnected connection still imports.
	if err := f.svc.Disconnect(f.ctxA, f.conn.Connection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ImportStatement(f.ctxA, f.conn.Connection.ID, strings.NewReader("tracking,amount\n7000001,1\n")); err != nil {
		t.Fatalf("statement after disconnect: %v", err)
	}
}

// unsupported is a carrier without a settlements API (Bosta).
type unsupported struct{ *fake.Carrier }

func (unsupported) CODSettlements(context.Context, carrier.Credentials, time.Time, time.Time) ([]carrier.Settlement, error) {
	return nil, carrier.ErrUnsupported
}

// TestPullSettlements: carriers with a settlements API are pulled daily
// into the same lines; one without stops the pull; so does a disconnect.
func TestPullSettlements(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	p := f.deliver(t)
	amount, err := money.New(52000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.carrier.Settle(p.TrackingNumber, carrier.Settlement{Amount: amount, SettledAt: time.Now().Add(-time.Hour), Reference: "PAY-1"}); err != nil {
		t.Fatal(err)
	}
	for range 2 { // pulling twice stores the payout once
		again, pullErr := f.svc.PullSettlements(f.ctxA, f.conn.Connection.ID)
		if pullErr != nil || !again {
			t.Fatalf("pull: %v %v", again, pullErr)
		}
	}
	c := f.report(t).Carriers[0]
	if c.Settled.Amount() != 52000 || !c.Held.IsZero() {
		t.Fatalf("after the pull: %+v", c)
	}
	if again, pullErr := f.newService(unsupported{f.carrier}).PullSettlements(f.ctxA, f.conn.Connection.ID); pullErr != nil || again {
		t.Fatalf("a carrier without the API: %v %v", again, pullErr)
	}
	if err = f.svc.Disconnect(f.ctxA, f.conn.Connection.ID); err != nil {
		t.Fatal(err)
	}
	if again, pullErr := f.svc.PullSettlements(f.ctxA, f.conn.Connection.ID); pullErr != nil || again {
		t.Fatalf("after disconnect: %v %v", again, pullErr)
	}
}

// TestCarrierCharges (ADR-0068): what a carrier kept of a settled
// parcel is its COD less what it paid out; unsettled parcels are not
// known; another store's orders are never found.
func TestCarrierCharges(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	p1, p2 := f.deliver(t), f.deliver(t)
	f.importCSV(t, "tracking_number,amount,settled_at,reference\n"+p1.TrackingNumber+",470,2026-10-20,CO-1\n")
	got, err := f.svc.CarrierCharges(f.ctxA, []ulid.ID{p1.OrderID, p2.OrderID, ulid.NewGenerator().New()})
	if err != nil || len(got) != 1 || got[p1.OrderID].Amount() != p1.COD.Amount()-47000 {
		t.Fatalf("charges = %+v, %v (COD %v)", got, err, p1.COD)
	}
	if got, err = f.svc.CarrierCharges(f.ctxB, []ulid.ID{p1.OrderID}); err != nil || len(got) != 0 {
		t.Fatalf("store B's charges = %+v, %v", got, err)
	}
	if _, err = f.svc.CarrierCharges(f.ctxA, make([]ulid.ID, app.MaxChargesOrders+1)); err == nil {
		t.Fatal("too many orders must fail")
	}
}
