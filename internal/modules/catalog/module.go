package catalog

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/httpapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/media"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/search"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/safehttp"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Deps are the module's explicit dependencies (CLAUDE.md rule 4).
type Deps struct {
	Pool     *pgxpool.Pool
	Jobs     jobs.Inserter
	IDs      *ulid.Generator
	Log      *slog.Logger
	Currency CurrencySource
	// Audit records merchant actions (identity's audit log); nil discards.
	Audit audit.Recorder
	// Media is object storage and image delivery (ADR-0012).
	Media config.Media
	// Search is the products index (ADR-0013).
	Search config.Search
	// Keyring seals WooCommerce credentials (ADR-0014).
	Keyring *secretbox.Keyring
	// Events receives the catalog's domain events (ADR-0015).
	Events eventbus.Publisher
}

// Module is the wired catalog module.
type Module struct {
	svc    *app.Service
	media  app.Media
	signer *media.Signer
	index  *search.Client
	deps   Deps
}

// New wires the module.
func New(d Deps) (*Module, error) {
	if d.Pool == nil || d.Jobs == nil || d.IDs == nil || d.Log == nil || d.Currency == nil || d.Keyring == nil || d.Events == nil {
		return nil, errors.New("catalog: missing dependency")
	}
	if d.Audit == nil {
		d.Audit = audit.Nop{}
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
	index := search.New(d.Search.URL, d.Search.APIKey, d.Search.Index, 10*time.Second)
	return &Module{
		svc: app.NewService(app.Deps{
			DB: d.Pool, Jobs: d.Jobs, IDs: d.IDs, Audit: d.Audit, Currency: d.Currency, Media: m, Search: index, Events: d.Events,
			Imports: app.Imports{
				Keyring: d.Keyring, Retry: retry.Default(),
				HTTP: safehttp.New(safehttp.Options{Timeout: 30 * time.Second, MaxResponseBytes: 20 << 20}),
			},
		}),
		media: m, index: index, deps: d,
		signer: media.NewSigner(mc.ImgproxyURL, mc.S3Bucket, mc.ImgproxyKey, mc.ImgproxySalt),
	}, nil
}

// PeriodicJobs are the module's scheduled jobs.
func (m *Module) PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{app.SearchSettingsPeriodic()}
}

// Queues are the River queues the module's jobs need besides the default.
func (m *Module) Queues() map[string]river.QueueConfig {
	return map[string]river.QueueConfig{app.QueueMedia: {MaxWorkers: max(m.deps.Media.ImageWorkers, 1)}}
}

// Catalog returns the use cases other modules call.
func (m *Module) Catalog() Catalog { return m.svc }

// RegisterWorkers adds the module's job workers.
func (m *Module) RegisterWorkers(w *river.Workers) error {
	events := app.Events{Pub: m.deps.Events, IDs: m.deps.IDs}
	iw := &app.ImageWorkers{DB: m.deps.Pool, Jobs: m.deps.Jobs, Media: m.media, Events: events}
	return errors.Join(
		wrap(river.AddWorkerSafely(w, &app.ExpireWorker{DB: m.deps.Pool, Events: events})),
		wrap(river.AddWorkerSafely(w, &app.ProcessWorker{ImageWorkers: iw})),
		wrap(river.AddWorkerSafely(w, &app.ExpireUploadWorker{ImageWorkers: iw})),
		wrap(river.AddWorkerSafely(w, &app.DeleteObjectsWorker{ImageWorkers: iw})),
		wrap(river.AddWorkerSafely(w, &app.IndexWorker{DB: m.deps.Pool, Index: m.index})),
		wrap(river.AddWorkerSafely(w, &app.SearchSettingsWorker{Index: m.index})),
		wrap(river.AddWorkerSafely(w, &app.ImportWorker{Svc: m.svc})),
	)
}

func wrap(err error) error {
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	return nil
}

// Mount registers the HTTP API. scope is identity's membership middleware.
func (m *Module) Mount(mux *http.ServeMux, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	httpapi.Mount(mux, m.svc, m.signer, auth, scope, resp)
}
