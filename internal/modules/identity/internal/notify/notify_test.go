package notify_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/notify"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

type captureMailer struct{ got []mailer.Message }

func (c *captureMailer) Send(_ context.Context, m mailer.Message) error {
	c.got = append(c.got, m)
	return nil
}

func TestOTPSenders(t *testing.T) {
	t.Parallel()
	num, err := phone.Parse("01012345678", phone.EG)
	if err != nil {
		t.Fatal(err)
	}
	mail := &captureMailer{}
	if err = (notify.MailOTPSender{Mailer: mail}).SendOTP(t.Context(), num, "code 123456"); err != nil {
		t.Fatal(err)
	}
	if len(mail.got) != 1 || mail.got[0].To != "201012345678@sms.invalid" || strings.Contains(mail.got[0].Subject, "12345678") {
		t.Fatalf("dev SMS = %+v", mail.got)
	}
	err = notify.DisabledOTPSender{}.SendOTP(t.Context(), num, "x")
	if !errors.Is(err, notify.ErrOTPUnavailable) || !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("disabled = %v", err)
	}
}
