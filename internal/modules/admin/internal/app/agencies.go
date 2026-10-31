package app

import (
	"context"
	"errors"
	"slices"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// AgencyDetail is an agency and its stores.
type AgencyDetail struct {
	Agency billing.Agency
	Stores []ulid.ID
}

var (
	errNoAgencies = errs.Unavailable(0, errors.New("admin: agencies are not wired"))
	errNotAMember = errs.NotFound("store_not_in_agency", "the store is not in this agency")
)

func (s *Service) agencies(ctx context.Context) (Identity, billing.Agencies, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Agencies == nil {
		return Identity{}, nil, errNoAgencies
	}
	return who, s.d.Agencies, nil
}

// Agencies lists every agency (ADR-0059).
func (s *Service) Agencies(ctx context.Context) ([]billing.Agency, error) {
	_, a, err := s.agencies(ctx)
	if err != nil {
		return nil, err
	}
	return a.Agencies(ctx)
}

// Agency is one agency and its stores.
func (s *Service) Agency(ctx context.Context, id ulid.ID) (AgencyDetail, error) {
	_, a, err := s.agencies(ctx)
	if err != nil {
		return AgencyDetail{}, err
	}
	ag, members, err := a.Agency(ctx, id)
	return AgencyDetail{Agency: ag, Stores: members}, err
}

// CreateAgency opens an agency account. It is recorded once billing has
// given it its ID: a refused creation leaves nothing to record.
func (s *Service) CreateAgency(ctx context.Context, name, currency, planCode, rawReason string) (AgencyDetail, error) {
	who, a, err := s.agencies(ctx)
	if err != nil {
		return AgencyDetail{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return AgencyDetail{}, err
	}
	ag, err := a.CreateAgency(ctx, name, currency, planCode)
	if err != nil {
		return AgencyDetail{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "agency.created", "agency", ag.ID.String(),
		map[string]any{"reason": reason, "name": ag.Name, "currency": ag.Currency, "plan_code": ag.PlanCode}); err != nil {
		return AgencyDetail{}, err
	}
	return AgencyDetail{Agency: ag, Stores: []ulid.ID{}}, nil
}

// AddStore moves store into agency, recorded first.
func (s *Service) AddStore(ctx context.Context, agency ulid.ID, store tenant.StoreID, rawReason string) (AgencyDetail, error) {
	who, a, err := s.agencies(ctx)
	if err != nil {
		return AgencyDetail{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return AgencyDetail{}, err
	}
	_, sctx, err := s.storeCtx(ctx, store)
	if err != nil {
		return AgencyDetail{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "store.agency_joined", "store", store.String(),
		map[string]any{"reason": reason, "agency": agency.String()}); err != nil {
		return AgencyDetail{}, err
	}
	if err = a.JoinAgency(sctx, agency); err != nil {
		return AgencyDetail{}, err
	}
	return s.Agency(ctx, agency)
}

// RemoveStore takes store out of agency, onto the free plan, recorded
// first.
func (s *Service) RemoveStore(ctx context.Context, agency ulid.ID, store tenant.StoreID, rawReason string) (AgencyDetail, error) {
	who, a, err := s.agencies(ctx)
	if err != nil {
		return AgencyDetail{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return AgencyDetail{}, err
	}
	_, sctx, err := s.storeCtx(ctx, store)
	if err != nil {
		return AgencyDetail{}, err
	}
	_, members, err := a.Agency(ctx, agency)
	if err != nil {
		return AgencyDetail{}, err
	}
	if !slices.Contains(members, store.ID) {
		return AgencyDetail{}, errNotAMember
	}
	if err = s.audit(ctx, &who.AdminID, "store.agency_left", "store", store.String(),
		map[string]any{"reason": reason, "agency": agency.String()}); err != nil {
		return AgencyDetail{}, err
	}
	if err = a.LeaveAgency(sctx); err != nil {
		return AgencyDetail{}, err
	}
	return s.Agency(ctx, agency)
}
