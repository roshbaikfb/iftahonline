package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/server"
)

var (
	infra  testinfra.Infra
	binary string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "platform-bin-")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "platform")
	buildCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-trimpath", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "build: %v\n%s", err, out)
		os.Exit(1)
	}
	code := testinfra.Run(m, &infra)
	cancel()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// syncBuffer collects process output safely across goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func roleEnv(addr string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"APP_ENV=test",
		"LOG_LEVEL=debug",
		"HTTP_ADDR=" + addr,
		"DATABASE_URL=" + infra.AppURL,
		"MIGRATE_DATABASE_URL=" + infra.OwnerURL,
		"REDIS_URL=" + infra.RedisURL,
		// Public development-only values (see .env.example), not secrets.
		"SECRETBOX_KEYS=1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=",
		"OTP_PEPPER=ZGV2LW9ubHktb3RwLXBlcHBlci1ub3QtYS1zZWNyZXQ=",
		"S3_ENDPOINT=http://127.0.0.1:1", "S3_BUCKET=iftah-media",
		"S3_ACCESS_KEY=local-dev-access", "S3_SECRET_KEY=local-dev-secret-not-for-prod",
		"IMGPROXY_URL=http://127.0.0.1:1",
		"MEILI_URL=http://127.0.0.1:1", "MEILI_API_KEY=local-dev-master-key-0123456789",
		"IMGPROXY_KEY=6465762d6f6e6c792d696d6770726f78792d6b65792d6e6f742d736563726574",
		"IMGPROXY_SALT=6465762d6f6e6c792d696d6770726f78792d73616c742d7075626c6963",
		"DRAIN_DELAY=700ms",
		"SHUTDOWN_TIMEOUT=5s",
	}
}

func status(ctx context.Context, url string) int {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func waitStatus(ctx context.Context, url string, want int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if status(ctx, url) == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestStartupAndGracefulShutdown is the P0 acceptance test: each long-
// running role answers /livez within 1s of exec, becomes ready, reports
// draining after SIGTERM, and exits 0.
func TestStartupAndGracefulShutdown(t *testing.T) {
	for _, role := range []string{"api", "storefront", "worker"} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			addr := freeAddr(t)
			base := "http://" + addr
			var out syncBuffer
			cmd := exec.CommandContext(t.Context(), binary, role)
			cmd.Env = roleEnv(addr)
			cmd.Stdout, cmd.Stderr = &out, &out

			start := time.Now()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			t.Cleanup(func() { _ = cmd.Process.Kill() })

			if !waitStatus(t.Context(), base+"/livez", http.StatusOK, time.Second) {
				t.Fatalf("/livez not 200 within 1s of exec\n%s", out.String())
			}
			t.Logf("%s: /livez 200 after %s", role, time.Since(start).Round(time.Millisecond))

			if !waitStatus(t.Context(), base+"/readyz", http.StatusOK, 10*time.Second) {
				t.Fatalf("/readyz never 200\n%s", out.String())
			}

			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if !waitStatus(t.Context(), base+"/readyz", http.StatusServiceUnavailable, 500*time.Millisecond) {
				t.Fatalf("/readyz not 503 while draining\n%s", out.String())
			}

			select {
			case err := <-exited:
				if err != nil {
					t.Fatalf("exit: %v\n%s", err, out.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("did not exit within 10s of SIGTERM\n%s", out.String())
			}
			logs := out.String()
			for _, want := range []string{`"msg":"draining"`, `"msg":"http server stopped"`, `"msg":"exited cleanly"`} {
				if !strings.Contains(logs, want) {
					t.Errorf("logs missing %s\n%s", want, logs)
				}
			}
			if strings.Contains(logs, "app_user:app_user@") {
				t.Errorf("database password logged\n%s", logs)
			}
		})
	}
}

// TestWorkerProcessesJobs proves the outbox pipeline end to end: a job
// inserted with the app role is completed by the worker process.
func TestWorkerProcessesJobs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	addr := freeAddr(t)
	cmd := exec.CommandContext(ctx, binary, "worker")
	cmd.Env = roleEnv(addr)
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})
	if !waitStatus(ctx, "http://"+addr+"/readyz", http.StatusOK, 10*time.Second) {
		t.Fatalf("worker not ready\n%s", out.String())
	}

	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	inserter, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := inserter.Insert(ctx, server.NoopArgs{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if err := pool.QueryRow(ctx, "SELECT state FROM river_job WHERE id = $1", res.Job.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "completed" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %d not completed\n%s", res.Job.ID, out.String())
}

func TestMigrateStatus(t *testing.T) {
	t.Parallel()
	cmd := exec.CommandContext(t.Context(), binary, "migrate", "status")
	cmd.Env = roleEnv("127.0.0.1:0")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "00001_platform_baseline.sql") {
		t.Fatalf("migrate status: %v\n%s", err, out)
	}
}

func TestRunExitCodes(t *testing.T) {
	t.Parallel()
	empty := func(string) (string, bool) { return "", false }
	tests := []struct {
		name string
		args []string
		want int
		out  string
	}{
		{"no args", nil, 2, ""},
		{"version", []string{"version"}, 0, `"go_version"`},
		{"bad config", []string{"api"}, 1, "DATABASE_URL: required"},
		{"unknown role", []string{"cron"}, 1, `unknown role`},
		{"migrate usage", []string{"migrate"}, 1, "usage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			lookup := empty
			if tt.name == "migrate usage" {
				lookup = func(k string) (string, bool) {
					if k == "MIGRATE_DATABASE_URL" {
						return infra.OwnerURL, true
					}
					return "", false
				}
			}
			if got := run(tt.args, strings.NewReader(""), &buf, lookup); got != tt.want || !strings.Contains(buf.String(), tt.out) {
				t.Fatalf("run(%v) = %d, output %q", tt.args, got, buf.String())
			}
		})
	}
}

// TestAPISignupEndToEnd runs the real api binary: sign up, then use the
// returned session on a store route and on /v1/me. The password is never
// logged.
func TestAPISignupEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	addr := freeAddr(t)
	cmd := exec.CommandContext(ctx, binary, "api")
	cmd.Env = append(roleEnv(addr), "ARGON2_MEMORY_KIB=8192", "ARGON2_TIME=1")
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})
	base := "http://" + addr
	if !waitStatus(ctx, base+"/readyz", http.StatusOK, 10*time.Second) {
		t.Fatalf("api not ready\n%s", out.String())
	}

	slug := fmt.Sprintf("e2e-%d", time.Now().UnixNano()%1_000_000_000)
	body := fmt.Sprintf(`{"email":"%s@example.com","password":"correct horse battery","name":"E2E","client":"mobile",
		"store":{"name":"E2E","slug":%q,"currency":"EGP","locale":"ar","timezone":"Africa/Cairo"}}`, slug, slug)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/auth/signup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var signup struct {
		Token   string `json:"token"`
		StoreID string `json:"store_id"`
	}
	err = json.NewDecoder(res.Body).Decode(&signup)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != http.StatusCreated || signup.Token == "" {
		t.Fatalf("signup = %d %+v %v\n%s", res.StatusCode, signup, err, out.String())
	}

	for _, path := range []string{"/v1/stores/" + signup.StoreID, "/v1/stores/" + signup.StoreID + "/domains", "/v1/me"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		req.Header.Set("Authorization", "Bearer "+signup.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d\n%s", path, res.StatusCode, out.String())
		}
	}
	if logs := out.String(); strings.Contains(logs, "correct horse battery") || strings.Contains(logs, signup.Token) {
		t.Fatalf("secret logged\n%s", logs)
	}
}

// TestAdminCLI creates a platform admin through `platform admin create`:
// the password comes from stdin, the TOTP enrolment is printed once, and
// neither appears in the JSON logs.
func TestAdminCLI(t *testing.T) {
	t.Parallel()
	lookup := func(k string) (string, bool) {
		v, ok := map[string]string{
			"APP_ENV":            "test",
			"ADMIN_DATABASE_URL": infra.AdminURL,
			"SECRETBOX_KEYS":     "1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=",
			"ARGON2_MEMORY_KIB":  "8192",
			"ARGON2_TIME":        "1",
		}[k]
		return v, ok
	}
	email := fmt.Sprintf("cli-%d@iftahonline.com", time.Now().UnixNano())
	var out bytes.Buffer
	code := run([]string{"admin", "create", "--email", email, "--name", "Ops CLI"}, strings.NewReader("a long admin passphrase\n"), &out, lookup)
	if code != 0 || !strings.Contains(out.String(), "admin created: "+email) || !strings.Contains(out.String(), "otpauth://totp/") {
		t.Fatalf("create = %d\n%s", code, out.String())
	}
	secret := strings.TrimSpace(strings.SplitN(strings.SplitN(out.String(), "secret: ", 2)[1], "\n", 2)[0])
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "{") && (strings.Contains(line, secret) || strings.Contains(line, "passphrase")) {
			t.Fatalf("secret in a log line: %s", line)
		}
	}

	for name, tc := range map[string]struct {
		args  []string
		stdin string
	}{
		"duplicate":     {[]string{"admin", "create", "--email", email, "--name", "Again"}, "a long admin passphrase\n"},
		"weak password": {[]string{"admin", "create", "--email", "x" + email, "--name", "Weak"}, "short\n"},
		"no flags":      {[]string{"admin", "create"}, ""},
		"no subcommand": {[]string{"admin"}, ""},
	} {
		var buf bytes.Buffer
		if got := run(tc.args, strings.NewReader(tc.stdin), &buf, lookup); got != 1 {
			t.Errorf("%s: exit %d\n%s", name, got, buf.String())
		}
	}
}

// TestAPIServesConsoleOnlyOnAdminHost: with ADMIN_DATABASE_URL the real
// api binary serves /admin/v1 on the admin host (401 without a session)
// and 404 everywhere else.
func TestAPIServesConsoleOnlyOnAdminHost(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	addr := freeAddr(t)
	cmd := exec.CommandContext(ctx, binary, "api")
	cmd.Env = append(roleEnv(addr), "ADMIN_DATABASE_URL="+infra.AdminURL, "ARGON2_MEMORY_KIB=8192", "ARGON2_TIME=1")
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})
	if !waitStatus(ctx, "http://"+addr+"/readyz", http.StatusOK, 10*time.Second) {
		t.Fatalf("api not ready\n%s", out.String())
	}
	for host, want := range map[string]int{"admin.iftahonline.com": http.StatusUnauthorized, "api.iftahonline.com": http.StatusNotFound} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/admin/v1/me", nil)
		req.Host = host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("Host %s: %d, want %d", host, res.StatusCode, want)
		}
	}
}
