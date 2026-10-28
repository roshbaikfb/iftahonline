// Package notify delivers one-time codes to phones. SMS/WhatsApp codes
// have no production sender yet. Outside production they are delivered as
// email to <digits>@sms.invalid, so developers read them in Mailpit and
// codes never reach logs. In production, phone OTP is unavailable (503)
// until a provider is wired. Email itself is platform/mailer.
package notify

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

// OTPSender delivers a one-time code to a phone.
type OTPSender interface {
	SendOTP(ctx context.Context, to phone.E164, text string) error
}

// ErrOTPUnavailable means no phone channel is configured.
var ErrOTPUnavailable = errs.Unavailable(time.Minute, errors.New("notify: no SMS provider configured"))

// MailOTPSender delivers codes by email to <digits>@sms.invalid; for
// development and tests only (cmd/platform never uses it in production).
type MailOTPSender struct{ Mailer mailer.Mailer }

// SendOTP implements OTPSender.
func (s MailOTPSender) SendOTP(ctx context.Context, to phone.E164, text string) error {
	digits := strings.TrimPrefix(to.Unmasked(), "+")
	return s.Mailer.Send(ctx, mailer.Message{To: digits + "@sms.invalid", Subject: "SMS to " + to.Masked(), Text: text})
}

// DisabledOTPSender refuses: phone OTP is unavailable.
type DisabledOTPSender struct{}

// SendOTP implements OTPSender.
func (DisabledOTPSender) SendOTP(context.Context, phone.E164, string) error { return ErrOTPUnavailable }
