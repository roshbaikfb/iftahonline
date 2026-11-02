package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
)

// AdminGetFounding implements StrictServerInterface.
func (h *Handlers) AdminGetFounding(ctx context.Context, _ AdminGetFoundingRequestObject) (AdminGetFoundingResponseObject, error) {
	p, members, err := h.svc.Founding(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminGetFounding200JSONResponse{Program: toFoundingProgram(p), Members: make([]AdminFoundingMember, len(members))}
	for i, m := range members {
		out.Members[i] = AdminFoundingMember{StoreId: m.StoreID.String(), Seat: m.Seat, DiscountBps: int(m.DiscountBps), Currency: m.Currency,
			PlanCode: m.PlanCode, Cycle: AdminFoundingMemberCycle(m.Cycle), LockedMinor: m.LockedMinor, JoinedAt: m.JoinedAt}
		if !m.Active() {
			lost, reason := m.LostAt, AdminFoundingMemberLostReason(m.LostReason)
			out.Members[i].LostAt, out.Members[i].LostReason = &lost, &reason
		}
	}
	return out, nil
}

// AdminSaveFounding implements StrictServerInterface.
func (h *Handlers) AdminSaveFounding(ctx context.Context, req AdminSaveFoundingRequestObject) (AdminSaveFoundingResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	p := billing.FoundingProgram{Enabled: b.Enabled, Seats: b.Seats, DiscountBps: int32(b.DiscountBps)} //nolint:gosec // the contract bounds it
	if err := h.svc.SaveFounding(ctx, p, b.Reason); err != nil {
		return nil, err
	}
	return AdminSaveFounding200JSONResponse(toFoundingProgram(p)), nil
}

func toFoundingProgram(p billing.FoundingProgram) AdminFoundingProgram {
	return AdminFoundingProgram{Enabled: p.Enabled, Seats: p.Seats, DiscountBps: int(p.DiscountBps)}
}
