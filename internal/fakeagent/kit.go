package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const HangGuard = 10 * time.Second

var piManifest = fmt.Sprintf(`name = %q
version = %q
attn_api_version = %d

[plugin]
kind = "executable"
path = %q
`, piPluginName, piVersion, piPluginAPIVersion, piPluginName)

type Kit struct {
	t        testing.TB
	cfg      config
	control  net.Listener
	mu       sync.Mutex
	launches map[string]chan *Run
	boots    map[string]chan struct{}
	fakes    []*fake
	failures []string
}

type fake struct {
	launch
	peer     *rpcPeer
	mu       sync.Mutex
	exitCode *int
}

func Install(t testing.TB, dir string, harnesses []Harness, wrapper string) *Kit {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	toolHome := filepath.Join(dir, "toolhome")
	cfg := config{
		Control:   filepath.Join(dir, "fakeagent.sock"),
		Bin:       filepath.Join(dir, "bin"),
		ToolHome:  toolHome,
		CodexHome: filepath.Join(toolHome, ".codex"),
		Harnesses: harnesses,
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	links := map[string]string{wrapperName: self}
	if wrapper != "" {
		links[wrapperName] = wrapper
	}
	for _, h := range []Harness{Claude, Codex, Copilot, Pi} {
		links[string(h)] = self
	}
	mustInstall(t, cfg.Bin, links, map[string]string{configName: string(encoded)})
	if slices.Contains(harnesses, Pi) {
		mustInstall(t, filepath.Join(dir, "plugins", piPluginName),
			map[string]string{piPluginName: self},
			map[string]string{configName: string(encoded), "attn-plugin.toml": piManifest})
	}
	listener, err := net.Listen("unix", cfg.Control)
	if err != nil {
		t.Fatal(err)
	}
	k := &Kit{t: t, cfg: cfg, control: listener, launches: map[string]chan *Run{}, boots: map[string]chan struct{}{}}
	go k.accept()
	t.Cleanup(k.verify)
	return k
}

func (k *Kit) Env() []string {
	return []string{
		"ATTN_TOOL_HOME=" + k.cfg.ToolHome,
		"CODEX_HOME=" + k.cfg.CodexHome,
		"PATH=" + k.cfg.Bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"ATTN_CLAUDE_EXECUTABLE=" + filepath.Join(k.cfg.Bin, string(Claude)),
		"ATTN_CODEX_EXECUTABLE=" + filepath.Join(k.cfg.Bin, string(Codex)),
		"ATTN_COPILOT_EXECUTABLE=" + filepath.Join(k.cfg.Bin, string(Copilot)),
		"ATTN_WRAPPER_PATH=" + filepath.Join(k.cfg.Bin, wrapperName),
	}
}

func mustInstall(t testing.TB, dir string, links, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func (k *Kit) Launched(sessionID string) *Run {
	k.t.Helper()
	select {
	case run := <-k.launchesFor(sessionID):
		if run.fake.Error != "" {
			k.t.Fatalf("fake %s for session %s failed to launch: %s", run.Harness, sessionID, run.fake.Error)
		}
		return run
	case <-time.After(HangGuard):
		k.t.Fatalf("no fake agent launched for session %s within %s%s", sessionID, HangGuard, k.failureSummary())
		return nil
	}
}

func (k *Kit) HoldBoot(sessionID string) (boot func()) {
	cue := make(chan struct{})
	k.mu.Lock()
	defer k.mu.Unlock()
	k.boots[sessionID] = cue
	return sync.OnceFunc(func() { close(cue) })
}

func (k *Kit) awaitBoot(sessionID string) {
	k.mu.Lock()
	cue := k.boots[sessionID]
	delete(k.boots, sessionID)
	k.mu.Unlock()
	if cue == nil {
		return
	}
	select {
	case <-cue:
	case <-time.After(HangGuard):
		k.fail(fmt.Sprintf("the boot of session %q was held and never released", sessionID))
	}
}

func (k *Kit) launchesFor(sessionID string) chan *Run {
	k.mu.Lock()
	defer k.mu.Unlock()
	launches, ok := k.launches[sessionID]
	if !ok {
		launches = make(chan *Run, 4)
		k.launches[sessionID] = launches
	}
	return launches
}

func (k *Kit) accept() {
	for {
		conn, err := k.control.Accept()
		if err != nil {
			return
		}
		f := &fake{}
		f.peer = newRPCPeer(conn, func(_ *rpcPeer, method string, params json.RawMessage) (any, error) {
			return struct{}{}, k.handle(f, method, params)
		})
		f.peer.start()
	}
}

func (k *Kit) handle(f *fake, method string, params json.RawMessage) error {
	switch method {
	case methodBooting:
		var booting bootingParams
		if err := json.Unmarshal(params, &booting); err != nil {
			return err
		}
		k.awaitBoot(booting.AttnSessionID)
	case methodLaunched:
		if err := json.Unmarshal(params, &f.launch); err != nil {
			return err
		}
		k.mu.Lock()
		k.fakes = append(k.fakes, f)
		k.mu.Unlock()
		if f.Error != "" {
			k.fail(fmt.Sprintf("fake %s for session %q failed to launch: %s (argv %q)", f.Harness, f.AttnSessionID, f.Error, f.Argv))
		}
		if f.Role == roleAgent {
			k.launchesFor(f.AttnSessionID) <- &Run{
				Harness:        f.Harness,
				SessionID:      f.AttnSessionID,
				ConversationID: f.ConversationID,
				Resumed:        f.Resumed,
				Argv:           f.Argv,
				AutoMode:       f.AutoMode,
				Yolo:           f.Yolo,
				t:              k.t,
				fake:           f,
			}
		}
	case methodUnexpected:
		var unexpected unexpectedLaunch
		if err := json.Unmarshal(params, &unexpected); err != nil {
			return err
		}
		k.fail(fmt.Sprintf("%s (argv %q)", unexpected.Reason, unexpected.Argv))
	case methodExiting:
		var exit exitParams
		if err := json.Unmarshal(params, &exit); err != nil {
			return err
		}
		f.mu.Lock()
		f.exitCode = &exit.Code
		f.mu.Unlock()
	default:
		return fmt.Errorf("unknown method %q", method)
	}
	return nil
}

func (f *fake) exited() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.exitCode == nil {
		return -1
	}
	return *f.exitCode
}

func (k *Kit) fail(failure string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.failures = append(k.failures, failure)
}

func (k *Kit) failureSummary() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.failures) == 0 {
		return ""
	}
	return "; " + strings.Join(k.failures, "; ")
}

func (k *Kit) verify() {
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	k.mu.Lock()
	fakes := slices.Clone(k.fakes)
	k.mu.Unlock()
	for _, f := range fakes {
		select {
		case <-f.peer.done:
		case <-ctx.Done():
			k.fail(fmt.Sprintf("fake %s %s (pid %d) was still running after the daemon stopped", f.Harness, f.Role, f.Pid))
			_ = syscall.Kill(f.Pid, syscall.SIGKILL)
		}
	}
	_ = k.control.Close()
	for _, f := range fakes {
		f.peer.close()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, failure := range k.failures {
		k.t.Errorf("fakeagent: %s", failure)
	}
}
