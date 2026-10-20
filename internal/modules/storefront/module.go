package storefront

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool *pgxpool.Pool
	IDs  *ulid.Generator
	// Audit records merchant actions (identity's audit log); nil discards.
	Audit audit.Recorder
	// Events receives the module's domain events (ADR-0015).
	Events eventbus.Publisher
	// Jobs inserts the logo's jobs in the caller's transaction.
	Jobs jobs.Inserter
	// Media is object storage and image delivery (ADR-0012).
	Media config.Media
}

// Module is the wired storefront module.
type Module struct {
	svc      *app.Service
	sections *sections.Registry
	media    app.Media
	signer   *media.Signer
	deps     Deps
}

// New wires the module. It fails if a section schema does not compile.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.IDs == nil || d.Events == nil || d.Jobs == nil {
		return nil, errors.New("storefront: missing dependency")
	}
	reg, err := sections.New()
	if err != nil {
		return nil, err
	}
	mc := d.Media
	objects, err := media.NewS3(media.S3Config{
		Endpoint: mc.S3Endpoint, PublicEndpoint: mc.S3PublicEndpoint, Region: mc.S3Region, Bucket: mc.S3Bucket,
		AccessKey: mc.S3AccessKey, SecretKey: mc.S3SecretKey,
	})
	if err != nil {
		return nil, err
	}
	m := app.Media{Objects: objects, Limits: media.Limits{MaxBytes: mc.MaxUploadBytes, MaxPixels: mc.MaxImagePixels}}
	return &Module{
		svc: app.NewService(app.Deps{DB: d.Pool, IDs: d.IDs, Audit: d.Audit, Events: d.Events, Sections: reg,
			Jobs: d.Jobs, Media: m}),
		sections: reg, media: m, deps: d,
		signer: media.NewSigner(mc.ImgproxyURL, mc.S3Bucket, mc.ImgproxyKey, mc.ImgproxySalt),
	}, nil
}

// RegisterWorkers adds the module's job workers (the logo pipeline). Its
// jobs run on the catalog's media queue.
func (m *Module) RegisterWorkers(w *river.Workers) error {
	aw := &app.AssetWorkers{DB: m.deps.Pool, Jobs: m.deps.Jobs, Media: m.media, Events: m.deps.Events, IDs: m.deps.IDs}
	for _, err := range []error{
		river.AddWorkerSafely(w, &app.ProcessAssetWorker{AssetWorkers: aw}),
		river.AddWorkerSafely(w, &app.ExpireAssetUploadWorker{AssetWorkers: aw}),
		river.AddWorkerSafely(w, &app.DeleteObjectsWorker{AssetWorkers: aw}),
	} {
		if err != nil {
			return fmt.Errorf("storefront: %w", err)
		}
	}
	return nil
}

// Mount registers the merchant API. scope is identity's membership
// middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, m.sections, m.signer, auth, scope, resp)
}
