package ptybackend

import (
	"context"
	"github.com/victorarias/attn/internal/stepclock"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-ptybackend-test-*")
	if err != nil {
		panic("ptybackend: TestMain: MkdirTemp: " + err.Error())
	}
	config.ScopeTestEnvironment(dir)
	hub, err := stepclock.Listen(filepath.Join(dir, "clock.sock"))
	if err != nil {
		panic("ptybackend: TestMain: listen for the stepped clock: " + err.Error())
	}
	testClock = hub
	stepclock.Process()
	key, value, _ := strings.Cut(hub.Env(), "=")
	_ = os.Setenv(key, value)
	code := m.Run()
	hub.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

var testClock *stepclock.Hub

// advance moves the stepped clock of this test process and of every worker it started.
func advance(t *testing.T, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := testClock.Advance(ctx, d); err != nil {
		t.Fatalf("advance(%s): %v within 10s", d, err)
	}
}

// awaitTimer waits for this test process or a worker it started to arm a timer of d after the last advance or awaitTimer.
func awaitTimer(t *testing.T, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if seen, ok := testClock.AwaitTimer(ctx, d); !ok {
		t.Fatalf("awaitTimer(%s): no such timer armed within 10s; saw timer durations %v", d, seen)
	}
}
