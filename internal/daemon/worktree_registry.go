package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const integrationBranchTTL = 24 * time.Hour

var errWorktreeStashCounts = errors.New("worktree stash counts unavailable")

type repositoryFacts struct {
	repo              string
	integrationBranch string
	integrationSHA    string
	treeHashes        map[string]bool
	stashes           map[string]int
	mergedBranches    map[string]store.MergedBranch
	liveSessions      map[string][]string
	openSeeds         map[string][]string
	sessionActivity   map[string]time.Time
}

func (d *Daemon) trackedRepositoriesContext(ctx context.Context) ([]string, error) {
	seen := make(map[string]bool)
	var repos []string
	add := func(repo string) {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			return
		}
		repo = git.CanonicalizePath(repo)
		if seen[repo] {
			return
		}
		seen[repo] = true
		repos = append(repos, repo)
	}
	if d.store == nil {
		return nil, nil
	}
	for _, repo := range d.store.ListWorktreeRepos() {
		add(repo)
	}
	// A session in a worktree resolves to its own root, which is not a repository.
	for _, session := range d.store.List("") {
		if repo := strings.TrimSpace(protocol.Deref(session.MainRepo)); repo != "" {
			add(repo)
			continue
		}
		mapped := false
		for _, row := range d.store.ListWorktrees() {
			if pathAtOrBelow(session.Directory, row.Path) {
				add(row.MainRepo)
				d.store.UpdateBranch(session.ID, protocol.Deref(session.Branch), true, row.MainRepo, row.MainRepo)
				mapped = true
				break
			}
		}
		if mapped {
			continue
		}
		root, err := git.RepositoryRootContext(ctx, session.Directory)
		if err != nil {
			return nil, err
		}
		if root != "" {
			add(root)
			d.store.UpdateBranch(session.ID, protocol.Deref(session.Branch), protocol.Deref(session.IsWorktree), root, root)
		}
	}
	return repos, nil
}

// False means the rows are stale and no verdict on them may be acted on.
func (d *Daemon) refreshRepositoryWorktrees(repo string, now time.Time) bool {
	return d.refreshRepositoryWorktreesContext(context.Background(), repo, now)
}

func (d *Daemon) refreshRepositoryWorktreesContext(ctx context.Context, repo string, now time.Time) bool {
	if d.store == nil {
		return false
	}

	states, err := d.reconcileWorktreeRegistryContext(ctx, repo, now)
	if err != nil {
		d.logf("worktree refresh: %s: listing worktrees: %v", repo, err)
		return false
	}

	facts, err := d.repositoryFactsContext(ctx, repo, now)
	if err != nil {
		d.logf("worktree refresh: %s: %v", repo, err)
		for _, state := range states {
			d.store.RecordWorktreeRefreshError(state.Path, err.Error())
		}
		return false
	}

	d.coalesceSnapshots(func() {
		for _, state := range states {
			d.refreshWorktreeRowContext(ctx, facts, state, now)
		}
	})
	return true
}

func (d *Daemon) reconcileWorktreeRegistryContext(ctx context.Context, repo string, now time.Time) ([]git.WorktreeState, error) {
	finish := d.beginGitOperation(protocol.GitOperationKindRefreshRepository, repo, nil)
	states, err := d.listWorktreeStatesContext(ctx, repo)
	finish(err)
	if err != nil {
		return nil, err
	}

	listed := make(map[string]bool, len(states))
	var rows []git.WorktreeState
	for _, state := range states {
		if state.Path == repo {
			continue
		}
		listed[state.Path] = true
		rows = append(rows, state)
		existing := d.store.GetWorktree(state.Path)
		if existing != nil {
			d.store.ApplyWorktreeInventory(state.Path, state.Branch, state.HeadSHA, state.Detached, state.Prunable)
			continue
		}
		d.store.AddWorktree(&store.Worktree{
			Path: state.Path, Branch: state.Branch, MainRepo: repo,
			CreatedAt: now, Origin: store.WorktreeOriginGit,
		})
		d.store.ApplyWorktreeInventory(state.Path, state.Branch, state.HeadSHA, state.Detached, state.Prunable)
	}

	for _, row := range d.store.ListWorktreesByRepo(repo) {
		if !listed[row.Path] {
			d.store.RemoveWorktree(row.Path)
			d.publishFact(FactWorktreeDeleted, row.Path, nil)
		}
	}
	return rows, nil
}

func (d *Daemon) listWorktreeStatesContext(ctx context.Context, repo string) ([]git.WorktreeState, error) {
	if d.worktreeListStates != nil {
		return d.worktreeListStates(ctx, repo)
	}
	return git.ListWorktreeStatesContext(ctx, repo)
}

func (d *Daemon) repositoryFactsContext(ctx context.Context, repo string, now time.Time) (*repositoryFacts, error) {
	if d.worktreeRepositoryFacts != nil {
		return d.worktreeRepositoryFacts(ctx, repo, now)
	}
	facts := &repositoryFacts{repo: repo}

	if err := d.refreshMergedPullRequestsContext(ctx, repo, now); err != nil {
		return nil, err
	}
	integrationBranch, err := d.integrationBranchContext(ctx, repo, now)
	if err != nil {
		return nil, err
	}
	facts.integrationBranch = integrationBranch
	integrationSHA, err := git.OutputContext(ctx, git.OpMetadata, repo, "rev-parse", facts.integrationBranch+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve integration ref %s: %w", facts.integrationBranch, err)
	}
	facts.integrationSHA = strings.TrimSpace(string(integrationSHA))

	finish := d.beginGitOperation(protocol.GitOperationKindRefreshRepository, repo, nil)
	treeHashes, err := git.TreeHashesOnHistoryContext(ctx, repo, facts.integrationSHA)
	if context.Cause(ctx) != nil {
		finish(context.Cause(ctx))
		return nil, context.Cause(ctx)
	}
	if err != nil {
		// Not an error: the pull request record still answers rung 1.
		d.logf("worktree refresh: %s: tree hashes for %s: %v", repo, facts.integrationBranch, err)
		treeHashes = nil
	}
	stashes, stashErr := git.StashCountsByBranchContext(ctx, repo)
	finish(stashErr)
	if stashErr != nil {
		// A missing stash map reads as no stash, and that gate is all that keeps a removal off it.
		return nil, fmt.Errorf("%w for %s: %v", errWorktreeStashCounts, repo, stashErr)
	}
	facts.treeHashes = treeHashes
	facts.stashes = stashes

	facts.mergedBranches = d.store.RepoMergedBranches(repo)
	if facts.mergedBranches == nil {
		facts.mergedBranches = make(map[string]store.MergedBranch)
	}
	for branch, record := range d.store.MergedSessionPullRequestBranches() {
		if _, known := facts.mergedBranches[branch]; !known {
			facts.mergedBranches[branch] = record
		}
	}

	facts.liveSessions = d.liveSessionsByWorktree(repo)
	facts.openSeeds = d.openSeedsByWorktree(repo)
	facts.sessionActivity = d.sessionActivityByWorktree(facts.liveSessions)
	return facts, nil
}

func (d *Daemon) sessionActivityByWorktree(liveSessions map[string][]string) map[string]time.Time {
	activity := make(map[string]time.Time, len(liveSessions))
	for path, sessionIDs := range liveSessions {
		for _, sessionID := range sessionIDs {
			session := d.store.Get(sessionID)
			if session == nil {
				continue
			}
			seen, err := time.Parse(time.RFC3339, session.LastSeen)
			if err == nil && seen.After(activity[path]) {
				activity[path] = seen
			}
		}
	}
	return activity
}

func (d *Daemon) refreshMergedPullRequestsContext(ctx context.Context, repo string, now time.Time) error {
	host, ownerRepo := git.OriginHostOwnerRepo(repo)
	if host == "" || ownerRepo == "" {
		return nil
	}
	if d.ghRegistry == nil {
		return nil
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil
	}
	if limited, resetAt := client.IsRateLimited("core"); limited {
		d.logf("worktree refresh: %s rate limited until %s, merged branches stay as recorded",
			host, resetAt.Format(time.RFC3339))
		return nil
	}

	merged, err := client.ListMergedPullRequestsContext(ctx, ownerRepo)
	if err != nil {
		if context.Cause(ctx) != nil {
			return context.Cause(ctx)
		}
		d.logf("worktree refresh: %s: listing merged pull requests: %v", repo, err)
		return nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}

	branches := make([]store.MergedBranch, 0, len(merged))
	baseCounts := make(map[string]int)
	for _, pr := range merged {
		branches = append(branches, store.MergedBranch{
			Branch: pr.HeadRef, MergedAt: pr.MergedAt, Number: pr.Number,
			URL: pr.URL, HeadSHA: pr.HeadSHA,
		})
		if pr.BaseRef != "" {
			baseCounts[pr.BaseRef]++
		}
	}
	d.store.RecordRepoMergedBranches(repo, branches, now)

	if base, ok := modalBaseBranch(baseCounts); ok {
		d.store.SetRepoIntegrationBranch(repo, base, "pull_requests", now)
	}
	return nil
}

func modalBaseBranch(counts map[string]int) (string, bool) {
	best, bestCount := "", 0
	for branch, count := range counts {
		// Ties break on the name so the resolved branch does not flip between passes.
		if count > bestCount || (count == bestCount && branch < best) {
			best, bestCount = branch, count
		}
	}
	return best, best != ""
}

func (d *Daemon) integrationBranchContext(ctx context.Context, repo string, now time.Time) (string, error) {
	if record := d.store.RepoIntegrationBranch(repo); record != nil && record.Branch != "" {
		resolvedAt, err := time.Parse(time.RFC3339, record.ResolvedAt)
		if err == nil && now.Sub(resolvedAt) < integrationBranchTTL {
			return resolveIntegrationRefContext(ctx, repo, record.Branch)
		}
		if record.Source == "pull_requests" {
			return resolveIntegrationRefContext(ctx, repo, record.Branch)
		}
	}
	branch, err := git.GetDefaultBranchContext(ctx, repo)
	if context.Cause(ctx) != nil {
		return "", context.Cause(ctx)
	}
	if err != nil || branch == "" {
		branch = "main"
	}
	d.store.SetRepoIntegrationBranch(repo, branch, "origin_head", now)
	return resolveIntegrationRefContext(ctx, repo, branch)
}

func resolveIntegrationRef(repo, branch string) string {
	ref, _ := resolveIntegrationRefContext(context.Background(), repo, branch)
	return ref
}

func resolveIntegrationRefContext(ctx context.Context, repo, branch string) (string, error) {
	if strings.HasPrefix(branch, "origin/") {
		return branch, nil
	}
	exists, err := git.RefExistsContext(ctx, repo, "origin/"+branch)
	if err != nil {
		return "", err
	}
	if exists {
		return "origin/" + branch, nil
	}
	return branch, nil
}

func (d *Daemon) refreshWorktreeRowContext(ctx context.Context, facts *repositoryFacts, state git.WorktreeState, now time.Time) {
	before := d.store.GetWorktree(state.Path)
	if before == nil {
		return
	}

	finish := d.beginGitOperation(protocol.GitOperationKindRefreshWorktree, state.Path, nil)
	observation, err := observeWorktreeContext(ctx, facts, state, now)
	finish(err)

	if err != nil {
		if context.Cause(ctx) == nil {
			d.store.RecordWorktreeRefreshError(state.Path, err.Error())
		}
		return
	}
	d.store.RecordWorktreeObservation(state.Path, observation, now)
	after := d.store.GetWorktree(state.Path)
	if after == nil || sameObservation(before, after) {
		return
	}
	d.publishWorktreeState(after)
}

func sameObservation(before, after *store.Worktree) bool {
	return before.Branch == after.Branch &&
		before.HeadSHA == after.HeadSHA &&
		before.Detached == after.Detached &&
		before.Dirty == after.Dirty &&
		before.DirtyFiles == after.DirtyFiles &&
		before.Stashes == after.Stashes &&
		before.Unpushed == after.Unpushed &&
		before.MergedSignal == after.MergedSignal &&
		before.Prunable == after.Prunable &&
		before.LastActivityAt == after.LastActivityAt &&
		before.RefreshError == after.RefreshError
}

func observeWorktree(facts *repositoryFacts, state git.WorktreeState, now time.Time) (store.WorktreeObservation, error) {
	return observeWorktreeContext(context.Background(), facts, state, now)
}

func observeWorktreeContext(ctx context.Context, facts *repositoryFacts, state git.WorktreeState, now time.Time) (store.WorktreeObservation, error) {
	observation := store.WorktreeObservation{
		Branch:   state.Branch,
		HeadSHA:  state.HeadSHA,
		Detached: state.Detached,
		Stashes:  facts.stashes[state.Branch],
	}

	if _, err := os.Stat(state.Path); err != nil {
		observation.Prunable = true
		return observation, nil
	}
	observation.Prunable = state.Prunable

	dirtyFiles, err := git.WorktreeDirtyCountContext(ctx, state.Path)
	if err != nil {
		observation.Error = err.Error()
		return observation, err
	}
	observation.DirtyFiles = dirtyFiles
	observation.Dirty = dirtyFiles > 0

	observation.MergedSignal, err = mergedSignalContext(ctx, facts, state)
	if err != nil {
		return observation, err
	}
	unpushed, err := commitsBeyondTheMergeContext(ctx, facts, state, observation.MergedSignal)
	if err != nil {
		// An uncounted commit reads as no commit, and this count is what keeps the sweep off it.
		observation.Error = err.Error()
		return observation, err
	}
	observation.Unpushed = unpushed
	observation.LastActivityAt, err = worktreeLastActivityContext(ctx, facts, state, now)
	if err != nil {
		return observation, err
	}
	return observation, nil
}

// No patch-id probe: it writes a loose object per branch into the user's repository.
func mergedSignalContext(ctx context.Context, facts *repositoryFacts, state git.WorktreeState) (store.MergedSignal, error) {
	if _, merged := facts.mergedBranches[state.Branch]; merged && state.Branch != "" {
		return store.MergedSignalPullRequest, nil
	}
	ref := state.Branch
	if ref == "" {
		ref = state.HeadSHA
	}
	if ref == "" || facts.integrationBranch == "" {
		return store.MergedSignalNone, nil
	}
	ancestor, err := git.IsAncestorContext(ctx, facts.repo, ref, facts.integrationSHA)
	if err != nil {
		return store.MergedSignalNone, err
	}
	if ancestor {
		return store.MergedSignalAncestor, nil
	}
	if len(facts.treeHashes) > 0 {
		if hash, err := git.TreeHashContext(ctx, facts.repo, ref); err == nil && facts.treeHashes[hash] {
			return store.MergedSignalTree, nil
		} else if context.Cause(ctx) != nil {
			return store.MergedSignalNone, context.Cause(ctx)
		}
	}
	return store.MergedSignalNone, nil
}

func commitsBeyondTheMergeContext(ctx context.Context, facts *repositoryFacts, state git.WorktreeState, signal store.MergedSignal) (int, error) {
	if facts.integrationBranch == "" || state.Branch == "" {
		return 0, nil
	}
	switch signal {
	case store.MergedSignalAncestor, store.MergedSignalTree:
		return 0, nil
	}
	ahead, err := git.CommitsAheadContext(ctx, facts.repo, facts.integrationSHA, state.Branch)
	if err != nil {
		return 0, fmt.Errorf("counting %s past %s: %w", state.Branch, facts.integrationBranch, err)
	}
	if ahead == 0 {
		return 0, nil
	}
	if signal != store.MergedSignalPullRequest {
		return ahead, nil
	}
	record := facts.mergedBranches[state.Branch]
	if record.HeadSHA != "" && record.HeadSHA == state.HeadSHA {
		return 0, nil
	}
	if record.HeadSHA == "" {
		return 0, nil
	}
	beyond, err := git.CommitsAheadContext(ctx, facts.repo, record.HeadSHA, state.Branch)
	if err != nil {
		// Every commit past the integration branch: more than the merge left.
		return ahead, nil
	}
	return beyond, nil
}

func worktreeLastActivityContext(ctx context.Context, facts *repositoryFacts, state git.WorktreeState, now time.Time) (time.Time, error) {
	newest := time.Time{}
	consider := func(candidate time.Time) {
		if candidate.After(newest) && !candidate.After(now) {
			newest = candidate
		}
	}
	if mtime, err := git.NewestTreeModTimeContext(ctx, state.Path); err == nil {
		consider(mtime)
	} else if context.Cause(ctx) != nil {
		return time.Time{}, context.Cause(ctx)
	}
	if committed, err := git.LastCommitTimeContext(ctx, state.Path); err == nil {
		consider(committed)
	} else if context.Cause(ctx) != nil {
		return time.Time{}, context.Cause(ctx)
	}
	consider(facts.sessionActivity[state.Path])
	return newest, nil
}

func (d *Daemon) liveSessionsByWorktree(repo string) map[string][]string {
	byPath := make(map[string][]string)
	if d.store == nil {
		return byPath
	}
	rows := d.store.ListWorktreesByRepo(repo)
	for _, session := range d.store.List("") {
		for _, row := range rows {
			if pathAtOrBelow(session.Directory, row.Path) {
				byPath[row.Path] = append(byPath[row.Path], session.ID)
			}
		}
	}
	return byPath
}

func (d *Daemon) publishWorktreeState(wt *store.Worktree) {
	d.publishFact(FactWorktreeStateChanged, wt.Path, protocolWorktree(wt))
}

func protocolWorktree(wt *store.Worktree) protocol.Worktree {
	out := protocol.Worktree{
		Path:     wt.Path,
		Branch:   wt.Branch,
		MainRepo: wt.MainRepo,
		Origin:   protocol.Ptr(string(wt.Origin)),
		Detached: protocol.Ptr(wt.Detached),
		Dirty:    protocol.Ptr(wt.Dirty),
		Prunable: protocol.Ptr(wt.Prunable),
		Pinned:   protocol.Ptr(wt.Pinned()),
	}
	if !wt.CreatedAt.IsZero() {
		out.CreatedAt = protocol.Ptr(wt.CreatedAt.Format(time.RFC3339))
	}
	if wt.HeadSHA != "" {
		out.HeadSHA = protocol.Ptr(wt.HeadSHA)
	}
	if wt.DirtyFiles > 0 {
		out.DirtyFiles = protocol.Ptr(wt.DirtyFiles)
	}
	if wt.Stashes > 0 {
		out.Stashes = protocol.Ptr(wt.Stashes)
	}
	if wt.Unpushed > 0 {
		out.Unpushed = protocol.Ptr(wt.Unpushed)
	}
	if wt.MergedSignal != store.MergedSignalNone {
		out.MergedSignal = protocol.Ptr(string(wt.MergedSignal))
	}
	if wt.ObservedAt != "" {
		out.ObservedAt = protocol.Ptr(wt.ObservedAt)
	}
	if wt.LastActivityAt != "" {
		out.LastActivityAt = protocol.Ptr(wt.LastActivityAt)
	}
	if wt.PinnedAt != "" {
		out.PinnedAt = protocol.Ptr(wt.PinnedAt)
	}
	if wt.SweepStatus != store.WorktreeSweepUnknown {
		out.SweepStatus = protocol.Ptr(string(wt.SweepStatus))
	}
	if wt.SweepReason != "" {
		out.SweepReason = protocol.Ptr(wt.SweepReason)
	}
	if wt.SweepAt != "" {
		out.SweepAt = protocol.Ptr(wt.SweepAt)
	}
	if wt.RefreshError != "" {
		out.RefreshError = protocol.Ptr(wt.RefreshError)
	}
	return out
}
