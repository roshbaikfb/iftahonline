package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/store/sitedb"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrSlugTaken is a guide whose address another guide has.
var ErrSlugTaken = errs.Conflict("slug_taken", "another guide has this address")

// Guides are every guide for the console, newest first, without their
// bodies.
func Guides(ctx context.Context, db Tx) ([]domain.Guide, error) {
	rows, err := sitedb.New(db).ListGuides(ctx)
	if err != nil {
		return nil, fmt.Errorf("site store: guides: %w", err)
	}
	out := make([]domain.Guide, len(rows))
	for i, r := range rows {
		out[i] = domain.Guide{ID: r.ID, Slug: r.Slug, Status: domain.GuideStatus(r.Status), TitleAr: r.TitleAr, SummaryAr: r.SummaryAr,
			TitleEn: r.TitleEn, CoverKey: r.CoverKey, PublishedAt: deref(r.PublishedAt), UpdatedAt: r.UpdatedAt}
	}
	return out, nil
}

// PublishedGuides are the guides the site shows, newest first.
func PublishedGuides(ctx context.Context, db Tx) ([]domain.Guide, error) {
	rows, err := sitedb.New(db).PublishedGuides(ctx)
	if err != nil {
		return nil, fmt.Errorf("site store: published guides: %w", err)
	}
	out := make([]domain.Guide, len(rows))
	for i, r := range rows {
		out[i] = domain.Guide{ID: r.ID, Slug: r.Slug, Status: domain.Published, TitleAr: r.TitleAr, SummaryAr: r.SummaryAr, BodyAr: r.BodyAr,
			TitleEn: r.TitleEn, SummaryEn: r.SummaryEn, BodyEn: r.BodyEn, CoverKey: r.CoverKey, PublishedAt: deref(r.PublishedAt), UpdatedAt: r.UpdatedAt}
	}
	return out, nil
}

// Guide is one guide, whole; false when there is none.
func Guide(ctx context.Context, db Tx, id ulid.ID) (domain.Guide, bool, error) {
	r, err := sitedb.New(db).GetGuide(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Guide{}, false, nil
	}
	if err != nil {
		return domain.Guide{}, false, fmt.Errorf("site store: guide: %w", err)
	}
	return domain.Guide{ID: r.ID, Slug: r.Slug, Status: domain.GuideStatus(r.Status), TitleAr: r.TitleAr, SummaryAr: r.SummaryAr, BodyAr: r.BodyAr,
		TitleEn: r.TitleEn, SummaryEn: r.SummaryEn, BodyEn: r.BodyEn, CoverKey: r.CoverKey, PublishedAt: deref(r.PublishedAt), UpdatedAt: r.UpdatedAt}, true, nil
}

// CountGuides counts every guide.
func CountGuides(ctx context.Context, db Tx) (int, error) {
	n, err := sitedb.New(db).CountGuides(ctx)
	if err != nil {
		return 0, fmt.Errorf("site store: count guides: %w", err)
	}
	return int(n), nil
}

// InsertGuide adds a guide (ErrSlugTaken when its address is used).
func InsertGuide(ctx context.Context, db Tx, g domain.Guide) error {
	err := sitedb.New(db).InsertGuide(ctx, sitedb.InsertGuideParams{ID: g.ID, Slug: g.Slug, Status: string(g.Status), TitleAr: g.TitleAr,
		SummaryAr: g.SummaryAr, BodyAr: g.BodyAr, TitleEn: g.TitleEn, SummaryEn: g.SummaryEn, BodyEn: g.BodyEn, CoverKey: g.CoverKey,
		PublishedAt: ref(g.PublishedAt)})
	return guideErr("insert guide", err)
}

// UpdateGuide rewrites a guide; false when there is none.
func UpdateGuide(ctx context.Context, db Tx, g domain.Guide) (bool, error) {
	n, err := sitedb.New(db).UpdateGuide(ctx, sitedb.UpdateGuideParams{ID: g.ID, Slug: g.Slug, Status: string(g.Status), TitleAr: g.TitleAr,
		SummaryAr: g.SummaryAr, BodyAr: g.BodyAr, TitleEn: g.TitleEn, SummaryEn: g.SummaryEn, BodyEn: g.BodyEn, CoverKey: g.CoverKey,
		PublishedAt: ref(g.PublishedAt)})
	return n > 0, guideErr("update guide", err)
}

// DeleteGuide removes a guide; false when there is none.
func DeleteGuide(ctx context.Context, db Tx, id ulid.ID) (bool, error) {
	n, err := sitedb.New(db).DeleteGuide(ctx, id)
	if err != nil {
		return false, fmt.Errorf("site store: delete guide: %w", err)
	}
	return n > 0, nil
}

func guideErr(op string, err error) error {
	switch {
	case err == nil:
		return nil
	case postgres.IsUniqueViolation(err, "site_guides_slug_key"):
		return ErrSlugTaken
	default:
		return fmt.Errorf("site store: %s: %w", op, err)
	}
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func ref(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
