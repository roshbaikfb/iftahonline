package app

import (
	"context"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
)

// TestPushEndpointOK: only https URLs of the big push services are
// reached (ADR-0080); a browser's endpoint is never trusted as an address.
func TestPushEndpointOK(t *testing.T) {
	t.Parallel()
	for endpoint, want := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":                true,
		"https://updates.push.services.mozilla.com/wpush/v2/abc": true,
		"https://web.push.apple.com/QGuQyavXutnMei":              true,
		"https://wns2-par02p.notify.windows.com/w/?token=abc":    true,
		"http://fcm.googleapis.com/fcm/send/abc":                 false,
		"https://fcm.googleapis.com:8443/fcm/send/abc":           false,
		"https://user@fcm.googleapis.com/fcm/send/abc":           false,
		"https://fcm.googleapis.com.evil.example/x":              false,
		"https://169.254.169.254/latest/meta-data":               false,
		"https://localhost/push":                                 false,
		"https://notify.windows.com.evil.example/x":              false,
		"not a url": false,
	} {
		if got := pushEndpointOK(endpoint); got != want {
			t.Errorf("%s = %v; want %v", endpoint, got, want)
		}
	}
	// The sender refuses one too, and asks to forget it.
	gone, err := NewWebPusher("pub", "priv", "mailto:ops@iftahonline.com").Push(context.Background(),
		store.PushDevice{Endpoint: "https://127.0.0.1/push"}, []byte("{}"), true)
	if !gone || err == nil {
		t.Fatalf("a private endpoint = %v, %v", gone, err)
	}
}
