package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	worktreeSweepKind    = "worktree_sweep"
	worktreeSweepTimeout = 30 * time.Minute
)

const gardenUnreadableReason = "the home's Garden could not be read, so nothing here is decided"

const defaultWorktreeSweepIdleDays = 14

const defaultWorktreeSweepInterval = time.Hour

func worktreeSweepIdle() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS")); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days >= 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	return defaultWorktreeSweepIdleDays * 24 * time.Hour
}

func worktreeSweepInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_WORKTREE_SWEEP_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultWorktreeSweepInterval
}

func (d *Daemon) worktreeSweepEnabled() bool {
	if d.store == nil {
		return false
	}
	return defaultOnBooleanSetting(d.machineSetting(settingWorktreeSweepEnabled))
}

func (d *Daemon) registerWorktreeSweepCron(runner *jobs.Runner) {
	if err := runner.RegisterCron(
		worktreeSweepKind,
		worktreeSweepInterval(),
		d.worktreeSweepHandler,
		jobs.HandlerConfig{Timeout: worktreeSweepTimeout},
	); err != nil {
		d.logf("worktree sweep: register tick: %v", err)
	}
}

func (d *Daemon) worktreeSweepHandler(ctx context.Context, _ *jobs.Job) (any, error) {
	refreshed, removed, kept, err := d.runWorktreeSweep(ctx, time.Now())
	if errors.Is(err, errAutomaticWorktreeCleanupPreempted) {
		err = nil
	}
	return map[string]any{"refreshed": refreshed, "removed": removed, "kept": kept}, err
}

type worktreeSweepCandidate struct {
	repo  string
	state attngit.WorktreeState
}

func (d *Daemon) runWorktreeSweep(ctx context.Context, now time.Time) (refreshed, removed, kept int, err error) {
	err = d.worktreeMaintenance.RunSweep(ctx, func(lease *worktreeSweepLease) error {
		refreshed, removed, kept, err = d.worktreeSweepPassWithLease(lease, now)
		return err
	})
	return refreshed, removed, kept, err
}

func (d *Daemon) worktreeSweepPassWithLease(lease *worktreeSweepLease, now time.Time) (refreshed, removed, kept int, err error) {
	if d.store == nil {
		return 0, 0, 0, nil
	}

	ctx := lease.Context()
	repos := d.store.MonitoredWorktreeRepositories()
	candidatesByRepo := make(map[string][]worktreeSweepCandidate)
	for _, repo := range repos {
		if cause := context.Cause(ctx); cause != nil {
			return refreshed, removed, kept, cause
		}
		states, listErr := d.reconcileWorktreeRegistryContext(ctx, repo, now)
		if listErr != nil {
			if context.Cause(ctx) != nil {
				return refreshed, removed, kept, context.Cause(ctx)
			}
			for _, wt := range d.store.ListWorktreesByRepo(repo) {
				d.recordSweepVerdict(wt, sweepVerdict{store.WorktreeSweepUnknown,
					"the repository could not be refreshed, so nothing here is decided", now}, now)
				kept++
			}
			continue
		}
		refreshed++
		facts, factsErr := d.sweepContext(ctx, repo)
		if factsErr != nil {
			if context.Cause(ctx) != nil {
				return refreshed, removed, kept, context.Cause(ctx)
			}
			d.logf("worktree sweep: %s: %v", repo, factsErr)
			for _, wt := range d.store.ListWorktreesByRepo(repo) {
				d.recordSweepVerdict(wt, sweepVerdict{store.WorktreeSweepUnknown, gardenUnreadableReason, time.Time{}}, now)
				kept++
			}
			continue
		}
		for _, state := range states {
			if state.Path == repo {
				continue
			}
			wt := d.store.GetWorktree(state.Path)
			if wt == nil {
				continue
			}
			if verdict, cheap := cheapWorktreeSweepVerdict(wt, state, facts, now, worktreeSweepIdle()); cheap {
				d.recordSweepVerdict(wt, verdict, now)
				kept++
				continue
			}
			candidatesByRepo[repo] = append(candidatesByRepo[repo], worktreeSweepCandidate{repo: repo, state: state})
		}
	}

	for _, repo := range repos {
		candidates := candidatesByRepo[repo]
		if len(candidates) == 0 {
			continue
		}
		facts, factsErr := d.repositoryFactsContext(ctx, repo, now)
		if factsErr != nil {
			if context.Cause(ctx) != nil {
				return refreshed, removed, kept, context.Cause(ctx)
			}
			for _, candidate := range candidates {
				reason := "last refresh failed: " + factsErr.Error()
				if errors.Is(factsErr, errWorktreeStashCounts) {
					reason = "the repository could not be refreshed, so nothing here is decided"
				}
				d.recordSweepRefreshFailure(candidate.state.Path, factsErr, reason, now)
				kept++
			}
			continue
		}
		for _, candidate := range candidates {
			if cause := context.Cause(ctx); cause != nil {
				return refreshed, removed, kept, cause
			}
			observation, observeErr := d.observeWorktreeContext(ctx, facts, candidate.state, now)
			if observeErr != nil {
				if context.Cause(ctx) != nil {
					return refreshed, removed, kept, context.Cause(ctx)
				}
				d.recordSweepRefreshFailure(candidate.state.Path, observeErr, "last refresh failed: "+observeErr.Error(), now)
				kept++
				continue
			}
			if cause := context.Cause(ctx); cause != nil {
				return refreshed, removed, kept, cause
			}
			d.store.RecordWorktreeObservation(candidate.state.Path, observation, now)
			wt := d.store.GetWorktree(candidate.state.Path)
			current, currentErr := d.sweepContext(ctx, repo)
			if currentErr != nil {
				if context.Cause(ctx) != nil {
					return refreshed, removed, kept, context.Cause(ctx)
				}
				d.logf("worktree sweep: %s: %v", repo, currentErr)
				d.recordSweepVerdict(wt, sweepVerdict{store.WorktreeSweepUnknown, gardenUnreadableReason, time.Time{}}, now)
				kept++
				continue
			}
			verdict := worktreeSweepVerdict(wt, current, now, worktreeSweepIdle())
			d.recordSweepVerdict(wt, verdict, now)
			if verdict.Status != store.WorktreeSweepRemoved {
				kept++
				continue
			}
			if !d.worktreeSweepEnabled() {
				d.recordSweepVerdict(wt, sweepVerdict{store.WorktreeSweepScheduled,
					"eligible now; the sweep is off (Settings › Files and locations › Worktree sweep)", now}, now)
				kept++
				continue
			}
			removal := deleteWorktreeOptions{RemovalAction: "removed", RemovalReason: verdict.Reason}
			deleteErr := lease.TryAutomaticRemoval(func(protection automaticWorktreeCleanupProtection) error {
				if err := d.finalWorktreeSweepGitCheck(protection.Context(), candidate); err != nil {
					return err
				}
				if err := d.finalWorktreeSweepProtectionCheck(protection.Context(), candidate); err != nil {
					return err
				}
				if failure := d.removeWorktreeCheckout(protection, wt, removal); failure != nil {
					return failure.err
				}
				return nil
			})
			if errors.Is(deleteErr, errAutomaticWorktreeCleanupPreempted) {
				return refreshed, removed, kept, deleteErr
			}
			if deleteErr != nil {
				d.recordSweptWorktreeFailure(wt, deleteErr, now)
				kept++
				continue
			}
			d.recordWorktreeRemoval(wt, removal, now)
			d.logf("worktree sweep: reclaimed %s (%s)", wt.Path, verdict.Reason)
			removed++
		}
	}
	if removed > 0 {
		d.logf("worktree sweep: reclaimed %d worktree(s), kept %d", removed, kept)
	}
	return refreshed, removed, kept, nil
}

func cheapWorktreeSweepVerdict(wt *store.Worktree, state attngit.WorktreeState, facts sweepContext, now time.Time, idleFor time.Duration) (sweepVerdict, bool) {
	if state.Prunable {
		return sweepVerdict{store.WorktreeSweepKeptStale, "git still lists it but the directory is gone; delete the row to drop the record", time.Time{}}, true
	}
	if state.Locked {
		return sweepVerdict{store.WorktreeSweepUnknown, "git reports the worktree locked, so this pass leaves it alone", time.Time{}}, true
	}
	if wt.Pinned() {
		return sweepVerdict{store.WorktreeSweepPinned, "kept forever by you", time.Time{}}, true
	}
	if sessions := facts.liveSessions[wt.Path]; len(sessions) > 0 {
		return sweepVerdict{store.WorktreeSweepKeptLiveSession, fmt.Sprintf("%s is running in it", strings.Join(sessions, ", ")), time.Time{}}, true
	}
	if seeds := facts.openSeeds[wt.Path]; len(seeds) > 0 {
		return sweepVerdict{store.WorktreeSweepKeptOpenSeed, fmt.Sprintf("open seed %s points at it", strings.Join(seeds, ", ")), time.Time{}}, true
	}
	floor := wt.CreatedAt
	if activity, parseErr := time.Parse(time.RFC3339, wt.LastActivityAt); parseErr == nil && activity.After(floor) {
		floor = activity
	}
	if !floor.IsZero() && now.Before(floor.Add(idleFor)) {
		return sweepVerdict{store.WorktreeSweepScheduled,
			fmt.Sprintf("too young for deep inspection; age floor is %d of %d days", int(now.Sub(floor).Hours()/24), int(idleFor.Hours()/24)), floor.Add(idleFor)}, true
	}
	return sweepVerdict{}, false
}

func (d *Daemon) finalWorktreeSweepGitCheck(ctx context.Context, candidate worktreeSweepCandidate) error {
	states, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitInteractive}, func(runCtx context.Context, client *attngit.Client) ([]attngit.WorktreeState, error) {
		return client.ListWorktreeStates(runCtx, candidate.repo)
	})
	if err != nil {
		return err
	}
	matched := false
	for _, state := range states {
		if state.Path != candidate.state.Path {
			continue
		}
		matched = state.Branch == candidate.state.Branch && state.HeadSHA == candidate.state.HeadSHA && state.Detached == candidate.state.Detached && !state.Locked && !state.Prunable
		break
	}
	if !matched {
		return errors.New("worktree identity changed before deletion")
	}
	return nil
}

func (d *Daemon) finalWorktreeSweepProtectionCheck(ctx context.Context, candidate worktreeSweepCandidate) error {
	openSeeds, err := d.openSeedsByWorktree(ctx, candidate.repo)
	if err != nil {
		return err
	}
	wt := d.store.GetWorktree(candidate.state.Path)
	if wt == nil || wt.Pinned() || len(d.liveSessionsByWorktree(candidate.repo)[wt.Path]) > 0 || len(openSeeds[wt.Path]) > 0 {
		return errors.New("worktree gained protection before deletion")
	}
	return nil
}

type sweepContext struct {
	liveSessions map[string][]string
	openSeeds    map[string][]string
	integration  string
}

func (d *Daemon) sweepContext(ctx context.Context, repo string) (sweepContext, error) {
	openSeeds, err := d.openSeedsByWorktree(ctx, repo)
	if err != nil {
		return sweepContext{}, err
	}
	return sweepContext{
		liveSessions: d.liveSessionsByWorktree(repo),
		openSeeds:    openSeeds,
		integration:  d.storedIntegrationBranch(repo),
	}, nil
}

func (d *Daemon) storedIntegrationBranch(repo string) string {
	if record := d.store.RepoIntegrationBranch(repo); record != nil {
		return record.Branch
	}
	return ""
}

type sweepVerdict struct {
	Status store.WorktreeSweepStatus
	Reason string
	At     time.Time
}

func worktreeSweepVerdict(wt *store.Worktree, facts sweepContext, now time.Time, idleFor time.Duration) sweepVerdict {
	if wt.Prunable {
		return sweepVerdict{store.WorktreeSweepKeptStale,
			"git still lists it but the directory is gone; delete the row to drop the record", time.Time{}}
	}
	if wt.Pinned() {
		return sweepVerdict{store.WorktreeSweepPinned, "kept forever by you", time.Time{}}
	}
	if sessions := facts.liveSessions[wt.Path]; len(sessions) > 0 {
		return sweepVerdict{store.WorktreeSweepKeptLiveSession,
			fmt.Sprintf("%s is running in it", strings.Join(sessions, ", ")), time.Time{}}
	}
	if seeds := facts.openSeeds[wt.Path]; len(seeds) > 0 {
		return sweepVerdict{store.WorktreeSweepKeptOpenSeed,
			fmt.Sprintf("open seed %s points at it", strings.Join(seeds, ", ")), time.Time{}}
	}
	if wt.ObservedAt == "" {
		return sweepVerdict{store.WorktreeSweepUnknown, "not refreshed yet", time.Time{}}
	}
	if wt.RefreshError != "" {
		return sweepVerdict{store.WorktreeSweepUnknown,
			"last refresh failed: " + wt.RefreshError, time.Time{}}
	}
	if wt.Dirty {
		return sweepVerdict{store.WorktreeSweepKeptDirty,
			fmt.Sprintf("%d uncommitted or untracked file(s)", wt.DirtyFiles), time.Time{}}
	}
	if wt.Stashes > 0 {
		return sweepVerdict{store.WorktreeSweepKeptDirty,
			fmt.Sprintf("%d stash entr%s on %s", wt.Stashes, plural(wt.Stashes, "y", "ies"), wt.Branch), time.Time{}}
	}
	base := facts.integration
	if base == "" {
		base = "the integration branch"
	}
	if wt.Detached && wt.MergedSignal != store.MergedSignalAncestor {
		return sweepVerdict{store.WorktreeSweepKeptDetached,
			fmt.Sprintf("detached HEAD at %s is not on %s, so its commits are on no branch", shortSHA(wt.HeadSHA), base), time.Time{}}
	}
	if wt.MergedSignal == store.MergedSignalNone {
		return sweepVerdict{store.WorktreeSweepKeptUnmerged,
			fmt.Sprintf("no merged signal for %s against %s", wt.Branch, base), time.Time{}}
	}
	if wt.Unpushed > 0 {
		return sweepVerdict{store.WorktreeSweepKeptUnpushed,
			fmt.Sprintf("%d commit(s) on %s the merge does not account for", wt.Unpushed, wt.Branch), time.Time{}}
	}

	lastActivity, err := time.Parse(time.RFC3339, wt.LastActivityAt)
	if err != nil {
		return sweepVerdict{store.WorktreeSweepUnknown, "no activity date observed yet", time.Time{}}
	}
	if wt.CreatedAt.After(lastActivity) {
		lastActivity = wt.CreatedAt
	}
	eligibleAt := lastActivity.Add(idleFor)
	if now.Before(eligibleAt) {
		return sweepVerdict{store.WorktreeSweepScheduled,
			fmt.Sprintf("merged and clean; idle %d of %d days",
				int(now.Sub(lastActivity).Hours()/24), int(idleFor.Hours()/24)), eligibleAt}
	}
	return sweepVerdict{store.WorktreeSweepRemoved,
		fmt.Sprintf("merged (%s) and clean, idle %d days", wt.MergedSignal, int(now.Sub(lastActivity).Hours()/24)), now}
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func (d *Daemon) recordSweepVerdict(wt *store.Worktree, verdict sweepVerdict, now time.Time) {
	if verdict.Status == store.WorktreeSweepRemoved {
		return
	}
	if wt.SweepStatus == verdict.Status && wt.SweepReason == verdict.Reason {
		return
	}
	d.store.SetWorktreeSweep(wt.Path, verdict.Status, verdict.Reason, verdict.At)
	if fresh := d.store.GetWorktree(wt.Path); fresh != nil {
		d.publishWorktreeState(fresh)
	}
}

func (d *Daemon) recordSweepRefreshFailure(path string, err error, reason string, now time.Time) {
	d.store.RecordWorktreeRefreshError(path, err.Error())
	if wt := d.store.GetWorktree(path); wt != nil {
		d.recordSweepVerdict(wt, sweepVerdict{store.WorktreeSweepUnknown, reason, time.Time{}}, now)
	}
}

func (d *Daemon) recordSweptWorktreeFailure(wt *store.Worktree, err error, now time.Time) {
	d.logf("worktree sweep: removing %s: %v", wt.Path, err)
	entry := store.WorktreeSweepLogEntry{
		Path: wt.Path, MainRepo: wt.MainRepo, Branch: wt.Branch,
		Action: "failed", Reason: err.Error(),
	}
	entry.ID = d.store.AppendWorktreeSweepLog(entry, now)
	d.publishFact(FactWorktreeSwept, wt.Path, protocolSweepEntry(entry))
}

func removalActionAndReason(opts deleteWorktreeOptions) (string, string) {
	if opts.RemovalAction == "" {
		return "deleted", "at your request"
	}
	return opts.RemovalAction, opts.RemovalReason
}

func worktreeRemovalNote(wt *store.Worktree, opts deleteWorktreeOptions) string {
	action, reason := removalActionAndReason(opts)
	return fmt.Sprintf("attn %s the worktree %s (branch %s of %s): %s.",
		action, wt.Path, wt.Branch, wt.MainRepo, reason)
}

func (d *Daemon) recordWorktreeRemoval(wt *store.Worktree, opts deleteWorktreeOptions, now time.Time) {
	action, reason := removalActionAndReason(opts)
	entry := store.WorktreeSweepLogEntry{
		Path: wt.Path, MainRepo: wt.MainRepo, Branch: wt.Branch,
		Action: action, Reason: reason,
	}
	entry.ID = d.store.AppendWorktreeSweepLog(entry, now)
	d.publishFact(FactWorktreeSwept, wt.Path, protocolSweepEntry(entry))
}

func (d *Daemon) seedsForWorktree(wt *store.Worktree) []string {
	var seeds []string
	d.eachSeedExecution(func(seed garden.Seed, dispatch garden.Execution) {
		if worktreeOwnsExecution(wt, dispatch) {
			seeds = append(seeds, seed.ID)
		}
	})
	return seeds
}

func (d *Daemon) openSeedsByWorktree(ctx context.Context, repo string) (map[string][]string, error) {
	byPath := make(map[string][]string)
	rows := d.store.ListWorktreesByRepo(repo)
	if len(rows) == 0 {
		return byPath, nil
	}
	checkouts := make([]protocol.WorktreeCheckout, 0, len(rows))
	for _, row := range rows {
		checkouts = append(checkouts, protocol.WorktreeCheckout{Path: row.Path, Branch: row.Branch})
	}
	owners, err := Call[protocol.WorktreeSeedOwners](ctx, d.router(), protocol.WorktreeSeedOwnersMessage{
		Cmd: protocol.CmdWorktreeSeedOwners, MainRepo: repo, Worktrees: checkouts,
	})
	if err != nil {
		return nil, fmt.Errorf("reading the home's Garden: %w", err)
	}
	for _, owner := range owners.Owners {
		byPath[owner.Path] = owner.Seeds
	}
	return byPath, nil
}

func (d *Daemon) worktreeSeedOwnersHere(_ context.Context, msg protocol.WorktreeSeedOwnersMessage) (protocol.WorktreeSeedOwners, error) {
	seedsByPath := make(map[string][]string)
	d.eachSeedExecution(func(seed garden.Seed, dispatch garden.Execution) {
		if garden.Closed(seed.Status) {
			return
		}
		for _, checkout := range msg.Worktrees {
			if worktreeOwnsExecution(&store.Worktree{Path: checkout.Path, MainRepo: msg.MainRepo, Branch: checkout.Branch}, dispatch) {
				seedsByPath[checkout.Path] = append(seedsByPath[checkout.Path], seed.ID)
			}
		}
	})
	owners := protocol.WorktreeSeedOwners{Owners: []protocol.WorktreeSeedOwner{}}
	for _, checkout := range msg.Worktrees {
		if seeds := seedsByPath[checkout.Path]; len(seeds) > 0 {
			owners.Owners = append(owners.Owners, protocol.WorktreeSeedOwner{Path: checkout.Path, Seeds: seeds})
		}
	}
	return owners, nil
}

func worktreeOwnsExecution(wt *store.Worktree, dispatch garden.Execution) bool {
	if pathAtOrBelow(dispatch.Cwd, wt.Path) {
		return true
	}
	return wt.Branch != "" && strings.TrimSpace(dispatch.Branch) == wt.Branch &&
		attngit.CanonicalizePath(dispatch.RepositoryRoot) == attngit.CanonicalizePath(wt.MainRepo)
}

func (d *Daemon) eachSeedExecution(visit func(garden.Seed, garden.Execution)) {
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace: garden.Namespace, Collection: garden.CollectionSeeds,
			Limit: docstore.MaxLimit, After: after,
		})
		if err != nil {
			d.logf("worktree sweep: reading seeds: %v", err)
			return
		}
		for _, doc := range read.Documents {
			seed, err := garden.Decode(doc.Body)
			if err != nil || protocol.TrimID(seed.LastExecutionID) == "" {
				continue
			}
			dispatch, ok := d.gardenDispatch(seed.LastExecutionID)
			if !ok {
				continue
			}
			visit(seed, dispatch)
		}
		if len(read.Documents) < docstore.MaxLimit {
			return
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}

func protocolSweepEntry(entry store.WorktreeSweepLogEntry) protocol.WorktreeSweepEntry {
	return protocol.WorktreeSweepEntry{
		ID: entry.ID, Path: entry.Path, MainRepo: entry.MainRepo,
		Branch: protocol.Ptr(entry.Branch), Action: entry.Action,
		Reason: protocol.Ptr(entry.Reason), At: entry.At,
	}
}
