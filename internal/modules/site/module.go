package site

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	// Pool reads the content the console edits (app_user).
	Pool *pgxpool.Pool
	// Media signs the URLs of the console's images; nil shows none.
	Media *config.Media
	// Plans reads the plans on sale (billing.NewShowcase).
	Plans Plans
	// Font is the brand typeface, served by the shop's assets.
	Font Font
	// Domain is the platform's domain the site answers on
	// (PLATFORM_BASE_DOMAIN); HTTPS says whether its links are https.
	Domain string
	HTTPS  bool
	// DashboardURL is where merchants sign up and sign in.
	DashboardURL string
	// WhatsApp is the support number in E.164 shown until the console's
	// settings are first read; "" shows none.
	WhatsApp string
	// CountryHeader names the CDN's visitor-country header;
	// DefaultCountryHeader when empty.
	CountryHeader string
	Log           *slog.Logger
}

// Module is the wired site.
type Module struct {
	site   *web.Site
	domain string
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Plans == nil || d.Domain == "" || d.DashboardURL == "" || d.Log == nil {
		return nil, errors.New("site: missing dependency")
	}
	if d.CountryHeader == "" {
		d.CountryHeader = DefaultCountryHeader
	}
	content := app.NewService(app.Deps{DB: d.Pool, Log: d.Log, Media: app.Media{Signer: signer(d.Media)}})
	images := ""
	if d.Media != nil {
		images = d.Media.ImgproxyURL
	}
	s, err := web.New(web.Deps{Plans: d.Plans, Content: content, Images: content.ImageURL, ImagesOrigin: images, FontFaces: d.Font.Faces, FontPreload: d.Font.Preload,
		Host: d.Domain, HTTPS: d.HTTPS, DashboardURL: d.DashboardURL, WhatsApp: d.WhatsApp, CountryHeader: d.CountryHeader, Log: d.Log})
	if err != nil {
		return nil, err
	}
	return &Module{site: s, domain: d.Domain}, nil
}

// Handler serves the site's pages on the platform's domain.
func (m *Module) Handler() http.Handler { return m.site }

// Domain is the platform's domain the site answers on.
func (m *Module) Domain() string { return m.domain }

// NewContacts reads the platform's contact details on pool (the site's
// role reads them).
func NewContacts(pool *pgxpool.Pool) Contacts {
	return app.NewService(app.Deps{DB: pool})
}

// NewConsole is what the super-admin console edits (the api role): it
// writes as app_admin, and keeps images in m's bucket (nil refuses
// uploads).
func NewConsole(adminPool *pgxpool.Pool, m *config.Media, ids *ulid.Generator, log *slog.Logger) (Console, error) {
	d := app.Deps{DB: adminPool, Admin: adminPool, IDs: ids, Log: log}
	if m != nil {
		objects, err := media.NewS3For(*m)
		if err != nil {
			return nil, err
		}
		d.Media = app.Media{Objects: objects, Limits: media.Limits{MaxBytes: m.MaxUploadBytes, MaxPixels: m.MaxImagePixels}, Signer: signer(m)}
	}
	return app.NewService(d), nil
}

func signer(m *config.Media) *media.Signer {
	if m == nil || m.ImgproxyURL == "" {
		return nil
	}
	return media.NewSigner(m.ImgproxyURL, m.S3Bucket, m.ImgproxyKey, m.ImgproxySalt)
}
