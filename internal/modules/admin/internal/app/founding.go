package app

import (
	"context"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

var errNoFounding = errs.Unavailable(0, errors.New("admin: the founding program is not wired"))

func (s *Service) founding(ctx context.Context) (Identity, billing.Founding, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Founding == nil {
		return Identity{}, nil, errNoFounding
	}
	return who, s.d.Founding, nil
}

// Founding is the founding-merchants program's settings and every
// member, by seat.
func (s *Service) Founding(ctx context.Context) (billing.FoundingProgram, []billing.FoundingMember, error) {
	_, f, err := s.founding(ctx)
	if err != nil {
		return billing.FoundingProgram{}, nil, err
	}
	p, err := f.FoundingProgram(ctx)
	if err != nil {
		return billing.FoundingProgram{}, nil, err
	}
	members, err := f.FoundingMembers(ctx)
	return p, members, err
}

// SaveFounding writes the program's settings, recorded first (refused
// settings leave nothing to record).
func (s *Service) SaveFounding(ctx context.Context, p billing.FoundingProgram, rawReason string) error {
	who, f, err := s.founding(ctx)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	if err = p.Validate(); err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "billing.founding_saved", "founding_program", "program",
		map[string]any{"reason": reason, "enabled": p.Enabled, "seats": p.Seats, "discount_bps": p.DiscountBps}); err != nil {
		return err
	}
	return f.SaveFoundingProgram(ctx, p)
}
