package daemon

import (
	"fmt"
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
	client := newProtocolTestClient()
	sessionID := "session-source"
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        sessionID,
		Cwd:       cwd,
		ProfileID: defaultProfileID(t, d.store),
		Placement: &protocol.SessionPlacement{},
		Agent:     protocol.AgentShellValue,
		Cols:      80,
		Rows:      24,
		Label:     protocol.Ptr("Source"),
	})
	expectSpawnResult(t, client, sessionID, true)
	return desktopOf(t, d, sessionID), sessionID, cwd
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

func desktopOf(t testing.TB, d *Daemon, sessionID string) string {
	t.Helper()
	placement, placed, err := d.store.SessionPlacement(sessionID)
	if err != nil {
		t.Fatalf("read the placement of %s: %v", sessionID, err)
	}
	if !placed {
		return ""
	}
	return placement.DesktopID
}

func TestDelegateSpawnsAgentBesideTheSourceWithBrief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	sourceDesktopID, sourceSessionID, cwd := setupDelegationSource(t, d, backend)

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
	if protocol.Deref(result.ProfileID) != defaultProfileID(t, d.store) || result.Directory != cwd {
		t.Fatalf("result = %+v, want the source's profile and directory=%s", result, cwd)
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
	if session == nil || session.ProfileID != defaultProfileID(t, d.store) || session.Agent != protocol.SessionAgentCodex {
		t.Fatalf("delegated session = %+v", session)
	}
	if got := desktopOf(t, d, result.SessionID); got != sourceDesktopID {
		t.Fatalf("delegated session landed on desktop %q, want the source's desktop %s", got, sourceDesktopID)
	}
	childPlacement, _, _ := d.store.SessionPlacement(result.SessionID)
	if protocol.Deref(result.DesktopID) != sourceDesktopID || protocol.Deref(result.PaneID) != childPlacement.PaneID {
		t.Fatalf("delegate result placement = desktop %q pane %q, want %s/%s", protocol.Deref(result.DesktopID), protocol.Deref(result.PaneID), sourceDesktopID, childPlacement.PaneID)
	}
	desktop, err := d.store.GetDesktop(sourceDesktopID)
	if err != nil {
		t.Fatal(err)
	}
	sourcePlacement, _, _ := d.store.SessionPlacement(sourceSessionID)
	if len(desktop.Panes) != 2 || desktop.ActivePaneID != sourcePlacement.PaneID {
		t.Fatalf("desktop panes = %+v active=%s, want the child split beside the source and the source still focused", desktop.Panes, desktop.ActivePaneID)
	}
}

func TestChiefOfStaffDelegationPreservesCoordinationIdentityWithAndWithoutACwd(t *testing.T) {
	for _, withCwd := range []bool{false, true} {
		t.Run(fmt.Sprintf("cwd=%v", withCwd), func(t *testing.T) {
			d := newDelegationDaemon(t)
			backend := &fakeSpawnBackend{}
			_, chiefSessionID, _ := setupDelegationSource(t, d, backend)
			if err := setTestChief(d, chiefSessionID); err != nil {
				t.Fatalf("set chief role: %v", err)
			}
			consumeDelegatedPrompt(t, backend)

			msg := &resolvedDelegationLaunch{
				Cmd:             protocol.CmdDelegate,
				SourceSessionID: protocol.Ptr(chiefSessionID),
				Brief:           protocol.Ptr("Exercise tracked coordination identity."),
				Agent:           protocol.Ptr("codex"),
			}
			if withCwd {
				msg.Cwd = t.TempDir()
			}

			result, err := d.delegateResolved(msg)
			if err != nil {
				t.Fatalf("delegate() error = %v", err)
			}
			spawn, ok := backend.LastSpawn()
			if !ok || spawn.CWD != result.Directory {
				t.Fatalf("spawn = %+v, result = %+v", spawn, result)
			}

			seedID, bound := d.gardenDispatchCrown(result.SessionID)
			if !bound {
				t.Fatalf("delegation bound no seed to session %s", result.SessionID)
			}
			seed, _, err := d.readSeed(seedID)
			if err != nil || seed.TenderSession != result.SessionID {
				t.Fatalf("bound seed = %+v, err=%v", seed, err)
			}
		})
	}
}

func TestDelegateLeavesNoPaneWhenSpawnFails(t *testing.T) {
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
	desktop, err := d.store.GetDesktop(desktopOf(t, d, sourceSessionID))
	if err != nil {
		t.Fatal(err)
	}
	if len(desktop.Panes) != 1 || desktop.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("desktop panes after rollback = %+v, want only the source", desktop.Panes)
	}
}

func TestDelegateStartsBesideTheSourceAtACustomDirectory(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Work in a separate directory."),
		Cwd:             targetDir,
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	targetDir = git.CanonicalizePath(targetDir)
	if result.Directory != targetDir {
		t.Fatalf("result = %+v", result)
	}
	if got, want := desktopOf(t, d, result.SessionID), desktopOf(t, d, sourceSessionID); got != want {
		t.Fatalf("delegated session on desktop %q, want the source's desktop %q", got, want)
	}
}

func TestDelegateNamesTheSessionAndItsPaneFromExplicitName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Name everything from --name."),
		Cwd:             targetDir,
		Label:           protocol.Ptr("launcher"),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	if session := d.store.Get(result.SessionID); session == nil || session.Label != "launcher" {
		t.Fatalf("delegated session = %+v, want label %q", session, "launcher")
	}
	desktop, err := d.store.GetDesktop(desktopOf(t, d, result.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range desktop.Panes {
		if pane.SessionID == result.SessionID && pane.Title != "launcher" {
			t.Fatalf("delegated pane = %+v, want it titled %q", pane, "launcher")
		}
	}
}

func TestDelegateRejectsDuplicateSessionNameOnTheDesktop(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	consumeDelegatedPrompt(t, backend)
	targetDir := t.TempDir()

	if _, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("First agent."),
		Cwd:             targetDir,
		Label:           protocol.Ptr("alpha"),
	}); err != nil {
		t.Fatalf("first delegate() error = %v", err)
	}

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Second agent, same name, same desktop."),
		Cwd:             targetDir,
		Label:           protocol.Ptr("alpha"),
	})
	if err == nil || !strings.Contains(err.Error(), "already used on this desktop") {
		t.Fatalf("second delegate() error = %v, want a duplicate-session error", err)
	}
}

func TestDelegateCreatesWorktreeBesideTheSource(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	sourceDesktopID, sourceSessionID, _ := setupDelegationSourceAt(t, d, backend, mainRepo)
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
	if desktopOf(t, d, result.SessionID) != sourceDesktopID ||
		result.Directory != worktreePath ||
		!protocol.Deref(result.WorktreeCreated) {
		t.Fatalf("result = %+v", result)
	}
	session := d.store.Get(result.SessionID)
	if session == nil ||
		session.Directory != worktreePath ||
		session.Label != "delegated" ||
		protocol.Deref(session.Branch) != "feat/delegated-current" {
		t.Fatalf("delegated worktree session = %+v", session)
	}
}

func TestDelegatePreservesTheWorktreeWhenSpawnFails(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupBackend := &fakeSpawnBackend{}
	sourceDesktopID, sourceSessionID, _ := setupDelegationSourceAt(t, d, setupBackend, mainRepo)
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
	desktop, err := d.store.GetDesktop(sourceDesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if len(desktop.Panes) != 1 || desktop.Panes[0].SessionID != sourceSessionID {
		t.Fatalf("source desktop panes after rollback = %+v", desktop.Panes)
	}
}

func TestDelegateNamedWorktreeStartsFromTheCurrentCheckout(t *testing.T) {
	tests := []struct {
		name    string
		useCWD  bool
		useRepo bool
	}{
		{name: "source checkout"},
		{name: "explicit repo", useRepo: true},
		{name: "cwd", useCWD: true},
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
