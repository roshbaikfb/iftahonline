// Package store adapts the sqlc queries in sitedb to domain types. The
// site's content is platform data (no store, no RLS): the storefront
// role's pool reads it, the console's pool writes it.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/store/sitedb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is what the functions run on: a pool or a transaction.
type Tx = sitedb.DBTX

// Settings are the site's contact details.
func Settings(ctx context.Context, db Tx) (domain.Settings, error) {
	s, _, err := SettingsAndIdentity(ctx, db)
	return s, err
}

// SettingsAndIdentity are the contact details and the site's identity,
// read together (one row).
func SettingsAndIdentity(ctx context.Context, db Tx) (domain.Settings, domain.Identity, error) {
	r, err := sitedb.New(db).GetSettings(ctx)
	if err != nil {
		return domain.Settings{}, domain.Identity{}, fmt.Errorf("site store: settings: %w", err)
	}
	id := domain.Identity{Story: domain.Story{Ar: r.StoryAr, En: r.StoryEn, VideoID: r.VideoID}, LogoKey: r.LogoKey, ShareKey: r.OgKey}
	s := domain.Settings{WhatsApp: r.Whatsapp, WhatsAppIntl: r.WhatsappIntl, Email: r.Email, CompanyNumber: r.CompanyNumber,
		Socials: map[domain.Social]string{}}
	var socials map[string]string
	if err = json.Unmarshal(r.Socials, &socials); err != nil {
		return domain.Settings{}, domain.Identity{}, fmt.Errorf("site store: socials: %w", err)
	}
	for k, v := range socials {
		s.Socials[domain.Social(k)] = v
	}
	return s, id, nil
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

// SaveStory writes the founding story and the video.
func SaveStory(ctx context.Context, db Tx, st domain.Story) error {
	if err := sitedb.New(db).SaveStory(ctx, sitedb.SaveStoryParams{StoryAr: st.Ar, StoryEn: st.En, VideoID: st.VideoID}); err != nil {
		return fmt.Errorf("site store: save story: %w", err)
	}
	return nil
}

// SetImage points the site's image at key ("" removes it).
func SetImage(ctx context.Context, db Tx, which domain.Image, key string) error {
	q := sitedb.New(db)
	var err error
	if which == domain.Logo {
		err = q.SetLogo(ctx, key)
	} else {
		err = q.SetShareImage(ctx, key)
	}
	if err != nil {
		return fmt.Errorf("site store: set %s: %w", which, err)
	}
	return nil
}

// People are the founders, then the team, each in order.
func People(ctx context.Context, db Tx) ([]domain.Person, error) {
	rows, err := sitedb.New(db).ListPeople(ctx)
	if err != nil {
		return nil, fmt.Errorf("site store: people: %w", err)
	}
	out := make([]domain.Person, len(rows))
	for i, r := range rows {
		out[i] = person(sitedb.GetPersonRow(r))
	}
	return out, nil
}

// Person is one person; false when there is none.
func Person(ctx context.Context, db Tx, id ulid.ID) (domain.Person, bool, error) {
	r, err := sitedb.New(db).GetPerson(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Person{}, false, nil
	}
	if err != nil {
		return domain.Person{}, false, fmt.Errorf("site store: person: %w", err)
	}
	return person(r), true, nil
}

func person(r sitedb.GetPersonRow) domain.Person {
	return domain.Person{ID: r.ID, Kind: domain.PersonKind(r.Kind), Position: int(r.Position), NameAr: r.NameAr, NameEn: r.NameEn,
		RoleAr: r.RoleAr, RoleEn: r.RoleEn, BioAr: r.BioAr, BioEn: r.BioEn, PhotoKey: r.PhotoKey, LinkedIn: r.LinkedinUrl, X: r.XUrl}
}

// InsertPerson adds a person.
func InsertPerson(ctx context.Context, db Tx, p domain.Person) error {
	u := personParams(p)
	if err := sitedb.New(db).InsertPerson(ctx, sitedb.InsertPersonParams{ID: u.ID, Kind: u.Kind, Position: u.Position, NameAr: u.NameAr, NameEn: u.NameEn,
		RoleAr: u.RoleAr, RoleEn: u.RoleEn, BioAr: u.BioAr, BioEn: u.BioEn, PhotoKey: u.PhotoKey, LinkedinUrl: u.LinkedinUrl, XUrl: u.XUrl}); err != nil {
		return fmt.Errorf("site store: insert person: %w", err)
	}
	return nil
}

// UpdatePerson rewrites a person; false when there is none.
func UpdatePerson(ctx context.Context, db Tx, p domain.Person) (bool, error) {
	n, err := sitedb.New(db).UpdatePerson(ctx, personParams(p))
	if err != nil {
		return false, fmt.Errorf("site store: update person: %w", err)
	}
	return n > 0, nil
}

func personParams(p domain.Person) sitedb.UpdatePersonParams {
	return sitedb.UpdatePersonParams{ID: p.ID, Kind: string(p.Kind), Position: int32(p.Position), //nolint:gosec // Validate bounds it
		NameAr: p.NameAr, NameEn: p.NameEn, RoleAr: p.RoleAr, RoleEn: p.RoleEn, BioAr: p.BioAr, BioEn: p.BioEn,
		PhotoKey: p.PhotoKey, LinkedinUrl: p.LinkedIn, XUrl: p.X}
}

// DeletePerson removes a person; false when there is none.
func DeletePerson(ctx context.Context, db Tx, id ulid.ID) (bool, error) {
	n, err := sitedb.New(db).DeletePerson(ctx, id)
	if err != nil {
		return false, fmt.Errorf("site store: delete person: %w", err)
	}
	return n > 0, nil
}

// Testimonials are the merchants' reviews in order.
func Testimonials(ctx context.Context, db Tx) ([]domain.Testimonial, error) {
	rows, err := sitedb.New(db).ListTestimonials(ctx)
	if err != nil {
		return nil, fmt.Errorf("site store: testimonials: %w", err)
	}
	out := make([]domain.Testimonial, len(rows))
	for i, r := range rows {
		out[i] = testimonial(sitedb.GetTestimonialRow(r))
	}
	return out, nil
}

// Testimonial is one review; false when there is none.
func Testimonial(ctx context.Context, db Tx, id ulid.ID) (domain.Testimonial, bool, error) {
	r, err := sitedb.New(db).GetTestimonial(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Testimonial{}, false, nil
	}
	if err != nil {
		return domain.Testimonial{}, false, fmt.Errorf("site store: testimonial: %w", err)
	}
	return testimonial(r), true, nil
}

func testimonial(r sitedb.GetTestimonialRow) domain.Testimonial {
	return domain.Testimonial{ID: r.ID, Position: int(r.Position), Name: r.Name, StoreName: r.StoreName, StoreURL: r.StoreUrl,
		QuoteAr: r.QuoteAr, QuoteEn: r.QuoteEn, PhotoKey: r.PhotoKey}
}

// InsertTestimonial adds a review.
func InsertTestimonial(ctx context.Context, db Tx, t domain.Testimonial) error {
	u := testimonialParams(t)
	if err := sitedb.New(db).InsertTestimonial(ctx, sitedb.InsertTestimonialParams{ID: u.ID, Position: u.Position, Name: u.Name, StoreName: u.StoreName,
		StoreUrl: u.StoreUrl, QuoteAr: u.QuoteAr, QuoteEn: u.QuoteEn, PhotoKey: u.PhotoKey}); err != nil {
		return fmt.Errorf("site store: insert testimonial: %w", err)
	}
	return nil
}

// UpdateTestimonial rewrites a review; false when there is none.
func UpdateTestimonial(ctx context.Context, db Tx, t domain.Testimonial) (bool, error) {
	n, err := sitedb.New(db).UpdateTestimonial(ctx, testimonialParams(t))
	if err != nil {
		return false, fmt.Errorf("site store: update testimonial: %w", err)
	}
	return n > 0, nil
}

func testimonialParams(t domain.Testimonial) sitedb.UpdateTestimonialParams {
	return sitedb.UpdateTestimonialParams{ID: t.ID, Position: int32(t.Position), Name: t.Name, StoreName: t.StoreName, //nolint:gosec // Validate bounds it
		StoreUrl: t.StoreURL, QuoteAr: t.QuoteAr, QuoteEn: t.QuoteEn, PhotoKey: t.PhotoKey}
}

// DeleteTestimonial removes a review; false when there is none.
func DeleteTestimonial(ctx context.Context, db Tx, id ulid.ID) (bool, error) {
	n, err := sitedb.New(db).DeleteTestimonial(ctx, id)
	if err != nil {
		return false, fmt.Errorf("site store: delete testimonial: %w", err)
	}
	return n > 0, nil
}
