// Package app holds the payments use cases (ADR-0029): merchants connect
// a gateway account; shoppers of card orders are sent to the gateway's
// hosted page; the gateway's word (re-queried after every webhook, and
// once more when a payment expires) marks orders paid and confirms them,
// or cancels the unpaid ones after an hour; merchants refund, in part or
// in full.
package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Errors.
var (
	ErrConnectionNotFound = errs.NotFound("payment_connection_not_found", "payment gateway connection not found")
	ErrGatewayConnected   = errs.Conflict("gateway_already_connected", "a payment gateway is already connected; disconnect it first")
	ErrPaymentNotFound    = errs.NotFound("payment_not_found", "payment not found")
	ErrCardUnavailable    = errs.Conflict("card_payments_unavailable", "this store does not take card payments")
	ErrNotPayable         = errs.Conflict("order_not_payable", "this order is not waiting for a card payment")
	ErrNotRefundable      = errs.Conflict("payment_not_refundable", "only a captured payment can be refunded")
	errUnknownGateway     = errs.Validation(errs.FieldError{Field: "gateway", Code: "unknown", Message: "this gateway is not available"})
	errKeysRefused        = errs.Validation(errs.FieldError{Field: "secret_key", Code: "refused", Message: "the gateway refused these keys"})
	errNoIntegration      = errs.Validation(errs.FieldError{Field: "integration_ids", Code: "required", Message: "give the card integration ID(s) from the gateway"})
)

// verifyTimeout bounds checking a merchant's keys while they wait.
const verifyTimeout = 10 * time.Second

// Orders is what payments use of the orders module (orders.Payments).
type Orders interface {
	Get(ctx context.Context, id ulid.ID) (orders.Order, error)
	TransitionSystemTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to orders.Status, actor, note string) (bool, error)
	SetPaymentStatusTx(ctx context.Context, tx pgx.Tx, id ulid.ID, st orders.PaymentStatus, actor string) (bool, error)
}

// Gateways are the adapters by name.
type Gateways map[string]gateway.Gateway

// Names lists the gateways, sorted.
func (g Gateways) Names() []string {
	names := make([]string, 0, len(g))
	for n := range g {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Service implements the use cases.
type Service struct {
	db        postgres.Beginner
	ids       *ulid.Generator
	audit     audit.Recorder
	events    eventbus.Publisher
	gateways  Gateways
	sealer    *envelope.Sealer
	publicURL string
	orders    Orders
	jobs      jobs.Inserter
	inbox     webhooks.Inbox
	now       func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB        postgres.Beginner
	IDs       *ulid.Generator
	Audit     audit.Recorder
	Events    eventbus.Publisher
	Gateways  Gateways
	Sealer    *envelope.Sealer
	PublicURL string // the platform's public origin, for webhook URLs
	Orders    Orders
	Jobs      jobs.Inserter
	Inbox     webhooks.Inbox // the worker role
}

// NewService builds the service. A nil Audit or Events discards.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	if d.Events == nil {
		d.Events = eventbus.Nop{}
	}
	return &Service{db: d.DB, ids: d.IDs, audit: d.Audit, events: d.Events, gateways: d.Gateways, sealer: d.Sealer,
		publicURL: d.PublicURL, orders: d.Orders, jobs: d.Jobs, inbox: d.Inbox, now: time.Now}
}

// SetClock replaces the clock (expiry tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// GatewayNames lists the gateways merchants may connect.
func (s *Service) GatewayNames() []string { return s.gateways.Names() }

// ConnectInput is a merchant's keys, as pasted from the gateway's
// dashboard.
type ConnectInput struct {
	Gateway        string
	SecretKey      string
	PublicKey      string
	WebhookSecret  string
	APIKey         string
	IntegrationIDs []int
}

// Connect checks the keys with the gateway, then stores them sealed. One
// active gateway per store.
func (s *Service) Connect(ctx context.Context, in ConnectInput) (domain.Connection, error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Connection{}, err
	}
	g, ok := s.gateways[in.Gateway]
	if !ok {
		return domain.Connection{}, errUnknownGateway
	}
	var creds gateway.Credentials
	var fields []errs.FieldError
	for _, f := range []struct {
		name     string
		in       string
		dst      *string
		required bool
	}{
		{"secret_key", in.SecretKey, &creds.SecretKey, false}, {"public_key", in.PublicKey, &creds.PublicKey, false},
		{"webhook_secret", in.WebhookSecret, &creds.WebhookSecret, true}, {"api_key", in.APIKey, &creds.APIKey, true},
	} {
		v, cleanErr := domain.CleanKey(f.name, f.in, f.required)
		var e *errs.Error
		if errors.As(cleanErr, &e) {
			fields = append(fields, e.Fields...)
		}
		*f.dst = v
	}
	if len(fields) > 0 {
		return domain.Connection{}, errs.Validation(fields...)
	}
	for _, id := range in.IntegrationIDs {
		if id > 0 {
			creds.IntegrationIDs = append(creds.IntegrationIDs, id)
		}
	}
	if in.Gateway != "fake" && len(creds.IntegrationIDs) == 0 {
		return domain.Connection{}, errNoIntegration
	}
	vctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	switch err = g.Verify(vctx, creds); {
	case errors.Is(err, gateway.ErrAuth):
		return domain.Connection{}, errKeysRefused
	case err != nil:
		return domain.Connection{}, errs.Unavailable(30*time.Second, fmt.Errorf("payments: verify %s keys: %w", in.Gateway, err))
	}
	id := s.ids.New()
	raw, err := json.Marshal(creds) //nolint:gosec // marshalled only to be sealed on the next line
	if err != nil {
		return domain.Connection{}, err
	}
	sealed, err := s.sealer.Seal(ctx, raw, connectionAAD(in.Gateway, storeID, id))
	if err != nil {
		return domain.Connection{}, err
	}
	var c domain.Connection
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if c, txErr = store.InsertConnection(ctx, tx, id, in.Gateway, sealed); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "payment_connection.created", TargetType: "payment_connection",
			TargetID: id.String(), Metadata: map[string]any{"gateway": in.Gateway}})
	})
	if errors.Is(err, store.ErrGatewayConnected) {
		return domain.Connection{}, ErrGatewayConnected
	}
	return c, err
}

// Connections lists the store's gateway connections, newest first.
func (s *Service) Connections(ctx context.Context) ([]domain.Connection, error) {
	var out []domain.Connection
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.ListConnections(ctx, tx)
		return txErr
	})
	return out, err
}

// Disconnect stops taking card payments through a connection.
func (s *Service) Disconnect(ctx context.Context, id ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, txErr := store.Disconnect(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "payment_connection.disconnected", TargetType: "payment_connection",
			TargetID: id.String(), Metadata: map[string]any{"gateway": c.Gateway}})
	})
	if errors.Is(err, domain.ErrNotFound) {
		return ErrConnectionNotFound
	}
	return err
}

// CardEnabled reports whether the store in ctx takes card payments.
func (s *Service) CardEnabled(ctx context.Context) (bool, error) {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		_, txErr := store.ActiveConnection(ctx, tx)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// open returns a connection's gateway and credentials; active only
// unless anyStatus (a payment made before a disconnect is still asked
// about and refunded).
func (s *Service) open(ctx context.Context, c store.SealedConnection, anyStatus bool) (gateway.Gateway, gateway.Credentials, error) {
	if c.Status != domain.ConnectionActive && !anyStatus {
		return nil, gateway.Credentials{}, ErrConnectionNotFound
	}
	g, ok := s.gateways[c.Gateway]
	if !ok {
		return nil, gateway.Credentials{}, fmt.Errorf("payments: gateway %q is not configured", c.Gateway)
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, gateway.Credentials{}, err
	}
	raw, err := s.sealer.Open(ctx, c.Credentials, connectionAAD(c.Gateway, storeID, c.ID))
	if err != nil {
		return nil, gateway.Credentials{}, fmt.Errorf("payments: open connection %s: %w", c.ID, err)
	}
	var creds gateway.Credentials
	if err = json.Unmarshal(raw, &creds); err != nil {
		return nil, gateway.Credentials{}, fmt.Errorf("payments: connection %s credentials: %w", c.ID, err)
	}
	return g, creds, nil
}

func (s *Service) connection(ctx context.Context, id ulid.ID) (store.SealedConnection, error) {
	var c store.SealedConnection
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		c, txErr = store.GetConnection(ctx, tx, id)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return store.SealedConnection{}, ErrConnectionNotFound
	}
	return c, err
}

// WebhookURL is where the gateway notifies a connection's payments.
func (s *Service) WebhookURL(storeID tenant.StoreID, c domain.Connection) string {
	return strings.TrimRight(s.publicURL, "/") + "/v1/webhooks/" + c.Gateway + "/" + storeID.String() + "/" + c.ID.String()
}

// ReturnURL is where the gateway sends the shopper back after paying an
// order: the API host's return route (ADR-0030), carrying the store's
// thank-you URL base64url-encoded, so the gateway's own query
// parameters cannot change it.
func (s *Service) ReturnURL(storeID tenant.StoreID, orderID ulid.ID, back string) string {
	return strings.TrimRight(s.publicURL, "/") + "/v1/pay/" + storeID.String() + "/" + orderID.String() + "/return/" +
		base64.RawURLEncoding.EncodeToString([]byte(back))
}

// connectionAAD binds sealed credentials to their gateway, store and
// connection (ADR-0024).
func connectionAAD(gw string, storeID tenant.StoreID, id ulid.ID) []byte {
	return []byte("payments:" + gw + ":" + storeID.String() + ":" + id.String())
}
