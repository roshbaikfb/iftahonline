package config_test

import (
	"bytes"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
)

func env(m map[string]string) config.LookupFunc {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func base() map[string]string {
	return map[string]string{
		"DATABASE_URL":         "postgres://app_user:s3cret@localhost:5432/iftah?sslmode=disable",
		"MIGRATE_DATABASE_URL": "postgres://app_owner:0wner@localhost:5432/iftah",
		"REDIS_URL":            "redis://localhost:6379/0",
	}
}

func with(kv ...string) map[string]string {
	m := base()
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == "<unset>" {
			delete(m, kv[i])
			continue
		}
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	c, err := config.Load(config.RoleAPI, env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != config.EnvDevelopment || c.LogLevel != slog.LevelInfo || c.HTTP.Addr != ":8080" ||
		c.HTTP.RequestTimeout != 10*time.Second || c.DB.MaxConns != 20 || c.Worker.Concurrency != 10 ||
		c.OTel.Endpoint != "" || c.OTel.SampleRatio != 0.1 || c.HTTP.DrainDelay != 5*time.Second {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	for role, addr := range map[config.Role]string{config.RoleStorefront: ":8081", config.RoleWorker: ":8082"} {
		rc, rerr := config.Load(role, env(base()))
		if rerr != nil || rc.HTTP.Addr != addr {
			t.Errorf("%s addr = %q, %v", role, rc.HTTP.Addr, rerr)
		}
	}
	m, err := config.Load(config.RoleMigrate, env(base()))
	if err != nil || !strings.Contains(m.DB.URL, "app_owner") {
		t.Fatalf("migrate must use MIGRATE_DATABASE_URL: %q, %v", m.DB.URL, err)
	}
}

func TestOverrides(t *testing.T) {
	t.Parallel()
	c, err := config.Load(config.RoleAPI, env(with(
		"APP_ENV", "production", "LOG_LEVEL", "warn", "HTTP_ADDR", "127.0.0.1:9000",
		"HTTP_REQUEST_TIMEOUT", "2s", "DRAIN_DELAY", "0s", "DB_MAX_CONNS", "50",
		"TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.7/24", "OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel:4318",
		"OTEL_TRACES_SAMPLER_ARG", "1", "WORKER_CONCURRENCY", "64", "HTTP_MAX_BODY_BYTES", "2048",
	)))
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.0/24")}
	if c.Env != "production" || c.LogLevel != slog.LevelWarn || c.HTTP.Addr != "127.0.0.1:9000" ||
		c.HTTP.RequestTimeout != 2*time.Second || c.HTTP.DrainDelay != 0 || c.DB.MaxConns != 50 ||
		len(c.HTTP.TrustedProxies) != 2 || c.HTTP.TrustedProxies[0] != want[0] || c.HTTP.TrustedProxies[1] != want[1] ||
		c.OTel.Endpoint != "http://otel:4318" || c.OTel.SampleRatio != 1 || c.Worker.Concurrency != 64 || c.HTTP.MaxBodyBytes != 2048 {
		t.Fatalf("overrides not applied: %+v", c)
	}
}

func TestInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		role    config.Role
		env     map[string]string
		wantMsg []string
	}{
		{"unknown role", config.Role("cron"), base(), []string{`unknown role "cron"`}},
		{"missing db", config.RoleAPI, with("DATABASE_URL", "<unset>"), []string{"DATABASE_URL: required"}},
		{"blank db", config.RoleAPI, with("DATABASE_URL", "   "), []string{"DATABASE_URL: required"}},
		{"missing migrate db", config.RoleMigrate, with("MIGRATE_DATABASE_URL", "<unset>"), []string{"MIGRATE_DATABASE_URL: required"}},
		{"missing redis", config.RoleStorefront, with("REDIS_URL", "<unset>"), []string{"REDIS_URL: required"}},
		{"bad db scheme", config.RoleAPI, with("DATABASE_URL", "mysql://u:p@h/db"), []string{"DATABASE_URL: scheme"}},
		{"bad env", config.RoleAPI, with("APP_ENV", "prod"), []string{"APP_ENV"}},
		{"bad level", config.RoleAPI, with("LOG_LEVEL", "loud"), []string{"LOG_LEVEL"}},
		{"bad addr", config.RoleAPI, with("HTTP_ADDR", "8080"), []string{"HTTP_ADDR"}},
		{"bad duration", config.RoleAPI, with("HTTP_READ_TIMEOUT", "fast"), []string{"HTTP_READ_TIMEOUT"}},
		{"zero duration", config.RoleAPI, with("SHUTDOWN_TIMEOUT", "0s"), []string{"SHUTDOWN_TIMEOUT: must be > 0"}},
		{"negative duration", config.RoleAPI, with("DRAIN_DELAY", "-1s"), []string{"DRAIN_DELAY"}},
		{"write <= request", config.RoleAPI, with("HTTP_WRITE_TIMEOUT", "5s"), []string{"must exceed HTTP_REQUEST_TIMEOUT"}},
		{"int range", config.RoleAPI, with("DB_MAX_CONNS", "0"), []string{"DB_MAX_CONNS"}},
		{"min > max", config.RoleAPI, with("DB_MIN_CONNS", "30"), []string{"must not exceed DB_MAX_CONNS"}},
		{"bad ratio", config.RoleAPI, with("OTEL_TRACES_SAMPLER_ARG", "2"), []string{"OTEL_TRACES_SAMPLER_ARG"}},
		{"bad otel url", config.RoleAPI, with("OTEL_EXPORTER_OTLP_ENDPOINT", "grpc://x"), []string{"OTEL_EXPORTER_OTLP_ENDPOINT"}},
		{"bad cidr", config.RoleAPI, with("TRUSTED_PROXIES", "10.0.0.0/8,nope"), []string{`"nope" is not a CIDR`}},
		{"all errors reported", config.RoleAPI, with("DATABASE_URL", "<unset>", "REDIS_URL", "<unset>", "LOG_LEVEL", "x"),
			[]string{"DATABASE_URL", "REDIS_URL", "LOG_LEVEL"}},
		{"unparsable url not echoed", config.RoleAPI, with("DATABASE_URL", "postgres://u:p@[bad"), []string{"DATABASE_URL: not a valid URL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Load(tt.role, env(tt.env))
			if err == nil {
				t.Fatal("expected error")
			}
			for _, w := range tt.wantMsg {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "u:p@") {
				t.Errorf("secret leaked in error: %v", err)
			}
		})
	}
}

func TestLogValueRedacts(t *testing.T) {
	t.Parallel()
	c, err := config.Load(config.RoleAPI, env(base()))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config", "cfg", c)
	if strings.Contains(buf.String(), "s3cret") || !strings.Contains(buf.String(), "app_user:xxxxx@") {
		t.Fatalf("not redacted: %s", buf.String())
	}
}
