package store

import (
	"context"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store/admindb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// An admin's own account (C3): name, password, the two-step phone, and
// the devices signed in.

// SetName renames admin id.
func SetName(ctx context.Context, db DB, id ulid.ID, name string) (domain.Admin, error) {
	r, err := admindb.New(db).SetAdminName(ctx, admindb.SetAdminNameParams{ID: id, Name: name})
	if err != nil {
		return domain.Admin{}, noRows(err)
	}
	return toAdmin(r), nil
}

// SetPassword sets admin id's password hash.
func SetPassword(ctx context.Context, db DB, id ulid.ID, hash string) error {
	n, err := admindb.New(db).SetAdminPassword(ctx, admindb.SetAdminPasswordParams{ID: id, PasswordHash: hash})
	if err != nil {
		return fmt.Errorf("admin store: set password: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetTOTP moves admin id to a new sealed seed, its first step claimed.
func SetTOTP(ctx context.Context, db DB, id ulid.ID, sealed []byte, step int64) error {
	n, err := admindb.New(db).SetAdminTOTP(ctx, admindb.SetAdminTOTPParams{ID: id, TotpSecret: sealed, Step: step})
	if err != nil {
		return fmt.Errorf("admin store: set totp: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// OwnSession is a device an admin is signed in on.
type OwnSession struct {
	ID         ulid.ID
	UserAgent  string
	IPPrefix   string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// ListSessions lists admin id's live sessions, the latest used first.
func ListSessions(ctx context.Context, db DB, id ulid.ID) ([]OwnSession, error) {
	rows, err := admindb.New(db).ListAdminSessions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("admin store: sessions: %w", err)
	}
	out := make([]OwnSession, len(rows))
	for i, r := range rows {
		out[i] = OwnSession{ID: r.ID, UserAgent: r.UserAgent, IPPrefix: r.IpPrefix, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt}
	}
	return out, nil
}

// RevokeOwnSession ends one of admin id's sessions; false when it is not
// theirs or not live.
func RevokeOwnSession(ctx context.Context, db DB, admin, session ulid.ID) (bool, error) {
	n, err := admindb.New(db).RevokeOwnAdminSession(ctx, admindb.RevokeOwnAdminSessionParams{ID: session, AdminID: admin})
	if err != nil {
		return false, fmt.Errorf("admin store: revoke session: %w", err)
	}
	return n == 1, nil
}

// RevokeOtherSessions ends every session of admin id but keep.
func RevokeOtherSessions(ctx context.Context, db DB, admin, keep ulid.ID) (int64, error) {
	n, err := admindb.New(db).RevokeOtherAdminSessions(ctx, admindb.RevokeOtherAdminSessionsParams{AdminID: admin, Keep: keep})
	if err != nil {
		return 0, fmt.Errorf("admin store: revoke sessions: %w", err)
	}
	return n, nil
}

// Notice kinds.
const (
	NoticePasswordChanged = "password_changed"
	NoticeTOTPChanged     = "totp_changed"
)

// InsertNotice records an email to send admin id (the outbox).
func InsertNotice(ctx context.Context, db DB, id, admin ulid.ID, kind, ipPrefix string) error {
	if err := admindb.New(db).InsertAdminNotice(ctx, admindb.InsertAdminNoticeParams{ID: id, AdminID: admin, Kind: kind, IpPrefix: ipPrefix}); err != nil {
		return fmt.Errorf("admin store: insert notice: %w", err)
	}
	return nil
}

// Notice is an email waiting to be sent.
type Notice struct {
	ID        ulid.ID
	Kind      string
	IPPrefix  string
	CreatedAt time.Time
	Email     string
	Name      string
}

// PendingNotices locks up to 50 unsent notices, oldest first, in tx.
func PendingNotices(ctx context.Context, tx DB) ([]Notice, error) {
	rows, err := admindb.New(tx).PendingAdminNotices(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin store: pending notices: %w", err)
	}
	out := make([]Notice, len(rows))
	for i, r := range rows {
		out[i] = Notice{ID: r.ID, Kind: r.Kind, IPPrefix: r.IpPrefix, CreatedAt: r.CreatedAt, Email: r.Email, Name: r.Name}
	}
	return out, nil
}

// MarkNoticeSent records a notice as mailed.
func MarkNoticeSent(ctx context.Context, tx DB, id ulid.ID) error {
	if err := admindb.New(tx).MarkAdminNoticeSent(ctx, id); err != nil {
		return fmt.Errorf("admin store: mark notice: %w", err)
	}
	return nil
}
