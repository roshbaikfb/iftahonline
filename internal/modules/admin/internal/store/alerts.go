package store

import (
	"context"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store/admindb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's alerts (ADR-0080).

// Alert is one opening of a kind of problem, until it resolves.
type Alert struct {
	ID         ulid.ID
	Kind       domain.AlertKind
	Severity   domain.Severity
	Value      int64
	OpenedAt   time.Time
	LastSeenAt time.Time
	ResolvedAt *time.Time
}

func toAlerts(rows []admindb.AdminAlert) []Alert {
	out := make([]Alert, len(rows))
	for i, r := range rows {
		out[i] = Alert{ID: r.ID, Kind: domain.AlertKind(r.Kind), Severity: domain.Severity(r.Severity), Value: r.Value, OpenedAt: r.OpenedAt,
			LastSeenAt: r.LastSeenAt, ResolvedAt: r.ResolvedAt}
	}
	return out
}

// AlertStreaks are the latest checks' streaks per kind.
func AlertStreaks(ctx context.Context, db DB) (map[domain.AlertKind]domain.AlertStreak, error) {
	rows, err := admindb.New(db).ListAlertStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin store: alert states: %w", err)
	}
	out := make(map[domain.AlertKind]domain.AlertStreak, len(rows))
	for _, r := range rows {
		out[domain.AlertKind(r.Kind)] = domain.AlertStreak{Bad: int(r.Bad), Good: int(r.Good)}
	}
	return out, nil
}

// SaveAlertStreak records a kind's streak after a check.
func SaveAlertStreak(ctx context.Context, db DB, kind domain.AlertKind, s domain.AlertStreak, at time.Time) error {
	if err := admindb.New(db).SaveAlertState(ctx, admindb.SaveAlertStateParams{Kind: string(kind), Bad: int32(min(s.Bad, 1000)), //nolint:gosec // capped
		Good: int32(min(s.Good, 1000)), CheckedAt: at}); err != nil { //nolint:gosec // capped
		return fmt.Errorf("admin store: save alert state: %w", err)
	}
	return nil
}

// LockOpenAlerts reads the open alerts, locked for the rest of tx.
func LockOpenAlerts(ctx context.Context, db DB) ([]Alert, error) {
	rows, err := admindb.New(db).LockOpenAlerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin store: open alerts: %w", err)
	}
	return toAlerts(rows), nil
}

// OpenAlert opens a new alert of a kind.
func OpenAlert(ctx context.Context, db DB, a Alert) error {
	if err := admindb.New(db).OpenAlert(ctx, admindb.OpenAlertParams{ID: a.ID, Kind: string(a.Kind), Severity: string(a.Severity),
		Value: a.Value, At: a.OpenedAt}); err != nil {
		return fmt.Errorf("admin store: open alert: %w", err)
	}
	return nil
}

// TouchAlert records an open alert's latest number.
func TouchAlert(ctx context.Context, db DB, id ulid.ID, value int64, at time.Time) error {
	if err := admindb.New(db).TouchAlert(ctx, admindb.TouchAlertParams{ID: id, Value: value, At: at}); err != nil {
		return fmt.Errorf("admin store: touch alert: %w", err)
	}
	return nil
}

// ResolveAlert resolves an open alert.
func ResolveAlert(ctx context.Context, db DB, id ulid.ID, at time.Time) error {
	if err := admindb.New(db).ResolveAlert(ctx, admindb.ResolveAlertParams{ID: id, At: &at}); err != nil {
		return fmt.Errorf("admin store: resolve alert: %w", err)
	}
	return nil
}

// Alerts are the open alerts and those resolved since, newest first (at
// most 200).
func Alerts(ctx context.Context, db DB, since time.Time) ([]Alert, error) {
	rows, err := admindb.New(db).ListAlerts(ctx, &since)
	if err != nil {
		return nil, fmt.Errorf("admin store: alerts: %w", err)
	}
	return toAlerts(rows), nil
}

// AlertsToDeliver are the alerts that may still owe a notice: opened
// since and still open, or red and resolved since.
func AlertsToDeliver(ctx context.Context, db DB, since time.Time) ([]Alert, error) {
	rows, err := admindb.New(db).AlertsToDeliver(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("admin store: alerts to deliver: %w", err)
	}
	return toAlerts(rows), nil
}

// Recipient is an active admin who may hear of alerts.
type Recipient struct {
	ID          ulid.ID
	Name, Email string
	Role        domain.Role
	WantsEmail  bool // alerts by email
}

// Recipients are the active admins.
func Recipients(ctx context.Context, db DB) ([]Recipient, error) {
	rows, err := admindb.New(db).AlertRecipients(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin store: recipients: %w", err)
	}
	out := make([]Recipient, len(rows))
	for i, r := range rows {
		out[i] = Recipient{ID: r.ID, Name: r.Name, Email: r.Email, Role: domain.Role(r.Role), WantsEmail: r.AlertEmail}
	}
	return out, nil
}

// Delivery is one notice sent: an alert's event to an admin on a channel.
type Delivery struct {
	Alert, Admin   ulid.ID
	Event, Channel string
}

// Deliveries are the notices already sent for these alerts.
func Deliveries(ctx context.Context, db DB, alerts []ulid.ID) (map[Delivery]bool, error) {
	rows, err := admindb.New(db).AlertDeliveries(ctx, alerts)
	if err != nil {
		return nil, fmt.Errorf("admin store: deliveries: %w", err)
	}
	out := make(map[Delivery]bool, len(rows))
	for _, r := range rows {
		out[Delivery{Alert: r.AlertID, Admin: r.AdminID, Event: r.Event, Channel: r.Channel}] = true
	}
	return out, nil
}

// RecordDelivery notes a notice sent (once).
func RecordDelivery(ctx context.Context, db DB, d Delivery) error {
	if err := admindb.New(db).RecordAlertDelivery(ctx, admindb.RecordAlertDeliveryParams{AlertID: d.Alert, AdminID: d.Admin, Event: d.Event,
		Channel: d.Channel}); err != nil {
		return fmt.Errorf("admin store: record delivery: %w", err)
	}
	return nil
}

// AlertEmail tells whether admin id wants alerts by email.
func AlertEmail(ctx context.Context, db DB, id ulid.ID) (bool, error) {
	on, err := admindb.New(db).GetAlertEmail(ctx, id)
	if err != nil {
		return false, noRows(err)
	}
	return on, nil
}

// SetAlertEmail turns admin id's alert emails on or off.
func SetAlertEmail(ctx context.Context, db DB, id ulid.ID, on bool) error {
	if err := admindb.New(db).SetAlertEmail(ctx, admindb.SetAlertEmailParams{ID: id, Enabled: on}); err != nil {
		return fmt.Errorf("admin store: set alert email: %w", err)
	}
	return nil
}
