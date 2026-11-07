package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// The account page (D4): the signed-in user changes their own name and
// password. An admin's support session never reaches these (Scope refuses
// account changes outside the store, ADR-0055).

// UpdateName renames the signed-in user.
func (s *Service) UpdateName(ctx context.Context, raw string) (domain.User, error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return domain.User{}, errNoSession
	}
	name, fe := domain.ValidateName(raw)
	if fe != nil {
		return domain.User{}, errs.Validation(*fe)
	}
	u, err := store.SetUserName(ctx, s.db, id.UserID, name)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, errNoSession
	}
	return u, err
}

// ChangePassword sets a new password for the signed-in user, who proves
// the current one (failures throttled like logins). Every other session is
// signed out, this one stays, and the user is told by email.
func (s *Service) ChangePassword(ctx context.Context, current, next string) error {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	u, err := s.verifyPassword(ctx, id.UserID, current, "current_password")
	if err != nil {
		return err
	}
	if fe := domain.ValidatePassword(next, u.Email); fe != nil {
		return errs.Validation(*fe)
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		if txErr := store.SetPasswordHash(ctx, tx, u.ID, hash); txErr != nil {
			return txErr
		}
		if txErr := store.RevokeUserSessions(ctx, tx, u.ID, id.SessionID); txErr != nil {
			return txErr
		}
		if _, txErr := s.jobs.InsertTx(ctx, tx, PasswordChangedArgs{UserID: u.ID, At: s.now().UTC()}, nil); txErr != nil {
			return fmt.Errorf("identity: enqueue password notice: %w", txErr)
		}
		return nil
	})
}
