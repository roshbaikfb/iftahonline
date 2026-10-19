// Package store adapts the admin sqlc queries (app_admin pool only).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store/admindb"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DB is the admin pool (or a transaction on it).
type DB = admindb.DBTX

// ErrEmailTaken means another admin uses the email.
var ErrEmailTaken = errors.New("admin store: email taken")

func noRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func toAdmin(r admindb.PlatformAdmin) domain.Admin {
	return domain.Admin{ID: r.ID, Email: r.Email, Name: r.Name, PasswordHash: r.PasswordHash, TOTPSecret: r.TotpSecret,
		TOTPLastStep: r.TotpLastStep, Active: r.Status == "active", CreatedAt: r.CreatedAt, LastLoginAt: r.LastLoginAt}
}

// InsertAdmin creates an admin with a sealed TOTP seed.
func InsertAdmin(ctx context.Context, db DB, id ulid.ID, email, name, hash string, sealedTOTP []byte) (domain.Admin, error) {
	row, err := admindb.New(db).InsertAdmin(ctx, admindb.InsertAdminParams{ID: id, Email: email, Name: name, PasswordHash: hash, TotpSecret: sealedTOTP})
	if err != nil {
		if postgres.IsUniqueViolation(err, "") {
			return domain.Admin{}, ErrEmailTaken
		}
		return domain.Admin{}, fmt.Errorf("admin store: insert: %w", err)
	}
	return toAdmin(row), nil
}

// AdminByEmail finds an admin.
func AdminByEmail(ctx context.Context, db DB, email string) (domain.Admin, error) {
	row, err := admindb.New(db).GetAdminByEmail(ctx, email)
	if err != nil {
		return domain.Admin{}, noRows(err)
	}
	return toAdmin(row), nil
}

// AdminByID finds an admin.
func AdminByID(ctx context.Context, db DB, id ulid.ID) (domain.Admin, error) {
	row, err := admindb.New(db).GetAdmin(ctx, id)
	if err != nil {
		return domain.Admin{}, noRows(err)
	}
	return toAdmin(row), nil
}

// UseTOTPStep claims a time step; false is a replay.
func UseTOTPStep(ctx context.Context, db DB, id ulid.ID, step int64) (bool, error) {
	n, err := admindb.New(db).UseAdminTOTPStep(ctx, admindb.UseAdminTOTPStepParams{ID: id, Step: step})
	if err != nil {
		return false, fmt.Errorf("admin store: totp step: %w", err)
	}
	return n == 1, nil
}

// RecordLogin notes a successful sign-in.
func RecordLogin(ctx context.Context, db DB, id ulid.ID) error {
	if err := admindb.New(db).RecordAdminLogin(ctx, id); err != nil {
		return fmt.Errorf("admin store: record login: %w", err)
	}
	return nil
}

// Session is a live admin session.
type Session struct {
	ID, AdminID         ulid.ID
	CreatedAt, LastSeen time.Time
	ExpiresAt           time.Time
	AdminActive         bool
}

// InsertSession opens a session.
func InsertSession(ctx context.Context, db DB, id, adminID ulid.ID, t authtoken.Token, ua, ipPrefix string, expires time.Time) (Session, error) {
	r, err := admindb.New(db).InsertAdminSession(ctx, admindb.InsertAdminSessionParams{
		ID: id, AdminID: adminID, TokenHash: t.Hash(), UserAgent: ua, IpPrefix: ipPrefix, ExpiresAt: expires,
	})
	if err != nil {
		return Session{}, fmt.Errorf("admin store: insert session: %w", err)
	}
	return Session{ID: r.ID, AdminID: r.AdminID, CreatedAt: r.CreatedAt, LastSeen: r.LastSeenAt, ExpiresAt: r.ExpiresAt, AdminActive: true}, nil
}

// SessionByToken loads a live session.
func SessionByToken(ctx context.Context, db DB, t authtoken.Token) (Session, error) {
	r, err := admindb.New(db).GetActiveAdminSession(ctx, t.Hash())
	if err != nil {
		return Session{}, noRows(err)
	}
	return Session{ID: r.ID, AdminID: r.AdminID, CreatedAt: r.CreatedAt, LastSeen: r.LastSeenAt, ExpiresAt: r.ExpiresAt,
		AdminActive: r.AdminStatus == "active"}, nil
}

// TouchSession slides the expiry.
func TouchSession(ctx context.Context, db DB, id ulid.ID, expires time.Time) error {
	if err := admindb.New(db).TouchAdminSession(ctx, admindb.TouchAdminSessionParams{ID: id, ExpiresAt: expires}); err != nil {
		return fmt.Errorf("admin store: touch session: %w", err)
	}
	return nil
}

// RevokeSession ends a session.
func RevokeSession(ctx context.Context, db DB, id ulid.ID) error {
	if err := admindb.New(db).RevokeAdminSession(ctx, id); err != nil {
		return fmt.Errorf("admin store: revoke session: %w", err)
	}
	return nil
}

// AuditEntry is one admin action.
type AuditEntry struct {
	ID         ulid.ID
	OccurredAt time.Time
	AdminID    *ulid.ID
	Action     string
	TargetType string
	TargetID   string
	IPPrefix   string
	Metadata   []byte
}

// InsertAudit appends to the admin audit log.
func InsertAudit(ctx context.Context, db DB, e AuditEntry) error {
	meta := e.Metadata
	if len(meta) == 0 {
		meta = []byte("{}")
	}
	if err := admindb.New(db).InsertAdminAudit(ctx, admindb.InsertAdminAuditParams{
		ID: e.ID, AdminID: e.AdminID, Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID, IpPrefix: e.IPPrefix, Metadata: meta,
	}); err != nil {
		return fmt.Errorf("admin store: audit: %w", err)
	}
	return nil
}

// ListAudit pages the admin log, newest first.
func ListAudit(ctx context.Context, db DB, beforeAt *time.Time, beforeID *ulid.ID, limit int) ([]AuditEntry, error) {
	rows, err := admindb.New(db).ListAdminAudit(ctx, admindb.ListAdminAuditParams{BeforeAt: beforeAt, BeforeID: beforeID, Lim: int32(limit)}) //nolint:gosec // bounded by the caller.
	if err != nil {
		return nil, fmt.Errorf("admin store: list audit: %w", err)
	}
	out := make([]AuditEntry, len(rows))
	for i, r := range rows {
		out[i] = AuditEntry{ID: r.ID, OccurredAt: r.OccurredAt, AdminID: r.AdminID, Action: r.Action, TargetType: r.TargetType,
			TargetID: r.TargetID, IPPrefix: r.IpPrefix, Metadata: r.Metadata}
	}
	return out, nil
}
