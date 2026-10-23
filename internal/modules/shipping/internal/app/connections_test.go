package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

const merchantKey = "merchant-secret-api-key-123"

func fieldIs(err error, field, code string) bool {
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		return false
	}
	for _, fe := range e.Fields {
		if fe.Field == field && fe.Code == code {
			return true
		}
	}
	return false
}

// TestConnect: a working key is stored sealed, never in the clear; the
// webhook secret is shown once and only its hash kept; jobs open the
// credentials back.
func TestConnect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service()
	storeA, _ := tenant.FromContext(f.ctxA)

	c, err := svc.Connect(f.ctxA, fake.Name, "  "+merchantKey+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Connection.Carrier != fake.Name || c.Connection.Status != "active" || len(c.WebhookSecret) < 40 ||
		c.WebhookURL != "https://app.example.com/v1/webhooks/fake/"+storeA.String()+"/"+c.Connection.ID.String() {
		t.Fatalf("connected: %+v", c)
	}
	var creds, hash []byte
	if err = postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctxA, `SELECT credentials, webhook_secret_hash FROM carrier_connections WHERE id = $1`, c.Connection.ID).Scan(&creds, &hash)
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(creds), merchantKey) || strings.Contains(string(hash), c.WebhookSecret) {
		t.Fatal("the key or the secret is stored in the clear")
	}
	adapter, got, err := svc.Credentials(f.ctxA, c.Connection.ID)
	if err != nil || adapter.Name() != fake.Name || got.APIKey != merchantKey {
		t.Fatalf("credentials: %v %+v %v", adapter, got, err)
	}
	if err = svc.VerifyWebhook(f.ctxA, fake.Name, c.Connection.ID, c.WebhookSecret); err != nil {
		t.Fatalf("right secret: %v", err)
	}
	for _, wrong := range []string{"", "x", c.WebhookSecret + "x", strings.ToUpper(c.WebhookSecret)} {
		if err = svc.VerifyWebhook(f.ctxA, fake.Name, c.Connection.ID, wrong); !errors.Is(err, app.ErrWebhookForged) {
			t.Errorf("secret %q: %v", wrong, err)
		}
	}
	if err = svc.VerifyWebhook(f.ctxA, "bosta", c.Connection.ID, c.WebhookSecret); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Errorf("another carrier's URL: %v", err)
	}
	list, err := svc.Connections(f.ctxA)
	if err != nil || len(list) != 1 || list[0].ID != c.Connection.ID {
		t.Fatalf("list: %+v %v", list, err)
	}
	for _, a := range f.rec.actions {
		if strings.Contains(a, merchantKey) {
			t.Fatal("audit carries the key")
		}
	}
	if audits, _ := f.rec.counts(); audits != 1 {
		t.Fatalf("audits = %d", audits)
	}
}

// failingVerify is a carrier whose API is down.
type failingVerify struct{ *fake.Carrier }

func (failingVerify) Verify(context.Context, carrier.Credentials) error {
	return errors.New("connection refused")
}

func TestConnectRefusals(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service()
	if _, err := svc.Connect(f.ctxA, fake.Name, fake.InvalidKey); !fieldIs(err, "api_key", "refused") {
		t.Errorf("refused key: %v", err)
	}
	if _, err := svc.Connect(f.ctxA, "dhl", merchantKey); !fieldIs(err, "carrier", "unknown") {
		t.Errorf("unknown carrier: %v", err)
	}
	for _, k := range []string{"", "   ", "has space", strings.Repeat("k", 513), "tab\tkey"} {
		if _, err := svc.Connect(f.ctxA, fake.Name, k); !fieldIs(err, "api_key", "invalid") {
			t.Errorf("key %q: %v", k, err)
		}
	}
	down := f.serviceWith(app.Carriers{fake.Name: failingVerify{fake.New()}})
	if _, err := down.Connect(f.ctxA, fake.Name, merchantKey); errs.KindOf(err) != errs.KindUnavailable {
		t.Errorf("carrier down: %v", err)
	}
	if list, err := svc.Connections(f.ctxA); err != nil || len(list) != 0 {
		t.Fatalf("refusals stored connections: %+v %v", list, err)
	}

	first, err := svc.Connect(f.ctxA, fake.Name, merchantKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Connect(f.ctxA, fake.Name, "another-key"); !errors.Is(err, app.ErrCarrierConnected) {
		t.Fatalf("second connection: %v", err)
	}
	if err = svc.Disconnect(f.ctxA, first.Connection.ID); err != nil {
		t.Fatal(err)
	}
	if err = svc.Disconnect(f.ctxA, first.Connection.ID); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Fatalf("disconnect twice: %v", err)
	}
	if _, _, err = svc.Credentials(f.ctxA, first.Connection.ID); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Fatalf("a disconnected key is opened: %v", err)
	}
	if err = svc.VerifyWebhook(f.ctxA, fake.Name, first.Connection.ID, first.WebhookSecret); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Fatalf("a disconnected connection takes webhooks: %v", err)
	}
	if _, err = svc.Connect(f.ctxA, fake.Name, "another-key"); err != nil {
		t.Fatalf("reconnect after disconnect: %v", err)
	}
	if list, listErr := svc.Connections(f.ctxA); listErr != nil || len(list) != 2 || list[1].Status != "disconnected" || list[1].DisconnectedAt == nil {
		t.Fatalf("history: %+v %v", list, listErr)
	}
}

// TestConnectionsCrossTenant: another store neither opens, verifies nor
// disconnects a connection, and a sealed blob copied into another
// store's row does not open there.
func TestConnectionsCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service()
	a, err := svc.Connect(f.ctxA, fake.Name, merchantKey)
	if err != nil {
		t.Fatal(err)
	}
	id := a.Connection.ID
	if _, _, err = svc.Credentials(f.ctxB, id); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Errorf("B opens A's credentials: %v", err)
	}
	if err = svc.VerifyWebhook(f.ctxB, fake.Name, id, a.WebhookSecret); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Errorf("B's URL with A's connection: %v", err)
	}
	if err = svc.Disconnect(f.ctxB, id); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Errorf("B disconnects A: %v", err)
	}
	if list, listErr := svc.Connections(f.ctxB); listErr != nil || len(list) != 0 {
		t.Fatalf("B lists A's connections: %+v %v", list, listErr)
	}

	b, err := svc.Connect(f.ctxB, fake.Name, "b-key")
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	if err = postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctxA, `SELECT credentials FROM carrier_connections WHERE id = $1`, id).Scan(&blob)
	}); err != nil {
		t.Fatal(err)
	}
	if err = postgres.InTenantTx(f.ctxB, f.pool, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(f.ctxB, `UPDATE carrier_connections SET credentials = $1 WHERE id = $2`, blob, b.Connection.ID)
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
	if _, got, openErr := svc.Credentials(f.ctxB, b.Connection.ID); openErr == nil {
		t.Fatalf("A's sealed key opened in B's row: %+v", got)
	}
}
