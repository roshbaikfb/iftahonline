package httpapi

import "context"

// AdminPushStatus implements StrictServerInterface.
func (h *Handlers) AdminPushStatus(ctx context.Context, _ AdminPushStatusRequestObject) (AdminPushStatusResponseObject, error) {
	st, err := h.svc.PushStatus(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminPushStatus200JSONResponse{Devices: st.Devices}
	if st.PublicKey != "" {
		out.PublicKey = &st.PublicKey
	}
	return out, nil
}

// AdminSubscribePush implements StrictServerInterface.
func (h *Handlers) AdminSubscribePush(ctx context.Context, req AdminSubscribePushRequestObject) (AdminSubscribePushResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.SubscribePush(ctx, req.Body.Endpoint, req.Body.Keys.P256dh, req.Body.Keys.Auth); err != nil {
		return nil, err
	}
	return AdminSubscribePush204Response{}, nil
}

// AdminUnsubscribePush implements StrictServerInterface.
func (h *Handlers) AdminUnsubscribePush(ctx context.Context, req AdminUnsubscribePushRequestObject) (AdminUnsubscribePushResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.UnsubscribePush(ctx, req.Body.Endpoint); err != nil {
		return nil, err
	}
	return AdminUnsubscribePush204Response{}, nil
}
