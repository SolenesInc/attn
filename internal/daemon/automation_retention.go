package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/store"
)

const (
	defaultAutomationRetentionKeep          = 200
	defaultAutomationRetentionMinAge        = 14 * 24 * time.Hour
	defaultAutomationRetentionSweepInterval = time.Hour
)

func automationRetentionKeep() int {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_RETENTION_KEEP")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return defaultAutomationRetentionKeep
}

func automationRetentionMinAge() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_RETENTION_MIN_AGE")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur >= 0 {
			return dur
		}
	}
	return defaultAutomationRetentionMinAge
}

func automationRetentionSweepInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_RETENTION_SWEEP_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultAutomationRetentionSweepInterval
}

// No initial pass at boot: retention must not compete with startup churn.
func (d *Daemon) runAutomationRetentionSweep() {
	ticker := time.NewTicker(automationRetentionSweepInterval())
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.automationRetentionSweepPass(time.Now())
		}
	}
}

func (d *Daemon) automationRetentionSweepPass(now time.Time) {
	if d.store == nil {
		return
	}
	ids, err := d.store.ListAutomationDefinitionIDsIncludingDeleted()
	if err != nil {
		d.logf("automation retention sweep: list definitions: %v", err)
		return
	}
	keep := automationRetentionKeep()
	cutoff := now.Add(-automationRetentionMinAge())
	pruned, keptDirty, boundThread := 0, 0, 0
	for _, defID := range ids {
		candidates, err := d.store.ListPrunableAutomationRuns(defID, keep, cutoff)
		if err != nil {
			d.logf("automation retention sweep: list prunable runs for %s: %v", defID, err)
			continue
		}
		for _, run := range candidates {
			block, err := d.automationRunCleanupSafety(run)
			if err != nil {
				d.logf("automation retention sweep: run %s: %v", run.ID, err)
				continue
			}
			switch block {
			case automationRunCleanupLiveSession:
				continue
			case automationRunCleanupBoundThread:
				boundThread++
				continue
			case automationRunCleanupDirtyWorktree:
				keptDirty++
				d.logf("automation retention sweep: keeping run %s (dirty worktree)", run.ID)
				continue
			}
			if err := d.removeAutomationRunWorktree(run); err != nil {
				d.logf("automation retention sweep: remove worktree for run %s: %v", run.ID, err)
				continue
			}
			if err := d.removeAutomationOccurrenceArtifact(run.ID); err != nil {
				d.logf("automation retention sweep: remove occurrence artifact for run %s: %v", run.ID, err)
				continue
			}
			if err := d.store.DeleteAutomationRun(run.ID); err != nil {
				d.logf("automation retention sweep: delete run %s: %v", run.ID, err)
				continue
			}
			pruned++
		}
	}
	if pruned > 0 || keptDirty > 0 || boundThread > 0 {
		d.logf("automation retention sweep: pruned %d run(s), kept %d dirty, %d bound to a live thread", pruned, keptDirty, boundThread)
	}
}

type automationRunCleanupBlock int

const (
	automationRunCleanupOK automationRunCleanupBlock = iota
	automationRunCleanupLiveSession
	// A thread reuses one session id and worktree across occurrences, so removing it bricks the next continue. Callers must surface it as "examined and kept", never silently skip it.
	automationRunCleanupBoundThread
	automationRunCleanupDirtyWorktree
)

func (d *Daemon) automationRunCleanupSafety(run store.AutomationRun) (automationRunCleanupBlock, error) {
	if run.SessionID != "" && d.store.Get(run.SessionID) != nil {
		return automationRunCleanupLiveSession, nil
	}
	if run.SessionID != "" {
		bound, err := d.store.AutomationSessionHasContinuityBinding(run.SessionID)
		if err != nil {
			return automationRunCleanupOK, err
		}
		if bound {
			return automationRunCleanupBoundThread, nil
		}
	}
	worktree, err := automationRunWorktreePath(run)
	if err != nil {
		return automationRunCleanupOK, err
	}
	if worktree == "" {
		return automationRunCleanupOK, nil
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return automationRunCleanupOK, nil
		}
		return automationRunCleanupOK, statErr
	}
	clean, err := git.IsWorktreeClean(worktree)
	if err != nil {
		return automationRunCleanupOK, err
	}
	if !clean {
		return automationRunCleanupDirtyWorktree, nil
	}
	return automationRunCleanupOK, nil
}

// Resolve from the run's persisted ResolvedLocationJSON, never by path convention: an absent resolved worktree means nothing to remove, not a signal to guess.
func automationRunWorktreePath(run store.AutomationRun) (string, error) {
	if strings.TrimSpace(run.ResolvedLocationJSON) == "" {
		return "", nil
	}
	var resolved automation.ResolvedLocation
	if err := json.Unmarshal([]byte(run.ResolvedLocationJSON), &resolved); err != nil {
		return "", fmt.Errorf("automation run %s resolved location: %w", run.ID, err)
	}
	return resolved.Worktree, nil
}

// Automation worktrees are never registered in the store's worktree registry, so this goes through git.DeleteWorktree directly: doDeleteWorktree's registry-aware path would no-op on them.
func (d *Daemon) removeAutomationRunWorktree(run store.AutomationRun) error {
	if strings.TrimSpace(run.ResolvedLocationJSON) == "" {
		return nil
	}
	var resolved automation.ResolvedLocation
	if err := json.Unmarshal([]byte(run.ResolvedLocationJSON), &resolved); err != nil {
		return fmt.Errorf("automation run %s resolved location: %w", run.ID, err)
	}
	if resolved.Worktree == "" {
		return nil
	}
	if _, err := os.Stat(resolved.Worktree); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return git.DeleteWorktree(resolved.MainRepository, resolved.Worktree, false)
}

// Must mirror ensureAutomationOccurrenceInput's dataRoot fallback exactly, or the sweep and the writer disagree about the file's home.
func (d *Daemon) removeAutomationOccurrenceArtifact(runID string) error {
	root := strings.TrimSpace(d.dataRoot)
	if root == "" {
		root = filepath.Dir(d.socketPath)
	}
	path := filepath.Join(root, "automation", "occurrences", runID+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d *Daemon) automationCleanup(ctx context.Context, id string) (cleaned, keptDirty, keptActive []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("deadline exceeded waiting to run automation cleanup: %w", err)
	}
	definition, err := d.store.GetAutomationDefinitionIncludingDeleted(id)
	if err != nil {
		return nil, nil, nil, err
	}
	if definition == nil {
		return nil, nil, nil, fmt.Errorf("automation %q not found", id)
	}
	runs, err := d.store.ListTerminalAutomationRuns(id)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, run := range runs {
		worktree, werr := automationRunWorktreePath(run)
		if werr != nil {
			d.logf("automation cleanup %s: run %s: %v", id, run.ID, werr)
			continue
		}
		if worktree == "" {
			continue
		}
		if _, statErr := os.Stat(worktree); statErr != nil {
			continue
		}
		block, safetyErr := d.automationRunCleanupSafety(run)
		if safetyErr != nil {
			d.logf("automation cleanup %s: run %s: %v", id, run.ID, safetyErr)
			continue
		}
		switch block {
		case automationRunCleanupLiveSession:
			d.logf("automation cleanup %s: run %s: kept active (live session)", id, run.ID)
			keptActive = append(keptActive, run.ID)
			continue
		case automationRunCleanupBoundThread:
			d.logf("automation cleanup %s: run %s: kept active (bound continuity thread)", id, run.ID)
			keptActive = append(keptActive, run.ID)
			continue
		case automationRunCleanupDirtyWorktree:
			keptDirty = append(keptDirty, run.ID)
			continue
		}
		if removeErr := d.removeAutomationRunWorktree(run); removeErr != nil {
			d.logf("automation cleanup %s: run %s: %v", id, run.ID, removeErr)
			continue
		}
		cleaned = append(cleaned, run.ID)
	}
	if len(cleaned) > 0 || len(keptDirty) > 0 || len(keptActive) > 0 {
		d.logf("automation cleanup %s: cleaned %d worktree(s), kept %d dirty, %d active", id, len(cleaned), len(keptDirty), len(keptActive))
	}
	return cleaned, keptDirty, keptActive, nil
}
