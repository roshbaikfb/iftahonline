package store

import (
	"context"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store/admindb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// PushDevice is a browser or phone an admin turned notifications on in
// (ADR-0080).
type PushDevice struct {
	ID, Admin    ulid.ID
	Endpoint     string // the push service's URL for the device
	P256dh, Auth string // its keys, to encrypt what is sent
	UserAgent    string
}

// SavePushDevice records a device; its endpoint is its key.
func SavePushDevice(ctx context.Context, db DB, d PushDevice) error {
	if err := admindb.New(db).SavePushSubscription(ctx, admindb.SavePushSubscriptionParams{ID: d.ID, AdminID: d.Admin, Endpoint: d.Endpoint,
		P256dh: d.P256dh, Auth: d.Auth, UserAgent: d.UserAgent}); err != nil {
		return fmt.Errorf("admin store: save push device: %w", err)
	}
	return nil
}

// RemovePushDevice forgets admin's device; false when it had none there.
func RemovePushDevice(ctx context.Context, db DB, admin ulid.ID, endpoint string) (bool, error) {
	n, err := admindb.New(db).DeletePushSubscription(ctx, admindb.DeletePushSubscriptionParams{AdminID: admin, Endpoint: endpoint})
	if err != nil {
		return false, fmt.Errorf("admin store: remove push device: %w", err)
	}
	return n > 0, nil
}

// ForgetPushDevice drops a device its push service no longer knows.
func ForgetPushDevice(ctx context.Context, db DB, id ulid.ID) error {
	if err := admindb.New(db).DeletePushEndpoint(ctx, id); err != nil {
		return fmt.Errorf("admin store: forget push device: %w", err)
	}
	return nil
}

// CountPushDevices counts admin's devices.
func CountPushDevices(ctx context.Context, db DB, admin ulid.ID) (int64, error) {
	n, err := admindb.New(db).CountPushSubscriptions(ctx, admin)
	if err != nil {
		return 0, fmt.Errorf("admin store: count push devices: %w", err)
	}
	return n, nil
}

// PushDevices are every active admin's devices, by admin (at most 1000).
func PushDevices(ctx context.Context, db DB) (map[ulid.ID][]PushDevice, error) {
	rows, err := admindb.New(db).ListPushSubscriptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin store: push devices: %w", err)
	}
	out := map[ulid.ID][]PushDevice{}
	for _, r := range rows {
		out[r.AdminID] = append(out[r.AdminID], PushDevice{ID: r.ID, Admin: r.AdminID, Endpoint: r.Endpoint, P256dh: r.P256dh, Auth: r.Auth})
	}
	return out, nil
}

// MarkPushSent notes a device reached.
func MarkPushSent(ctx context.Context, db DB, id ulid.ID) error {
	if err := admindb.New(db).MarkPushSent(ctx, id); err != nil {
		return fmt.Errorf("admin store: mark push sent: %w", err)
	}
	return nil
}
