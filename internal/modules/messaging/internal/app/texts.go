package app

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Page sizes of the message log.
const (
	DefaultPage = 50
	MaxPage     = 200
)

// Errors the HTTP layer renders.
var (
	ErrTextNotFound = errs.NotFound("message_text_not_found", "no such message kind or locale")
	errPageSize     = errs.Validation(errs.FieldError{Field: "limit", Code: "range", Message: "limit must be 1 to 200"})
	errBadStatus    = errs.Validation(errs.FieldError{Field: "status", Code: "invalid", Message: "unknown status"})
	errBadSMS       = errs.Validation(errs.FieldError{Field: "sms_body", Code: "invalid",
		Message: "1 to 400 characters, using only {{name}}, {{store}}, {{ref}}, {{total}} and {{url}}; an order confirmation needs {{url}}"})
)

// smsBody is the store's SMS text for kind and locale, or the default.
func (s *Service) smsBody(ctx context.Context, kind domain.Kind, locale string) (string, error) {
	body := domain.DefaultSMS(kind, locale)
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		own, found, err := store.SMSBody(ctx, tx, kind, locale)
		if found {
			body = own
		}
		return err
	})
	return body, err
}

// Texts lists the store's SMS text for every kind and locale: its own,
// or the default.
func (s *Service) Texts(ctx context.Context) ([]domain.Text, error) {
	var own []domain.Text
	if err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		own, err = store.Texts(ctx, tx)
		return err
	}); err != nil {
		return nil, err
	}
	var out []domain.Text
	for _, k := range domain.Kinds() {
		for _, l := range domain.Locales() {
			t := domain.Text{Kind: k, Locale: l, SMSBody: domain.DefaultSMS(k, l)}
			for _, o := range own {
				if o.Kind == k && o.Locale == l {
					t = o
				}
			}
			out = append(out, t)
		}
	}
	return out, nil
}

func checkText(kind domain.Kind, locale string) error {
	if !slices.Contains(domain.Kinds(), kind) || !domain.ValidLocale(locale) {
		return ErrTextNotFound
	}
	return nil
}

// SaveText sets the store's SMS text for kind and locale.
func (s *Service) SaveText(ctx context.Context, kind domain.Kind, locale, body string) (domain.Text, error) {
	if err := checkText(kind, locale); err != nil {
		return domain.Text{}, err
	}
	if !domain.ValidSMS(kind, body) {
		return domain.Text{}, errBadSMS
	}
	if err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.SaveSMSBody(ctx, tx, kind, locale, body); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "message_text.updated", TargetType: "message_text",
			TargetID: string(kind) + ":" + locale})
	}); err != nil {
		return domain.Text{}, err
	}
	at := s.now().UTC()
	return domain.Text{Kind: kind, Locale: locale, SMSBody: body, Custom: true, UpdatedAt: &at}, nil
}

// ResetText goes back to the default SMS text for kind and locale.
func (s *Service) ResetText(ctx context.Context, kind domain.Kind, locale string) (domain.Text, error) {
	if err := checkText(kind, locale); err != nil {
		return domain.Text{}, err
	}
	if err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		had, err := store.DeleteSMSBody(ctx, tx, kind, locale)
		if err != nil || !had {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "message_text.reset", TargetType: "message_text",
			TargetID: string(kind) + ":" + locale})
	}); err != nil {
		return domain.Text{}, err
	}
	return domain.Text{Kind: kind, Locale: locale, SMSBody: domain.DefaultSMS(kind, locale)}, nil
}

// Page is one page of the log, with the last item to continue after.
type Page struct {
	Items []domain.Message
	Next  *domain.Message
}

// Messages returns the log of the store in ctx, newest first, after
// (exclusive) a message; status "" is any.
func (s *Service) Messages(ctx context.Context, after *domain.Message, status domain.Status, limit int) (Page, error) {
	if limit == 0 {
		limit = DefaultPage
	}
	if limit < 1 || limit > MaxPage {
		return Page{}, errPageSize
	}
	if status != "" && !status.Valid() {
		return Page{}, errBadStatus
	}
	beforeAt, beforeID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), ulid.ID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if after != nil {
		beforeAt, beforeID = after.CreatedAt, after.ID
	}
	var items []domain.Message
	if err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		items, err = store.List(ctx, tx, beforeAt, beforeID, status, limit+1)
		return err
	}); err != nil {
		return Page{}, err
	}
	p := Page{Items: items}
	if len(items) > limit {
		p.Items = items[:limit]
		p.Next = &p.Items[limit-1]
	}
	return p, nil
}

// OrderMessages returns an order's messages, newest first.
func (s *Service) OrderMessages(ctx context.Context, order ulid.ID) ([]domain.Message, error) {
	var out []domain.Message
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		out, err = store.OrderMessages(ctx, tx, order)
		return err
	})
	return out, err
}

// Usage is the store's message quota this month.
func (s *Service) Usage(ctx context.Context) (entitlements.Usage, error) {
	return s.quota.Usage(ctx, entitlements.Messages)
}
