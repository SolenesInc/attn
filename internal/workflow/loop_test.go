package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type cancellingStub struct {
	cancel   context.CancelFunc
	hold     bool
	released atomic.Bool
}

func (s *cancellingStub) Run(ctx context.Context, call AgentCall) (json.RawMessage, error) {
	s.cancel()
	if s.hold {
		<-ctx.Done()
		s.released.Store(true)
		return nil, ctx.Err()
	}
	return json.RawMessage(`"ok"`), nil
}

func TestRunawayScriptsEndWithAnErrorNamingTheLimit(t *testing.T) {
	overCap := func(each string) string {
		return `const items = []; for (let i = 0; i < 5; i++) items.push(i); return await ` + each + `;`
	}
	cases := []struct {
		name      string
		script    string
		configure func(*Config)
		cancels   bool
		holds     bool
		status    RunStatus
		err       string
		liveCalls int
	}{
		{name: "an infinite loop trips the watchdog", script: `while(true){}`,
			configure: func(c *Config) { c.WatchdogTimeout = 150 * time.Millisecond }, status: StatusInterrupted, err: "watchdog"},
		{name: "a busy loop after an await trips the watchdog", script: `await agent("warmup"); while(true){}`,
			configure: func(c *Config) { c.WatchdogTimeout = 150 * time.Millisecond }, status: StatusInterrupted, err: "watchdog", liveCalls: 1},
		{name: "cancelling a busy loop interrupts it", script: `await agent("warmup"); while(true){}`, cancels: true,
			status: StatusInterrupted, err: "cancel"},
		{name: "cancelling while an agent runs interrupts the run and releases the agent", script: `return await agent("x");`, cancels: true, holds: true,
			status: StatusInterrupted, err: "cancel"},
		{name: "the agent lifetime cap ends an endless run", script: `let n = 0; while (true) { await agent("call " + n); n++; }`,
			configure: func(c *Config) { c.AgentLifetimeCap = 5 }, status: StatusErrored, err: "lifetime cap", liveCalls: 5},
		{name: "parallel over the per-call cap is refused", script: overCap(`parallel(items.map((i) => () => agent("x" + i)))`),
			configure: func(c *Config) { c.MaxItemsPerCall = 4 }, status: StatusErrored, err: "per-call cap"},
		{name: "pipeline over the per-call cap is refused", script: overCap(`pipeline(items, (v, item) => agent("x" + item))`),
			configure: func(c *Config) { c.MaxItemsPerCall = 4 }, status: StatusErrored, err: "per-call cap"},
	}
	for _, tc := range cases {
		run := func(t *testing.T, check func(func())) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := Config{WatchdogTimeout: 30 * time.Second}
			if tc.configure != nil {
				tc.configure(&cfg)
			}
			stub := &cancellingStub{cancel: func() {}, hold: tc.holds}
			if tc.cancels {
				stub.cancel = cancel
				cfg.Stub = stub
			}
			var res RunResult
			check(func() { res, _ = New(cfg).Run(ctx, tc.script, nil) })
			if res.Status != tc.status || res.Err == nil || !strings.Contains(res.Err.Error(), tc.err) {
				t.Fatalf("the run ended %s with %v, want %s naming %q", res.Status, res.Err, tc.status, tc.err)
			}
			if tc.liveCalls != 0 && res.LiveCalls != tc.liveCalls {
				t.Errorf("the run made %d agent calls, want %d", res.LiveCalls, tc.liveCalls)
			}
			if tc.holds && !stub.released.Load() {
				t.Error("the agent in flight when the run was cancelled was never released")
			}
		}
		t.Run(tc.name, func(t *testing.T) {
			if !tc.holds {
				run(t, func(runIt func()) { runIt() })
				return
			}
			synctest.Test(t, func(t *testing.T) {
				run(t, func(runIt func()) { runIt(); synctest.Wait() })
			})
		})
	}
}
