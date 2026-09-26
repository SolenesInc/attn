package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func setupDelegationSource(t *testing.T, d *Daemon, backend *fakeSpawnBackend) (string, string, string) {
	t.Helper()
	return setupDelegationSourceAt(t, d, backend, t.TempDir())
}

func setupDelegationSourceAt(t *testing.T, d *Daemon, backend *fakeSpawnBackend, cwd string) (string, string, string) {
	t.Helper()
	setupDelegationGarden(t, d)
	d.ptyBackend = backend
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-source"
	sessionID := "session-source"

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Source workspace",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-source"),
		SessionID:   sessionID,
		Title:       protocol.Ptr("Source"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-source", true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		WorkspaceID: workspaceID,
		Agent:       protocol.AgentShellValue,
		Cols:        80,
		Rows:        24,
		Label:       protocol.Ptr("Source"),
	})
	expectSpawnResult(t, client, sessionID, true)
	return workspaceID, sessionID, cwd
}

func setupDelegationGarden(t *testing.T, d *Daemon) {
	t.Helper()
	if d.daemonInstanceID == "" {
		id, err := enrollment.EnsureDaemonID(d.dataRoot)
		if err != nil {
			t.Fatalf("prepare test Garden identity: %v", err)
		}
		d.daemonInstanceID = id
		if err := d.ensureEnrollment(); err != nil {
			t.Fatalf("prepare test Garden enrollment: %v", err)
		}
	}
	d.ensureGardenCollections()
}

func consumeDelegatedPrompt(t *testing.T, backend *fakeSpawnBackend) {
	t.Helper()
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		if _, err := os.ReadFile(opts.InitialPromptFile); err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatalf("remove initial prompt: %v", err)
		}
	}
}

func newDelegationDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	return d
}

func TestDelegateRollsBackPaneWhenSpawnFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	d.ptyBackend = &failingSpawnBackend{err: os.ErrPermission}

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("This spawn should fail."),
		Agent:           protocol.Ptr("codex"),
	}); err == nil {
		t.Fatal("delegate() succeeded, want spawn failure")
	}
	layout := d.store.GetWorkspaceLayout("workspace-source")
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("workspace layout after rollback = %+v", layout)
	}
}

func TestDelegateDefaultsEffortIntoPluginDriver(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	client, done := startPluginPipe(t, d, "fixture-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "fixture", map[string]bool{
		"initial_prompt": true,
		"model_pin":      true,
		"effort_pin":     true,
	})

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		for {
			request := decodeJSONRPCMessage(t, client)
			if request.Method == pluginHealthMethod {
				respondPluginRequest(t, client, request, pluginHealthResult{OK: true})
				continue
			}
			if request.Method != "driver.spawn" {
				t.Errorf("method=%q, want driver.spawn", request.Method)
				return
			}
			var got pluginDriverSpawnParams
			if err := json.Unmarshal(request.Params, &got); err != nil {
				t.Errorf("decode plugin spawn params: %v", err)
				return
			}
			if got.Model != "spotify-glm/zai-org/GLM-5.2-FP8" || got.Effort != "medium" {
				t.Errorf("plugin spawn pins=%q/%q, want selected model/medium", got.Model, got.Effort)
			}
			respondPluginRequest(t, client, request, pluginDriverSpawnResult{Argv: []string{"fixture"}})
			return
		}
	}()

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Use the selected OpenCode variant."),
		Agent:           protocol.Ptr("fixture"),
		Model:           protocol.Ptr("spotify-glm/zai-org/GLM-5.2-FP8"),
	}); err != nil {
		t.Fatalf("delegate() error=%v", err)
	}
	<-requestDone
}

func TestDelegateRejectsRemoteSourceSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	source := d.store.Get(sourceSessionID)
	source.EndpointID = protocol.Ptr("endpoint-remote")
	d.store.Add(source)

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Do not launch this locally."),
		Agent:           protocol.Ptr("codex"),
	})
	if err == nil || !strings.Contains(err.Error(), "delegation from remote session") {
		t.Fatalf("delegate() error = %v, want remote source rejection", err)
	}
	if len(backend.spawnOpts) != 1 {
		t.Fatalf("spawn count = %d, want only source session", len(backend.spawnOpts))
	}
}

func TestDelegateKillsSpawnedRuntimeWhenPersistenceFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceSessionID {
			return
		}
		if opts.InitialPromptFile != "" {
			_ = os.Remove(opts.InitialPromptFile)
		}
		if err := d.store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Persistence will fail after this runtime starts."),
		Agent:           protocol.Ptr("codex"),
	})
	if err == nil || !strings.Contains(err.Error(), "persist spawned session") {
		t.Fatalf("delegate() error = %v, want persistence failure", err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.ID == sourceSessionID {
		t.Fatalf("last spawn = %+v, want delegated runtime", spawn)
	}
	if !backend.WasKilledAndRemoved(spawn.ID) {
		t.Fatalf("delegated runtime %s was not killed and removed", spawn.ID)
	}
}

func TestTruncateDelegationName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"within limit is unchanged", "attn--feat-agent-cost-tooling", "attn--feat-agent-cost-tooling"},
		{"exactly at limit is unchanged", strings.Repeat("a", 48), strings.Repeat("a", 48)},
		{"cuts to the rune limit", strings.Repeat("abcd-", 10) + "tail", strings.Repeat("abcd-", 9) + "abc"},
		{"trims a trailing dash after the cut", strings.Repeat("a", 47) + "-more-stuff", strings.Repeat("a", 47)},
		{"trims trailing punctuation and whitespace", strings.Repeat("b", 44) + "   . more", strings.Repeat("b", 44)},
		{"multi-byte runes counted as one", strings.Repeat("é", 60), strings.Repeat("é", 48)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateDelegationName(tc.input)
			if got != tc.want {
				t.Fatalf("truncateDelegationName(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if len([]rune(got)) > maxSessionNameRunes {
				t.Fatalf("truncateDelegationName(%q) = %q exceeds max runes", tc.input, got)
			}
		})
	}
}

func TestDelegatePreservesCurrentWorkspaceWorktreeWhenSpawnFails(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupBackend := &fakeSpawnBackend{}
	sourceWorkspaceID, sourceSessionID, _ := setupDelegationSourceAt(t, d, setupBackend, mainRepo)
	d.ptyBackend = &failingSpawnBackend{err: os.ErrPermission}
	worktreePath := filepath.Join(root, "repo--feat-current-rollback")

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("This spawn should roll back in the source workspace."),
		Label:           protocol.Ptr("rollback"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/current-rollback",
			Path:   protocol.Ptr(worktreePath),
		},
	}); err == nil {
		t.Fatal("delegate() succeeded, want spawn failure")
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("failed launch removed worktree: %v", err)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 1 || workspaces[0].ID != sourceWorkspaceID {
		t.Fatalf("workspaces after rollback = %+v, want only source workspace", workspaces)
	}
	layout := d.store.GetWorkspaceLayout(sourceWorkspaceID)
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("source workspace layout after rollback = %+v", layout)
	}
}

func TestDelegatePreservesNewWorkspaceWorktreeWhenSpawnFails(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupBackend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, setupBackend, mainRepo)
	d.ptyBackend = &failingSpawnBackend{err: os.ErrPermission}
	worktreePath := filepath.Join(root, "repo--feat-rollback")

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("This spawn should roll back."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Label:           protocol.Ptr("rollback"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/rollback",
			Path:   protocol.Ptr(worktreePath),
		},
	}); err == nil {
		t.Fatal("delegate() succeeded, want spawn failure")
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("failed launch removed worktree: %v", err)
	}
	for _, workspace := range d.store.ListWorkspaces() {
		if workspace != nil && workspace.Directory == worktreePath {
			t.Fatalf("delegated workspace still exists after rollback: %+v", workspace)
		}
	}
}

func initDelegationRepo(t *testing.T, root, name string) string {
	t.Helper()
	repo := filepath.Join(root, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "init")
	return git.CanonicalizePath(repo)
}

func TestDelegatePreservesWorktreeWhenTheFinalStepFails(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	sourceWorkspaceID, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	d.delegationFinalizeHook = func() error { return errors.New("the last step of the delegation failed") }
	worktreePath := filepath.Join(root, "repo--feat-ticket-rollback")

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("This delegation should roll back at the last step."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Label:           protocol.Ptr("ticket-rollback"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/ticket-rollback",
			Path:   protocol.Ptr(worktreePath),
		},
	}); err == nil {
		t.Fatal("delegate() succeeded, want the seeded final-step failure")
	}

	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("failed launch removed worktree: %v", err)
	}
	workspaces := d.store.ListWorkspaces()
	if len(workspaces) != 1 || workspaces[0].ID != sourceWorkspaceID {
		t.Fatalf("workspaces after rollback = %+v, want only the source workspace", workspaces)
	}
	if layout := d.store.GetWorkspaceLayout(sourceWorkspaceID); layout == nil ||
		len(layout.Panes) != 1 || layout.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("source workspace layout after rollback = %+v", layout)
	}
	for _, session := range d.store.List("") {
		if session.ID != sourceSessionID {
			t.Fatalf("delegated session %q survived the rollback", session.ID)
		}
	}
}

func TestDelegateRollsBackSpawnedSessionWhenTheFinalStepFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	sourceWorkspaceID, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	d.delegationFinalizeHook = func() error { return errors.New("the last step of the delegation failed") }

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("This delegation should roll back at the last step."),
		Label:           protocol.Ptr("tkt-rb-cur"),
	}); err == nil {
		t.Fatal("delegate() succeeded, want the seeded final-step failure")
	}

	for _, session := range d.store.List("") {
		if session.ID != sourceSessionID {
			t.Fatalf("delegated session %q survived the rollback", session.ID)
		}
	}
	if layout := d.store.GetWorkspaceLayout(sourceWorkspaceID); layout == nil ||
		len(layout.Panes) != 1 || layout.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("source workspace layout after rollback = %+v", layout)
	}
}
