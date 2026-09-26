package daemon

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func crewHandoffCall(t *testing.T, d *Daemon, sessionID, note string) protocol.Response {
	t.Helper()
	msg := protocol.CrewHandoffMessage{Cmd: protocol.CmdCrewHandoff, SessionID: sessionID, Note: note}
	return gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
}

func crewHandoffRetryCall(t *testing.T, d *Daemon, sessionID string) protocol.Response {
	t.Helper()
	msg := protocol.CrewHandoffMessage{Cmd: protocol.CmdCrewHandoff, SessionID: sessionID, Retry: protocol.Ptr(true)}
	return gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
}

func TestCrewHandoff_ARetryTurnsTheDayOverEvenWithTheUserAway(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	backend.mu.Lock()
	backend.spawnErr = errors.New("no room to launch")
	backend.mu.Unlock()

	first := crewHandoffCall(t, d, woken.SessionID, "The letter, written once.")
	if !first.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(first.Error))
	}
	if protocol.Deref(first.CrewHandoffResult.NapError) == "" {
		t.Fatal("the nap was supposed to fail")
	}

	backend.mu.Lock()
	backend.spawnErr = nil
	backend.mu.Unlock()
	setUserAway(d, time.Now().Add(-3*time.Hour))

	retried := crewHandoffRetryCall(t, d, woken.SessionID)
	if !retried.Ok {
		t.Fatalf("retry: %v", protocol.Deref(retried.Error))
	}
	result := retried.CrewHandoffResult
	if got := protocol.Deref(result.Outcome); got != protocol.CrewDayCloseNap {
		t.Fatalf("outcome = %q, want the turnover the retry asked for", got)
	}
	if successor := protocol.Deref(result.SessionID); successor == "" || successor == woken.SessionID {
		t.Fatalf("the successor's session is %q; the retry must start the next day", successor)
	}
}

func spawnedSessions(t *testing.T, backend *fakeSpawnBackend) []ptybackend.SpawnOptions {
	t.Helper()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]ptybackend.SpawnOptions(nil), backend.spawnOpts...)
}

func handoffFiles(t *testing.T, d *Daemon, member string) []string {
	t.Helper()
	dir := filepath.Join(d.dataRoot, crew.HomesDirName, member, crew.HandoffsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	crew.SortHandoffNames(names)
	return names
}

func TestCrewHandoff_TheSuccessorWakesOnTheMembersModel(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	if resp := crewSet(t, d, protocol.CrewSetMessage{Member: "trellis", Model: protocol.Ptr("claude-haiku-4-5")}); !resp.Ok {
		t.Fatalf("crew set: %v", protocol.Deref(resp.Error))
	}
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	resp := crewHandoffCall(t, d, woken.SessionID, "Filed and gone.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	spawns := spawnedSessions(t, backend)
	if len(spawns) != 2 {
		t.Fatalf("the nap spawned %d sessions in total, want 2", len(spawns))
	}
	if spawns[1].Model != "claude-haiku-4-5" {
		t.Fatalf("the successor woke on model %q, want the member's model", spawns[1].Model)
	}
}

func TestCrewHandoff_TheMemberIsNeverUnboundDuringTheNap(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	var atSpawn string
	backend.mu.Lock()
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == woken.SessionID {
			return
		}
		atSpawn = protocol.Deref(memberByID(t, crewList(t, d), "keel").BindingSession)
	}
	backend.mu.Unlock()

	resp := crewHandoffCall(t, d, woken.SessionID, "Filed and gone.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	successor := protocol.Deref(resp.CrewHandoffResult.SessionID)
	if atSpawn != successor {
		t.Fatalf("at the successor's spawn the binding was %q, want %q — the binding must move in one write, never release and re-claim", atSpawn, successor)
	}
}

func TestCrewHandoff_TheSuccessorKeepsApprovalButReturnsToTheMembersLaunchPins(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	intent, ok := d.store.LaunchIntent(woken.SessionID)
	if !ok {
		t.Fatal("the woken day recorded no launch intent")
	}
	intent.YoloMode = true
	intent.ApprovalRoute = launchcontract.ApprovalRouteBypass
	intent.Model = "opus"
	intent.Effort = "high"
	d.store.SetLaunchIntent(woken.SessionID, intent)

	resp := crewHandoffCall(t, d, woken.SessionID, "Carry it forward.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	spawns := spawnedSessions(t, backend)
	successor := spawns[len(spawns)-1]
	if successor.ApprovalRoute != launchcontract.ApprovalRouteBypass || !successor.YoloMode {
		t.Errorf("the successor launched route=%q yolo=%t; a nap must not silently change how a member runs", successor.ApprovalRoute, successor.YoloMode)
	}
	if successor.Effort != "" {
		t.Errorf("the successor carried the closed day's effort %q past the day boundary", successor.Effort)
	}
	if successor.Model != crewWakeFallbackModel {
		t.Errorf("the successor launched model=%q, want the fallback %q", successor.Model, crewWakeFallbackModel)
	}
}

func TestCrewHandoff_OneDayHarnessOverrideReturnsToSavedPinsWithoutExecutableLeakage(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.store.SetSetting(SettingDefaultEffortPrefix+"claude", "medium")
	if response := crewSet(t, d, protocol.CrewSetMessage{
		Member: "keel", Agent: protocol.Ptr("claude"), Model: protocol.Ptr("claude-haiku-4-5"), Effort: protocol.Ptr(""),
	}); !response.Ok {
		t.Fatalf("save member launch pins: %v", protocol.Deref(response.Error))
	}
	woken, err := d.crewWake("keel", "codex")
	if err != nil {
		t.Fatalf("wake one day on codex: %v", err)
	}
	intent, ok := d.store.LaunchIntent(woken.SessionID)
	if !ok {
		t.Fatal("the overridden day recorded no launch intent")
	}
	intent.Executable = "/tmp/codex-one-day"
	intent.ApprovalRoute = launchcontract.ApprovalRouteReviewer
	intent.UnattendedLaunch = launchcontract.UnattendedLaunchSpec{
		Agent: "codex", Model: "gpt-one-day", Effort: "high", Executable: "/tmp/codex-one-day",
		ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAutoReview,
		DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
	}
	d.store.SetLaunchIntent(woken.SessionID, intent)

	response := crewHandoffCall(t, d, woken.SessionID, "Return to the saved launch contract.")
	if !response.Ok || response.CrewHandoffResult.NapError != nil {
		t.Fatalf("handoff: %+v", response)
	}
	spawns := spawnedSessions(t, backend)
	successor := spawns[len(spawns)-1]
	if successor.Agent != "claude" || successor.Model != "claude-haiku-4-5" || successor.Effort != "medium" {
		t.Fatalf("successor agent/model/effort = %q/%q/%q", successor.Agent, successor.Model, successor.Effort)
	}
	if successor.Executable != "" || !successor.UnattendedLaunch.IsZero() {
		t.Fatalf("successor leaked executable/contract = %q/%#v", successor.Executable, successor.UnattendedLaunch)
	}
	if successor.ApprovalRoute != launchcontract.ApprovalRouteReviewer || !successor.AutoApprove {
		t.Fatalf("successor approval route/auto = %q/%t", successor.ApprovalRoute, successor.AutoApprove)
	}
}

func TestCrewHandoff_AnOutpostHoldsNoneOfIt(t *testing.T) {
	const home = "d-cccccccccccccccccccccccccccccccc"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()
	addSession(t, d, "sess-outpost")

	resp := crewHandoffCall(t, d, "sess-outpost", "Filed from an outpost.")
	if resp.Ok {
		t.Fatal("an outpost filed a crew letter")
	}
	if !strings.Contains(protocol.Deref(resp.Error), home) {
		t.Errorf("the refusal %q does not name the home", protocol.Deref(resp.Error))
	}
	if names := handoffFiles(t, d, "trellis"); len(names) != 1 {
		t.Fatalf("the handoffs dir holds %v; an outpost wrote into a home", names)
	}
}

func TestCrewHandoff_ARestartRecordedDuringTheNapIsSettledByTheSuccessor(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	backend.mu.Lock()
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == woken.SessionID {
			return
		}
		if _, err := setCrewRestart(d, "keel", &crew.Restart{RequestID: "mid-nap", SessionID: woken.SessionID, State: crew.RestartQueued}); err != nil {
			t.Errorf("record restart during the nap: %v", err)
		}
	}
	backend.mu.Unlock()

	resp := crewHandoffCall(t, d, woken.SessionID, "Filed while a restart landed.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	successor := protocol.Deref(resp.CrewHandoffResult.SessionID)
	member := memberByID(t, crewList(t, d), "keel")
	if member.Restart == nil || member.Restart.RequestID != "mid-nap" || member.Restart.State != protocol.CrewRestartStateCompleted ||
		protocol.Deref(member.Restart.SuccessorSessionID) != successor {
		t.Fatalf("restart after the nap = %+v, want completed by successor %s", member.Restart, successor)
	}
}
