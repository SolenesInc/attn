package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func automationResolvedLocationJSON(t *testing.T, mainRepo, worktree string) string {
	t.Helper()
	resolved, err := json.Marshal(automation.ResolvedLocation{Type: "repository_worktree", MainRepository: mainRepo, Worktree: worktree})
	if err != nil {
		t.Fatal(err)
	}
	return string(resolved)
}

func claimTerminalAutomationRun(t *testing.T, s *store.Store, def *store.AutomationDefinition, requestID string, observedAt time.Time, resolvedLocationJSON string) *store.AutomationRun {
	t.Helper()
	run, created, err := s.ClaimManualAutomationRun(def.ID, requestID, "", `{}`, def.Revision, `{}`, observedAt, store.AutomationRunReservation{
		RunID: "run-" + requestID, OccurrenceID: "occ-" + requestID, SeedID: "ticket-" + requestID, SessionID: "session-" + requestID, WorkspaceID: "workspace-" + requestID, PaneID: "pane-" + requestID,
	})
	if err != nil || !created {
		t.Fatalf("claim %s created=%v err=%v", requestID, created, err)
	}
	if err := markAutomationRunDeliveredForTest(s, run.ID, resolvedLocationJSON, observedAt); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.GetAutomationRun(run.ID)
	if err != nil || reloaded == nil {
		t.Fatalf("reload %s: %#v err=%v", requestID, reloaded, err)
	}
	return reloaded
}

func TestAutomationRetentionSweepDirtyWorktreeBlocksPruning(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	root, mainRepo := initProviderTestRepo(t)
	worktree := filepath.Join(root, "repo--dirty")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/dirty", worktree)
	if err := os.WriteFile(filepath.Join(worktree, "untracked.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := store.New()
	d := &Daemon{gitExec: testGitExecutor(t, productionGitExecutorConfig), store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	run := claimTerminalAutomationRun(t, s, def, "dirty-1", old, automationResolvedLocationJSON(t, mainRepo, worktree))

	d.automationRetentionSweepPass(time.Now())

	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("a run with a dirty worktree must not be pruned, got %#v err=%v", got, err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("dirty worktree must not be removed, got err=%v", err)
	}
}

func TestAutomationRetentionSweepCleanWorktreeRemovesEverything(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	root, mainRepo := initProviderTestRepo(t)
	worktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/clean", worktree)

	s := store.New()
	d := &Daemon{gitExec: testGitExecutor(t, productionGitExecutorConfig), store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	run := claimTerminalAutomationRun(t, s, def, "clean-1", old, automationResolvedLocationJSON(t, mainRepo, worktree))

	artifactDir := filepath.Join(root, "automation", "occurrences")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(artifactDir, run.ID+".json")
	if err := os.WriteFile(artifactPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	d.automationRetentionSweepPass(time.Now())

	if got, err := s.GetAutomationRun(run.ID); err != nil || got != nil {
		t.Fatalf("expected the run row to be removed, got %#v err=%v", got, err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("expected the clean worktree to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("expected the occurrence artifact to be removed, stat err=%v", err)
	}
}

func TestAutomationRetentionSweepLiveSessionSkipped(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_RETENTION_KEEP", "0")
	t.Setenv("ATTN_AUTOMATION_RETENTION_MIN_AGE", "1h")
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	run := claimTerminalAutomationRun(t, s, def, "live-1", old, "{}")
	s.Add(&protocol.Session{
		ID: run.SessionID, Label: "reviewer", Agent: string(protocol.SessionAgentCodex), Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: old.Format(time.RFC3339), StateUpdatedAt: old.Format(time.RFC3339), LastSeen: old.Format(time.RFC3339), WorkspaceID: run.WorkspaceID,
	})

	d.automationRetentionSweepPass(time.Now())

	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("a run whose session is still live must not be pruned, got %#v err=%v", got, err)
	}
}
