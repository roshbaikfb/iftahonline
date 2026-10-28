package fake_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/channeltest"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/fake"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

func mustPhone(t *testing.T, s string) phone.E164 {
	t.Helper()
	p, err := phone.Parse(s, phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestContract(t *testing.T) {
	t.Parallel()
	for _, templates := range []bool{true, false} {
		c := fake.New("fake", []byte("secret"))
		unreachable := mustPhone(t, "01099999999")
		c.Unreachable(unreachable.Unmasked())
		channeltest.Run(t, channeltest.Harness{Channel: c, To: mustPhone(t, "01012345678"), Unreachable: unreachable, Templates: templates,
			Delivered: func(_ *testing.T, sent channel.Sent, ref string) channel.Webhook {
				return c.Webhook(channel.Event{Kind: channel.StatusEvent, ID: sent.ProviderID + ":delivered", ProviderID: sent.ProviderID,
					Ref: ref, Status: channel.StatusDelivered})
			}})
	}
}

func TestFakeRecordsAndFails(t *testing.T) {
	t.Parallel()
	c := fake.New("fake", []byte("secret"))
	to := mustPhone(t, "01012345678")
	if _, err := c.Send(t.Context(), channel.Message{To: to, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("down")
	c.FailWith(boom)
	if _, err := c.Send(t.Context(), channel.Message{To: to, Text: "hi"}); !errors.Is(err, boom) {
		t.Fatalf("FailWith: %v", err)
	}
	c.FailWith(nil)
	if got := c.Sent(); len(got) != 1 || got[0].Text != "hi" {
		t.Fatalf("Sent = %+v", got)
	}
	if !c.Undeliverable(fake.UndeliverableCode) || c.Undeliverable("x") || c.Name() != "fake" {
		t.Fatal("codes/name")
	}
	if _, err := c.VerifyWebhook(channel.Webhook{Header: map[string][]string{fake.SignatureHeader: {fake.Sign([]byte("secret"), []byte("{"))}}, Body: []byte("{")}); !errors.Is(err, channel.ErrBadWebhook) {
		t.Fatalf("bad body: %v", err)
	}
}
