package storefront

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/captcha"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/cdn"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/redisx"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// InvalidateChannel carries the IDs of stores whose rendered pages are
// stale. The worker publishes on it when an event says something a
// shopper sees changed; every storefront instance listens (ADR-0018).
const InvalidateChannel = "storefront:invalidate"

// AssetsPrefix is the path the shop's static files are served under.
const AssetsPrefix = web.AssetsPrefix

// InvalidatingEvents are the events after which a store's pages must be
// rendered again.
func InvalidatingEvents() []string {
	return []string{
		catalog.EventProductChanged, catalog.EventProductDeleted, catalog.EventCategoryChanged,
		EventThemeChanged, EventPageChanged,
	}
}

// InvalidateSubscriber is the event bus subscriber name of
// InvalidateHandler.
const InvalidateSubscriber = "storefront.invalidate_pages"

// InvalidateHandler tells every storefront instance that the event's
// store changed. Subscribe it to InvalidatingEvents on every role's bus.
func InvalidateHandler(rdb *redis.Client) eventbus.Handler {
	return func(ctx context.Context, d eventbus.Delivery) error {
		return rdb.Publish(ctx, InvalidateChannel, d.Meta.StoreID.String()).Err()
	}
}

// PurgeSubscriber is the event bus subscriber name of PurgeHandler.
const PurgeSubscriber = "storefront.purge_edge"

// PurgeHandler purges the edge cache's pages that show what an event
// changed (ADR-0019). Subscribe it to InvalidatingEvents on every role's
// bus; with cdn.Nop it does nothing.
func PurgeHandler(p cdn.Purger) eventbus.Handler {
	return func(ctx context.Context, d eventbus.Delivery) error {
		tags, err := PurgeTags(d)
		if err != nil {
			return river.JobCancel(err) // retrying cannot fix a payload that does not decode
		}
		return p.PurgeTags(ctx, tags)
	}
}

// PurgeTags maps an event to the Cache-Tags of the pages it changes:
//   - a product: its page (p-) and every listing (l-), which may show it;
//   - a category or the theme: the whole store (s-), since the header
//     and the look are on every page;
//   - a policy: the whole store (its link is in every footer);
//   - another page: that page (pg-).
func PurgeTags(d eventbus.Delivery) ([]string, error) {
	store := d.Meta.StoreID.String()
	switch d.Meta.Type {
	case catalog.EventProductChanged:
		ev, err := eventbus.Decode[catalog.ProductChanged](d)
		return []string{"p-" + ev.ProductID.String(), "l-" + store}, err
	case catalog.EventProductDeleted:
		ev, err := eventbus.Decode[catalog.ProductDeleted](d)
		return []string{"p-" + ev.ProductID.String(), "l-" + store}, err
	case EventPageChanged:
		ev, err := eventbus.Decode[PageChanged](d)
		if ev.Kind == KindPolicy {
			return []string{"s-" + store}, err
		}
		return []string{"pg-" + ev.PageID.String()}, err
	}
	return []string{"s-" + store}, nil // categories, theme, logo
}

// CacheConfig bounds the shop's in-memory caches.
type CacheConfig = web.CacheConfig

// DefaultCacheConfig is the shop's default cache.
func DefaultCacheConfig() CacheConfig { return web.DefaultCacheConfig() }

// ShopDeps are what the storefront role needs to render pages.
type ShopDeps struct {
	Pool    *pgxpool.Pool
	Redis   *redis.Client
	Catalog catalog.Storefront
	Stores  web.Stores // tenancy.Service
	Media   config.Media
	Log     *slog.Logger
	Cache   CacheConfig
	HTTPS   bool
	EdgeTTL time.Duration // s-maxage of pages (config.Edge.CacheTTL)
	// ClientIP is the shopper's address from the trusted proxies, for the
	// cart's per-IP rate limit.
	ClientIP func(*http.Request) string
	// Orders places shoppers' orders (checkout and landing pages' one-step
	// form) and backs the thank-you page; nil disables them (404).
	Orders orders.Shop
	// Captcha is Cloudflare Turnstile, shown to risky order submissions
	// (ADR-0022); empty keys disable it.
	Captcha config.Captcha
	// Cards tells checkout whether the store takes cards, and
	// CheckoutURL is the API host card shoppers pay through (ADR-0030);
	// nil Cards offers cash on delivery only.
	Cards       payments.CardCheck
	CheckoutURL string
	// Search is true when Catalog can search (a search key is configured):
	// pages then show the search box.
	Search bool
}

var _ web.Stores = (tenancy.Service)(nil)

// Shop renders stores' pages for shoppers.
type Shop struct {
	shop   *web.Shop
	assets *web.Assets
	redis  *redis.Client
	log    *slog.Logger
}

// NewShop builds the shop. It reads only: no jobs, no storage writes.
func NewShop(d ShopDeps) (*Shop, error) {
	if d.Pool == nil || d.Redis == nil || d.Catalog == nil || d.Stores == nil || d.Log == nil {
		return nil, errors.New("storefront: missing shop dependency")
	}
	assets, err := web.LoadAssets()
	if err != nil {
		return nil, err
	}
	limiter, err := ratelimit.New(d.Redis, "storefront")
	if err != nil {
		return nil, err
	}
	content := app.NewService(app.Deps{DB: d.Pool, IDs: ulid.NewGenerator(), Variants: d.Catalog, Carts: store.NewCarts(d.Redis)})
	var ords web.Orders
	if d.Orders != nil {
		ords = shopOrders{shop: d.Orders}
	}
	var pays web.Payments
	if d.Cards != nil && d.Orders != nil {
		if d.CheckoutURL == "" {
			return nil, errors.New("storefront: card payment needs CheckoutURL")
		}
		pays = shopPayments{cards: d.Cards, origin: d.CheckoutURL}
	}
	var verifier web.CaptchaVerifier
	if d.Captcha.Enabled() {
		verifier = captcha.NewTurnstile(d.Captcha.SecretKey)
	}
	shop, err := web.New(web.Deps{
		Catalog: d.Catalog, Stores: d.Stores, Content: content, Carts: content, Orders: ords, Payments: pays,
		Limiter: limiter, ClientIP: d.ClientIP, Captcha: verifier, CaptchaSiteKey: d.Captcha.SiteKey,
		Signer: media.NewSigner(d.Media.ImgproxyURL, d.Media.S3Bucket, d.Media.ImgproxyKey, d.Media.ImgproxySalt),
		Assets: assets, Log: d.Log, Cache: d.Cache, HTTPS: d.HTTPS, EdgeTTL: d.EdgeTTL, Search: d.Search,
	})
	if err != nil {
		return nil, err
	}
	return &Shop{shop: shop, assets: assets, redis: d.Redis, log: d.Log}, nil
}

// Pages serves a store's pages; the tenant must be in the context.
func (s *Shop) Pages() http.Handler { return s.shop }

// Assets serves the static files, the same for every store.
func (s *Shop) Assets() http.Handler { return s.assets }

// Invalidate drops a store's cached pages on this instance.
func (s *Shop) Invalidate(store tenant.StoreID) { s.shop.Invalidate(store) }

// Listen applies invalidations published on InvalidateChannel until ctx
// ends. A lost message costs at most the cache TTL.
func (s *Shop) Listen(ctx context.Context) {
	for payload := range redisx.Subscribe(ctx, s.redis, InvalidateChannel, s.log) {
		id, err := tenant.ParseStoreID(payload)
		if err != nil {
			s.log.WarnContext(ctx, "storefront: bad invalidation message", "err", err)
			continue
		}
		s.shop.Invalidate(id)
	}
}
