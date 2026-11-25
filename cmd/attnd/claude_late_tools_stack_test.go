package main_test

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/claudemod"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/fakeclaudeapi"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type claudeBehindToolsProxy struct {
	stack     *testworld.Stack
	model     *fakeclaudeapi.Model
	work      string
	configDir string
	debugLog  *lineLog
	answer    atomic.Pointer[http.HandlerFunc]
}

func (c *claudeBehindToolsProxy) answerWith(answer http.HandlerFunc) { c.answer.Store(&answer) }

func newClaudeBehindToolsProxy(t *testing.T) *claudeBehindToolsProxy {
	t.Helper()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	c := &claudeBehindToolsProxy{model: fakeclaudeapi.Start(t)}
	fifo := makeFifo(t, filepath.Join(t.TempDir(), "claude-debug"))
	c.debugLog = followLines(t, fifo)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { (*c.answer.Load())(w, r) }))
	t.Cleanup(proxy.Close)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	c.stack = s
	c.work = s.Path("shop")
	c.configDir = filepath.Join(s.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, c.configDir, c.work)
	if err := os.WriteFile(filepath.Join(c.configDir, "settings.json"), []byte(`{"permissions":{"allow":["Bash(attn agent inbox)","Bash(attn seed standby:*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	upstream, err := url.Parse("http://" + s.WSAddr)
	if err != nil {
		t.Fatal(err)
	}
	c.answerWith(httputil.NewSingleHostReverseProxy(upstream).ServeHTTP)
	launcher := filepath.Join(s.Dir, "claude-behind-proxy")
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in *mcpServers*) ;; *) exec %[1]s "$@" ;; esac
for arg do
  shift
  case $arg in *mcpServers*) arg=$(printf %%s "$arg" | sed 's#http://%[2]s/#%[3]s/#') ;; esac
  set -- "$@" "$arg"
done
exec %[1]s --debug-file %[4]s "$@"
`, claude, s.WSAddr, proxy.URL, fifo)
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Vars = append(s.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+launcher,
		"CLAUDE_CONFIG_DIR="+c.configDir,
		"ANTHROPIC_BASE_URL="+c.model.URL,
		"ANTHROPIC_API_KEY="+fakeclaudeapi.APIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	s.Start()
	requireStdout(t, s.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
	return c
}

type lineLog struct {
	mu      sync.Mutex
	lines   []string
	changed chan struct{}
}

func followLines(t *testing.T, fifo string) *lineLog {
	t.Helper()
	f, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	l := &lineLog{changed: make(chan struct{})}
	go func() {
		scanner := bufio.NewScanner(f)
		scanner.Buffer(nil, 16<<20)
		for scanner.Scan() {
			l.mu.Lock()
			l.lines = append(l.lines, scanner.Text())
			close(l.changed)
			l.changed = make(chan struct{})
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *lineLog) await(t *testing.T, contains string) string {
	t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	for read := 0; ; {
		l.mu.Lock()
		seen, changed := l.lines, l.changed
		unread := seen[read:]
		read = len(seen)
		l.mu.Unlock()
		for _, line := range unread {
			if strings.Contains(line, contains) {
				return line
			}
		}
		select {
		case <-changed:
		case <-guard.C:
			t.Fatalf("no line with %q within %s; lines:\n%s", contains, fakeagent.HangGuard, strings.Join(seen, "\n"))
		}
	}
}

func TestRealClaudesDebugLogSaysWhyASessionStartedWithoutAttnsTools(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer http.HandlerFunc
		want   string
	}{
		{"a server that never answers", func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		}, "attn's tools had not connected after 8000 ms"},
		{"a server that refuses the credential", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }, "attn's tools did not connect (failed): "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newClaudeBehindToolsProxy(t)
			c.answerWith(tc.answer)
			app := c.stack.App()
			const brief = "write the migration guide"
			c.stack.Spawn(app, fakeagent.Claude, c.work, func(m *protocol.SpawnSessionMessage) { m.InitialPrompt = protocol.Ptr(brief) })
			t.Log(c.debugLog.await(t, tc.want))
			if offered := c.model.AwaitToolsOffered(t, brief); slices.Contains(offered, "mcp__attn__seed_show") {
				t.Errorf("the first request offered %q, want it without attn's tools", offered)
			}
		})
	}
}

// Receipt (2.1.332): Claude asks server/discover, then initialize, before it gives up one connect.
const requestsOfOneConnect = 2

func TestMailThatResumesARealClaudeWaitsForAttnsToolsWhenTheirServerComesUpLate(t *testing.T) {
	t.Parallel()
	c := newClaudeBehindToolsProxy(t)
	s, model := c.stack, c.model
	const reviewer = "rev-1111-2222"
	register(t, s, reviewer, "reviewer")
	app := s.App()
	tender := s.Spawn(app, fakeagent.Claude, c.work)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("tender screen:\n%s", s.Attn("agent", "peek", tender).Stdout)
		}
	})
	s.AwaitTimer(claudemod.PollHoldTimer)
	var seed protocol.Seed
	s.Run(testworld.Invocation{Args: []string{"seed", "plant", "Merge the discount field", "--json"}, Session: tender}).JSON(t, &seed)
	if tend := s.Run(testworld.Invocation{Args: []string{"seed", "tend", seed.ID}, Session: tender}); tend.Code != 0 {
		t.Fatalf("seed tend exited %d: %s", tend.Code, tend.Stderr)
	}
	const note = "On approval: merge the pull request."
	const ring = "attn agent inbox"
	model.RunBashAfter(ring, "attn seed standby "+seed.ID+" -m \""+note+"\"")
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", tender, "wait for the review"}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == tender
	})

	upstream := *c.answer.Load()
	var refused atomic.Int32
	c.answerWith(func(w http.ResponseWriter, r *http.Request) {
		if refused.Add(1) <= requestsOfOneConnect {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		upstream(w, r)
	})
	mark := model.Mark()
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", seed.ID, "the pull request is approved"}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg %s exited %d: %s%s", seed.ID, sent.Code, sent.Stdout, sent.Stderr)
	}
	if offered := model.AwaitToolsOfferedSince(t, mark, note); !slices.Contains(offered, "mcp__attn__seed_show") {
		t.Errorf("the request of the turn the mail woke offered %q, want attn's tools among them", offered)
	}
}
