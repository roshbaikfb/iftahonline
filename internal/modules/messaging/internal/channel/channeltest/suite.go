// Package channeltest is the contract every messaging channel adapter
// passes (ADR-0039): against the fake channel and in-process stand-ins
// of WhatsApp Cloud and Twilio in every build, and against the real
// services once test accounts exist.
package channeltest

import (
	"context"
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Harness is one adapter under test.
type Harness struct {
	Channel channel.Channel
	// To is a reachable recipient; Unreachable one the channel reports
	// as such (zero: the harness cannot make one; the case is skipped).
	To, Unreachable phone.E164
	// Templates: the channel sends template messages (WhatsApp); else
	// text (SMS).
	Templates bool
	// Delivered is the signed webhook the channel sends when the message
	// it accepted as sent, with reference ref, reached the phone.
	Delivered func(t *testing.T, sent channel.Sent, ref string) channel.Webhook
}

// Ref is a fresh message reference (domain.Ref's format).
func Ref() string {
	ids := ulid.NewGenerator()
	return "m1." + tenant.StoreID{ID: ids.New()}.String() + "." + ids.New().String()
}

// Message is a valid message to to with reference ref.
func (h Harness) Message(to phone.E164, ref string) channel.Message {
	if h.Templates {
		return channel.Message{To: to, Template: "order_confirmation_v1", Language: "ar", Params: []string{"منى", "متجر سوق", "7P0R1S2T", "520.00 EGP"},
			Buttons: []channel.Button{{Payload: "b1.confirm"}, {Payload: "b1.cancel"}}, Ref: ref}
	}
	return channel.Message{To: to, Text: "متجر سوق: أكّد طلبك رقم 7P0R1S2T من هنا: https://souq.example.com/ar/o/tok", Ref: ref}
}

// Run runs the contract.
func Run(t *testing.T, h Harness) {
	c := h.Channel
	t.Run("send and hear it was delivered", func(t *testing.T) {
		ref := Ref()
		sent, err := c.Send(t.Context(), h.Message(h.To, ref))
		if err != nil || sent.ProviderID == "" {
			t.Fatalf("Send = %+v, %v", sent, err)
		}
		events, err := c.VerifyWebhook(h.Delivered(t, sent, ref))
		if err != nil || len(events) != 1 {
			t.Fatalf("VerifyWebhook = %+v, %v", events, err)
		}
		e := events[0]
		if e.Kind != channel.StatusEvent || e.Status != channel.StatusDelivered || e.ProviderID != sent.ProviderID || e.Ref != ref || e.ID == "" {
			t.Fatalf("event = %+v", e)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		if h.Unreachable.IsZero() {
			t.Skip("the harness cannot make an unreachable recipient")
		}
		if _, err := c.Send(t.Context(), h.Message(h.Unreachable, Ref())); !errors.Is(err, channel.ErrUndeliverable) {
			t.Fatalf("Send = %v, want ErrUndeliverable", err)
		}
	})
	t.Run("invalid message rejected", func(t *testing.T) {
		m := h.Message(phone.E164{}, Ref())
		if _, err := c.Send(t.Context(), m); !errors.Is(err, channel.ErrRejected) {
			t.Fatalf("no recipient: %v", err)
		}
		m = h.Message(h.To, Ref())
		m.Text, m.Template = "", ""
		if _, err := c.Send(t.Context(), m); !errors.Is(err, channel.ErrRejected) {
			t.Fatalf("no content: %v", err)
		}
	})
	t.Run("forged webhook", func(t *testing.T) {
		ref := Ref()
		sent, err := c.Send(t.Context(), h.Message(h.To, ref))
		if err != nil {
			t.Fatal(err)
		}
		w := h.Delivered(t, sent, ref)
		w.Body = append(append([]byte(nil), w.Body...), ' ')
		if _, err = c.VerifyWebhook(w); !errors.Is(err, channel.ErrForged) {
			t.Fatalf("changed body: %v", err)
		}
		w = h.Delivered(t, sent, ref)
		w.Header = map[string][]string{}
		if _, err = c.VerifyWebhook(w); !errors.Is(err, channel.ErrForged) {
			t.Fatalf("unsigned: %v", err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := c.Send(ctx, h.Message(h.To, Ref())); err == nil {
			t.Fatal("sent with a cancelled context")
		}
	})
}
