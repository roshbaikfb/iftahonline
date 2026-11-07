package app

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Moving a store's ownership (C2, the approved mockups). The owner moves
// it from the team page, at once, proving it with the password (and the
// second factor when on). Platform support moves it from the console for
// an owner who cannot (sold the store, left, lost the account): that waits
// SupportDelay and the owner now is emailed a link that cancels it
// (ADR-0095). Either way the old owner stays on as a manager, and the new
// owner must be an active member already.

var (
	errNotOwnerNow    = errs.Forbidden("only the store's owner moves its ownership")
	errSupportSession = errs.Forbidden("a support session cannot move a store's ownership")
	errAlreadyOwner   = errs.Validation(errs.FieldError{Field: "user_id", Code: "already_owner", Message: "this member owns the store already"})
	errMemberDisabled = errs.Conflict("member_disabled", "this member's account is disabled")
)

// Ownership notices: the old owner's and the new one's, and the new
// owner's heads-up when support asks.
const (
	NoticeOwnershipGiven    = "ownership_given"
	NoticeOwnershipReceived = "ownership_received"
	NoticeTransferAsked     = "ownership_transfer_asked"
)

// transferTx moves the store-in-ctx's ownership from from to member to in
// tx, refusing a target that is not an active member or a from that no
// longer owns the store.
func transferTx(ctx context.Context, tx pgx.Tx, from, to ulid.ID) error {
	if from == to {
		return errAlreadyOwner
	}
	u, role, err := member(ctx, tx, to)
	if err != nil {
		return err
	}
	if role == authz.RoleOwner {
		return errAlreadyOwner
	}
	if u.Status != domain.UserActive {
		return errMemberDisabled
	}
	ok, err := store.TransferOwner(ctx, tx, from, to)
	if err != nil {
		return err
	}
	if !ok {
		return errNotOwnerNow
	}
	return nil
}

// noticeTransferTx enqueues the two ownership emails, in tx.
func noticeTransferTx(ctx context.Context, tx pgx.Tx, ins jobs.Inserter, targs jobs.TenantArgs, from, to ulid.ID, at time.Time) error {
	for _, n := range []SupportNoticeArgs{
		{TenantArgs: targs, UserID: from, Other: to, Event: NoticeOwnershipGiven, At: at},
		{TenantArgs: targs, UserID: to, Other: from, Event: NoticeOwnershipReceived, At: at},
	} {
		if _, err := ins.InsertTx(ctx, tx, n, nil); err != nil {
			return fmt.Errorf("identity: enqueue ownership notice: %w", err)
		}
	}
	return nil
}

// TransferOwnership moves the store-in-ctx's ownership from the signed-in
// owner to member to, at once. The owner proves it with the password (and
// the second factor when on; failures throttled like logins); a support
// session never moves it. Both are emailed and the store's log records it.
func (s *Service) TransferOwnership(ctx context.Context, to ulid.ID, password, code string) error {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	if id.Impersonation != nil {
		return errSupportSession
	}
	if p, signed := authz.PrincipalFrom(ctx); !signed || p.Role != authz.RoleOwner {
		return errNotOwnerNow
	}
	u, err := s.verifyPassword(ctx, id.UserID, password, "password")
	if err != nil {
		return err
	}
	if u.MFAEnabled() {
		if err = s.checkSecondFactor(ctx, u, code); err != nil {
			return err
		}
	}
	targs, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if txErr := transferTx(ctx, tx, u.ID, to); txErr != nil {
			return txErr
		}
		if txErr := noticeTransferTx(ctx, tx, s.jobs, targs, u.ID, to, s.now().UTC()); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "store.ownership_transferred", TargetType: "user", TargetID: to.String(),
			Metadata: map[string]any{"from": u.ID.String(), "to": to.String()}})
	})
}

// RequestOwnershipTransfer asks for the store-in-ctx's ownership to move
// to member to in SupportDelay (the console): the owner now gets a cancel
// link, the new owner a heads-up.
func (s *Service) RequestOwnershipTransfer(ctx context.Context, admin, to ulid.ID, reason string) (store.SupportRequest, error) {
	return s.openRequest(ctx, admin, store.KindOwnershipTransfer, to, reason,
		func(tx pgx.Tx, u domain.User, role authz.Role) (*ulid.ID, []recipient, error) {
			if role == authz.RoleOwner {
				return nil, nil, errAlreadyOwner
			}
			if u.Status != domain.UserActive {
				return nil, nil, errMemberDisabled
			}
			owner, err := ownerOf(ctx, tx)
			if err != nil {
				return nil, nil, err
			}
			if owner == nil {
				return nil, nil, errNoOwner
			}
			return &owner.ID, []recipient{{user: *owner, owner: true}}, nil
		},
		func(tx pgx.Tx, targs jobs.TenantArgs, req store.SupportRequest) error {
			_, err := s.jobs.InsertTx(ctx, tx, SupportNoticeArgs{TenantArgs: targs, UserID: req.UserID, Other: *req.FromUserID,
				Event: NoticeTransferAsked, At: req.EffectiveAt}, nil)
			return err
		})
}

// verifyPassword checks the signed-in user's password; a wrong one is a
// field error on field, and failures are throttled per user like logins.
func (s *Service) verifyPassword(ctx context.Context, userID ulid.ID, password, field string) (domain.User, error) {
	wrong := errs.Validation(errs.FieldError{Field: field, Code: "wrong", Message: "the password is not right"})
	failKey := "password:fail:" + userID.String()
	if err := s.peek(ctx, failKey, s.limits.LoginFailPerUser); err != nil {
		return domain.User{}, err
	}
	u, err := store.UserByID(ctx, s.db, userID)
	if err != nil {
		return domain.User{}, errNoSession
	}
	if len(password) > 4*domain.MaxPasswordLen {
		return domain.User{}, wrong
	}
	match, _, err := s.hasher.Verify(ctx, password, u.PasswordHash)
	if err != nil {
		return domain.User{}, err
	}
	if !match {
		if _, err = s.limiter.Allow(ctx, failKey, s.limits.LoginFailPerUser); err != nil {
			return domain.User{}, errs.Unavailable(5*time.Second, err)
		}
		return domain.User{}, wrong
	}
	if err = s.limiter.Reset(ctx, failKey); err != nil {
		s.log.WarnContext(ctx, "identity: reset password failures", "err", err)
	}
	return u, nil
}
