// Package config loads process configuration from environment variables
// only, validating everything at startup (CLAUDE.md Linux runtime rules).
// Load reports every problem at once so a misconfigured deploy fails fast
// with a complete list.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/logging"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
)

// Role is the binary's subcommand.
type Role string

// Roles.
const (
	RoleAPI        Role = "api"
	RoleStorefront Role = "storefront"
	RoleWorker     Role = "worker"
	RoleMigrate    Role = "migrate"
	RoleAdmin      Role = "admin" // CLI: manage platform admins (ADMIN_DATABASE_URL)
)

// Environments.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// LookupFunc is os.LookupEnv in production and a map in tests.
type LookupFunc func(key string) (string, bool)

// Config is the validated configuration for one role.
type Config struct {
	Role     Role
	Env      string
	LogLevel slog.Level
	HTTP     HTTP
	DB       DB
	Redis    Redis
	OTel     OTel
	Worker   Worker
	Tenancy  Tenancy
	Identity Identity
	Secrets  Secrets
	Mail     Mail
	// AdminDB is the super-admin console's pool (app_admin). Empty URL:
	// the console is disabled.
	AdminDB DB
	Media   Media
	Search  Search
	Edge    Edge
	Captcha Captcha
	// Shipping configures the carriers merchants connect (api, worker).
	Shipping Shipping
	// Payments configures the card gateways merchants connect (api,
	// worker).
	Payments Payments
}

// Captcha configures Cloudflare Turnstile, which the storefront shows
// only to risky order submissions (ADR-0022). Both keys empty disables
// it: a submission that would need it is refused as "busy".
type Captcha struct {
	SiteKey   string
	SecretKey string
}

// Enabled reports whether risky orders can be verified.
func (c Captcha) Enabled() bool { return c.SiteKey != "" }

// Shipping configures carrier adapters (ADR-0026).
type Shipping struct {
	// BostaBaseURL is Bosta's API; its production API by default (the
	// staging API is https://stg-app.bosta.co/api/v2).
	BostaBaseURL string
	// FakeCarrier offers the in-memory carrier, for development and
	// demos. Refused in production.
	FakeCarrier bool
}

// Payments configures card gateway adapters (ADR-0029).
type Payments struct {
	// PaymobBaseURL overrides Paymob's API, which is otherwise chosen by
	// the payment's currency (Egypt, Saudi Arabia, the UAE).
	PaymobBaseURL string
	// FakeGateway offers the in-memory gateway, for development and
	// demos. Refused in production.
	FakeGateway bool
	// CheckoutURL is the API host's public origin, where the storefront
	// sends card orders' shoppers (ADR-0030; storefront role).
	CheckoutURL string
}

// Edge configures the CDN in front of the storefront (ADR-0019). The
// worker purges by tag when Cloudflare is configured; the storefront
// tells the edge how long it may keep pages.
type Edge struct {
	// CacheTTL is the storefront's s-maxage. Keep it at 60s or less
	// without purging (the freshness promise); raise it (e.g. 10m) once
	// the worker purges.
	CacheTTL time.Duration
	// CloudflareZoneID and CloudflareToken (Cache Purge permission) let
	// the worker purge; both empty disables purging.
	CloudflareZoneID string
	CloudflareToken  string
}

// Purges reports whether the worker purges the edge cache.
func (e Edge) Purges() bool { return e.CloudflareZoneID != "" }

// Search configures Meilisearch (api and worker). Use a key limited to
// the index's search and document actions in production, never the
// master key.
type Search struct {
	URL    string
	APIKey string
	Index  string // the one products index shared by every store (ADR-0013)
}

// Media configures object storage and image delivery. The api and worker
// get all of it; the storefront only what delivery needs (the bucket name
// and imgproxy), never storage credentials.
type Media struct {
	S3Endpoint string // e.g. https://s3.eu-central-1.amazonaws.com or http://localhost:18333
	// S3PublicEndpoint is where browsers send uploads (presigned POSTs);
	// it defaults to S3Endpoint.
	S3PublicEndpoint string
	S3Region         string
	S3Bucket         string
	S3AccessKey      string
	S3SecretKey      string
	// ImgproxyURL is the public base of the imgproxy deployment, e.g.
	// https://img.iftahonline.com. Delivery URLs are signed with
	// ImgproxyKey and ImgproxySalt (hex, from the secret manager).
	ImgproxyURL    string
	ImgproxyKey    []byte
	ImgproxySalt   []byte
	MaxUploadBytes int64 // largest accepted original
	MaxImagePixels int64 // width × height bound, checked before decoding
	ImageWorkers   int   // concurrent image jobs per worker (each holds a decoded image)
}

// Secrets are keys from the secret manager (never from files or flags).
// String/LogValue never print them.
type Secrets struct {
	// Keyring seals secrets at rest (TOTP seeds, pending codes):
	// SECRETBOX_KEYS="v:base64key[,v:base64key]", first = current.
	Keyring *secretbox.Keyring
	// OTPPepper keys the HMAC of one-time codes (OTP_PEPPER, base64, >= 32 bytes).
	OTPPepper []byte
}

// Mail configures outgoing email. Empty SMTPAddr logs a masked notice
// instead of sending (no provider chosen yet, ADR-0005); in development,
// point it at Mailpit (deploy/compose).
type Mail struct {
	SMTPAddr     string // host:port
	SMTPUsername string
	SMTPPassword string
	From         string // e.g. "iftahonline <no-reply@iftahonline.com>"
	DashboardURL string // base for links in emails, e.g. https://app.iftahonline.com
}

// HTTP configures the server for api, storefront and the worker's health port.
type HTTP struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	RequestTimeout    time.Duration // per-request context deadline
	MaxBodyBytes      int64
	ShutdownTimeout   time.Duration // max time to finish in-flight requests
	DrainDelay        time.Duration // /readyz reports 503 this long before shutdown starts
	TrustedProxies    []netip.Prefix
}

// DB configures the pgx pool.
type DB struct {
	URL              string // app_user (api/storefront/worker) or app_owner (migrate)
	MaxConns         int32
	MinConns         int32
	MaxConnLifetime  time.Duration
	StatementTimeout time.Duration
}

// Redis configures the go-redis client.
type Redis struct{ URL string }

// OTel configures tracing/metrics export. Empty Endpoint disables export.
type OTel struct {
	Endpoint    string
	SampleRatio float64
}

// Worker configures River.
type Worker struct{ Concurrency int }

// Identity configures password hashing (ADR-0005). Each concurrent hash
// holds Argon2MemoryKiB, so HashConcurrency × Argon2MemoryKiB must fit
// comfortably under GOMEMLIMIT.
type Identity struct {
	Argon2MemoryKiB uint32
	Argon2Time      uint32
	Argon2Threads   uint8
	HashConcurrency int
}

// Tenancy configures store hosts and the host → store caches (ADR-0004).
type Tenancy struct {
	BaseDomain        string        // store subdomains live under it, e.g. iftahonline.com
	AdminHost         string        // the only host that serves the super-admin API
	HostCacheSize     int           // L1 entries for known hosts
	HostCacheTTL      time.Duration // L1 lifetime
	NegativeCacheSize int           // L1 entries for unknown hosts (separate, so floods cannot evict real stores)
	NegativeTTL       time.Duration // lifetime of unknown-host entries in L1 and L2
	SharedCacheTTL    time.Duration // L2 (Redis) lifetime for known hosts
}

// Load reads configuration for role.
func Load(role Role, lookup LookupFunc) (Config, error) {
	r := reader{lookup: lookup}
	c := Config{Role: role}

	switch role {
	case RoleAPI, RoleStorefront, RoleWorker, RoleMigrate, RoleAdmin:
	default:
		return Config{}, fmt.Errorf("config: unknown role %q", role)
	}

	c.Env = r.oneOf("APP_ENV", EnvDevelopment, EnvDevelopment, EnvTest, EnvStaging, EnvProduction)
	c.LogLevel = r.level("LOG_LEVEL", slog.LevelInfo)

	defaultAddr := map[Role]string{RoleAPI: ":8080", RoleStorefront: ":8081", RoleWorker: ":8082"}[role]
	c.HTTP = HTTP{
		Addr:              r.addr("HTTP_ADDR", defaultAddr),
		ReadHeaderTimeout: r.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       r.duration("HTTP_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:      r.duration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       r.duration("HTTP_IDLE_TIMEOUT", 120*time.Second),
		RequestTimeout:    r.duration("HTTP_REQUEST_TIMEOUT", 10*time.Second),
		MaxBodyBytes:      r.int64("HTTP_MAX_BODY_BYTES", 1<<20, 1, 1<<30),
		ShutdownTimeout:   r.duration("SHUTDOWN_TIMEOUT", 20*time.Second),
		DrainDelay:        r.durationAllowZero("DRAIN_DELAY", 5*time.Second),
		TrustedProxies:    r.prefixes("TRUSTED_PROXIES"),
	}
	if c.HTTP.WriteTimeout <= c.HTTP.RequestTimeout {
		r.errorf("HTTP_WRITE_TIMEOUT (%s) must exceed HTTP_REQUEST_TIMEOUT (%s)", c.HTTP.WriteTimeout, c.HTTP.RequestTimeout)
	}

	dbKey := "DATABASE_URL"
	switch role {
	case RoleMigrate:
		dbKey = "MIGRATE_DATABASE_URL" // owner role; services use the NOBYPASSRLS app_user
	case RoleAdmin:
		dbKey = "ADMIN_DATABASE_URL" // the console's app_admin role
	}
	c.DB = DB{
		URL:              r.url(dbKey, true, "postgres", "postgresql"),
		MaxConns:         int32(r.int64("DB_MAX_CONNS", 20, 1, 1000)), //nolint:gosec // bounded above.
		MinConns:         int32(r.int64("DB_MIN_CONNS", 2, 0, 1000)),  //nolint:gosec // bounded above.
		MaxConnLifetime:  r.duration("DB_MAX_CONN_LIFETIME", 30*time.Minute),
		StatementTimeout: r.duration("DB_STATEMENT_TIMEOUT", 5*time.Second),
	}
	if c.DB.MinConns > c.DB.MaxConns {
		r.errorf("DB_MIN_CONNS (%d) must not exceed DB_MAX_CONNS (%d)", c.DB.MinConns, c.DB.MaxConns)
	}

	if role == RoleAPI {
		// Optional: without it the super-admin console is not served.
		c.AdminDB = DB{
			URL: r.url("ADMIN_DATABASE_URL", false, "postgres", "postgresql"), MaxConns: 4, MinConns: 0,
			MaxConnLifetime: c.DB.MaxConnLifetime, StatementTimeout: c.DB.StatementTimeout,
		}
	}

	if role != RoleMigrate && role != RoleAdmin {
		// The worker needs Redis too: it invalidates the shared host cache.
		c.Redis = Redis{URL: r.url("REDIS_URL", true, "redis", "rediss")}
		c.Tenancy = Tenancy{
			BaseDomain:        r.hostname("PLATFORM_BASE_DOMAIN", "iftahonline.com"),
			HostCacheSize:     int(r.int64("HOSTCACHE_SIZE", 20000, 1, 10_000_000)),
			HostCacheTTL:      r.duration("HOSTCACHE_TTL", time.Minute),
			NegativeCacheSize: int(r.int64("HOSTCACHE_NEGATIVE_SIZE", 4096, 1, 1_000_000)),
			NegativeTTL:       r.duration("HOSTCACHE_NEGATIVE_TTL", 15*time.Second),
			SharedCacheTTL:    r.duration("HOSTCACHE_SHARED_TTL", 10*time.Minute),
		}
		c.Tenancy.AdminHost = r.hostname("ADMIN_HOST", "admin."+c.Tenancy.BaseDomain)
	}
	if role == RoleAPI || role == RoleWorker || role == RoleAdmin {
		if kr, err := secretbox.ParseKeyring(r.secret("SECRETBOX_KEYS")); err != nil {
			r.errorf("SECRETBOX_KEYS: %v", err)
		} else {
			c.Secrets.Keyring = kr
		}
	}
	if role == RoleAPI || role == RoleWorker {
		if pepper, err := base64.StdEncoding.DecodeString(r.secret("OTP_PEPPER")); err != nil || len(pepper) < 32 {
			r.errorf("OTP_PEPPER: must be base64 of at least 32 random bytes")
		} else {
			c.Secrets.OTPPepper = pepper
		}
		c.Mail = Mail{
			SMTPAddr:     r.addr("SMTP_ADDR", ""),
			SMTPUsername: r.secretOptional("SMTP_USERNAME"),
			SMTPPassword: r.secretOptional("SMTP_PASSWORD"),
			From:         r.str("MAIL_FROM", "iftahonline <no-reply@"+c.Tenancy.BaseDomain+">"),
			DashboardURL: r.url("DASHBOARD_URL", false, "http", "https"),
		}
		if c.Mail.DashboardURL == "" {
			c.Mail.DashboardURL = "https://app." + c.Tenancy.BaseDomain
		}
		if c.Env == EnvProduction && c.Mail.SMTPAddr != "" && c.Mail.SMTPUsername == "" {
			r.errorf("SMTP_USERNAME is required with SMTP_ADDR in production")
		}
		c.Shipping = Shipping{
			BostaBaseURL: r.url("BOSTA_BASE_URL", false, "https", "http"),
			FakeCarrier:  r.oneOf("SHIPPING_FAKE_CARRIER", "false", "true", "false") == "true",
		}
		if c.Env == EnvProduction && c.Shipping.FakeCarrier {
			r.errorf("SHIPPING_FAKE_CARRIER: the fake carrier is not for production")
		}
		c.Payments = Payments{
			PaymobBaseURL: r.url("PAYMOB_BASE_URL", false, "https", "http"),
			FakeGateway:   r.oneOf("PAYMENTS_FAKE_GATEWAY", "false", "true", "false") == "true",
		}
		if c.Env == EnvProduction && c.Payments.FakeGateway {
			r.errorf("PAYMENTS_FAKE_GATEWAY: the fake gateway is not for production")
		}
	}
	if role == RoleAPI || role == RoleAdmin {
		c.Identity = Identity{
			Argon2MemoryKiB: uint32(r.int64("ARGON2_MEMORY_KIB", 19*1024, 8*1024, 1024*1024)), //nolint:gosec // bounded above.
			Argon2Time:      uint32(r.int64("ARGON2_TIME", 2, 1, 10)),                         //nolint:gosec // bounded above.
			Argon2Threads:   uint8(r.int64("ARGON2_THREADS", 1, 1, 16)),                       //nolint:gosec // bounded above.
			HashConcurrency: int(r.int64("PASSWORD_HASH_CONCURRENCY", 8, 1, 256)),
		}
	}

	if role == RoleStorefront {
		c.Media = r.delivery()
		c.Edge.CacheTTL = r.duration("EDGE_CACHE_TTL", time.Minute)
		if c.Edge.CacheTTL > 24*time.Hour {
			r.errorf("EDGE_CACHE_TTL: at most 24h")
		}
		c.Payments.CheckoutURL = r.url("CHECKOUT_URL", false, "https", "http")
		if c.Payments.CheckoutURL == "" {
			c.Payments.CheckoutURL = "https://app." + c.Tenancy.BaseDomain
		}
		c.Captcha.SiteKey = r.str("TURNSTILE_SITE_KEY", "")
		c.Captcha.SecretKey = r.secretOptional("TURNSTILE_SECRET_KEY")
		if (c.Captcha.SiteKey == "") != (c.Captcha.SecretKey == "") {
			r.errorf("TURNSTILE_SITE_KEY and TURNSTILE_SECRET_KEY go together")
		}
	}
	if role == RoleWorker {
		c.Edge.CloudflareZoneID = r.str("CLOUDFLARE_ZONE_ID", "")
		c.Edge.CloudflareToken = r.secretOptional("CLOUDFLARE_API_TOKEN")
		if (c.Edge.CloudflareZoneID == "") != (c.Edge.CloudflareToken == "") {
			r.errorf("CLOUDFLARE_ZONE_ID and CLOUDFLARE_API_TOKEN go together")
		}
	}
	if role == RoleAPI || role == RoleWorker {
		c.Media = r.media()
		c.Search = Search{
			URL:    r.url("MEILI_URL", true, "http", "https"),
			APIKey: r.secret("MEILI_API_KEY"),
			Index:  r.str("MEILI_INDEX", "products"),
		}
	}

	c.OTel = OTel{
		Endpoint:    r.url("OTEL_EXPORTER_OTLP_ENDPOINT", false, "http", "https"),
		SampleRatio: r.ratio("OTEL_TRACES_SAMPLER_ARG", 0.1),
	}
	c.Worker = Worker{Concurrency: int(r.int64("WORKER_CONCURRENCY", 10, 1, 1000))}

	if err := errors.Join(r.errs...); err != nil {
		return Config{}, fmt.Errorf("config: invalid environment:\n%w", err)
	}
	return c, nil
}

// LogValue keeps secrets out of logs: URLs are redacted.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("role", string(c.Role)),
		slog.String("env", c.Env),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("http_addr", c.HTTP.Addr),
		slog.String("db", redactURL(c.DB.URL)),
		slog.Int("db_max_conns", int(c.DB.MaxConns)),
		slog.String("redis", redactURL(c.Redis.URL)),
		slog.Bool("otel", c.OTel.Endpoint != ""),
		slog.String("base_domain", c.Tenancy.BaseDomain),
		slog.Bool("smtp", c.Mail.SMTPAddr != ""),
		slog.Bool("admin_console", c.AdminDB.URL != ""),
		slog.String("s3_endpoint", redactURL(c.Media.S3Endpoint)),
		slog.String("s3_bucket", c.Media.S3Bucket),
		slog.String("imgproxy", redactURL(c.Media.ImgproxyURL)),
		slog.Duration("edge_cache_ttl", c.Edge.CacheTTL),
		slog.Bool("edge_purge", c.Edge.Purges()),
		slog.Bool("captcha", c.Captcha.Enabled()),
		slog.Bool("fake_carrier", c.Shipping.FakeCarrier),
		slog.Bool("fake_gateway", c.Payments.FakeGateway),
		slog.String("meili", redactURL(c.Search.URL)),
	)
}

func (r *reader) media() Media {
	m := r.delivery()
	m.S3Endpoint = r.url("S3_ENDPOINT", true, "http", "https")
	m.S3Region = r.str("S3_REGION", "us-east-1")
	m.S3AccessKey = r.secret("S3_ACCESS_KEY")
	m.S3SecretKey = r.secret("S3_SECRET_KEY")
	m.MaxUploadBytes = r.int64("MEDIA_MAX_UPLOAD_BYTES", 15<<20, 1<<20, 100<<20)
	m.MaxImagePixels = r.int64("MEDIA_MAX_IMAGE_PIXELS", 25_000_000, 1_000_000, 100_000_000)
	m.ImageWorkers = int(r.int64("MEDIA_IMAGE_WORKERS", 2, 1, 64))
	m.S3PublicEndpoint = r.url("S3_PUBLIC_ENDPOINT", false, "http", "https")
	if m.S3PublicEndpoint == "" {
		m.S3PublicEndpoint = m.S3Endpoint
	}
	return m
}

// delivery reads what serving images needs: the bucket the masters are
// in (part of every signed URL) and imgproxy.
func (r *reader) delivery() Media {
	m := Media{
		S3Bucket:     r.str("S3_BUCKET", ""),
		ImgproxyURL:  r.url("IMGPROXY_URL", true, "http", "https"),
		ImgproxyKey:  r.hexSecret("IMGPROXY_KEY"),
		ImgproxySalt: r.hexSecret("IMGPROXY_SALT"),
	}
	if m.S3Bucket == "" {
		r.errorf("S3_BUCKET is required")
	}
	return m
}

// hexSecret reads a required hex-encoded secret of at least 16 bytes.
func (r *reader) hexSecret(key string) []byte {
	v := r.secret(key)
	if v == "" {
		return nil
	}
	b, err := hex.DecodeString(v)
	if err != nil || len(b) < 16 {
		r.errorf("%s: must be hex of at least 16 random bytes", key)
		return nil
	}
	return b
}

func redactURL(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return "[unparsable]"
	}
	return u.Redacted()
}

type reader struct {
	lookup LookupFunc
	errs   []error
}

func (r *reader) errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

func (r *reader) get(key string) (string, bool) {
	v, ok := r.lookup(key)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

func (r *reader) oneOf(key, def string, allowed ...string) string {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	r.errorf("%s=%q: must be one of %s", key, v, strings.Join(allowed, ", "))
	return def
}

func (r *reader) level(key string, def slog.Level) slog.Level {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	l, err := logging.ParseLevel(v)
	if err != nil {
		r.errorf("%s: %w", key, err)
		return def
	}
	return l
}

func (r *reader) addr(key, def string) string {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	if _, port, err := net.SplitHostPort(v); err != nil || port == "" {
		r.errorf("%s=%q: must be host:port", key, v)
	}
	return v
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	d := r.durationAllowZero(key, def)
	if d == 0 {
		r.errorf("%s: must be > 0", key)
	}
	return d
}

func (r *reader) durationAllowZero(key string, def time.Duration) time.Duration {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		r.errorf("%s=%q: must be a non-negative Go duration such as 5s", key, v)
		return def
	}
	return d
}

func (r *reader) int64(key string, def, lo, hi int64) int64 {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < lo || n > hi {
		r.errorf("%s=%q: must be an integer in [%d, %d]", key, v, lo, hi)
		return def
	}
	return n
}

func (r *reader) ratio(key string, def float64) float64 {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 1 {
		r.errorf("%s=%q: must be a number in [0, 1]", key, v)
		return def
	}
	return f
}

func (r *reader) url(key string, required bool, schemes ...string) string {
	v, ok := r.get(key)
	if !ok {
		if required {
			r.errorf("%s: required", key)
		}
		return ""
	}
	u, err := url.Parse(v)
	if err != nil {
		r.errorf("%s: not a valid URL", key) // never echo: may contain a password
		return ""
	}
	for _, s := range schemes {
		if u.Scheme == s && u.Host != "" {
			return v
		}
	}
	r.errorf("%s: scheme must be one of %s with a host", key, strings.Join(schemes, ", "))
	return ""
}

// secret reads a required secret. Its value never appears in errors.
func (r *reader) secret(key string) string {
	v, ok := r.get(key)
	if !ok || strings.TrimSpace(v) == "" {
		r.errorf("%s is required", key)
		return ""
	}
	return strings.TrimSpace(v)
}

func (r *reader) secretOptional(key string) string {
	v, _ := r.get(key)
	return strings.TrimSpace(v)
}

func (r *reader) str(key, def string) string {
	if v, ok := r.get(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// hostname reads a lower-case DNS name with at least two labels.
func (r *reader) hostname(key, def string) string {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	v = strings.ToLower(strings.TrimSuffix(v, "."))
	labels := strings.Split(v, ".")
	valid := len(v) <= 253 && len(labels) >= 2
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' ||
			strings.IndexFunc(l, func(c rune) bool { return (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' }) >= 0 {
			valid = false
		}
	}
	if !valid {
		r.errorf("%s: %q is not a domain name", key, v)
	}
	return v
}

func (r *reader) prefixes(key string) []netip.Prefix {
	v, ok := r.get(key)
	if !ok {
		return nil
	}
	var out []netip.Prefix
	for part := range strings.SplitSeq(v, ",") {
		p, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			r.errorf("%s: %q is not a CIDR prefix", key, part)
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}
