package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Connection errors.
var (
	ErrConnectionNotFound = errs.NotFound("carrier_connection_not_found", "carrier connection not found")
	ErrCarrierConnected   = errs.Conflict("carrier_already_connected", "this carrier is already connected; disconnect it first")
	errUnknownCarrier     = errs.Validation(errs.FieldError{Field: "carrier", Code: "unknown", Message: "this carrier is not available"})
	errKeyRefused         = errs.Validation(errs.FieldError{Field: "api_key", Code: "refused", Message: "the carrier refused this API key"})
	// ErrWebhookForged: a carrier webhook whose secret does not match.
	ErrWebhookForged = errors.New("shipping: webhook secret does not match")
)

// verifyTimeout bounds checking a merchant's key while they wait.
const verifyTimeout = 10 * time.Second

// Carriers are the adapters by name.
type Carriers map[string]carrier.Carrier

// Names lists the carriers, sorted.
func (c Carriers) Names() []string {
	names := make([]string, 0, len(c))
	for n := range c {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// CarrierNames lists the carriers merchants may connect.
func (s *Service) CarrierNames() []string { return s.carriers.Names() }

// Connected is a new connection with what the merchant must copy into
// the carrier's dashboard: the webhook URL and its secret, shown once.
type Connected struct {
	Connection    domain.Connection
	WebhookURL    string
	WebhookSecret string
}

// Connect checks apiKey with the carrier, then stores it sealed with a
// fresh webhook secret. The check is a call to the carrier while the
// merchant waits (bounded by verifyTimeout): a key that does not work
// must not be saved.
func (s *Service) Connect(ctx context.Context, carrierName, apiKey string) (Connected, error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return Connected{}, err
	}
	c, ok := s.carriers[carrierName]
	if !ok {
		return Connected{}, errUnknownCarrier
	}
	key, err := domain.CleanAPIKey(apiKey)
	if err != nil {
		return Connected{}, err
	}
	creds := carrier.Credentials{APIKey: key}
	vctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	switch err = c.Verify(vctx, creds); {
	case errors.Is(err, carrier.ErrAuth):
		return Connected{}, errKeyRefused
	case err != nil:
		return Connected{}, errs.Unavailable(30*time.Second, fmt.Errorf("shipping: verify %s key: %w", carrierName, err))
	}

	id := s.ids.New()
	raw, err := json.Marshal(creds) //nolint:gosec // marshalled only to be sealed on the next line

	if err != nil {
		return Connected{}, err
	}
	sealed, err := s.sealer.Seal(ctx, raw, connectionAAD(carrierName, storeID, id))
	if err != nil {
		return Connected{}, err
	}
	secret, err := newSecret()
	if err != nil {
		return Connected{}, err
	}
	var out Connected
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out.Connection, txErr = store.InsertConnection(ctx, tx, store.SealedConnection{
			Connection: domain.Connection{ID: id, Carrier: carrierName}, Credentials: sealed, WebhookSecretHash: domain.SecretHash(secret)})
		if txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "carrier_connection.created", TargetType: "carrier_connection",
			TargetID: id.String(), Metadata: map[string]any{"carrier": carrierName}})
	})
	if errors.Is(err, store.ErrCarrierConnected) {
		return Connected{}, ErrCarrierConnected
	}
	if err != nil {
		return Connected{}, err
	}
	out.WebhookURL, out.WebhookSecret = s.WebhookURL(storeID, out.Connection), secret
	return out, nil
}

// WebhookURL is where the carrier sends a connection's webhooks.
func (s *Service) WebhookURL(storeID tenant.StoreID, c domain.Connection) string {
	return strings.TrimRight(s.publicURL, "/") + "/v1/webhooks/" + c.Carrier + "/" + storeID.String() + "/" + c.ID.String()
}

// Connections lists the store's connections, newest first.
func (s *Service) Connections(ctx context.Context) ([]domain.Connection, error) {
	var out []domain.Connection
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.ListConnections(ctx, tx)
		return txErr
	})
	return out, err
}

// Disconnect stops using a connection. Its credentials stay sealed in
// place but are never opened again.
func (s *Service) Disconnect(ctx context.Context, id ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, txErr := store.Disconnect(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "carrier_connection.disconnected", TargetType: "carrier_connection",
			TargetID: id.String(), Metadata: map[string]any{"carrier": c.Carrier}})
	})
	if errors.Is(err, domain.ErrNotFound) {
		return ErrConnectionNotFound
	}
	return err
}

// Credentials opens an active connection's credentials for a job, with
// its carrier.
func (s *Service) Credentials(ctx context.Context, id ulid.ID) (carrier.Carrier, carrier.Credentials, error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, carrier.Credentials{}, err
	}
	c, err := s.activeConnection(ctx, id)
	if err != nil {
		return nil, carrier.Credentials{}, err
	}
	adapter, ok := s.carriers[c.Carrier]
	if !ok {
		return nil, carrier.Credentials{}, fmt.Errorf("shipping: carrier %q is not configured", c.Carrier)
	}
	raw, err := s.sealer.Open(ctx, c.Credentials, connectionAAD(c.Carrier, storeID, c.ID))
	if err != nil {
		return nil, carrier.Credentials{}, fmt.Errorf("shipping: open connection %s: %w", c.ID, err)
	}
	var creds carrier.Credentials
	if err = json.Unmarshal(raw, &creds); err != nil {
		return nil, carrier.Credentials{}, fmt.Errorf("shipping: connection %s credentials: %w", c.ID, err)
	}
	return adapter, creds, nil
}

// VerifyWebhook checks a delivery's secret against an active connection
// of carrierName. It returns ErrConnectionNotFound for no such
// connection and ErrWebhookForged for a wrong or missing secret.
func (s *Service) VerifyWebhook(ctx context.Context, carrierName string, id ulid.ID, presented string) error {
	c, err := s.activeConnection(ctx, id)
	if err != nil {
		return err
	}
	if c.Carrier != carrierName {
		return ErrConnectionNotFound
	}
	if !domain.SecretMatches(presented, c.WebhookSecretHash) {
		return ErrWebhookForged
	}
	return nil
}

func (s *Service) activeConnection(ctx context.Context, id ulid.ID) (store.SealedConnection, error) {
	var c store.SealedConnection
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		c, txErr = store.GetConnection(ctx, tx, id)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) || (err == nil && c.Status != domain.ConnectionActive) {
		return store.SealedConnection{}, ErrConnectionNotFound
	}
	return c, err
}

// connectionAAD binds sealed credentials to their carrier, store and
// connection (ADR-0024).
func connectionAAD(carrierName string, storeID tenant.StoreID, id ulid.ID) []byte {
	return []byte("shipping:" + carrierName + ":" + storeID.String() + ":" + id.String())
}

// newSecret is a webhook secret: 32 random bytes, URL-safe.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("shipping: webhook secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
