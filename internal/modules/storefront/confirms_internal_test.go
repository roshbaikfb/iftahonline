package storefront

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

func TestToConfirmView(t *testing.T) {
	t.Parallel()
	tests := []struct {
		v    messaging.LinkView
		want string
	}{
		{messaging.LinkView{Status: messaging.ConfirmationPending}, web.ConfirmWaiting},
		{messaging.LinkView{Status: messaging.ConfirmationUnreached}, web.ConfirmWaiting},
		{messaging.LinkView{Status: messaging.ConfirmationConfirmed}, web.ConfirmDone},
		{messaging.LinkView{Status: messaging.ConfirmationConfirmed, AwaitingCall: true}, web.ConfirmAwaitingCall},
		{messaging.LinkView{Status: messaging.ConfirmationCancelled}, web.ConfirmCancelled},
		{messaging.LinkView{Status: messaging.ConfirmationExpired}, web.ConfirmClosed},
	}
	for _, tt := range tests {
		got, err := toConfirmView(tt.v, nil)
		if err != nil || got.State != tt.want {
			t.Errorf("%+v = %q, %v; want %q", tt.v, got.State, err, tt.want)
		}
	}
	if _, err := toConfirmView(messaging.LinkView{}, messaging.ErrConfirmationNotFound); !errors.Is(err, web.ErrConfirmationNotFound) {
		t.Errorf("not found: %v", err)
	}
}
