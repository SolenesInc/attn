package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/toolhome"
)

type fakeReloadBackend struct {
	mu        sync.Mutex
	liveIDs   []string
	info      ptybackend.SessionInfo
	params    ptybackend.SessionLaunchParams
	paramsErr error
	spawnErr  error
	calls     []string
	spawnOpts []ptybackend.SpawnOptions
	spawnGate *rendezvous
	onKill    func(string)
}

type rendezvous struct {
	want    int
	timeout time.Duration
	mu      sync.Mutex
	count   int
	release chan struct{}
}

func newRendezvous(want int, timeout time.Duration) *rendezvous {
	return &rendezvous{want: want, timeout: timeout, release: make(chan struct{})}
}

func (r *rendezvous) arrive() {
	r.mu.Lock()
	r.count++
	if r.count >= r.want {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	select {
	case <-r.release:
	case <-time.After(r.timeout):
	}
}

func (b *fakeReloadBackend) Spawn(_ context.Context, opts ptybackend.SpawnOptions) error {
	if b.spawnGate != nil {
		b.spawnGate.arrive()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, "spawn:"+opts.ID)
	if b.spawnErr != nil {
		return b.spawnErr
	}
	for _, id := range b.liveIDs {
		if id == opts.ID {
			return fmt.Errorf("session %s already exists", opts.ID)
		}
	}
	b.liveIDs = append(b.liveIDs, opts.ID)
	b.spawnOpts = append(b.spawnOpts, opts)
	return nil
}
func (b *fakeReloadBackend) Attach(context.Context, string, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{Running: true}, newFakeOutputStream(), nil
}
func (b *fakeReloadBackend) Input(context.Context, string, []byte) error { return nil }
func (b *fakeReloadBackend) Resize(context.Context, string, uint16, uint16, uint16, uint16) (ptybackend.ResizeResult, error) {
	return ptybackend.ResizeResult{Changed: true}, nil
}
func (b *fakeReloadBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *fakeReloadBackend) Kill(_ context.Context, id string, _ syscall.Signal) error {
	if b.onKill != nil {
		b.onKill(id)
	}
	b.mu.Lock()
	b.calls = append(b.calls, "kill:"+id)
	b.liveIDs = removeReloadID(b.liveIDs, id)
	b.mu.Unlock()
	return nil
}
func (b *fakeReloadBackend) Remove(_ context.Context, id string) error {
	b.mu.Lock()
	b.calls = append(b.calls, "remove:"+id)
	b.liveIDs = removeReloadID(b.liveIDs, id)
	b.mu.Unlock()
	return nil
}

func removeReloadID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
}
func (b *fakeReloadBackend) SessionIDs(context.Context) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.liveIDs...)
}
func (b *fakeReloadBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *fakeReloadBackend) Shutdown(context.Context) error { return nil }
func (b *fakeReloadBackend) SessionInfo(context.Context, string) (ptybackend.SessionInfo, error) {
	return b.info, nil
}
func (b *fakeReloadBackend) SessionLaunchParams(context.Context, string) (ptybackend.SessionLaunchParams, error) {
	return b.params, b.paramsErr
}

func (b *fakeReloadBackend) callOrder() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}
func (b *fakeReloadBackend) lastSpawn() (ptybackend.SpawnOptions, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.spawnOpts) == 0 {
		return ptybackend.SpawnOptions{}, false
	}
	return b.spawnOpts[len(b.spawnOpts)-1], true
}
func (b *fakeReloadBackend) spawnCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.spawnOpts)
}
func newReloadTestDaemon(t *testing.T, backend *fakeReloadBackend) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = backend
	return d
}

func addReloadSession(d *Daemon, id string, agent protocol.SessionAgent, state protocol.SessionState) {
	addReloadSessionAt(d, id, agent, state, "/tmp/"+id)
}

func addReloadSessionAt(d *Daemon, id string, agent protocol.SessionAgent, state protocol.SessionState, directory string) {
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: agent, Directory: directory,
		WorkspaceID: "ws-" + id, State: state, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func TestReloadSessionAgentAbortsWhenLaunchParamsNotRecorded(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"chief"},
		info:    ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:  ptybackend.SessionLaunchParams{Recorded: false, YoloMode: true},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.reloadSessionAgent("chief")

	if order := backend.callOrder(); len(order) != 0 {
		t.Fatalf("expected no kill/remove/spawn when launch params unrecorded, got %v", order)
	}
	if d.consumeReloading("chief") {
		t.Fatal("reloading flag must not be set when the reload aborts")
	}
}

func TestBuildReloadSpawnOptionsCarriesContextWindowCap(t *testing.T) {
	t.Setenv(toolhome.EnvVar, t.TempDir())

	newDaemonWithSession := func(t *testing.T, sessionID string) *Daemon {
		t.Helper()
		backend := &fakeReloadBackend{params: ptybackend.SessionLaunchParams{Recorded: true}}
		d := newReloadTestDaemon(t, backend)
		addReloadSession(d, sessionID, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		return d
	}

	t.Run("reloaded chief keeps the configured cap", func(t *testing.T) {
		d := newDaemonWithSession(t, "chief")
		if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "chief"); err != nil {
			t.Fatalf("assign chief role: %v", err)
		}
		d.store.SetSetting(SettingChiefContextWindowCap, "160000")

		opts, err := d.buildReloadSpawnOptions(d.store.Get("chief"))
		if err != nil {
			t.Fatalf("buildReloadSpawnOptions: %v", err)
		}
		if opts.ContextWindowCap != 160000 {
			t.Fatalf("ContextWindowCap = %d, want 160000 (reloaded chief must stay capped)", opts.ContextWindowCap)
		}
	})

	t.Run("reloaded chief with no configured cap falls back to the default", func(t *testing.T) {
		d := newDaemonWithSession(t, "chief")
		if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "chief"); err != nil {
			t.Fatalf("assign chief role: %v", err)
		}

		opts, err := d.buildReloadSpawnOptions(d.store.Get("chief"))
		if err != nil {
			t.Fatalf("buildReloadSpawnOptions: %v", err)
		}
		if opts.ContextWindowCap != agentdriver.DefaultContextWindowCap {
			t.Fatalf("ContextWindowCap = %d, want default %d", opts.ContextWindowCap, agentdriver.DefaultContextWindowCap)
		}
	})

	t.Run("reloaded non-chief session stays uncapped even with a chief cap configured", func(t *testing.T) {
		d := newDaemonWithSession(t, "worker")
		d.store.SetSetting(SettingChiefContextWindowCap, "160000")

		opts, err := d.buildReloadSpawnOptions(d.store.Get("worker"))
		if err != nil {
			t.Fatalf("buildReloadSpawnOptions: %v", err)
		}
		if opts.ContextWindowCap != 0 {
			t.Fatalf("ContextWindowCap = %d, want 0 (non-chief reload must stay uncapped)", opts.ContextWindowCap)
		}
	})

	t.Run("reloaded non-chief session keeps its agent's default cap", func(t *testing.T) {
		d := newDaemonWithSession(t, "worker")
		d.store.SetSetting(SettingDefaultContextWindowCapPrefix+"claude", "800000")

		opts, err := d.buildReloadSpawnOptions(d.store.Get("worker"))
		if err != nil {
			t.Fatalf("buildReloadSpawnOptions: %v", err)
		}
		if opts.ContextWindowCap != 800000 {
			t.Fatalf("ContextWindowCap = %d, want 800000 (reloaded session must stay capped)", opts.ContextWindowCap)
		}
	})

	t.Run("reloaded session keeps its own pin over every setting", func(t *testing.T) {
		d := newDaemonWithSession(t, "worker")
		d.store.SetSetting(SettingDefaultContextWindowCapPrefix+"claude", "800000")
		if !d.store.SetSessionContextWindowCap("worker", 300000) {
			t.Fatalf("SetSessionContextWindowCap failed")
		}

		opts, err := d.buildReloadSpawnOptions(d.store.Get("worker"))
		if err != nil {
			t.Fatalf("buildReloadSpawnOptions: %v", err)
		}
		if opts.ContextWindowCap != 300000 {
			t.Fatalf("ContextWindowCap = %d, want the 300000 pin (a reload is how the pin takes effect)", opts.ContextWindowCap)
		}
	})
}

func TestReloadSessionAgentRecomposesPluginChiefInstructionsBeforeKill(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"plugin-chief"},
		info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
		params: ptybackend.SessionLaunchParams{
			Recorded: true,
			YoloMode: true,
			Model:    "provider/model",
			Effort:   "high",
		},
	}
	d := newReloadTestDaemon(t, backend)
	addTestWorkspace(d, "ws-plugin-chief", t.TempDir())
	addReloadSession(d, "plugin-chief", protocol.SessionAgent("example"), protocol.SessionStateIdle)
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "plugin-chief"); err != nil {
		t.Fatalf("assign chief role: %v", err)
	}
	if !d.store.BeginAgentDriverRun("plugin-chief", "example-plugin", "run-old") {
		t.Fatal("begin old plugin run")
	}
	if !d.store.ApplyAgentDriverMetadata("plugin-chief", "run-old", 1, `{"native_id":"same-session"}`) {
		t.Fatal("seed plugin metadata")
	}
	plugin, done := startPluginPipe(t, d, "example-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "example", map[string]bool{
		"resume": true, "yolo": true, "model_pin": true, "effort_pin": true, "launch_instructions": true,
	})
	closed := make(chan pluginDriverSessionClosedParams, 1)
	go func() {
		request := decodeJSONRPCMessage(t, plugin)
		if request.Method != "driver.resume" {
			t.Errorf("method=%q, want driver.resume", request.Method)
			return
		}
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode resume params: %v", err)
			return
		}
		if params.Instructions == nil || params.Instructions.Kind != pluginInstructionKindChief || !strings.Contains(params.Instructions.Content, "You are the chief of staff") {
			t.Errorf("resume instructions=%+v, want current chief guidance", params.Instructions)
			return
		}
		if params.Model != "provider/model" || params.Effort != "high" || !params.Yolo || string(params.Metadata) != `{"native_id":"same-session"}` {
			t.Errorf("resume params=%+v, want preserved flags and metadata", params)
			return
		}
		respondPluginRequest(t, plugin, request, pluginDriverSpawnResult{Argv: []string{"example-launcher"}})
		request = decodeJSONRPCMessage(t, plugin)
		var closeParams pluginDriverSessionClosedParams
		if err := json.Unmarshal(request.Params, &closeParams); err != nil {
			t.Errorf("decode session_closed params: %v", err)
			return
		}
		respondPluginRequest(t, plugin, request, pluginDriverSessionClosedResult{OK: true})
		closed <- closeParams
	}()

	d.reloadSessionAgent("plugin-chief")

	if order := backend.callOrder(); !reflect.DeepEqual(order, []string{"kill:plugin-chief", "remove:plugin-chief", "spawn:plugin-chief"}) {
		t.Fatalf("orchestration order=%v", order)
	}
	spawn, ok := backend.lastSpawn()
	if !ok || !reflect.DeepEqual(spawn.ExternalCommand, []string{"example-launcher"}) || spawn.LifecycleID == "" {
		t.Fatalf("plugin respawn=%+v", spawn)
	}
	if active := d.store.GetAgentDriverRun("plugin-chief"); active.RunID != spawn.LifecycleID || active.PluginName != "example-plugin" {
		t.Fatalf("active plugin run=%+v, want replacement", active)
	}
	select {
	case params := <-closed:
		if params.RunID != "run-old" || params.Reason != "reloaded" {
			t.Fatalf("closed old run=%+v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("old plugin run was not closed after replacement")
	}
}

func TestReloadSessionAgentLeavesPluginWorkerAliveWhenResumeCannotBePrepared(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"plugin-chief"},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addTestWorkspace(d, "ws-plugin-chief", t.TempDir())
	addReloadSession(d, "plugin-chief", protocol.SessionAgent("example"), protocol.SessionStateIdle)
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "plugin-chief"); err != nil {
		t.Fatalf("assign chief role: %v", err)
	}
	plugin, done := startPluginPipe(t, d, "example-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "example", map[string]bool{"resume": true, "launch_instructions": true})
	closed := make(chan struct{})
	go func() {
		request := decodeJSONRPCMessage(t, plugin)
		_ = json.NewEncoder(plugin).Encode(jsonRPCMessage{
			JSONRPC: "2.0",
			ID:      request.ID,
			Error:   &jsonRPCError{Code: jsonRPCInternalError, Message: "native resume unavailable"},
		})
		request = decodeJSONRPCMessage(t, plugin)
		respondPluginRequest(t, plugin, request, pluginDriverSessionClosedResult{OK: true})
		close(closed)
	}()

	d.reloadSessionAgent("plugin-chief")

	if order := backend.callOrder(); len(order) != 0 {
		t.Fatalf("failed preflight touched live worker: %v", order)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("failed replacement run was not cleaned up")
	}
}

func TestSetChiefOfStaffRejectsPluginRoleChangeWhenResumePreflightFails(t *testing.T) {
	for _, test := range []struct {
		name         string
		promote      bool
		initialChief string
		wantChief    string
		wantKind     string
	}{
		{name: "promotion", promote: true, wantKind: pluginInstructionKindChief},
		{name: "demotion", promote: false, initialChief: "plugin-chief", wantChief: "plugin-chief", wantKind: pluginInstructionKindAgent},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeReloadBackend{
				liveIDs: []string{"plugin-chief"},
				info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
				params:  ptybackend.SessionLaunchParams{Recorded: true},
			}
			d := newReloadTestDaemon(t, backend)
			addTestWorkspace(d, "ws-plugin-chief", t.TempDir())
			addReloadSession(d, "plugin-chief", protocol.SessionAgent("example"), protocol.SessionStateIdle)
			d.store.SetSetting(SettingNotebookRoot, t.TempDir())
			if test.initialChief != "" {
				if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, test.initialChief); err != nil {
					t.Fatalf("seed chief role: %v", err)
				}
			}
			if !d.store.BeginAgentDriverRun("plugin-chief", "example-plugin", "run-live") {
				t.Fatal("begin live plugin run")
			}

			plugin, done := startPluginPipe(t, d, "example-plugin", nil)
			defer func() {
				_ = plugin.Close()
				<-done
			}()
			registerTestPluginDriver(t, plugin, "example", map[string]bool{"resume": true, "launch_instructions": true})
			closed := make(chan struct{})
			go func() {
				request := decodeJSONRPCMessage(t, plugin)
				if request.Method != "driver.resume" {
					t.Errorf("method=%q, want driver.resume", request.Method)
					return
				}
				var params pluginDriverSpawnParams
				if err := json.Unmarshal(request.Params, &params); err != nil {
					t.Errorf("decode resume params: %v", err)
					return
				}
				if params.Instructions == nil || params.Instructions.Kind != test.wantKind {
					t.Errorf("instructions=%+v, want kind %q", params.Instructions, test.wantKind)
					return
				}
				_ = json.NewEncoder(plugin).Encode(jsonRPCMessage{
					JSONRPC: "2.0",
					ID:      request.ID,
					Error:   &jsonRPCError{Code: jsonRPCInternalError, Message: "native resume unavailable"},
				})
				request = decodeJSONRPCMessage(t, plugin)
				respondPluginRequest(t, plugin, request, pluginDriverSessionClosedResult{OK: true})
				close(closed)
			}()

			client := newRenameTestClient()
			d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
				Cmd: protocol.CmdSetChiefOfStaff, SessionID: "plugin-chief", ChiefOfStaff: test.promote,
			})
			result := readChiefOfStaffResult(t, client)
			if result.Success || !strings.Contains(protocol.Deref(result.Error), "native resume unavailable") {
				t.Fatalf("result=%+v, want resume preflight failure", result)
			}
			if got := d.chiefOfStaffSessionID(); got != test.wantChief {
				t.Fatalf("persisted chief role=%q, want %q", got, test.wantChief)
			}
			if order := backend.callOrder(); len(order) != 0 {
				t.Fatalf("failed preflight touched live worker: %v", order)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("failed prepared run was not cleaned up")
			}
		})
	}
}

func TestReloadSessionAgentRespawnFailureBroadcastsSessionExited(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs:  []string{"chief"},
		info:     ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:   ptybackend.SessionLaunchParams{Recorded: true},
		spawnErr: fmt.Errorf("boom"),
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateWorking)

	var exited bool
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) {
		if e != nil && e.Event == protocol.EventSessionExited && protocol.Deref(e.ID) == "chief" {
			exited = true
		}
	}

	d.reloadSessionAgent("chief")

	if !exited {
		t.Fatal("a failed respawn must broadcast session_exited (dead-pane fallback)")
	}
	if d.consumeReloading("chief") {
		t.Fatal("reloading flag must be cleared after a failed respawn")
	}
}

func TestReloadSessionAgentSerializesConcurrentReloads(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs:   []string{"chief"},
		info:      ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:    ptybackend.SessionLaunchParams{Recorded: true},
		spawnGate: newRendezvous(2, 500*time.Millisecond),
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateWorking)

	var exited int
	var exitedMu sync.Mutex
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) {
		if e != nil && e.Event == protocol.EventSessionExited && protocol.Deref(e.ID) == "chief" {
			exitedMu.Lock()
			exited++
			exitedMu.Unlock()
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.reloadSessionAgent("chief")
		}()
	}
	wg.Wait()

	exitedMu.Lock()
	gotExited := exited
	exitedMu.Unlock()
	if gotExited != 0 {
		t.Fatalf("concurrent reloads broadcast %d session_exited; want 0 (no tear-down)", gotExited)
	}
	if live := backend.SessionIDs(context.Background()); len(live) != 1 || live[0] != "chief" {
		t.Fatalf("live workers after concurrent reloads = %v, want exactly [chief]", live)
	}
	if backend.spawnCount() != 2 {
		t.Fatalf("spawn count = %d, want 2 (both reloads respawned, serialized)", backend.spawnCount())
	}
}

func laneIsClosed(d *Daemon, sessionID string) bool {
	lane := laneFor(d, sessionID)
	if lane == nil {
		return true
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	return lane.stopped
}

func TestReloadFencesTheInputLaneBeforeTheRuntimeIsReplaced(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"crew"},
		info:    ptybackend.SessionInfo{Cols: 120, Rows: 40},
		params:  ptybackend.SessionLaunchParams{Recorded: true, Executable: "/custom/claude"},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "crew", protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)
	if err := d.writeSessionPTY("crew", []byte("half written"), "user"); err != nil {
		t.Fatalf("user input: %v", err)
	}
	delivery := maintenanceSessionInput("crew-sleep", "crew", "crew", crewSleepPrompt, sessionInputAtTurnBoundary)
	delivery.resend = func() {}
	if attempt := d.sessionInputs().try(context.Background(), delivery); !sessionInputQuietDeferral(attempt.err) {
		t.Fatalf("the sleep ask into a typed-in composer = %v, want the quiet-window deferral", attempt.err)
	}
	if retryEntry(d, "crew", delivery.id) == nil {
		t.Fatal("the deferred sleep ask armed no retry")
	}

	fencedAtKill := false
	backend.onKill = func(id string) { fencedAtKill = laneIsClosed(d, id) }
	d.reloadSessionAgent("crew")

	if !fencedAtKill {
		t.Fatal("the lane was still open when the reload killed the runtime")
	}
	if entry := retryEntry(d, "crew", delivery.id); entry != nil {
		t.Fatal("a retry from the old runtime survived the replacement")
	}
}

func TestReloadSessionForClientResumesPluginWithoutLaunchInstructions(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"pi-session"},
		info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
		params:  ptybackend.SessionLaunchParams{Recorded: true, Model: "provider/model"},
	}
	d := newReloadTestDaemon(t, backend)
	addTestWorkspace(d, "ws-pi-session", t.TempDir())
	addReloadSession(d, "pi-session", protocol.SessionAgent("pi"), protocol.SessionStateIdle)
	if !d.store.BeginAgentDriverRun("pi-session", "pi-plugin", "run-old") {
		t.Fatal("begin old plugin run")
	}
	if !d.store.ApplyAgentDriverMetadata("pi-session", "run-old", 1, `{"pi_session_id":"conv-1"}`) {
		t.Fatal("seed plugin metadata")
	}
	plugin, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "pi", map[string]bool{"resume": true, "model_pin": true})
	closed := make(chan pluginDriverSessionClosedParams, 1)
	go func() {
		request := decodeJSONRPCMessage(t, plugin)
		if request.Method != "driver.resume" {
			t.Errorf("method=%q, want driver.resume", request.Method)
			return
		}
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode resume params: %v", err)
			return
		}
		if params.Instructions != nil || params.Model != "provider/model" || string(params.Metadata) != `{"pi_session_id":"conv-1"}` {
			t.Errorf("resume params=%+v, want no instructions with preserved model and metadata", params)
			return
		}
		respondPluginRequest(t, plugin, request, pluginDriverSpawnResult{Argv: []string{"pi", "--session-id", "conv-1"}})
		request = decodeJSONRPCMessage(t, plugin)
		var closeParams pluginDriverSessionClosedParams
		if err := json.Unmarshal(request.Params, &closeParams); err != nil {
			t.Errorf("decode session_closed params: %v", err)
			return
		}
		respondPluginRequest(t, plugin, request, pluginDriverSessionClosedResult{OK: true})
		closed <- closeParams
	}()

	if err := d.reloadSessionForClient("pi-session", 0, 0); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if order := backend.callOrder(); !reflect.DeepEqual(order, []string{"kill:pi-session", "remove:pi-session", "spawn:pi-session"}) {
		t.Fatalf("orchestration order=%v", order)
	}
	spawn, ok := backend.lastSpawn()
	if !ok || !reflect.DeepEqual(spawn.ExternalCommand, []string{"pi", "--session-id", "conv-1"}) {
		t.Fatalf("plugin respawn=%+v", spawn)
	}
	select {
	case params := <-closed:
		if params.RunID != "run-old" || params.Reason != "reloaded" {
			t.Fatalf("closed old run=%+v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("old plugin run was not closed after replacement")
	}
}

func TestReloadSessionForClientRefusesPluginChiefWithoutLaunchInstructions(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"pi-chief"},
		info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addTestWorkspace(d, "ws-pi-chief", t.TempDir())
	addReloadSession(d, "pi-chief", protocol.SessionAgent("pi"), protocol.SessionStateIdle)
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "pi-chief"); err != nil {
		t.Fatalf("assign chief role: %v", err)
	}
	plugin, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "pi", map[string]bool{"resume": true})

	err := d.reloadSessionForClient("pi-chief", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "launch_instructions") {
		t.Fatalf("err=%v, want launch_instructions refusal", err)
	}
	if order := backend.callOrder(); len(order) != 0 {
		t.Fatalf("refused reload touched live worker: %v", order)
	}
}
