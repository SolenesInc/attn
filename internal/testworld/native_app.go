package testworld

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nativeAppTestPrefix = "TestTheNativeApp"

// NativeApp is the native app with no window, against this stack's daemon, driven over its
// automation protocol.
type NativeApp struct {
	t       testing.TB
	port    int
	token   string
	running *Running
}

// StartNativeApp runs `attn-app --automation-headless` against the running daemon and returns
// once its automation server listens. The binary comes from ATTN_NATIVE_APP_BIN, which
// scripts/test-go.sh sets for a run that selects a test named TestTheNativeApp…, the prefix
// it selects by. CI's Daemon jobs have no Rust for the native workspace, so there the test
// skips and the Native job runs it.
func (s *Stack) StartNativeApp() *NativeApp {
	s.T.Helper()
	if !strings.HasPrefix(s.T.Name(), nativeAppTestPrefix) {
		s.T.Fatalf("%s starts the native app, so its name must start with %s: scripts/test-go.sh builds attn-app only for those tests", s.T.Name(), nativeAppTestPrefix)
	}
	binary := os.Getenv("ATTN_NATIVE_APP_BIN")
	if binary == "" && os.Getenv("CI") != "" {
		s.T.Skip("CI's Native job runs native app stack tests, with ATTN_NATIVE_APP_BIN")
	}
	if binary == "" {
		s.T.Fatal("ATTN_NATIVE_APP_BIN is unset: run native app tests through scripts/test-go.sh, which provides attn-app")
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
