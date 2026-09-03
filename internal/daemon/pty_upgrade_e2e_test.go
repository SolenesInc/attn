package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
	"nhooyr.io/websocket"
)

// Run with scripts/test-pty-upgrade.sh: the legacy daemon is built from a pinned
// revision, so this exercises the old executable and registry format unchanged.
func TestPTYUpgradeAcrossDaemonBinaries(t *testing.T) {
	oldBinary, hostBinary := os.Getenv("ATTN_UPGRADE_OLD_BIN"), os.Getenv("ATTN_TEST_PTY_HOST")
	if oldBinary == "" || hostBinary == "" {
		t.Skip("run scripts/test-pty-upgrade.sh for the two-binary upgrade test")
	}
	root := shortTempDir(t)
	newBinary := attnBinaryForE2ETest(t, root)
	fixture := filepath.Join(root, "fixture-codex")
	script := `#!/bin/sh
set -eu
stty -echo
transcript="$ATTN_TOOL_HOME/.codex/sessions/native-$ATTN_SESSION_ID.jsonl"
mkdir -p "$ATTN_TOOL_HOME/.codex/sessions"
printf '{"type":"session_meta","payload":{"id":"native-%s","cwd":"%s"}}\n' "$ATTN_SESSION_ID" "$PWD" > "$transcript"
printf '{"session_id":"native-%s","transcript_path":"%s"}' "$ATTN_SESSION_ID" "$transcript" | "$ATTN_WRAPPER_PATH" _hook-session-start
while IFS= read -r line; do
  printf '__ACK_%s_%s_%s__\n' "$ATTN_SESSION_ID" "$line" "$$"
  previous=''; resume=''
  for arg in "$@"; do
    if [ "$previous" = resume ]; then resume="$arg"; fi
    previous="$arg"
  done
  printf '__RESUME_%s__\n' "$resume"
done
`
	if err := os.WriteFile(fixture, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "dscl"), []byte("#!/bin/sh\nprintf 'UserShell: /bin/sh\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var workers = make(map[int]bool)
	agentPIDs := make(map[string]int)
	t.Cleanup(func() {
		for pid := range workers {
			process, _ := os.FindProcess(pid)
			_ = process.Kill()
		}
	})
	start := func(binary, host string) *upgradeDaemon {
		d := startUpgradeDaemon(t, root, binary, host)
		d.workers = workers
		d.agentPIDs = agentPIDs
		return d
	}
	old := start(oldBinary, "")
	old.command(map[string]any{"cmd": "register_workspace", "id": "upgrade", "title": "upgrade", "directory": root}, "workspace_registered", "")
	old.spawn("legacy-shell", "shell", "")
	old.spawn("legacy-agent", "codex", fixture)
	old.spawn("promote-agent", "codex", fixture)
	identities := map[string]ptyworker.RegistryEntry{}
	for _, id := range []string{"legacy-shell", "legacy-agent", "promote-agent"} {
		identities[id] = old.identity(id, false)
		old.probe(id, identities[id].ChildPID, "old-daemon")
	}
	old.stop()

	current := start(newBinary, hostBinary)
	for _, id := range []string{"legacy-shell", "legacy-agent", "promote-agent"} {
		current.assertIdentity(id, false, identities[id])
		current.probe(id, identities[id].ChildPID, "new-daemon")
	}
	current.spawn("new-agent", "codex", fixture)
	identities["new-agent"] = current.identity("new-agent", true)
	current.probe("new-agent", identities["new-agent"].ChildPID, "rust-agent")
	current.command(map[string]any{"cmd": "reload_session", "id": "promote-agent", "cols": 80, "rows": 24}, "reload_session_result", "promote-agent")
	promoted := current.identity("promote-agent", true)
	if promoted.ChildPID == identities["promote-agent"].ChildPID || promoted.WorkerPID != identities["new-agent"].WorkerPID {
		t.Fatalf("reload did not replace only the selected child on the shared host: before=%+v after=%+v", identities["promote-agent"], promoted)
	}
	identities["promote-agent"] = promoted
	priorAgentPID := agentPIDs["promote-agent"]
	delete(agentPIDs, "promote-agent")
	output := current.probe("promote-agent", promoted.ChildPID, "promoted")
	if !strings.Contains(output, "__RESUME_native-promote-agent__") || agentPIDs["promote-agent"] == priorAgentPID {
		t.Fatalf("reload lost native conversation identity: %q", output)
	}
	assertAll := func(d *upgradeDaemon, phase string) {
		for _, id := range []string{"legacy-shell", "legacy-agent", "promote-agent", "new-agent"} {
			shared := id == "promote-agent" || id == "new-agent"
			d.assertIdentity(id, shared, identities[id])
			d.probe(id, identities[id].ChildPID, phase)
		}
	}
	assertAll(current, "after-reload")
	current.stop()

	current = start(newBinary, hostBinary)
	assertAll(current, "mixed-restart")
	current.stop()

	// A wrapper changes the host binary identity while running the same protocol.
	// Real upgrades use the new executable's hash to make this same decision.
	nextHost := filepath.Join(root, "next-pty-host")
	if err := os.WriteFile(nextHost, []byte("#!/bin/sh\nexec '"+strings.ReplaceAll(hostBinary, "'", "'\\''")+"' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	current = start(newBinary, nextHost)
	assertAll(current, "host-upgrade")
	current.spawn("next-agent", "codex", fixture)
	next := current.identity("next-agent", true)
	if next.WorkerPID == identities["new-agent"].WorkerPID {
		t.Fatal("new host generation reused the old host")
	}
	current.probe("next-agent", next.ChildPID, "next-host")
	assertAll(current, "all-generations-alive")
	current.command(map[string]any{"cmd": "pty_resize", "id": "legacy-shell", "cols": 97, "rows": 31}, "pty_resized", "legacy-shell")
	current.probe("legacy-shell", identities["legacy-shell"].ChildPID, "resized")
	for _, id := range []string{"legacy-shell", "legacy-agent", "promote-agent", "new-agent", "next-agent"} {
		current.command(map[string]any{"cmd": "workspace_layout_close_pane", "workspace_id": "upgrade", "pane_id": "pane-" + id}, "workspace_layout_action_result", "")
	}
	current.stop()
}

type upgradeDaemon struct {
	t          *testing.T
	root       string
	instanceID string
	cmd        *exec.Cmd
	done       chan error
	ws         *websocket.Conn
	workers    map[int]bool
	agentPIDs  map[string]int
	stopped    bool
}

func startUpgradeDaemon(t *testing.T, root, binary, host string) *upgradeDaemon {
	t.Helper()
	port, err := freeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err := watcher.Add(root); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "daemon.log")
	var offset int64
	if info, err := os.Stat(logPath); err == nil {
		offset = info.Size()
	}
	cmd := exec.Command(binary, "daemon")
	cmd.Dir = root
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if !strings.HasPrefix(key, "ATTN_") && key != "DEBUG" && key != "PATH" && key != "CODEX_HOME" {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "ATTN_DATA_DIR="+root, "ATTN_WS_PORT="+strconv.Itoa(port), "ATTN_HEADLESS_TASKS=0", "ATTN_WRAPPER_PATH="+binary, "SHELL=/bin/sh", "DEBUG=debug")
	cmd.Env = append(cmd.Env, "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, "ATTN_TOOL_HOME="+filepath.Join(root, "tools"))
	if host != "" {
		cmd.Env = append(cmd.Env, "ATTN_PTY_HOST_BINARY="+host)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &upgradeDaemon{t: t, root: root, cmd: cmd, done: make(chan error, 1)}
	go func() { d.done <- cmd.Wait() }()
	t.Cleanup(d.stop)
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		data, _ := os.ReadFile(logPath)
		if int64(len(data)) >= offset && bytes.Contains(data[offset:], []byte("WebSocket server starting")) {
			break
		}
		select {
		case <-watcher.Events:
		case err := <-watcher.Errors:
			if !os.IsNotExist(err) {
				t.Fatalf("daemon log watch: %v", err)
			}
		case err := <-d.done:
			d.stopped = true
			t.Fatalf("daemon exited before readiness: %v\n%s\n%s", err, stderr.String(), data)
		case <-deadline.C:
			t.Fatalf("daemon readiness timeout\n%s", data)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d.ws, _, err = websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", port), nil)
	if err != nil {
		t.Fatal(err)
	}
	d.ws.SetReadLimit(16 << 20)
	token, err := os.ReadFile(filepath.Join(root, "client-token"))
	if err != nil {
		t.Fatal(err)
	}
	d.write(map[string]any{"cmd": "client_hello", "client_kind": "pty-upgrade-test", "version": "test", "capabilities": []string{"workspace_sessions", "binary_pty_output"}, "client_token": strings.TrimSpace(string(token))})
	initial := d.event("initial_state", "")
	d.instanceID, _ = initial["daemon_instance_id"].(string)
	if d.instanceID == "" {
		t.Fatal("initial_state has no daemon instance identity")
	}
	t.Logf("daemon binary=%s pid=%d ready instance=%s", binary, cmd.Process.Pid, d.instanceID)
	return d
}

func (d *upgradeDaemon) stop() {
	if d.stopped {
		return
	}
	d.stopped = true
	if d.ws != nil {
		_ = d.ws.CloseNow()
	}
	_ = d.cmd.Process.Kill()
	select {
	case <-d.done:
	case <-time.After(10 * time.Second):
		d.t.Error("test daemon did not exit")
	}
}

func (d *upgradeDaemon) write(value any) {
	d.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		d.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.ws.Write(ctx, websocket.MessageText, data); err != nil {
		d.t.Fatal(err)
	}
}

func (d *upgradeDaemon) read(ctx context.Context) (map[string]any, string, []byte) {
	d.t.Helper()
	kind, data, err := d.ws.Read(ctx)
	if err != nil {
		log, _ := os.ReadFile(filepath.Join(d.root, "daemon.log"))
		d.t.Fatalf("read daemon event: %v\n%s", err, log)
	}
	if kind == websocket.MessageBinary && len(data) > 0 && data[0] == protocol.BinaryFrameTypePtyOutput {
		id, _, output, err := protocol.DecodePtyOutputFrame(data)
		if err != nil {
			d.t.Fatal(err)
		}
		return nil, id, output
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		d.t.Fatalf("invalid event: %v: %q", err, data)
	}
	if event["event"] == "pty_output" {
		output, _ := base64.StdEncoding.DecodeString(asString(event["data"]))
		return event, asString(event["id"]), output
	}
	return event, "", nil
}

func (d *upgradeDaemon) event(name, id string) map[string]any {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		event, _, _ := d.read(ctx)
		if event["event"] == name && (id == "" || event["id"] == id) {
			if success, ok := event["success"].(bool); ok && !success {
				d.t.Fatalf("%s failed: %v", name, event)
			}
			return event
		}
		if event["event"] == "error" {
			d.t.Fatalf("daemon command failed: %v", event)
		}
	}
}

func (d *upgradeDaemon) command(value any, event, id string) map[string]any {
	d.t.Helper()
	d.write(value)
	return d.event(event, id)
}

func (d *upgradeDaemon) spawn(id, agent, executable string) {
	d.t.Helper()
	d.command(map[string]any{"cmd": "workspace_layout_add_session_pane", "workspace_id": "upgrade", "session_id": id, "pane_id": "pane-" + id}, "workspace_layout_action_result", "")
	d.command(map[string]any{"cmd": "spawn_session", "id": id, "workspace_id": "upgrade", "cwd": d.root, "agent": agent, "codex_executable": executable, "cols": 80, "rows": 24}, "spawn_result", id)
	for _, shared := range []bool{false, true} {
		path := filepath.Join(d.root, "workers", d.instanceID, "registry", id+".json")
		if shared {
			path = ptyhost.SessionRegistryPath(d.root, d.instanceID, id)
		}
		if entry, err := ptyworker.ReadRegistry(path); err == nil {
			d.workers[entry.WorkerPID] = true
		}
	}
}

func (d *upgradeDaemon) identity(id string, shared bool) ptyworker.RegistryEntry {
	d.t.Helper()
	path := filepath.Join(d.root, "workers", d.instanceID, "registry", id+".json")
	if shared {
		path = ptyhost.SessionRegistryPath(d.root, d.instanceID, id)
	}
	entry, err := ptyworker.ReadRegistry(path)
	if err != nil {
		d.t.Fatalf("%s shared=%t registry: %v", id, shared, err)
	}
	if entry.ChildPID <= 0 || entry.WorkerPID <= 0 {
		d.t.Fatalf("invalid process identity: %+v", entry)
	}
	d.workers[entry.WorkerPID] = true
	d.t.Logf("%s shared=%t worker=%d child=%d", id, shared, entry.WorkerPID, entry.ChildPID)
	return entry
}

func (d *upgradeDaemon) assertIdentity(id string, shared bool, want ptyworker.RegistryEntry) {
	d.t.Helper()
	got := d.identity(id, shared)
	if got.ChildPID != want.ChildPID || got.WorkerPID != want.WorkerPID || got.DaemonInstanceID != want.DaemonInstanceID {
		d.t.Fatalf("surviving session %s moved: got=%+v want=%+v", id, got, want)
	}
}

func (d *upgradeDaemon) probe(id string, pid int, phase string) string {
	d.t.Helper()
	attached := d.command(map[string]any{"cmd": "attach_session", "id": id, "attach_policy": "same_app_remount"}, "attach_result", id)
	if attached["running"] != true || int(attached["pid"].(float64)) != pid {
		d.t.Fatalf("%s attached to the wrong process: %v", id, attached)
	}
	input := phase + "\n"
	want := fmt.Sprintf("__ACK_%d_%s__", pid, phase)
	if id != "legacy-shell" {
		want = "__ACK_" + id + "_" + phase + "_"
	}
	if id == "legacy-shell" {
		input = fmt.Sprintf("stty -echo; printf '__ACK_%%s_%%s__\\n' '%d' '%s'\n", pid, phase)
	}
	d.write(map[string]any{"cmd": "pty_input", "id": id, "data": input})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output strings.Builder
	ack := regexp.MustCompile(regexp.QuoteMeta(want) + `([0-9]+)__`)
	resume := regexp.MustCompile(`__RESUME_[^\r\n]*__\r?\n`)
	complete := func() bool {
		if id == "legacy-shell" {
			return strings.Contains(output.String(), want)
		}
		return ack.MatchString(output.String()) && resume.MatchString(output.String())
	}
	for !complete() {
		_, outputID, data := d.read(ctx)
		if outputID == id {
			output.Write(data)
		}
	}
	if id != "legacy-shell" {
		match := ack.FindStringSubmatch(output.String())
		if len(match) != 2 {
			d.t.Fatalf("missing agent PID acknowledgement: %q", output.String())
		}
		agentPID, _ := strconv.Atoi(match[1])
		if previous := d.agentPIDs[id]; previous != 0 && previous != agentPID {
			d.t.Fatalf("agent %s unexpectedly restarted: pid %d -> %d", id, previous, agentPID)
		}
		d.agentPIDs[id] = agentPID
	}
	d.t.Logf("%s acknowledged %s with original child %d", id, phase, pid)
	return output.String()
}
