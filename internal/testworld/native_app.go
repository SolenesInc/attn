package testworld

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var nativeAppBinary = sync.OnceValues(func() (string, error) {
	if binary := os.Getenv("ATTN_NATIVE_APP_BIN"); binary != "" {
		return binary, nil
	}
	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("find the repository to build attn-app: %w", err)
	}
	workspace := filepath.Join(strings.TrimSpace(string(root)), "native")
	build := exec.Command("cargo", "build", "--locked", "-p", "attn-app")
	build.Dir = workspace
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build attn-app: %w\n%s", err, output)
	}
	target := os.Getenv("CARGO_TARGET_DIR")
	if target == "" {
		target = filepath.Join(workspace, "target")
	}
	return filepath.Join(target, "debug", "attn-app"), nil
})

// NativeApp is the native app with no window, against this stack's daemon, driven over its
// automation protocol.
type NativeApp struct {
	t       testing.TB
	port    int
	token   string
	running *Running
}

// StartNativeApp runs `attn-app --automation-headless` against the running daemon and returns
// once its automation server listens. ATTN_NATIVE_APP_BIN names a prebuilt binary; without
// it the test builds the native workspace's debug attn-app. CI's Daemon jobs have no Rust
// for the native workspace, so there the test skips and the Native job runs it.
func (s *Stack) StartNativeApp() *NativeApp {
	s.T.Helper()
	if os.Getenv("ATTN_NATIVE_APP_BIN") == "" && os.Getenv("CI") != "" {
		s.T.Skip("CI's Native job runs native app stack tests, with ATTN_NATIVE_APP_BIN")
	}
	binary, err := nativeAppBinary()
	if err != nil {
		s.T.Fatal(err)
	}
	manifest := filepath.Join(s.Dir, "native-app", "ui-automation.json")
	running := s.Launch(Invocation{Binary: binary, Args: []string{"--automation-headless", "--manifest", manifest}})
	running.AwaitStdout("ready port=")
	data, err := os.ReadFile(manifest)
	if err != nil {
		s.T.Fatal(err)
	}
	var m struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		s.T.Fatalf("read the native app's manifest %s: %v", manifest, err)
	}
	return &NativeApp{t: s.T, port: m.Port, token: m.Token, running: running}
}

// Do sends one automation request and returns its result, or the app's error.
func (a *NativeApp) Do(action string, payload any) (json.RawMessage, error) {
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", a.port))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	request, err := json.Marshal(map[string]any{"id": action, "token": a.token, "action": action, "payload": payload})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("%s: read the reply: %w", action, err)
	}
	var reply struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		return nil, fmt.Errorf("%s: decode %q: %w", action, line, err)
	}
	if !reply.OK {
		return nil, fmt.Errorf("%s: %s", action, reply.Error)
	}
	return reply.Result, nil
}

// Must is Do for a request the test cannot go on without; it decodes the result into out
// when out is not nil.
func (a *NativeApp) Must(action string, payload any, out any) {
	a.t.Helper()
	result, err := a.Do(action, payload)
	if err != nil {
		stdout, stderr := a.running.Output()
		a.t.Fatalf("native app: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if out != nil {
		if err := json.Unmarshal(result, out); err != nil {
			a.t.Fatalf("native app: decode %s's %s: %v", action, result, err)
		}
	}
}
