package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
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

func TestActiveSessionInLinkedWorktree_IgnoresRecoverableSession(t *testing.T) {
	root := t.TempDir()
	repo := initDelegationRepo(t, root, "repo")
	worktree := filepath.Join(root, "repo--recoverable")
	runGitDaemon(t, repo, "worktree", "add", "-b", "recoverable", worktree)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "recoverable",
		Label:          "recoverable",
		Agent:          protocol.SessionAgentClaude,
		Directory:      worktree,
		State:          protocol.SessionStateRecoverable,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	if _, active := d.activeSessionInLinkedWorktree(worktree); active {
		t.Fatal("recoverable session must not block delegation into its linked worktree")
	}
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

func TestDelegateSpawnsAgentInSourceWorkspaceWithBrief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceSessionID, cwd := setupDelegationSource(t, d, backend)

	var prompt string
	var promptPath string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		promptPath = opts.InitialPromptFile
		content, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		prompt = string(content)
		if err := os.Remove(promptPath); err != nil {
			t.Fatalf("remove consumed initial prompt: %v", err)
		}
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Investigate the delegated task."),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if protocol.Deref(result.WorkspaceID) != workspaceID || result.Directory != cwd {
		t.Fatalf("result = %+v, want workspace=%s directory=%s", result, workspaceID, cwd)
	}
	if strings.Contains(prompt, "Investigate the delegated task.") || !strings.Contains(prompt, "attn seed show") {
		t.Fatalf("initial prompt = %q", prompt)
	}
	if promptPath == "" {
		t.Fatal("delegated spawn had no initial prompt file")
	}
	if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
		t.Fatalf("initial prompt file still exists after spawn: %v", err)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.WorkspaceID != workspaceID || session.Agent != protocol.SessionAgentCodex {
		t.Fatalf("delegated session = %+v", session)
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("workspace layout = %+v, want two panes", layout)
	}
}

func newDelegationDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	return d
}

func TestDelegateRefusesTicketAdoption(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "planned-fix", Title: "Planned fix", Description: "Implement the complete planned fix.",
		Status: store.TicketStatusDone,
	}, "you", time.Now()); err != nil {
		t.Fatal(err)
	}

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr(sourceSessionID),
		TicketID: protocol.Ptr("planned-fix"), Agent: protocol.Ptr("codex"),
	})
	if err == nil {
		t.Fatal("delegate() adopted a ticket, want a refusal")
	}
	if !strings.Contains(err.Error(), "attn seed plant") {
		t.Fatalf("refusal does not name the garden move: %v", err)
	}
	if sessions := d.store.List(""); len(sessions) != 1 || sessions[0].ID != sourceSessionID {
		t.Fatalf("a refused delegation still launched: %+v", sessions)
	}
}

func TestDelegateDefaultsToNewWorktreeForGitRepository(t *testing.T) {
	root := t.TempDir()
	mainRepo := initDelegationRepo(t, root, "repo")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Investigate the repository."),
		Label:           protocol.Ptr("parser"),
		Worktree:        &protocol.DelegateWorktreeRequest{},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if protocol.Deref(result.WorkspaceID) != workspaceID ||
		result.Directory == mainRepo ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v, want isolated worktree in source workspace", result)
	}
	if got := git.CanonicalizePath(git.GetMainRepoFromWorktree(result.Directory)); got != mainRepo {
		t.Fatalf("worktree main repo = %q, want %q", got, mainRepo)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || !strings.HasPrefix(protocol.Deref(session.Branch), "delegate/parser-") {
		t.Fatalf("delegated session = %+v, want generated branch", session)
	}
}

func TestDelegateNoWorktreeReusesGitCheckout(t *testing.T) {
	root := t.TempDir()
	mainRepo := initDelegationRepo(t, root, "repo")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Continue in the existing checkout."),
		Label:           protocol.Ptr("continuation"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if protocol.Deref(result.WorkspaceID) != workspaceID ||
		result.Directory != mainRepo ||
		protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v, want source checkout without worktree", result)
	}
}

func TestChiefOfStaffDelegateBindsSeedAndPrompt(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}

	var prompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceSessionID || opts.InitialPromptFile == "" {
			return
		}
		content, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		prompt = string(content)
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatalf("remove initial prompt: %v", err)
		}
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Investigate the tracked task."),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	if strings.Contains(prompt, "Investigate the tracked task.") || !strings.Contains(prompt, "attn seed show") {
		t.Fatalf("tracked initial prompt = %q", prompt)
	}

	seedID, bound := d.gardenDispatchCrown(result.SessionID)
	if !bound {
		t.Fatalf("delegation bound no seed to session %s", result.SessionID)
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read the bound seed: %v", err)
	}
	if seed.Body != "Investigate the tracked task." || seed.TenderSession != result.SessionID {
		t.Fatalf("bound seed = %+v", seed)
	}
	if !strings.Contains(prompt, seedID) {
		t.Fatalf("initial prompt does not name the seed %s: %q", seedID, prompt)
	}

	if ids := d.delegatedFromChiefSessionIDs(); !ids[result.SessionID] {
		t.Fatalf("delegated session missing from chief-delegated set: %v", ids)
	}
}

func TestChiefOfStaffDelegationPreservesCoordinationIdentityAcrossPlacements(t *testing.T) {
	for _, placement := range []string{
		delegationPlacementCurrent,
		delegationPlacementNew,
		delegationPlacementExisting,
	} {
		t.Run(placement, func(t *testing.T) {
			d := newDelegationDaemon(t)
			backend := &fakeSpawnBackend{}
			_, chiefSessionID, _ := setupDelegationSource(t, d, backend)
			if err := d.store.SetProfileRole(profileRoleChiefOfStaff, chiefSessionID); err != nil {
				t.Fatalf("set chief role: %v", err)
			}
			consumeDelegatedPrompt(t, backend)

			msg := &resolvedDelegationLaunch{
				Cmd:             protocol.CmdDelegate,
				SourceSessionID: protocol.Ptr(chiefSessionID),
				Brief:           protocol.Ptr("Exercise tracked coordination identity."),
				Agent:           protocol.Ptr("codex"),
				Placement:       protocol.Ptr(placement),
			}
			switch placement {
			case delegationPlacementNew:
				msg.Cwd = t.TempDir()
			case delegationPlacementExisting:
				targetDirectory := t.TempDir()
				msg.WorkspaceID = protocol.Ptr("workspace-target")
				d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
					Cmd:       protocol.CmdRegisterWorkspace,
					ID:        protocol.Deref(msg.WorkspaceID),
					Title:     "Target",
					Directory: targetDirectory,
				})
			}

			result, err := d.delegateResolved(msg)
			if err != nil {
				t.Fatalf("delegate() error = %v", err)
			}
			spawn, ok := backend.LastSpawn()
			if !ok || spawn.ID != result.SessionID || spawn.CWD != result.Directory {
				t.Fatalf("spawn = %+v, result = %+v", spawn, result)
			}

			seedID, bound := d.gardenDispatchCrown(spawn.ID)
			if !bound {
				t.Fatalf("delegation bound no seed to session %s", spawn.ID)
			}
			seed, _, err := d.readSeed(seedID)
			if err != nil || seed.TenderSession != spawn.ID {
				t.Fatalf("bound seed = %+v, err=%v", seed, err)
			}
		})
	}
}

func TestDelegatedFromChiefDecoratesBroadcastSession(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	consumeDelegatedPrompt(t, backend)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Investigate the tracked task."),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	var delegated, chief *protocol.Session
	for _, session := range d.sessionsForBroadcast(d.store.List("")) {
		session := session
		switch session.ID {
		case result.SessionID:
			delegated = &session
		case sourceSessionID:
			chief = &session
		}
	}

	if delegated == nil {
		t.Fatal("delegated session missing from broadcast")
	}
	if !protocol.Deref(delegated.DelegatedFromChief) {
		t.Fatalf("delegated session = %+v, want delegated_from_chief=true", delegated)
	}
	if protocol.Deref(delegated.ChiefOfStaff) {
		t.Fatalf("delegated session should not be the chief itself: %+v", delegated)
	}

	if chief == nil {
		t.Fatal("chief session missing from broadcast")
	}
	if !protocol.Deref(chief.ChiefOfStaff) {
		t.Fatalf("chief session = %+v, want chief_of_staff=true", chief)
	}
	if protocol.Deref(chief.DelegatedFromChief) {
		t.Fatalf("chief session should not carry delegated_from_chief: %+v", chief)
	}
}

func TestOrdinaryDelegationBindsSeedWithoutChiefDecoration(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	var prompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceSessionID || opts.InitialPromptFile == "" {
			return
		}
		content, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		prompt = string(content)
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatalf("remove initial prompt: %v", err)
		}
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Plain delegated task."),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	seedID, bound := d.gardenDispatchCrown(result.SessionID)
	if !bound {
		t.Fatalf("ordinary delegation bound no seed to session %s", result.SessionID)
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil || seed.Body != "Plain delegated task." || seed.TenderSession != result.SessionID {
		t.Fatalf("bound seed = %+v, err=%v", seed, err)
	}

	if !strings.Contains(prompt, "attn seed show "+seedID) || strings.Contains(prompt, "Plain delegated task.") {
		t.Fatalf("ordinary delegated prompt must reference the seed without copying its task: %q", prompt)
	}

	delegated := d.sessionForBroadcast(d.store.Get(result.SessionID))
	if delegated == nil {
		t.Fatal("delegated session missing")
	}
	if protocol.Deref(delegated.DelegatedFromChief) {
		t.Fatalf("ordinary delegated session should not carry delegated_from_chief: %+v", delegated)
	}
	if protocol.Deref(delegated.SeedID) != seedID {
		t.Fatalf("broadcast session does not name its seed: %+v", delegated)
	}
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

func TestDelegateAcceptsCopilotInitialPrompt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	var prompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceSessionID || opts.InitialPromptFile == "" {
			return
		}
		content, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		prompt = string(content)
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Fatalf("remove initial prompt: %v", err)
		}
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Use Copilot for this delegated task."),
		Agent:           protocol.Ptr("copilot"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v, want copilot delegation to succeed", err)
	}
	if strings.Contains(prompt, "Use Copilot for this delegated task.") || !strings.Contains(prompt, "attn seed show") {
		t.Fatalf("delegated initial prompt = %q", prompt)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.WorkspaceID != workspaceID || session.Agent != protocol.SessionAgentCopilot {
		t.Fatalf("delegated session = %+v, want copilot session in %s", session, workspaceID)
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("workspace layout = %+v, want source + delegated panes", layout)
	}
}

func TestDelegateThreadsModelAndEffortIntoSpawn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Run pinned to a specific model."),
		Agent:           protocol.Ptr("claude"),
		Model:           protocol.Ptr("claude-fable-5"),
		Effort:          protocol.Ptr("Low"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.ID != result.SessionID {
		t.Fatalf("last spawn = %+v, want delegated session %s", spawn, result.SessionID)
	}
	if spawn.Model != "claude-fable-5" || spawn.Effort != "low" {
		t.Fatalf("spawn model/effort = %q/%q, want claude-fable-5/low", spawn.Model, spawn.Effort)
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

func TestDelegateDefaultsEffortForSupportedAgent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Run with the default effort."),
		Agent:           protocol.Ptr("claude"),
		Model:           protocol.Ptr("opus"),
	}); err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.Model != "opus" || spawn.Effort != "medium" {
		t.Fatalf("spawn model/effort = %q/%q, want opus/medium", spawn.Model, spawn.Effort)
	}
}

func TestDelegateDoesNotDefaultEffortForUnsupportedAgent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Run without an unsupported effort pin."),
		Agent:           protocol.Ptr("copilot"),
	}); err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	spawn, ok := backend.LastSpawn()
	if !ok || spawn.Effort != "" {
		t.Fatalf("spawn effort = %q, want empty", spawn.Effort)
	}
}

func TestDelegateRejectsModelEffortForUnsupportedAgent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	for flag, msg := range map[string]*resolvedDelegationLaunch{
		"--model": {
			Cmd:             protocol.CmdDelegate,
			SourceSessionID: protocol.Ptr(sourceSessionID),
			Brief:           protocol.Ptr("Pin a model on copilot."),
			Agent:           protocol.Ptr("copilot"),
			Model:           protocol.Ptr("gpt-5"),
		},
		"--effort": {
			Cmd:             protocol.CmdDelegate,
			SourceSessionID: protocol.Ptr(sourceSessionID),
			Brief:           protocol.Ptr("Pin effort on copilot."),
			Agent:           protocol.Ptr("copilot"),
			Effort:          protocol.Ptr("high"),
		},
	} {
		_, err := d.delegateResolved(msg)
		if err == nil || !strings.Contains(err.Error(), "does not support "+flag) {
			t.Fatalf("delegate(%s) error = %v, want unsupported-agent rejection", flag, err)
		}
	}
	if len(backend.spawnOpts) != 1 {
		t.Fatalf("spawn count = %d, want only source session", len(backend.spawnOpts))
	}
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

func TestDelegateRejectsUnknownExplicitSourceSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	setupDelegationSource(t, d, backend)

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr("missing-source"),
		Brief:           protocol.Ptr("Do not lose source attribution."),
		Agent:           protocol.Ptr("codex"),
	})
	if err == nil || !strings.Contains(err.Error(), "source session missing-source was not found") {
		t.Fatalf("delegate() error = %v, want unknown source rejection", err)
	}
	if len(backend.spawnOpts) != 1 {
		t.Fatalf("spawn count = %d, want only the fixture source", len(backend.spawnOpts))
	}
}

func TestDelegateWebSocketCommandReturnsResult(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		backend := &fakeSpawnBackend{}
		_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
		consumeDelegatedPrompt(t, backend)
		client := newWorkspaceProtocolTestClient()
		client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

		payload, err := json.Marshal(protocol.DelegateMessage{
			Cmd: protocol.CmdDelegate, RequestID: "websocket-delegation",
			SourceSessionID: protocol.Ptr(sourceSessionID),
			Assignment:      protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Handle this through the websocket dispatcher.")},
			Cwd:             d.store.Get(sourceSessionID).Directory, Agent: protocol.Ptr("codex"),
		})
		if err != nil {
			t.Fatalf("marshal delegate message: %v", err)
		}
		d.handleClientMessage(client, payload)

		// handleDelegateWS polls the operation every 100ms; on the fake clock a
		// generous run-out is free.
		time.Sleep(time.Second)
		outbound := requireOutbound(t, client, "no delegate_result reached the client")
		var result protocol.DelegateResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode delegate result: %v", err)
		}
		if result.Event != protocol.EventDelegateResult || !result.Success || result.Result == nil {
			t.Fatalf("delegate result = %+v", result)
		}
		if protocol.Deref(result.Result.WorkspaceID) != "workspace-source" {
			t.Fatalf("workspace = %q, want workspace-source", protocol.Deref(result.Result.WorkspaceID))
		}
	})
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

func TestResolveDelegationAgentSupportsRegisteredPluginWithInitialPrompt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	registry := d.ensurePluginRegistry()
	registry.mu.Lock()
	registry.drivers["fixture"] = pluginDriverRegistration{
		PluginName: "fixture-plugin",
		Agent:      "fixture",
		Capabilities: map[string]bool{
			"initial_prompt": true,
		},
	}
	registry.mu.Unlock()

	agent, err := d.resolveDelegationAgent("codex", protocol.Ptr("fixture"))
	if err != nil {
		t.Fatalf("resolveDelegationAgent() error = %v", err)
	}
	if agent != "fixture" {
		t.Fatalf("agent = %q", agent)
	}
}

func TestDelegateCreatesNewWorkspaceAtCustomDirectory(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Work in a separate directory."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             targetDir,
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	targetDir = git.CanonicalizePath(targetDir)
	if result.Directory != targetDir {
		t.Fatalf("result = %+v", result)
	}
	workspace := d.store.GetWorkspace(protocol.Deref(result.WorkspaceID))
	if workspace == nil || workspace.Directory != targetDir {
		t.Fatalf("delegated workspace = %+v", workspace)
	}
	layout := d.store.GetWorkspaceLayout(protocol.Deref(result.WorkspaceID))
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].SessionID != result.SessionID {
		t.Fatalf("delegated workspace layout = %+v", layout)
	}
}

func TestDelegateNamesNewWorkspaceAndSessionFromExplicitName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Name everything from --name."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             targetDir,
		Label:           protocol.Ptr("launcher"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if workspace := d.store.GetWorkspace(protocol.Deref(result.WorkspaceID)); workspace == nil || workspace.Title != "launcher" {
		t.Fatalf("delegated workspace = %+v, want title %q", workspace, "launcher")
	}
	if session := d.store.Get(result.SessionID); session == nil || session.Label != "launcher" {
		t.Fatalf("delegated session = %+v, want label %q", session, "launcher")
	}
	layout := d.store.GetWorkspaceLayout(protocol.Deref(result.WorkspaceID))
	if layout == nil || len(layout.Panes) != 1 || layout.Panes[0].Title != "launcher" {
		t.Fatalf("delegated layout = %+v, want one pane titled %q", layout, "launcher")
	}
}

func TestDelegateDefaultsNameToDirectoryBasename(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Default the name to the folder."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             targetDir,
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if workspace := d.store.GetWorkspace(protocol.Deref(result.WorkspaceID)); workspace == nil || workspace.Title != "myproj" {
		t.Fatalf("delegated workspace = %+v, want title %q", workspace, "myproj")
	}
	if session := d.store.Get(result.SessionID); session == nil || session.Label != "myproj" {
		t.Fatalf("delegated session = %+v, want label %q", session, "myproj")
	}
}

func TestDelegateRejectsNameTooLong(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Name is too long."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             targetDir,
		Label:           protocol.Ptr(strings.Repeat("long-", 10)),
	})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("delegate() error = %v, want a name-too-long error", err)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 1 {
		t.Fatalf("workspaces = %+v, want only the source workspace", workspaces)
	}
}

func TestDelegateRejectsDuplicateWorkspaceName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        "workspace-taken",
		Title:     "taken",
		Directory: t.TempDir(),
	})

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Reuse a workspace name."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             t.TempDir(),
		Label:           protocol.Ptr("taken"),
	})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("delegate() error = %v, want a duplicate-workspace error", err)
	}
}

func TestDelegateRejectsDuplicateSessionNameInWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	first, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("First agent."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             targetDir,
		Label:           protocol.Ptr("alpha"),
	})
	if err != nil {
		t.Fatalf("first delegate() error = %v", err)
	}

	_, err = d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Second agent, same name, same workspace."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     first.WorkspaceID,
		Label:           protocol.Ptr("alpha"),
	})
	if err == nil || !strings.Contains(err.Error(), "already used in this workspace") {
		t.Fatalf("second delegate() error = %v, want a duplicate-session error", err)
	}
}

func TestDelegateTruncatesLongWorktreeDefaultName(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-delegated-with-a-branch-name-past-the-cap")

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("No --name; the worktree folder is too long."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/delegated-with-a-branch-name-past-the-cap",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	wantName := "repo--feat-delegated-with-a-branch-name-past-the"
	if len([]rune(wantName)) > maxSessionNameRunes {
		t.Fatalf("test setup bug: wantName %q exceeds max", wantName)
	}
	workspace := d.store.GetWorkspace(protocol.Deref(result.WorkspaceID))
	if workspace == nil || workspace.Title != wantName {
		t.Fatalf("delegated workspace = %+v, want title %q", workspace, wantName)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.Label != wantName {
		t.Fatalf("delegated session = %+v, want label %q", session, wantName)
	}
}

func TestDelegateSeparatesCreationFromExplicitCheckoutReuse(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--shared")
	base := resolvedDelegationLaunch{
		Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr(sourceSessionID), Brief: protocol.Ptr("First owner."),
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("owner"), Placement: protocol.Ptr(delegationPlacementNew),
		Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(mainRepo), Branch: "feat/shared", Path: protocol.Ptr(worktreePath)},
	}
	owner, err := d.delegateResolved(&base)
	if err != nil {
		t.Fatalf("first delegate: %v", err)
	}
	backend.sessionIDs = append(backend.sessionIDs, owner.SessionID)

	retry := base
	retry.Brief = protocol.Ptr("Unintentional second owner.")
	retry.Label = protocol.Ptr("collision")
	retry.Placement = protocol.Ptr(delegationPlacementCurrent)
	if _, err := d.delegateResolved(&retry); err == nil || !strings.Contains(err.Error(), "cannot be reinterpreted as checkout reuse") {
		t.Fatalf("collision error=%v, want explicit creation-vs-reuse guidance", err)
	}
	retry.AllowWorktreeReuse = protocol.Ptr(true)
	if _, err := d.delegateResolved(&retry); err == nil || !strings.Contains(err.Error(), "cannot be reinterpreted as checkout reuse") {
		t.Fatalf("sharing consent reinterpreted creation as reuse: %v", err)
	}

	byCWD := resolvedDelegationLaunch{
		Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr(sourceSessionID), Brief: protocol.Ptr("CWD collision."),
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("cwd-collision"),
		Placement: protocol.Ptr(delegationPlacementNew), Cwd: worktreePath,
	}
	if main := git.GetMainRepoFromWorktree(worktreePath); main == "" {
		t.Fatal("test setup did not produce a linked worktree")
	}
	if !d.store.HasSessionInDirectory(git.CanonicalizePath(worktreePath)) {
		t.Fatal("test setup has no active session in shared worktree")
	}
	if protocol.Deref(byCWD.AllowWorktreeReuse) {
		t.Fatal("cwd collision unexpectedly opted into reuse")
	}
	if _, err := d.delegateResolved(&byCWD); err == nil || !strings.Contains(err.Error(), "--allow-worktree-reuse") {
		t.Fatalf("cwd collision error=%v, want explicit reuse guidance", err)
	}
	byCWD.AllowWorktreeReuse = protocol.Ptr(true)
	if _, err := d.delegateResolved(&byCWD); err != nil {
		t.Fatalf("explicit cwd reuse: %v", err)
	}

	subdir := filepath.Join(worktreePath, "nested", "package")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	bySubdir := resolvedDelegationLaunch{
		Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr(sourceSessionID), Brief: protocol.Ptr("Nested CWD collision."),
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("nested-collision"),
		Placement: protocol.Ptr(delegationPlacementNew), Cwd: subdir,
	}
	if _, err := d.delegateResolved(&bySubdir); err == nil || !strings.Contains(err.Error(), git.CanonicalizePath(worktreePath)) {
		t.Fatalf("nested cwd collision error=%v, want resolved worktree root", err)
	}
	bySubdir.AllowWorktreeReuse = protocol.Ptr(true)
	if _, err := d.delegateResolved(&bySubdir); err != nil {
		t.Fatalf("explicit nested cwd reuse: %v", err)
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

func TestValidateDelegationName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-taken", Title: "Taken", Directory: t.TempDir(),
	})
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-busy", Title: "Busy WS", Directory: t.TempDir(),
	})
	d.store.Add(&protocol.Session{ID: "sess-busy", Label: "Busy", WorkspaceID: "ws-busy", Directory: t.TempDir()})

	cases := []struct {
		name              string
		input             string
		creatingWorkspace bool
		targetWorkspaceID string
		wantErr           string
	}{
		{"forty-eight ASCII accepted", strings.Repeat("a", 48), false, "", ""},
		{"forty-nine ASCII rejected", strings.Repeat("a", 49), false, "", "too long"},
		{"forty-eight runes accepted", strings.Repeat("é", 48), false, "", ""},
		{"forty-nine runes rejected", strings.Repeat("é", 49), false, "", "too long"},
		{"blank rejected", "   ", false, "", "a name is required"},
		{"dot rejected", ".", false, "", "not a usable name"},
		{"separator rejected", string(filepath.Separator), false, "", "not a usable name"},
		{"workspace duplicate is case-insensitive", "taken", true, "", "already in use"},
		{"fresh workspace name accepted", "fresh", true, "", ""},
		{"session duplicate is case-insensitive", "busy", false, "ws-busy", "already used in this workspace"},
		{"distinct session name accepted", "other", false, "ws-busy", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := d.validateDelegationName(tc.input, tc.creatingWorkspace, tc.targetWorkspaceID)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateDelegationName(%q) = %v, want nil", tc.input, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateDelegationName(%q) = %v, want error containing %q", tc.input, err, tc.wantErr)
			}
		})
	}
}

func TestDelegateRejectsDuplicateWorkspaceNameFromDefault(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-existing", Title: "myproj", Directory: t.TempDir(),
	})
	targetDir := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Default name collides with an existing workspace."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             targetDir,
	})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("delegate() error = %v, want a duplicate-workspace error from the default-name path", err)
	}
}

func TestDelegateRejectsNameMatchingSourceSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	source := d.store.Get(sourceSessionID)
	if source == nil || strings.TrimSpace(source.Label) == "" {
		t.Fatalf("source session has no label: %+v", source)
	}
	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Clash with the pre-existing source session name."),
		Label:           protocol.Ptr(strings.ToLower(source.Label)),
	})
	if err == nil || !strings.Contains(err.Error(), "already used in this workspace") {
		t.Fatalf("delegate() error = %v, want a duplicate-session error", err)
	}
}

func TestDelegateTargetsExistingWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, sourceDir := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()
	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: targetDir,
	})
	if _, errMsg := d.toggleWorkspaceMute(targetWorkspaceID); errMsg != "" {
		t.Fatalf("mute target workspace: %s", errMsg)
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Join the target workspace."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if protocol.Deref(result.WorkspaceID) != targetWorkspaceID || result.Directory != sourceDir {
		t.Fatalf("result = %+v", result)
	}
	if workspace := d.store.GetWorkspace(targetWorkspaceID); workspace == nil || !workspace.Muted {
		t.Fatalf("ordinary delegation changed target mute state: %+v", workspace)
	}
}

func TestDelegateTargetsPinnedEmptyWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, sourceDir := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()
	targetWorkspaceID := "workspace-empty-pinned"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Empty pinned",
		Directory: targetDir,
	})
	if _, errMsg := d.setWorkspacePinned(targetWorkspaceID, true); errMsg != "" {
		t.Fatalf("pin target workspace: %s", errMsg)
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Reuse the empty pinned workspace."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if protocol.Deref(result.WorkspaceID) != targetWorkspaceID || result.Directory != sourceDir {
		t.Fatalf("result = %+v", result)
	}
	if sessions := d.store.SessionsInWorkspace(targetWorkspaceID); len(sessions) != 1 || sessions[0] != result.SessionID {
		t.Fatalf("target workspace sessions = %v, want delegated session %s", sessions, result.SessionID)
	}
}

func TestChiefOfStaffDelegateUnmutesExistingWorkspace(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sourceSessionID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	targetDir := t.TempDir()
	targetWorkspaceID := "workspace-muted-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Muted target",
		Directory: targetDir,
	})
	if _, errMsg := d.toggleWorkspaceMute(targetWorkspaceID); errMsg != "" {
		t.Fatalf("mute target workspace: %s", errMsg)
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Join the muted target workspace."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if _, bound := d.gardenDispatchCrown(result.SessionID); !bound {
		t.Fatalf("chief delegation bound no seed to session %s", result.SessionID)
	}
	if workspace := d.store.GetWorkspace(targetWorkspaceID); workspace == nil || workspace.Muted {
		t.Fatalf("chief delegation did not unmute target workspace: %+v", workspace)
	}
	workspace, ok := d.workspaces.snapshot(targetWorkspaceID)
	if !ok || workspace.Muted {
		t.Fatalf("registry target workspace still muted: %+v, found=%v", workspace, ok)
	}
}

func TestDelegateCreatesWorktreeInExistingWorkspace(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: mainRepo,
	})

	worktreePath := filepath.Join(root, "repo--feat-existing-ws")
	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Work in a worktree placed in an existing workspace."),
		Label:           protocol.Ptr("delegated"),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/existing-ws",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if protocol.Deref(result.WorkspaceID) != targetWorkspaceID ||
		result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v", result)
	}
	session := d.store.Get(result.SessionID)
	if session == nil ||
		session.WorkspaceID != targetWorkspaceID ||
		session.Directory != worktreePath ||
		session.Label != "delegated" ||
		protocol.Deref(session.Branch) != "feat/existing-ws" {
		t.Fatalf("delegated worktree session = %+v", session)
	}
	layout := d.store.GetWorkspaceLayout(targetWorkspaceID)
	if layout == nil || len(layout.Panes) != 1 {
		t.Fatalf("target workspace layout = %+v, want one pane", layout)
	}
}

func TestDelegateCreatesWorktreeInSourceWorkspace(t *testing.T) {
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
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-delegated-current")

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Implement this in an isolated branch in this workspace."),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/delegated-current",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if protocol.Deref(result.WorkspaceID) != sourceWorkspaceID ||
		result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v", result)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 1 {
		t.Fatalf("workspaces = %+v, want only source workspace", workspaces)
	}
	session := d.store.Get(result.SessionID)
	if session == nil ||
		session.WorkspaceID != sourceWorkspaceID ||
		session.Directory != worktreePath ||
		session.Label != "delegated" ||
		protocol.Deref(session.Branch) != "feat/delegated-current" {
		t.Fatalf("delegated worktree session = %+v", session)
	}
	layout := d.store.GetWorkspaceLayout(sourceWorkspaceID)
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("source workspace layout = %+v, want two panes", layout)
	}
}

func TestDelegateCreatesWorktreeAndNewWorkspace(t *testing.T) {
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
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-delegated")

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Implement this in an isolated branch."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(mainRepo),
			Branch: "feat/delegated",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if protocol.Deref(result.WorkspaceID) == sourceWorkspaceID ||
		result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v", result)
	}
	if workspaces := d.store.ListWorkspaces(); len(workspaces) != 2 {
		t.Fatalf("workspaces = %+v, want source and delegated workspaces", workspaces)
	}
	delegatedWorkspace := d.store.GetWorkspace(protocol.Deref(result.WorkspaceID))
	if delegatedWorkspace == nil || delegatedWorkspace.Title != "delegated" {
		t.Fatalf("delegated workspace = %+v, want title %q", delegatedWorkspace, "delegated")
	}
	if info, err := os.Stat(worktreePath); err != nil || !info.IsDir() {
		t.Fatalf("worktree path stat = %v, info = %+v", err, info)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || protocol.Deref(session.Branch) != "feat/delegated" {
		t.Fatalf("delegated worktree session = %+v", session)
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

func TestDelegateComposesCwdAndWorktree(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	worktreePath := filepath.Join(root, "repo--feat-cwd-compose")

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Compose --cwd with --worktree."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Label:           protocol.Ptr("composed"),
		Cwd:             mainRepo,
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/cwd-compose",
			Path:   protocol.Ptr(worktreePath),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	worktreePath = git.CanonicalizePath(worktreePath)
	if result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v, want directory %q", result, worktreePath)
	}
	workspace := d.store.GetWorkspace(protocol.Deref(result.WorkspaceID))
	if workspace == nil || workspace.Directory != worktreePath {
		t.Fatalf("delegated workspace = %+v, want directory %q", workspace, worktreePath)
	}
	session := d.store.Get(result.SessionID)
	if session == nil ||
		session.Directory != worktreePath ||
		protocol.Deref(session.Branch) != "feat/cwd-compose" {
		t.Fatalf("delegated session = %+v", session)
	}
	if info, err := os.Stat(worktreePath); err != nil || !info.IsDir() {
		t.Fatalf("worktree path stat = %v, info = %+v", err, info)
	}
}

func TestDelegateComposedCwdWorktreeRequiresRepoWhenNotAGitRepo(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	notARepo := t.TempDir()

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("cwd is not a git repo and no --repo is given."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             notARepo,
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/no-repo",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not in a git repository; pass --repo") {
		t.Fatalf("delegate() error = %v, want a not-a-git-repository error", err)
	}
}

func TestDelegateTruncatesLongDirectoryDefaultName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := filepath.Join(t.TempDir(), "a-very-long-directory-name-indeed-longer-than-the-cap")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("No --name; the directory basename is too long."),
		Placement:       protocol.Ptr(delegationPlacementNew),
		Cwd:             targetDir,
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	wantName := "a-very-long-directory-name-indeed-longer-than-th"
	if len([]rune(wantName)) > maxSessionNameRunes {
		t.Fatalf("test setup bug: wantName %q exceeds max", wantName)
	}
	workspace := d.store.GetWorkspace(protocol.Deref(result.WorkspaceID))
	if workspace == nil || workspace.Title != wantName {
		t.Fatalf("delegated workspace = %+v, want title %q", workspace, wantName)
	}
	session := d.store.Get(result.SessionID)
	if session == nil || session.Label != wantName {
		t.Fatalf("delegated session = %+v, want label %q", session, wantName)
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

func addWorkspaceSessionAt(t *testing.T, d *Daemon, workspaceID, sessionID, cwd string) {
	t.Helper()
	client := newWorkspaceProtocolTestClient()
	paneID := "pane-" + sessionID
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(sessionID),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		WorkspaceID: workspaceID,
		Agent:       protocol.AgentShellValue,
		Cols:        80,
		Rows:        24,
		Label:       protocol.Ptr(sessionID),
	})
	expectSpawnResult(t, client, sessionID, true)
}

func TestDelegateWorktreeIgnoresStaleWorkspaceDirectory(t *testing.T) {
	root := t.TempDir()
	repoA := initDelegationRepo(t, root, "repo-a")
	repoB := initDelegationRepo(t, root, "repo-b")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repoA,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-target", repoA)
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repoB,
	})
	if workspace := d.store.GetWorkspace(targetWorkspaceID); workspace == nil || workspace.Directory != repoB {
		t.Fatalf("precondition: workspace directory = %+v, want %q", workspace, repoB)
	}

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Work on the repo this workspace actually uses."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/right-repo",
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	wantPath := git.CanonicalizePath(git.GenerateWorktreePath(repoA, "feat/right-repo"))
	if result.Directory != wantPath {
		t.Fatalf("worktree directory = %q, want %q (off repoA)", result.Directory, wantPath)
	}
	if main := git.GetMainRepoFromWorktree(result.Directory); git.CanonicalizePath(main) != repoA {
		t.Fatalf("worktree main repo = %q, want %q", main, repoA)
	}
	strayPath := git.CanonicalizePath(git.GenerateWorktreePath(repoB, "feat/right-repo"))
	if _, statErr := os.Stat(strayPath); !os.IsNotExist(statErr) {
		t.Fatalf("worktree created in the wrong repository at %s", strayPath)
	}
}

func TestDelegateWorktreeAmbiguousWorkspaceRepoRequiresRepo(t *testing.T) {
	root := t.TempDir()
	repoA := initDelegationRepo(t, root, "repo-a")
	repoB := initDelegationRepo(t, root, "repo-b")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repoA,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-a", repoA)
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-b", repoB)

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Ambiguous repo."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/ambiguous",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "pass --repo") {
		t.Fatalf("delegate() error = %v, want an ambiguous-repository error naming --repo", err)
	}
	for _, repo := range []string{repoA, repoB} {
		strayPath := git.CanonicalizePath(git.GenerateWorktreePath(repo, "feat/ambiguous"))
		if _, statErr := os.Stat(strayPath); !os.IsNotExist(statErr) {
			t.Fatalf("worktree created at %s despite ambiguous repository", strayPath)
		}
	}
}

func TestDelegateNoWorktreeReusesSourceCheckoutInMixedWorkspace(t *testing.T) {
	root := t.TempDir()
	sourceRepo := initDelegationRepo(t, root, "source")
	repoA := initDelegationRepo(t, root, "repo-a")
	repoB := initDelegationRepo(t, root, "repo-b")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, sourceRepo)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-mixed"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: targetWorkspaceID, Title: "Mixed", Directory: repoA,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-a", repoA)
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-b", repoB)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:     protocol.Ptr("Reuse the checkout this command was invoked from."),
		Placement: protocol.Ptr(delegationPlacementExisting), WorkspaceID: protocol.Ptr(targetWorkspaceID),
		AllowWorktreeReuse: protocol.Ptr(true),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if protocol.Deref(result.WorkspaceID) != targetWorkspaceID || result.Directory != sourceRepo || protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v, want mixed workspace organization with source checkout %s", result, sourceRepo)
	}
}

func TestDelegateRejectsConflictingRepositoryPlacement(t *testing.T) {
	t.Run("cwd and repo", func(t *testing.T) {
		root := t.TempDir()
		repoA := initDelegationRepo(t, root, "repo-a")
		repoB := initDelegationRepo(t, root, "repo-b")
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		backend := &fakeSpawnBackend{}
		_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

		_, err := d.delegateResolved(&resolvedDelegationLaunch{
			Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr(sourceSessionID), Brief: protocol.Ptr("Conflicting repositories."),
			Placement: protocol.Ptr(delegationPlacementNew), Cwd: repoA,
			Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repoB), Branch: "feat/conflict"},
		})
		if err == nil || !strings.Contains(err.Error(), repoA) || !strings.Contains(err.Error(), repoB) || !strings.Contains(err.Error(), "remove --repo") {
			t.Fatalf("delegate() error = %v, want both repositories and correction guidance", err)
		}
		if len(backend.spawnOpts) != 1 {
			t.Fatalf("spawn count = %d, want only source session", len(backend.spawnOpts))
		}
	})

	t.Run("selected repo and existing worktree path", func(t *testing.T) {
		root := t.TempDir()
		repoA := initDelegationRepo(t, root, "repo-a")
		repoB := initDelegationRepo(t, root, "repo-b")
		pathB := filepath.Join(root, "repo-b--shared")
		runGitDaemon(t, repoB, "worktree", "add", "-b", "feat/shared", pathB)
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		backend := &fakeSpawnBackend{}
		_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

		_, err := d.delegateResolved(&resolvedDelegationLaunch{
			Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr(sourceSessionID), Brief: protocol.Ptr("Conflicting worktree path."),
			Worktree: &protocol.DelegateWorktreeRequest{
				Repo: protocol.Ptr(repoA), Branch: "feat/shared", Path: protocol.Ptr(pathB),
			},
			AllowWorktreeReuse: protocol.Ptr(true),
		})
		if err == nil || !strings.Contains(err.Error(), repoA) || !strings.Contains(err.Error(), repoB) || !strings.Contains(err.Error(), "--worktree-path") {
			t.Fatalf("delegate() error = %v, want both repositories and --worktree-path guidance", err)
		}
	})
}

func TestDelegateWorktreeExplicitRepoOverridesWorkspaceSessions(t *testing.T) {
	root := t.TempDir()
	repoA := initDelegationRepo(t, root, "repo-a")
	repoB := initDelegationRepo(t, root, "repo-b")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repoA,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-target", repoA)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Explicit repo wins."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Repo:   protocol.Ptr(repoB),
			Branch: "feat/explicit",
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	wantPath := git.CanonicalizePath(git.GenerateWorktreePath(repoB, "feat/explicit"))
	if result.Directory != wantPath {
		t.Fatalf("worktree directory = %q, want %q (off explicit --repo)", result.Directory, wantPath)
	}
}

func TestDelegateNamedWorktreeUsesRepoDefaultForEveryPlacement(t *testing.T) {
	tests := []struct {
		name      string
		placement string
		useCWD    bool
		useRepo   bool
	}{
		{name: "no placement flag", placement: delegationPlacementCurrent},
		{name: "new workspace", placement: delegationPlacementNew, useRepo: true},
		{name: "cwd", placement: delegationPlacementNew, useCWD: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			repo := initDelegationRepo(t, root, "repo")
			runGitDaemon(t, repo, "branch", "-M", "main")
			defaultHead := gitRevParseDaemon(t, repo, "main")
			runGitDaemon(t, repo, "checkout", "-q", "-b", "topic/ambient")
			runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "ambient")
			ambientHead := gitRevParseDaemon(t, repo, "HEAD")

			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			backend := &fakeSpawnBackend{}
			var sourceSessionID string
			if tt.useCWD {
				_, sourceSessionID, _ = setupDelegationSource(t, d, backend)
			} else {
				_, sourceSessionID, _ = setupDelegationSourceAt(t, d, backend, repo)
			}
			consumeDelegatedPrompt(t, backend)

			request := &protocol.DelegateWorktreeRequest{
				Branch: "feat/from-default",
				Path:   protocol.Ptr(filepath.Join(root, "repo--delegated")),
			}
			if tt.useRepo {
				request.Repo = protocol.Ptr(repo)
			}
			msg := &resolvedDelegationLaunch{
				Cmd:             protocol.CmdDelegate,
				SourceSessionID: protocol.Ptr(sourceSessionID),
				Brief:           protocol.Ptr("Start from the default branch."),
				Placement:       protocol.Ptr(tt.placement),
				Label:           protocol.Ptr("delegated"),
				Worktree:        request,
			}
			if tt.useCWD {
				msg.Cwd = repo
			}

			result, err := d.delegateResolved(msg)
			if err != nil {
				t.Fatalf("delegate() error = %v", err)
			}
			head := gitRevParseDaemon(t, result.Directory, "HEAD")
			if head != ambientHead {
				t.Fatalf("legacy internal launch head = %s, want current checkout head %s; public delegation requires an explicit base", head, ambientHead)
			}
			_ = defaultHead
		})
	}
}

func TestLegacyDelegateWorktreeDoesNotInferARepositoryDefault(t *testing.T) {
	root := t.TempDir()
	repo := initDelegationRepo(t, root, "repo")
	runGitDaemon(t, repo, "branch", "-M", "main")
	mainHead := gitRevParseDaemon(t, repo, "HEAD")

	worktreeA := filepath.Join(root, "repo--feat-a")
	worktreeB := filepath.Join(root, "repo--feat-b")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/a", worktreeA)
	runGitDaemon(t, worktreeA, "commit", "--allow-empty", "-m", "a")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/b", worktreeB)
	runGitDaemon(t, worktreeB, "commit", "--allow-empty", "-m", "b")
	headA := gitRevParseDaemon(t, worktreeA, "HEAD")
	headB := gitRevParseDaemon(t, worktreeB, "HEAD")

	runGitDaemon(t, repo, "checkout", "-q", "-b", "topic/ambient")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "ambient")
	ambientHead := gitRevParseDaemon(t, repo, "HEAD")
	if ambientHead == mainHead {
		t.Fatal("precondition: main checkout HEAD must diverge from the default branch")
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repo,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-a", worktreeA)
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-b", worktreeB)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Same repo, two branches."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/from-default",
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v, want success (repository is unambiguous)", err)
	}
	wantPath := git.CanonicalizePath(git.GenerateWorktreePath(repo, "feat/from-default"))
	if result.Directory != wantPath {
		t.Fatalf("worktree directory = %q, want %q", result.Directory, wantPath)
	}

	head := gitRevParseDaemon(t, result.Directory, "HEAD")
	if head == headA || head == headB {
		t.Fatalf("new branch started from a member session's branch (head %s; feat/a %s, feat/b %s); "+
			"the starting ref must not depend on which session sorts first", head, headA, headB)
	}
	if head != ambientHead {
		t.Fatalf("legacy internal launch head = %s, want current checkout head %s; public delegation requires an explicit base", head, ambientHead)
	}
	_ = mainHead
}

func TestDelegateWorktreeExplicitFromStillWins(t *testing.T) {
	root := t.TempDir()
	repo := initDelegationRepo(t, root, "repo")
	runGitDaemon(t, repo, "branch", "-M", "main")
	worktreeA := filepath.Join(root, "repo--feat-a")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feat/a", worktreeA)
	runGitDaemon(t, worktreeA, "commit", "--allow-empty", "-m", "a")
	headA := gitRevParseDaemon(t, worktreeA, "HEAD")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repo,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-a", worktreeA)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Explicit from."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch:       "feat/explicit-from",
			StartingFrom: protocol.Ptr("feat/a"),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if head := gitRevParseDaemon(t, result.Directory, "HEAD"); head != headA {
		t.Fatalf("new branch head = %s, want feat/a head %s", head, headA)
	}
}

func TestLegacyDelegateWorktreeDoesNotFetchRemoteDefaultBranch(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	runGitDaemon(t, root, "init", "--bare", "-b", "main", origin)

	repo := initDelegationRepo(t, root, "repo")
	runGitDaemon(t, repo, "branch", "-M", "main")
	runGitDaemon(t, repo, "remote", "add", "origin", origin)
	runGitDaemon(t, repo, "push", "-q", "-u", "origin", "main")
	staleLocalHead := gitRevParseDaemon(t, repo, "main")

	other := filepath.Join(root, "other")
	runGitDaemon(t, root, "clone", "-q", origin, other)
	runGitDaemon(t, other, "commit", "--allow-empty", "-m", "upstream advance")
	runGitDaemon(t, other, "push", "-q", "origin", "main")
	upstreamHead := gitRevParseDaemon(t, other, "HEAD")
	if upstreamHead == staleLocalHead {
		t.Fatal("precondition: origin/main must be ahead of the local default branch")
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: repo,
	})
	addWorkspaceSessionAt(t, d, targetWorkspaceID, "session-a", repo)

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Start from upstream."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/from-upstream",
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	head := gitRevParseDaemon(t, result.Directory, "HEAD")
	if head != staleLocalHead {
		t.Fatalf("legacy internal launch fetched or inferred a remote base: head=%s local=%s upstream=%s", head, staleLocalHead, upstreamHead)
	}
}

func TestLegacyDelegateWorktreeExplicitRepoDoesNotInferABase(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	runGitDaemon(t, root, "init", "--bare", "-b", "main", origin)

	repo := initDelegationRepo(t, root, "repo")
	runGitDaemon(t, repo, "branch", "-M", "main")
	runGitDaemon(t, repo, "remote", "add", "origin", origin)
	runGitDaemon(t, repo, "push", "-q", "-u", "origin", "main")

	legacy := filepath.Join(root, "repo--legacy")
	runGitDaemon(t, repo, "worktree", "add", "-b", "topic/legacy", legacy)
	runGitDaemon(t, legacy, "commit", "--allow-empty", "-m", "legacy")
	legacyHead := gitRevParseDaemon(t, legacy, "HEAD")

	runGitDaemon(t, repo, "checkout", "-q", "-b", "topic/ambient")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "ambient")
	ambientHead := gitRevParseDaemon(t, repo, "HEAD")

	other := filepath.Join(root, "other")
	runGitDaemon(t, root, "clone", "-q", origin, other)
	runGitDaemon(t, other, "commit", "--allow-empty", "-m", "upstream advance")
	runGitDaemon(t, other, "push", "-q", "origin", "main")
	upstreamHead := gitRevParseDaemon(t, other, "HEAD")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	targetWorkspaceID := "workspace-target"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: legacy,
	})

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Explicit repo, default ref."),
		Placement:       protocol.Ptr(delegationPlacementExisting),
		WorkspaceID:     protocol.Ptr(targetWorkspaceID),
		Label:           protocol.Ptr("delegated"),
		Worktree: &protocol.DelegateWorktreeRequest{
			Branch: "feat/explicit-repo-default",
			Repo:   protocol.Ptr(repo),
		},
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}

	head := gitRevParseDaemon(t, result.Directory, "HEAD")
	if head == legacyHead {
		t.Fatalf("new branch started from the workspace directory's branch (%s); "+
			"--repo selects the repository, not the starting ref", head)
	}
	if head != ambientHead {
		t.Fatalf("legacy internal launch inferred a base: head=%s ambient=%s upstream=%s", head, ambientHead, upstreamHead)
	}
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

func TestDelegateRefusesDefaultWorktreeFromNonRepoSource(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, cwd := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Fix the parser."),
		Label:           protocol.Ptr("parser"),
		Worktree:        &protocol.DelegateWorktreeRequest{},
	})
	if err == nil {
		t.Fatal("delegate() error = nil, want refusal")
	}
	for _, want := range []string{cwd, "not a git repository", "--cwd", "omit checkout flags"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("delegate() error = %q, want it to mention %q", err, want)
		}
	}
}

func TestDelegateExplicitPlacementBypassesNonRepoGate(t *testing.T) {
	nonRepo := t.TempDir()
	otherWorkspaceDir := t.TempDir()

	cases := []struct {
		name    string
		mutate  func(msg *resolvedDelegationLaunch)
		prepare func(t *testing.T, d *Daemon)
	}{
		{
			name: "no-worktree",
			mutate: func(msg *resolvedDelegationLaunch) {
				msg.Worktree = nil
			},
		},
		{
			name: "cwd",
			mutate: func(msg *resolvedDelegationLaunch) {
				msg.Placement = protocol.Ptr(delegationPlacementNew)
				msg.Cwd = otherWorkspaceDir
			},
		},
		{
			name: "new-workspace",
			mutate: func(msg *resolvedDelegationLaunch) {
				msg.Placement = protocol.Ptr(delegationPlacementNew)
			},
		},
		{
			name: "workspace",
			prepare: func(t *testing.T, d *Daemon) {
				client := newWorkspaceProtocolTestClient()
				d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
					Cmd:       protocol.CmdRegisterWorkspace,
					ID:        "workspace-target",
					Title:     "Target",
					Directory: otherWorkspaceDir,
				})
			},
			mutate: func(msg *resolvedDelegationLaunch) {
				msg.Placement = protocol.Ptr(delegationPlacementExisting)
				msg.WorkspaceID = protocol.Ptr("workspace-target")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			backend := &fakeSpawnBackend{}
			_, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, nonRepo)
			consumeDelegatedPrompt(t, backend)
			if tc.prepare != nil {
				tc.prepare(t, d)
			}

			msg := &resolvedDelegationLaunch{
				Cmd:             protocol.CmdDelegate,
				SourceSessionID: protocol.Ptr(sourceSessionID),
				Brief:           protocol.Ptr("Fix the parser."),
				Label:           protocol.Ptr("parser"),
				Worktree:        &protocol.DelegateWorktreeRequest{},
			}
			tc.mutate(msg)

			result, err := d.delegateResolved(msg)
			if err != nil {
				t.Fatalf("delegate() error = %v, want the explicit placement to proceed", err)
			}
			if protocol.Deref(result.WorktreeCreated) {
				t.Fatalf("result = %+v, want no worktree from a non-repository target", result)
			}
		})
	}
}
