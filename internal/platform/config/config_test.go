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
		"SECRETBOX_KEYS":       "1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=",
		"OTP_PEPPER":           "ZGV2LW9ubHktb3RwLXBlcHBlci1ub3QtYS1zZWNyZXQ=",
		"S3_ENDPOINT":          "http://localhost:18333",
		"S3_BUCKET":            "iftah-media",
		"S3_ACCESS_KEY":        "local-dev-access",
		"S3_SECRET_KEY":        "local-dev-secret-not-for-prod",
		"IMGPROXY_URL":         "http://localhost:18088",
		"IMGPROXY_KEY":         "6465762d6f6e6c792d696d6770726f78792d6b65792d6e6f742d736563726574",
		"IMGPROXY_SALT":        "6465762d6f6e6c792d696d6770726f78792d73616c742d7075626c6963",
		"MEILI_URL":            "http://localhost:17700",
		"MEILI_API_KEY":        "local-dev-master-key-0123456789",
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
	if c.Tenancy.BaseDomain != "iftahonline.com" || c.Tenancy.AdminHost != "admin.iftahonline.com" ||
		c.Tenancy.HostCacheSize != 20000 || c.Tenancy.HostCacheTTL != time.Minute ||
		c.Tenancy.NegativeCacheSize != 4096 || c.Tenancy.NegativeTTL != 15*time.Second ||
		c.Tenancy.SharedCacheTTL != 10*time.Minute {
		t.Fatalf("unexpected tenancy defaults: %+v", c.Tenancy)
	}
	if c.Identity.Argon2MemoryKiB != 19456 || c.Identity.Argon2Time != 2 || c.Identity.Argon2Threads != 1 || c.Identity.HashConcurrency != 8 {
		t.Fatalf("unexpected identity defaults: %+v", c.Identity)
	}
	if _, err = config.Load(config.RoleAPI, env(with("ARGON2_MEMORY_KIB", "1024"))); err == nil {
		t.Fatal("argon2 memory below 8 MiB must be rejected")
	}
	m, err := config.Load(config.RoleMigrate, env(base()))
	if err != nil || !strings.Contains(m.DB.URL, "app_owner") {
		t.Fatalf("migrate must use MIGRATE_DATABASE_URL: %q, %v", m.DB.URL, err)
	}
}

func TestTenancyConfig(t *testing.T) {
	t.Parallel()
	c, err := config.Load(config.RoleStorefront, env(with(
		"PLATFORM_BASE_DOMAIN", "Shops.Example.EG.", "HOSTCACHE_SIZE", "10", "HOSTCACHE_NEGATIVE_TTL", "1s",
	)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Tenancy.BaseDomain != "shops.example.eg" || c.Tenancy.AdminHost != "admin.shops.example.eg" ||
		c.Tenancy.HostCacheSize != 10 || c.Tenancy.NegativeTTL != time.Second {
		t.Fatalf("tenancy = %+v", c.Tenancy)
	}
	for _, bad := range []string{"localhost", "-a.com", "a..com", "a_b.com", "a.com/x", strings.Repeat("a", 64) + ".com"} {
		if _, err := config.Load(config.RoleAPI, env(with("PLATFORM_BASE_DOMAIN", bad))); err == nil {
			t.Errorf("PLATFORM_BASE_DOMAIN=%q must be rejected", bad)
		}
	}
	if _, err := config.Load(config.RoleAPI, env(with("ADMIN_HOST", "nope"))); err == nil {
		t.Error("ADMIN_HOST must be a domain name")
	}
	if _, err := config.Load(config.RoleWorker, env(with("REDIS_URL", "<unset>"))); err == nil {
		t.Error("the worker needs REDIS_URL")
	}
	if m, err := config.Load(config.RoleMigrate, env(with("REDIS_URL", "<unset>"))); err != nil || m.Tenancy.BaseDomain != "" {
		t.Errorf("migrate needs no Redis or tenancy config: %+v, %v", m.Tenancy, err)
	}
}

func TestSecretsAndMail(t *testing.T) {
	t.Parallel()
	for _, role := range []config.Role{config.RoleAPI, config.RoleWorker} {
		c, err := config.Load(role, env(base()))
		if err != nil {
			t.Fatal(err)
		}
		if c.Secrets.Keyring == nil || len(c.Secrets.OTPPepper) != 32 || c.Mail.SMTPAddr != "" ||
			c.Mail.DashboardURL != "https://app.iftahonline.com" || !strings.Contains(c.Mail.From, "no-reply@iftahonline.com") {
			t.Fatalf("%s: secrets/mail = %+v %+v", role, c.Secrets, c.Mail)
		}
	}
	tests := map[string][]string{
		"missing keyring":        {"SECRETBOX_KEYS", "<unset>"},
		"bad keyring":            {"SECRETBOX_KEYS", "1:short"},
		"missing pepper":         {"OTP_PEPPER", "<unset>"},
		"short pepper":           {"OTP_PEPPER", "c2hvcnQ="},
		"bad smtp addr":          {"SMTP_ADDR", "no-port"},
		"prod smtp needs a user": {"APP_ENV", "production", "SMTP_ADDR", "smtp.example.com:587"},
	}
	for name, kv := range tests {
		_, err := config.Load(config.RoleAPI, env(with(kv...)))
		if err == nil {
			t.Errorf("%s: want error", name)
			continue
		}
		if strings.Contains(err.Error(), "ZGV2") {
			t.Errorf("%s: error leaks a secret: %v", name, err)
		}
	}
	// The storefront needs neither.
	if _, err := config.Load(config.RoleStorefront, env(with("SECRETBOX_KEYS", "<unset>", "OTP_PEPPER", "<unset>"))); err != nil {
		t.Fatalf("storefront: %v", err)
	}
	// Secrets never reach the startup log line.
	c, _ := config.Load(config.RoleAPI, env(with("SMTP_ADDR", "smtp.example.com:587", "SMTP_PASSWORD", "hunter2-smtp")))
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "config", c)
	if strings.Contains(buf.String(), "hunter2") || strings.Contains(buf.String(), "ZGV2") || !strings.Contains(buf.String(), `"smtp":true`) {
		t.Fatalf("log line = %s", buf.String())
	}
}

func TestAdminConfig(t *testing.T) {
	t.Parallel()
	c, err := config.Load(config.RoleAPI, env(base()))
	if err != nil || c.AdminDB.URL != "" {
		t.Fatalf("console must be off by default: %q, %v", c.AdminDB.URL, err)
	}
	c, err = config.Load(config.RoleAPI, env(with("ADMIN_DATABASE_URL", "postgres://app_admin:pw@db:5432/iftah")))
	if err != nil || !strings.Contains(c.AdminDB.URL, "app_admin") || c.AdminDB.MaxConns != 4 {
		t.Fatalf("admin db = %+v, %v", c.AdminDB, err)
	}
	// The admin CLI uses the admin role as its database, needs the
	// keyring and hashing parameters, but no Redis or OTP pepper.
	a, err := config.Load(config.RoleAdmin, env(map[string]string{
		"ADMIN_DATABASE_URL": "postgres://app_admin:pw@db:5432/iftah",
		"SECRETBOX_KEYS":     base()["SECRETBOX_KEYS"],
	}))
	if err != nil || !strings.Contains(a.DB.URL, "app_admin") || a.Secrets.Keyring == nil || a.Identity.Argon2MemoryKiB == 0 {
		t.Fatalf("admin cli = %+v, %v", a, err)
	}
	if _, err := config.Load(config.RoleAdmin, env(map[string]string{"SECRETBOX_KEYS": base()["SECRETBOX_KEYS"]})); err == nil {
		t.Fatal("admin cli needs ADMIN_DATABASE_URL")
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

func TestMedia(t *testing.T) {
	t.Parallel()
	for _, role := range []config.Role{config.RoleAPI, config.RoleWorker} {
		c, err := config.Load(role, env(base()))
		if err != nil {
			t.Fatal(err)
		}
		m := c.Media
		if m.S3PublicEndpoint != m.S3Endpoint || m.S3Region != "us-east-1" || m.S3Bucket != "iftah-media" ||
			len(m.ImgproxyKey) != 32 || len(m.ImgproxySalt) < 16 || m.MaxUploadBytes != 15<<20 || m.ImageWorkers != 2 {
			t.Fatalf("%s: media = %+v", role, m)
		}
	}
	c, err := config.Load(config.RoleAPI, env(with("S3_PUBLIC_ENDPOINT", "https://up.example.com")))
	if err != nil || c.Media.S3PublicEndpoint != "https://up.example.com" {
		t.Fatalf("public endpoint: %v %+v", err, c.Media)
	}
	if c, err := config.Load(config.RoleStorefront, env(with("S3_ENDPOINT", "<unset>"))); err != nil || c.Media.S3Endpoint != "" {
		t.Fatalf("storefront needs no media config: %v", err)
	}
	tests := map[string][]string{
		"missing endpoint": {"S3_ENDPOINT", "<unset>"},
		"bad endpoint":     {"S3_ENDPOINT", "ftp://x"},
		"missing bucket":   {"S3_BUCKET", "<unset>"},
		"missing secret":   {"S3_SECRET_KEY", "<unset>"},
		"missing imgproxy": {"IMGPROXY_URL", "<unset>"},
		"key not hex":      {"IMGPROXY_KEY", "not-hex"},
		"short salt":       {"IMGPROXY_SALT", "abcd"},
		"huge uploads":     {"MEDIA_MAX_UPLOAD_BYTES", "999999999999"},
	}
	for name, kv := range tests {
		_, err := config.Load(config.RoleWorker, env(with(kv...)))
		if err == nil {
			t.Errorf("%s: want error", name)
			continue
		}
		if strings.Contains(err.Error(), "local-dev-secret") || strings.Contains(err.Error(), "6465762d") {
			t.Errorf("%s: error leaks a secret: %v", name, err)
		}
	}
}

func TestSearch(t *testing.T) {
	t.Parallel()
	c, err := config.Load(config.RoleWorker, env(base()))
	if err != nil || c.Search.URL != "http://localhost:17700" || c.Search.Index != "products" || c.Search.APIKey == "" {
		t.Fatalf("search = %+v %v", c.Search, err)
	}
	for name, kv := range map[string][]string{
		"missing url": {"MEILI_URL", "<unset>"},
		"bad url":     {"MEILI_URL", "meili:7700"},
		"missing key": {"MEILI_API_KEY", "<unset>"},
	} {
		_, err := config.Load(config.RoleAPI, env(with(kv...)))
		if err == nil {
			t.Errorf("%s: want error", name)
		} else if strings.Contains(err.Error(), "local-dev-master-key") {
			t.Errorf("%s: error leaks the key", name)
		}
	}
}
