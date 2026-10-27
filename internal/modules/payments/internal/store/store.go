// Package store adapts the sqlc queries in paymentsdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/store/paymentsdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = paymentsdb.DBTX

// ErrGatewayConnected is returned when the store already has an active
// connection to the gateway.
var ErrGatewayConnected = errors.New("payments store: gateway already connected")

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("payments store: %w", err)
	}
	return id.ID, nil
}

func notFound(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return fmt.Errorf("payments store: %s: %w", what, err)
}

// SealedConnection is a connection with its sealed credentials.
type SealedConnection struct {
	domain.Connection
	Credentials []byte
}

func toConnection(r paymentsdb.PaymentConnection) SealedConnection {
	return SealedConnection{Connection: domain.Connection{ID: r.ID, Gateway: r.Gateway, Status: r.Status, CreatedAt: r.CreatedAt,
		DisconnectedAt: r.DisconnectedAt}, Credentials: r.Credentials}
}

// InsertConnection stores a new active connection.
func InsertConnection(ctx context.Context, tx Tx, id ulid.ID, gateway string, sealed []byte) (domain.Connection, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Connection{}, err
	}
	row, err := paymentsdb.New(tx).InsertConnection(ctx, paymentsdb.InsertConnectionParams{ID: id, StoreID: store, Gateway: gateway, Credentials: sealed})
	if postgres.IsUniqueViolation(err, "payment_connections_one_active") {
		return domain.Connection{}, ErrGatewayConnected
	}
	if err != nil {
		return domain.Connection{}, fmt.Errorf("payments store: insert connection: %w", err)
	}
	return toConnection(row).Connection, nil
}

// ListConnections returns the store's connections, newest first.
func ListConnections(ctx context.Context, tx Tx) ([]domain.Connection, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := paymentsdb.New(tx).ListConnections(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("payments store: list connections: %w", err)
	}
	out := make([]domain.Connection, len(rows))
	for i, r := range rows {
		out[i] = toConnection(r).Connection
	}
	return out, nil
}

// GetConnection returns a connection with its sealed credentials.
func GetConnection(ctx context.Context, tx Tx, id ulid.ID) (SealedConnection, error) {
	store, err := current(ctx)
	if err != nil {
		return SealedConnection{}, err
	}
	row, err := paymentsdb.New(tx).GetConnection(ctx, paymentsdb.GetConnectionParams{StoreID: store, ID: id})
	if err != nil {
		return SealedConnection{}, notFound(err, "get connection")
	}
	return toConnection(row), nil
}

// ActiveConnection returns the store's active gateway connection.
func ActiveConnection(ctx context.Context, tx Tx) (SealedConnection, error) {
	store, err := current(ctx)
	if err != nil {
		return SealedConnection{}, err
	}
	row, err := paymentsdb.New(tx).ActiveConnection(ctx, store)
	if err != nil {
		return SealedConnection{}, notFound(err, "active connection")
	}
	return toConnection(row), nil
}

// Disconnect marks an active connection disconnected.
func Disconnect(ctx context.Context, tx Tx, id ulid.ID) (domain.Connection, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Connection{}, err
	}
	row, err := paymentsdb.New(tx).DisconnectConnection(ctx, paymentsdb.DisconnectConnectionParams{StoreID: store, ID: id})
	if err != nil {
		return domain.Connection{}, notFound(err, "disconnect")
	}
	return toConnection(row).Connection, nil
}

// InsertPayment stores a pending payment.
func InsertPayment(ctx context.Context, tx Tx, p domain.Payment) (domain.Payment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Payment{}, err
	}
	row, err := paymentsdb.New(tx).InsertPayment(ctx, paymentsdb.InsertPaymentParams{ID: p.ID, StoreID: store, OrderID: p.OrderID,
		ConnectionID: p.ConnectionID, Gateway: p.Gateway, Currency: string(p.Amount.Currency()), AmountMinor: p.Amount.Amount()})
	if err != nil {
		return domain.Payment{}, fmt.Errorf("payments store: insert payment: %w", err)
	}
	return toPayment(row)
}

// SetCheckout records where the shopper pays.
func SetCheckout(ctx context.Context, tx Tx, id ulid.ID, url, providerRef string) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = paymentsdb.New(tx).SetCheckout(ctx, paymentsdb.SetCheckoutParams{StoreID: store, ID: id, CheckoutUrl: url, ProviderRef: providerRef}); err != nil {
		return fmt.Errorf("payments store: set checkout: %w", err)
	}
	return nil
}

// GetPayment returns a payment, locked when lock is set.
func GetPayment(ctx context.Context, tx Tx, id ulid.ID, lock bool) (domain.Payment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Payment{}, err
	}
	q := paymentsdb.New(tx)
	var row paymentsdb.Payment
	if lock {
		row, err = q.GetPaymentForUpdate(ctx, paymentsdb.GetPaymentForUpdateParams{StoreID: store, ID: id})
	} else {
		row, err = q.GetPayment(ctx, paymentsdb.GetPaymentParams{StoreID: store, ID: id})
	}
	if err != nil {
		return domain.Payment{}, notFound(err, "get payment")
	}
	return toPayment(row)
}

// OrderPayments returns an order's payments, newest first.
func OrderPayments(ctx context.Context, tx Tx, orderID ulid.ID) ([]domain.Payment, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := paymentsdb.New(tx).OrderPayments(ctx, paymentsdb.OrderPaymentsParams{StoreID: store, OrderID: orderID})
	if err != nil {
		return nil, fmt.Errorf("payments store: order payments: %w", err)
	}
	return toPayments(rows)
}

// LockOrderPayments returns an order's payments locked until tx ends.
func LockOrderPayments(ctx context.Context, tx Tx, orderID ulid.ID) ([]domain.Payment, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := paymentsdb.New(tx).LockOrderPayments(ctx, paymentsdb.LockOrderPaymentsParams{StoreID: store, OrderID: orderID})
	if err != nil {
		return nil, fmt.Errorf("payments store: lock order payments: %w", err)
	}
	return toPayments(rows)
}

// ListPayments returns one page of the store's payments, newest first.
func ListPayments(ctx context.Context, tx Tx, beforeAt time.Time, beforeID ulid.ID, limit int) ([]domain.Payment, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := paymentsdb.New(tx).ListPayments(ctx, paymentsdb.ListPaymentsParams{StoreID: store, BeforeAt: beforeAt, BeforeID: beforeID,
		Lim: int32(limit)}) //nolint:gosec // bounded by the caller
	if err != nil {
		return nil, fmt.Errorf("payments store: list payments: %w", err)
	}
	return toPayments(rows)
}

// SetState records a payment's new state.
func SetState(ctx context.Context, tx Tx, p domain.Payment) (domain.Payment, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Payment{}, err
	}
	row, err := paymentsdb.New(tx).SetPaymentState(ctx, paymentsdb.SetPaymentStateParams{StoreID: store, ID: p.ID, Status: string(p.Status),
		RefundedMinor: p.Refunded.Amount(), TransactionID: p.TransactionID, Failure: p.Failure})
	if err != nil {
		return domain.Payment{}, notFound(err, "set payment state")
	}
	return toPayment(row)
}

// InsertRefund stores a requested refund.
func InsertRefund(ctx context.Context, tx Tx, r domain.Refund) (domain.Refund, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Refund{}, err
	}
	row, err := paymentsdb.New(tx).InsertRefund(ctx, paymentsdb.InsertRefundParams{ID: r.ID, StoreID: store, PaymentID: r.PaymentID,
		Currency: string(r.Amount.Currency()), AmountMinor: r.Amount.Amount(), Reason: r.Reason})
	if err != nil {
		return domain.Refund{}, fmt.Errorf("payments store: insert refund: %w", err)
	}
	return toRefund(row)
}

// GetRefund returns a refund locked for update.
func GetRefund(ctx context.Context, tx Tx, id ulid.ID) (domain.Refund, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Refund{}, err
	}
	row, err := paymentsdb.New(tx).GetRefundForUpdate(ctx, paymentsdb.GetRefundForUpdateParams{StoreID: store, ID: id})
	if err != nil {
		return domain.Refund{}, notFound(err, "get refund")
	}
	return toRefund(row)
}

// Refunds returns a payment's refunds, oldest first.
func Refunds(ctx context.Context, tx Tx, paymentID ulid.ID) ([]domain.Refund, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := paymentsdb.New(tx).PaymentRefunds(ctx, paymentsdb.PaymentRefundsParams{StoreID: store, PaymentID: paymentID})
	if err != nil {
		return nil, fmt.Errorf("payments store: refunds: %w", err)
	}
	out := make([]domain.Refund, len(rows))
	for i, r := range rows {
		if out[i], err = toRefund(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RequestedRefunds is the sum of a payment's unanswered refunds.
func RequestedRefunds(ctx context.Context, tx Tx, paymentID ulid.ID, cur money.Currency) (money.Money, error) {
	store, err := current(ctx)
	if err != nil {
		return money.Money{}, err
	}
	n, err := paymentsdb.New(tx).RequestedRefunds(ctx, paymentsdb.RequestedRefundsParams{StoreID: store, PaymentID: paymentID})
	if err != nil {
		return money.Money{}, fmt.Errorf("payments store: requested refunds: %w", err)
	}
	return money.New(n, cur)
}

// SetRefund records a refund's outcome.
func SetRefund(ctx context.Context, tx Tx, id ulid.ID, status, providerID, failure string) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = paymentsdb.New(tx).SetRefund(ctx, paymentsdb.SetRefundParams{StoreID: store, ID: id, Status: status, ProviderRefundID: providerID,
		Failure: failure}); err != nil {
		return fmt.Errorf("payments store: set refund: %w", err)
	}
	return nil
}

func toPayments(rows []paymentsdb.Payment) ([]domain.Payment, error) {
	out := make([]domain.Payment, len(rows))
	for i, r := range rows {
		var err error
		if out[i], err = toPayment(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func toPayment(r paymentsdb.Payment) (domain.Payment, error) {
	cur := money.Currency(r.Currency)
	amount, err := money.New(r.AmountMinor, cur)
	if err != nil {
		return domain.Payment{}, fmt.Errorf("payments store: %w", err)
	}
	refunded, err := money.New(r.RefundedMinor, cur)
	if err != nil {
		return domain.Payment{}, fmt.Errorf("payments store: %w", err)
	}
	return domain.Payment{ID: r.ID, OrderID: r.OrderID, ConnectionID: r.ConnectionID, Gateway: r.Gateway, Amount: amount, Refunded: refunded,
		Status: domain.Status(r.Status), CheckoutURL: r.CheckoutUrl, ProviderRef: r.ProviderRef, TransactionID: r.TransactionID,
		Failure: r.Failure, StatusAt: r.StatusAt, CreatedAt: r.CreatedAt, Version: int(r.Version)}, nil
}

func toRefund(r paymentsdb.PaymentRefund) (domain.Refund, error) {
	amount, err := money.New(r.AmountMinor, money.Currency(r.Currency))
	if err != nil {
		return domain.Refund{}, fmt.Errorf("payments store: %w", err)
	}
	return domain.Refund{ID: r.ID, PaymentID: r.PaymentID, Amount: amount, Status: r.Status, Reason: r.Reason, ProviderID: r.ProviderRefundID,
		Failure: r.Failure, CreatedAt: r.CreatedAt}, nil
}

// DefaultMethod is the store's checkout starting method: domain.DefaultCOD
// until it chose another.
func DefaultMethod(ctx context.Context, tx Tx) (string, error) {
	store, err := current(ctx)
	if err != nil {
		return "", err
	}
	m, err := paymentsdb.New(tx).GetPaymentSettings(ctx, store)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DefaultCOD, nil
	}
	if err != nil {
		return "", fmt.Errorf("payments store: settings: %w", err)
	}
	return m, nil
}

// SaveDefaultMethod sets the store's checkout starting method.
func SaveDefaultMethod(ctx context.Context, tx Tx, method string) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = paymentsdb.New(tx).SavePaymentSettings(ctx, paymentsdb.SavePaymentSettingsParams{StoreID: store, DefaultMethod: method}); err != nil {
		return fmt.Errorf("payments store: save settings: %w", err)
	}
	return nil
}
