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
		root, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitDeferred}, func(runCtx context.Context, client *git.Client) (string, error) {
			return client.RepositoryRoot(runCtx, session.Directory)
		})
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
	return gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitDeferred}, func(runCtx context.Context, client *git.Client) ([]git.WorktreeState, error) {
		return client.ListWorktreeStates(runCtx, repo)
	})
}

func (d *Daemon) repositoryFactsContext(ctx context.Context, repo string, now time.Time) (*repositoryFacts, error) {
	facts := &repositoryFacts{repo: repo}

	if err := d.refreshMergedPullRequestsContext(ctx, repo, now); err != nil {
		return nil, err
	}
	integrationBranch, err := d.integrationBranchContext(ctx, repo, now)
	if err != nil {
		return nil, err
	}
	facts.integrationBranch = integrationBranch
	type gitFacts struct {
		integrationSHA string
		treeHashes     map[string]bool
		stashes        map[string]int
		treeErr        error
	}
	finish := d.beginGitOperation(protocol.GitOperationKindRefreshRepository, repo, nil)
	observed, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitDeferred}, func(runCtx context.Context, client *git.Client) (gitFacts, error) {
		integrationSHA, resolveErr := client.Output(runCtx, git.OpMetadata, repo, "rev-parse", facts.integrationBranch+"^{commit}")
		if resolveErr != nil {
			return gitFacts{}, fmt.Errorf("resolve integration ref %s: %w", facts.integrationBranch, resolveErr)
		}
		sha := strings.TrimSpace(string(integrationSHA))
		treeHashes, treeErr := client.TreeHashesOnHistory(runCtx, repo, sha)
		if context.Cause(runCtx) != nil {
			return gitFacts{}, context.Cause(runCtx)
		}
		stashes, stashErr := client.StashCountsByBranch(runCtx, repo)
		if stashErr != nil {
			return gitFacts{}, fmt.Errorf("%w for %s: %v", errWorktreeStashCounts, repo, stashErr)
		}
		return gitFacts{integrationSHA: sha, treeHashes: treeHashes, stashes: stashes, treeErr: treeErr}, nil
	})
	if context.Cause(ctx) != nil {
		finish(context.Cause(ctx))
		return nil, context.Cause(ctx)
	}
	if err != nil {
		finish(err)
		return nil, err
	}
	finish(nil)
	facts.integrationSHA = observed.integrationSHA
	facts.treeHashes = observed.treeHashes
	facts.stashes = observed.stashes
	if observed.treeErr != nil {
		d.logf("worktree refresh: %s: tree hashes for %s: %v", repo, facts.integrationBranch, observed.treeErr)
		facts.treeHashes = nil
	}

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
	identity, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitDeferred}, func(runCtx context.Context, client *git.Client) ([2]string, error) {
		host, ownerRepo, runErr := client.OriginHostOwnerRepo(runCtx, repo)
		return [2]string{host, ownerRepo}, runErr
	})
	host, ownerRepo := identity[0], identity[1]
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return nil
	}
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
			return d.resolveIntegrationRefContext(ctx, repo, record.Branch)
		}
		if record.Source == "pull_requests" {
			return d.resolveIntegrationRefContext(ctx, repo, record.Branch)
		}
	}
	branch, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitDeferred}, func(runCtx context.Context, client *git.Client) (string, error) {
		return client.GetDefaultBranch(runCtx, repo)
	})
	if context.Cause(ctx) != nil {
		return "", context.Cause(ctx)
	}
	if err != nil || branch == "" {
		branch = "main"
	}
	d.store.SetRepoIntegrationBranch(repo, branch, "origin_head", now)
	return d.resolveIntegrationRefContext(ctx, repo, branch)
}

func (d *Daemon) resolveIntegrationRefContext(ctx context.Context, repo, branch string) (string, error) {
	if strings.HasPrefix(branch, "origin/") {
		return branch, nil
	}
	exists, err := d.refExists(ctx, gitTask{Kind: gitTaskWorktreeObserve, Lane: gitDeferred}, repo, "origin/"+branch)
	if err != nil {
		return "", err
	}
	if exists {
		return "origin/" + branch, nil
	}
	return branch, nil
}

func (d *Daemon) observeWorktreeContext(ctx context.Context, facts *repositoryFacts, state git.WorktreeState, now time.Time) (store.WorktreeObservation, error) {
	return gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskWorktreeObserve, Lane: gitDeferred}, func(runCtx context.Context, client *git.Client) (store.WorktreeObservation, error) {
		return observeWorktreeWithClient(runCtx, client, facts, state, now)
	})
}

func observeWorktreeWithClient(ctx context.Context, client *git.Client, facts *repositoryFacts, state git.WorktreeState, now time.Time) (store.WorktreeObservation, error) {
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

	dirtyFiles, err := client.WorktreeDirtyCount(ctx, state.Path)
	if err != nil {
		observation.Error = err.Error()
		return observation, err
	}
	observation.DirtyFiles = dirtyFiles
	observation.Dirty = dirtyFiles > 0

	observation.MergedSignal, err = mergedSignalContext(ctx, client, facts, state)
	if err != nil {
		return observation, err
	}
	unpushed, err := commitsBeyondTheMergeContext(ctx, client, facts, state, observation.MergedSignal)
	if err != nil {
		observation.Error = err.Error()
		return observation, err
	}
	observation.Unpushed = unpushed
	observation.LastActivityAt, err = worktreeLastActivityContext(ctx, client, facts, state, now)
	if err != nil {
		return observation, err
	}
	return observation, nil
}

func mergedSignalContext(ctx context.Context, client *git.Client, facts *repositoryFacts, state git.WorktreeState) (store.MergedSignal, error) {
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
	ancestor, err := client.IsAncestor(ctx, facts.repo, ref, facts.integrationSHA)
	if err != nil {
		return store.MergedSignalNone, err
	}
	if ancestor {
		return store.MergedSignalAncestor, nil
	}
	if len(facts.treeHashes) > 0 {
		if hash, err := client.TreeHash(ctx, facts.repo, ref); err == nil && facts.treeHashes[hash] {
			return store.MergedSignalTree, nil
		} else if context.Cause(ctx) != nil {
			return store.MergedSignalNone, context.Cause(ctx)
		}
	}
	return store.MergedSignalNone, nil
}

func commitsBeyondTheMergeContext(ctx context.Context, client *git.Client, facts *repositoryFacts, state git.WorktreeState, signal store.MergedSignal) (int, error) {
	if facts.integrationBranch == "" || state.Branch == "" {
		return 0, nil
	}
	switch signal {
	case store.MergedSignalAncestor, store.MergedSignalTree:
		return 0, nil
	}
	ahead, err := client.CommitsAhead(ctx, facts.repo, facts.integrationSHA, state.Branch)
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
	beyond, err := client.CommitsAhead(ctx, facts.repo, record.HeadSHA, state.Branch)
	if err != nil {
		return ahead, nil
	}
	return beyond, nil
}

func worktreeLastActivityContext(ctx context.Context, client *git.Client, facts *repositoryFacts, state git.WorktreeState, now time.Time) (time.Time, error) {
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
	if committed, err := client.LastCommitTime(ctx, state.Path); err == nil {
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
