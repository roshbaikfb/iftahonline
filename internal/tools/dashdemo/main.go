// Command dashdemo runs the real API for the dashboard's browser tests: it
// starts throwaway PostgreSQL and Redis (testinfra), runs `platform api`
// on them, seeds a merchant through the API itself (sign-up, a product,
// orders), and only then serves the API on -addr, so a test that waits
// for -addr finds the data ready. It is a test tool and is never
// deployed.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
)

// Demo credentials (a throwaway database; printed for the tests).
const (
	Email    = "owner@demo.test"
	Password = "demo-password-123"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18180", "where the seeded API is served")
	bin := flag.String("platform", "bin/platform", "the platform binary")
	flag.Parse()
	if err := run(*addr, *bin); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "dashdemo:", err)
		os.Exit(1)
	}
}

func run(addr, bin string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	infra, teardown, err := testinfra.Start(startCtx)
	if err != nil {
		return err
	}
	defer teardown()

	inner := "127.0.0.1:18189"
	api := exec.CommandContext(ctx, bin, "api") //nolint:gosec // a test tool running our own binary
	api.Stdout, api.Stderr = os.Stderr, os.Stderr
	// Only what the api needs: nothing leaks in from this process's
	// environment (config comes from explicit variables, validated at
	// startup).
	api.Env = append([]string{},
		"APP_ENV=test", "HTTP_ADDR="+inner, "DATABASE_URL="+infra.AppURL, "REDIS_URL="+infra.RedisURL, "DRAIN_DELAY=0s",
		"SHIPPING_FAKE_CARRIER=true", "PAYMENTS_FAKE_GATEWAY=true", "MESSAGING_FAKE_CHANNELS=true",
		// Public development-only values (see .env.example), not secrets.
		"SECRETBOX_KEYS=1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=",
		"OTP_PEPPER=ZGV2LW9ubHktb3RwLXBlcHBlci1ub3QtYS1zZWNyZXQ=",
		"S3_ENDPOINT=http://127.0.0.1:1", "S3_BUCKET=iftah-media", "S3_ACCESS_KEY=local-dev-access", "S3_SECRET_KEY=local-dev-secret-not-for-prod",
		"IMGPROXY_URL=http://127.0.0.1:1", "MEILI_URL=http://127.0.0.1:1", "MEILI_API_KEY=local-dev-master-key-0123456789",
		"IMGPROXY_KEY=6465762d6f6e6c792d696d6770726f78792d6b65792d6e6f742d736563726574",
		"IMGPROXY_SALT=6465762d6f6e6c792d696d6770726f78792d73616c742d7075626c6963",
	)
	if err = api.Start(); err != nil {
		return err
	}
	defer func() {
		if sigErr := api.Process.Signal(syscall.SIGTERM); sigErr != nil {
			_, _ = fmt.Fprintln(os.Stderr, "dashdemo: stop api:", sigErr)
		}
		if waitErr := api.Wait(); waitErr != nil {
			_, _ = fmt.Fprintln(os.Stderr, "dashdemo: api exited:", waitErr) // a signal exit is expected here
		}
	}()

	base := "http://" + inner
	if err = waitReady(ctx, base+"/readyz"); err != nil {
		return err
	}
	if err = seed(ctx, base); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	target, err := url.Parse(base)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: httputil.NewSingleHostReverseProxy(target), ReadHeaderTimeout: 5 * time.Second}
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		if shutErr := srv.Shutdown(shutdown); shutErr != nil {
			_, _ = fmt.Fprintln(os.Stderr, "dashdemo: shutdown:", shutErr)
		}
	}()
	fmt.Printf("dashdemo: ready at http://%s (%s / %s)\n", addr, Email, Password) //nolint:forbidigo // the ready line tests wait for
	if err = srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func waitReady(ctx context.Context, u string) error {
	for i := 0; i < 120; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("the api did not become ready")
}

// seed creates the merchant through the public API, as the dashboard
// would: sign-up (a mobile client, to get a bearer token), the setup
// wizard closed, one product, three orders, one of them confirmed.
func seed(ctx context.Context, base string) error {
	c := client{ctx: ctx, base: base}
	var signup struct {
		Token   string `json:"token"`
		StoreID string `json:"store_id"`
	}
	if err := c.do(http.MethodPost, "/v1/auth/signup", map[string]any{
		"email": Email, "password": Password, "name": "منى أحمد", "client": "mobile",
		"store": map[string]any{"name": "متجر منى", "slug": "mona-demo", "currency": "EGP", "locale": "ar", "timezone": "Africa/Cairo"},
	}, http.StatusCreated, &signup); err != nil {
		return err
	}
	c.token = signup.Token
	store := "/v1/stores/" + signup.StoreID
	// The demo merchant runs an established store: their setup wizard
	// (ADR-0034) is closed, so signing in opens the orders.
	if err := c.do(http.MethodPost, store+"/onboarding/complete", nil, http.StatusOK, nil); err != nil {
		return err
	}
	var product struct {
		Variants []struct {
			ID string `json:"id"`
		} `json:"variants"`
	}
	if err := c.do(http.MethodPost, store+"/products", map[string]any{
		"title": "عطر العود الملكي", "status": "active", "variants": []any{map[string]any{"price_minor": 45000, "on_hand": 100}},
	}, http.StatusCreated, &product); err != nil {
		return err
	}
	people := []struct{ name, phone, gov string }{
		{"سارة علي", "01011111111", "EG-C"}, {"أحمد حسن", "01022222222", "EG-GZ"}, {"منى محمود", "01033333333", "EG-ALX"},
	}
	var ids []string
	for _, p := range people {
		var d struct {
			Order struct {
				ID string `json:"id"`
			} `json:"order"`
		}
		if err := c.do(http.MethodPost, store+"/orders", map[string]any{
			"name": p.name, "phone": p.phone, "governorate": p.gov, "city": "المدينة", "address": "12 شارع التحرير",
			"lines": []any{map[string]any{"variant_id": product.Variants[0].ID, "qty": 1}},
		}, http.StatusCreated, &d); err != nil {
			return err
		}
		ids = append(ids, d.Order.ID)
	}
	return c.do(http.MethodPost, store+"/orders/"+ids[0]+"/transitions", map[string]any{"status": "confirmed"}, http.StatusOK, nil)
}

type client struct {
	ctx   context.Context
	base  string
	token string
}

func (c client) do(method, path string, body any, want int, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != want {
		return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
