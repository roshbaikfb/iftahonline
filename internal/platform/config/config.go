// Package config loads process configuration from environment variables
// only, validating everything at startup (CLAUDE.md Linux runtime rules).
// Load reports every problem at once so a misconfigured deploy fails fast
// with a complete list.
package config

import (
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
)

// Role is the binary's subcommand.
type Role string

// Roles.
const (
	RoleAPI        Role = "api"
	RoleStorefront Role = "storefront"
	RoleWorker     Role = "worker"
	RoleMigrate    Role = "migrate"
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

// Load reads configuration for role.
func Load(role Role, lookup LookupFunc) (Config, error) {
	r := reader{lookup: lookup}
	c := Config{Role: role}

	switch role {
	case RoleAPI, RoleStorefront, RoleWorker, RoleMigrate:
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
	if role == RoleMigrate {
		dbKey = "MIGRATE_DATABASE_URL" // owner role; services use the NOBYPASSRLS app_user
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

	if role == RoleAPI || role == RoleStorefront {
		c.Redis = Redis{URL: r.url("REDIS_URL", true, "redis", "rediss")}
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
	)
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
