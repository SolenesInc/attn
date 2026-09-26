package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
)

type recoveryHome struct {
	claudeProjects string
	codexSessions  string
}

func newRecoveryHome(t *testing.T) recoveryHome {
	t.Helper()
	home, codexHome := t.TempDir(), t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("CODEX_HOME", codexHome)
	h := recoveryHome{
		claudeProjects: filepath.Join(home, ".claude", "projects", "proj"),
		codexSessions:  filepath.Join(codexHome, "sessions", "2026", "08", "10"),
	}
	for _, dir := range []string{h.claudeProjects, h.codexSessions} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return h
}

func (h recoveryHome) resumableClaude(t *testing.T, resumeID string) {
	t.Helper()
	path := filepath.Join(h.claudeProjects, resumeID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write claude transcript for %s: %v", resumeID, err)
	}
}

func (h recoveryHome) resumableCodex(t *testing.T, resumeID string) {
	t.Helper()
	rollout := []byte(`{"type":"session_meta","payload":{"id":"` + resumeID + `","cwd":"/tmp"}}` + "\n")
	path := filepath.Join(h.codexSessions, "rollout-"+resumeID+".jsonl")
	if err := os.WriteFile(path, rollout, 0o644); err != nil {
		t.Fatalf("write codex rollout for %s: %v", resumeID, err)
	}
}

func giveRestorationEvidence(t *testing.T, d *Daemon, sessionID, resumeID string) {
	t.Helper()
	d.store.SetResumeSessionID(sessionID, resumeID)
	giveLaunchIntent(t, d, sessionID)
}

func giveLaunchIntent(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	d.store.SetLaunchIntent(sessionID, store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser})
}

func addStaleSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent, state protocol.SessionState) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          agent,
		Directory:      "/tmp/" + id,
		State:          state,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	t.Cleanup(func() { d.store.Remove(id) })
}

func deadWorkerBackend() *fakeWorkerReconcileBackend {
	return &fakeWorkerReconcileBackend{liveIDs: nil, info: map[string]ptybackend.SessionInfo{}}
}

func TestRecoveryDoesNotResurrectAnIntentionalClose(t *testing.T) {
	home := newRecoveryHome(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addStaleSession(t, d, "closed-on-purpose", protocol.SessionAgentCodex, protocol.SessionStateWorking)
	home.resumableCodex(t, "native-closed-on-purpose")
	giveRestorationEvidence(t, d, "closed-on-purpose", "native-closed-on-purpose")
	d.store.MarkSessionIntentionalClose("closed-on-purpose", time.Now())
	d.ptyBackend = deadWorkerBackend()

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})

	if session := d.store.Get("closed-on-purpose"); session != nil {
		t.Fatalf("session = %+v, want gone: the user already dismissed it", session)
	}
}

func TestRecoveryJudgesPluginSessionsOnTheirPersistedHandle(t *testing.T) {
	newRecoveryHome(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	addStaleSession(t, d, "plugin-with-handle", "snipe", protocol.SessionStateWorking)
	giveLaunchIntent(t, d, "plugin-with-handle")
	if !d.store.BeginAgentDriverRun("plugin-with-handle", "snipe-plugin", "run-handle") {
		t.Fatal("BeginAgentDriverRun(plugin-with-handle) failed")
	}
	if !d.store.ApplyAgentDriverMetadata("plugin-with-handle", "run-handle", 1, `{"native_id":"resume-me"}`) {
		t.Fatal("ApplyAgentDriverMetadata(plugin-with-handle) failed")
	}
	d.store.EndAgentDriverRun("plugin-with-handle")

	addStaleSession(t, d, "plugin-capability-only", "snipe-live", protocol.SessionStateWorking)
	giveLaunchIntent(t, d, "plugin-capability-only")
	plugin := &pluginConnection{name: "snipe-live-plugin"}
	if err := d.ensurePluginRegistry().register(plugin); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := d.ensurePluginRegistry().registerDriver(plugin, pluginDriverRegisterParams{
		Agent:        "snipe-live",
		Capabilities: map[string]bool{"resume": true},
	}); err != nil {
		t.Fatalf("register resumable driver: %v", err)
	}

	d.ptyBackend = deadWorkerBackend()
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})

	if session := d.store.Get("plugin-with-handle"); session == nil || session.State != protocol.SessionStateRecoverable {
		t.Fatalf("plugin-with-handle = %+v, want recoverable", session)
	}
	if session := d.store.Get("plugin-capability-only"); session != nil {
		t.Fatalf("plugin-capability-only = %+v, want reaped: nothing names the conversation", session)
	}
}
