package ads

import (
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool // app_user: the storefront's reads and clicks
	// AdminPool and Media serve the console (the api role); nil elsewhere.
	AdminPool *pgxpool.Pool
	Media     *config.Media
	IDs       *ulid.Generator
	Log       *slog.Logger
}

// Module is the wired ads module.
type Module struct{ svc *app.Service }

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil {
		return nil, errors.New("ads: missing dependency")
	}
	deps := app.Deps{DB: d.Pool, IDs: d.IDs, Log: d.Log}
	if d.AdminPool != nil {
		deps.Admin = d.AdminPool
	}
	if d.Media != nil {
		objects, err := media.NewS3For(*d.Media)
		if err != nil {
			return nil, err
		}
		deps.Media = app.Media{Objects: objects, Limits: media.Limits{MaxBytes: d.Media.MaxUploadBytes, MaxPixels: d.Media.MaxImagePixels},
			Signer: media.NewSigner(d.Media.ImgproxyURL, d.Media.S3Bucket, d.Media.ImgproxyKey, d.Media.ImgproxySalt)}
	}
	return &Module{svc: app.NewService(deps)}, nil
}

// Shop is what the storefront asks.
func (m *Module) Shop() Shop { return m.svc }

// Console is what the console manages.
func (m *Module) Console() Console { return m.svc }
