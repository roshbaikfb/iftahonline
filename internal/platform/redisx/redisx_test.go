package redisx_test

import (
	"os"
	"testing"

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
