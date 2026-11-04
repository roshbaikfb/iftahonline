package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/clientinfo"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/safehttp"
)

// The console's mobile notifications (ADR-0080): Web Push to the devices
// an admin turned them on in. Only the big push services' hosts are
// reached, through safehttp: a device's endpoint is the browser's word,
// never trusted as an address.

// Pusher sends one notification to one device; gone is true when the
// push service no longer knows the device (to forget it).
type Pusher interface {
	Push(ctx context.Context, d store.PushDevice, payload []byte, urgent bool) (gone bool, err error)
}

// pushHosts are the push services browsers use: Chrome and Android,
// Firefox, Safari and iOS, Edge.
var pushHosts = []string{"fcm.googleapis.com", "updates.push.services.mozilla.com", "web.push.apple.com"} //nolint:gochecknoglobals // a fixed list

var errPushEndpoint = errs.Validation(errs.FieldError{Field: "endpoint", Code: "invalid", Message: "not a push service this console sends to"})

// pushEndpointOK reports whether endpoint is an https URL of a known push
// service.
func pushEndpointOK(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h {
			return true
		}
	}
	return strings.HasSuffix(host, ".notify.windows.com")
}

// WebPusher sends Web Push (RFC 8030, encrypted per RFC 8291, signed
// with VAPID per RFC 8292).
type WebPusher struct {
	client                       *http.Client
	publicKey, privateKey, owner string
}

// NewWebPusher sends with the VAPID keys; subject is a mailto: or https:
// contact the push services may use.
func NewWebPusher(publicKey, privateKey, subject string) *WebPusher {
	return &WebPusher{client: safehttp.New(safehttp.Options{MaxResponseBytes: 64 << 10, MaxRedirects: -1, AllowedPorts: []int{443}}),
		publicKey: publicKey, privateKey: privateKey, owner: strings.TrimPrefix(subject, "mailto:")}
}

// Push implements Pusher.
func (p *WebPusher) Push(ctx context.Context, d store.PushDevice, payload []byte, urgent bool) (bool, error) {
	if !pushEndpointOK(d.Endpoint) {
		return true, errPushEndpoint
	}
	urgency := webpush.UrgencyNormal
	if urgent {
		urgency = webpush.UrgencyHigh
	}
	res, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: d.Endpoint,
		Keys: webpush.Keys{P256dh: d.P256dh, Auth: d.Auth}}, &webpush.Options{HTTPClient: p.client, Subscriber: p.owner,
		VAPIDPublicKey: p.publicKey, VAPIDPrivateKey: p.privateKey, TTL: 24 * 60 * 60, Urgency: urgency})
	if err != nil {
		return false, fmt.Errorf("admin: web push: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return true, nil
	case res.StatusCode >= 300:
		return false, fmt.Errorf("admin: web push: %s", res.Status)
	}
	return false, nil
}

// PushStatus is what the console needs to turn notifications on: the
// public key (empty when they are off on this server) and how many of
// the admin's devices have them.
type PushStatus struct {
	PublicKey string
	Devices   int64
}

// PushStatus is the signed-in admin's.
func (s *Service) PushStatus(ctx context.Context) (PushStatus, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return PushStatus{}, errNoSession
	}
	n, err := store.CountPushDevices(ctx, s.d.DB, who.AdminID)
	return PushStatus{PublicKey: s.d.PushPublicKey, Devices: n}, err
}

var (
	errPushOff  = errs.Unavailable(0, errors.New("admin: mobile notifications are not configured"))
	errPushKeys = errs.Validation(errs.FieldError{Field: "keys", Code: "invalid", Message: "the browser's p256dh and auth keys"})
)

// SubscribePush records the device the signed-in admin turned
// notifications on in.
func (s *Service) SubscribePush(ctx context.Context, endpoint, p256dh, auth string) error {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	if s.d.PushPublicKey == "" {
		return errPushOff
	}
	if !pushEndpointOK(endpoint) || len(endpoint) > 2048 {
		return errPushEndpoint
	}
	if !keyOf(p256dh, 65) || !keyOf(auth, 16) {
		return errPushKeys
	}
	return store.SavePushDevice(ctx, s.d.DB, store.PushDevice{ID: s.d.IDs.New(), Admin: who.AdminID, Endpoint: endpoint, P256dh: p256dh,
		Auth: auth, UserAgent: clientinfo.TruncateUserAgent(metaFrom(ctx).UserAgent)})
}

// UnsubscribePush forgets the signed-in admin's device.
func (s *Service) UnsubscribePush(ctx context.Context, endpoint string) error {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	_, err := store.RemovePushDevice(ctx, s.d.DB, who.AdminID, endpoint)
	return err
}

// keyOf reports whether v is a base64url key of n bytes.
func keyOf(v string, n int) bool {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(v, "="))
	return err == nil && len(b) == n
}
