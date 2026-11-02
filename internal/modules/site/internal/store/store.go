// Package store adapts the sqlc queries in sitedb to domain types. The
// site's content is platform data (no store, no RLS): the storefront
// role's pool reads it, the console's pool writes it.
package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/store/sitedb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is what the functions run on: a pool or a transaction.
type Tx = sitedb.DBTX

// Settings are the site's contact details.
func Settings(ctx context.Context, db Tx) (domain.Settings, error) {
	r, err := sitedb.New(db).GetSettings(ctx)
	if err != nil {
		return domain.Settings{}, fmt.Errorf("site store: settings: %w", err)
	}
	s := domain.Settings{WhatsApp: r.Whatsapp, WhatsAppIntl: r.WhatsappIntl, Email: r.Email, CompanyNumber: r.CompanyNumber,
		Socials: map[domain.Social]string{}}
	var socials map[string]string
	if err = json.Unmarshal(r.Socials, &socials); err != nil {
		return domain.Settings{}, fmt.Errorf("site store: socials: %w", err)
	}
	for k, v := range socials {
		s.Socials[domain.Social(k)] = v
	}
	return s, nil
}

// SaveSettings writes the site's contact details.
func SaveSettings(ctx context.Context, db Tx, s domain.Settings) error {
	socials := make(map[string]string, len(s.Socials))
	for k, v := range s.Socials {
		socials[string(k)] = v
	}
	raw, err := json.Marshal(socials)
	if err != nil {
		return fmt.Errorf("site store: socials: %w", err)
	}
	if err = sitedb.New(db).SaveSettings(ctx, sitedb.SaveSettingsParams{Whatsapp: s.WhatsApp, WhatsappIntl: s.WhatsAppIntl, Email: s.Email,
		Socials: raw, CompanyNumber: s.CompanyNumber}); err != nil {
		return fmt.Errorf("site store: save settings: %w", err)
	}
	return nil
}

// FAQs are the questions in order.
func FAQs(ctx context.Context, db Tx) ([]domain.FAQ, error) {
	rows, err := sitedb.New(db).ListFAQs(ctx)
	if err != nil {
		return nil, fmt.Errorf("site store: faqs: %w", err)
	}
	out := make([]domain.FAQ, len(rows))
	for i, r := range rows {
		out[i] = domain.FAQ{ID: r.ID, Position: int(r.Position), OnHome: r.OnHome, QuestionAr: r.QuestionAr, AnswerAr: r.AnswerAr,
			QuestionEn: r.QuestionEn, AnswerEn: r.AnswerEn}
	}
	return out, nil
}

// InsertFAQ adds a question.
func InsertFAQ(ctx context.Context, db Tx, f domain.FAQ) error {
	if err := sitedb.New(db).InsertFAQ(ctx, sitedb.InsertFAQParams{ID: f.ID, Position: int32(f.Position), OnHome: f.OnHome, //nolint:gosec // Validate bounds it
		QuestionAr: f.QuestionAr, AnswerAr: f.AnswerAr, QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn}); err != nil {
		return fmt.Errorf("site store: insert faq: %w", err)
	}
	return nil
}

// UpdateFAQ rewrites a question; false when there is none.
func UpdateFAQ(ctx context.Context, db Tx, f domain.FAQ) (bool, error) {
	n, err := sitedb.New(db).UpdateFAQ(ctx, sitedb.UpdateFAQParams{ID: f.ID, Position: int32(f.Position), OnHome: f.OnHome, //nolint:gosec // Validate bounds it
		QuestionAr: f.QuestionAr, AnswerAr: f.AnswerAr, QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn})
	if err != nil {
		return false, fmt.Errorf("site store: update faq: %w", err)
	}
	return n > 0, nil
}

// DeleteFAQ removes a question; false when there is none.
func DeleteFAQ(ctx context.Context, db Tx, id ulid.ID) (bool, error) {
	n, err := sitedb.New(db).DeleteFAQ(ctx, id)
	if err != nil {
		return false, fmt.Errorf("site store: delete faq: %w", err)
	}
	return n > 0, nil
}
