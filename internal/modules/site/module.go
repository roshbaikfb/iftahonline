package site

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	// Pool reads the content the console edits (app_user).
	Pool *pgxpool.Pool
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
	content := app.NewService(app.Deps{DB: d.Pool, Log: d.Log})
	s, err := web.New(web.Deps{Plans: d.Plans, Content: content, FontFaces: d.Font.Faces, FontPreload: d.Font.Preload,
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

// NewConsole is what the super-admin console edits (the api role): it
// writes as app_admin.
func NewConsole(adminPool *pgxpool.Pool, ids *ulid.Generator, log *slog.Logger) Console {
	return app.NewService(app.Deps{DB: adminPool, Admin: adminPool, IDs: ids, Log: log})
}
