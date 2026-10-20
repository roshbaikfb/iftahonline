package redisx_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/redisx"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

func TestNewAndPing(t *testing.T) {
	t.Parallel()
	c, err := redisx.New(config.Redis{URL: infra.RedisURL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestBadURL(t *testing.T) {
	t.Parallel()
	if _, err := redisx.New(config.Redis{URL: "mysql://x"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestSubscribe(t *testing.T) {
	t.Parallel()
	c, err := redisx.New(config.Redis{URL: infra.RedisURL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(t.Context())
	msgs := redisx.Subscribe(ctx, c, "test:subscribe", slog.New(slog.NewTextHandler(io.Discard, nil)))

	// The subscription is asynchronous: publish until it is heard.
	deadline := time.After(10 * time.Second)
	for got := false; !got; {
		if err = c.Publish(t.Context(), "test:subscribe", "store-1").Err(); err != nil {
			t.Fatal(err)
		}
		select {
		case m := <-msgs:
			if m != "store-1" {
				t.Fatalf("payload %q", m)
			}
			got = true
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("no message")
		}
	}
	cancel()
	for range msgs { // drains, then closes when ctx ends
	}
}
