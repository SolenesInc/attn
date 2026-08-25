package daemon

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

const groundTruthMaxLines = 5

const groundTruthMaxLookups = 3

const groundTruthLookupTimeout = 10 * time.Second

type prStateFetcher func(repo string, number int) (state string, merged bool, title string, err error)

const groundTruthMaxPRNumber = 100000

type groundTruthCaps struct {
	lineCap   bool
	lookupCap bool
	timeout   bool
}

func (c groundTruthCaps) any() bool { return c.lineCap || c.lookupCap || c.timeout }

var (
	prHashRefPattern   = regexp.MustCompile(`#(\d+)`)
	prWordRefPattern   = regexp.MustCompile(`(?i)\bPR\s+(\d+)\b`)
	prGitHubURLPattern = regexp.MustCompile(`(?i)github\.com/[\w.-]+/[\w.-]+/pull/(\d+)`)
)

func extractPRRefs(text string) []int {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	var refs []int
	seen := make(map[int]bool)
	add := func(numStr string) {
		n, err := strconv.Atoi(numStr)
		if err != nil || n <= 0 || n > groundTruthMaxPRNumber {
			return
		}
		if seen[n] {
			return
		}
		seen[n] = true
		refs = append(refs, n)
	}

	type match struct {
		pos int
		num string
	}
	var matches []match
	for _, pat := range []*regexp.Regexp{prHashRefPattern, prWordRefPattern, prGitHubURLPattern} {
		for _, loc := range pat.FindAllStringSubmatchIndex(text, -1) {
			matches = append(matches, match{pos: loc[0], num: text[loc[2]:loc[3]]})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].pos < matches[j].pos })
	for _, m := range matches {
		add(m.num)
	}
	return refs
}

// Must stay a positive allowlist: the prs table holds only `is:open` rows and its
// State carries attn's annotation, so a blacklist would call every open PR finished.
var groundTruthTerminalStates = map[string]bool{
	"merged": true,
	"closed": true,
}

func reconcileGroundTruthLines(refs []int, repoSlug string, prs []*protocol.PR) (lines []string, lineCap bool) {
	if repoSlug == "" || len(prs) == 0 || len(refs) == 0 {
		return nil, false
	}

	byNumber := make(map[int]*protocol.PR, len(prs))
	for _, pr := range prs {
		if pr == nil {
			continue
		}
		byNumber[pr.Number] = pr
	}

	for _, n := range refs {
		if len(lines) >= groundTruthMaxLines {
			lineCap = true
			break
		}
		pr, ok := byNumber[n]
		if !ok || pr == nil {
			continue
		}
		if !groundTruthTerminalStates[strings.ToLower(pr.State)] {
			continue
		}
		lines = append(lines, groundTruthLine(n, pr.State, pr.Title))
	}
	return lines, lineCap
}

func groundTruthLine(number int, state, title string) string {
	return fmt.Sprintf(
		"Ground-truth check: PR #%d is %s (%q) — the verdict text may be stale on this point.",
		number, state, title)
}

func groundTruthUntrackedLines(ctx context.Context, refs []int, tracked map[int]bool, repoSlug string, fetch prStateFetcher) (lines []string, caps groundTruthCaps) {
	if fetch == nil || repoSlug == "" || len(refs) == 0 {
		return nil, groundTruthCaps{}
	}

	lookups := 0
	for _, n := range refs {
		if tracked[n] {
			continue
		}
		if len(lines) >= groundTruthMaxLines {
			caps.lineCap = true
			break
		}
		if lookups >= groundTruthMaxLookups {
			caps.lookupCap = true
			break
		}
		if ctx.Err() != nil {
			caps.timeout = true
			break
		}
		lookups++
		state, merged, title, err := fetchPRStateCtx(ctx, fetch, repoSlug, n)
		if err != nil {
			continue
		}
		switch {
		case merged:
			lines = append(lines, groundTruthLine(n, "merged", title))
		case strings.EqualFold(state, "closed"):
			lines = append(lines, groundTruthLine(n, "closed", title))
		}
	}
	return lines, caps
}

// fetchPRStateCtx runs fetch under ctx: github.Client has no context plumbing, so
// the call is abandoned on a goroutine and finishes against its own HTTP timeout.
func fetchPRStateCtx(ctx context.Context, fetch prStateFetcher, repo string, number int) (state string, merged bool, title string, err error) {
	type result struct {
		state  string
		merged bool
		title  string
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		s, m, t, e := fetch(repo, number)
		ch <- result{s, m, t, e}
	}()
	select {
	case r := <-ch:
		return r.state, r.merged, r.title, r.err
	case <-ctx.Done():
		return "", false, "", ctx.Err()
	}
}

func (d *Daemon) reconcileGroundTruth(ctx context.Context, verdict *ticketReconcileVerdict, cwd string) []string {
	if verdict == nil {
		return nil
	}
	host, repoSlug := git.OriginHostOwnerRepo(cwd)
	if repoSlug == "" {
		return nil
	}
	refs := extractPRRefs(verdict.WhatsLeft + "\n" + verdict.Evidence)
	if len(refs) == 0 {
		return nil
	}

	prs := d.store.ListPRsByRepo(repoSlug)
	lines, trackedLineCap := reconcileGroundTruthLines(refs, repoSlug, prs)

	tracked := make(map[int]bool, len(prs))
	for _, pr := range prs {
		if pr != nil {
			tracked[pr.Number] = true
		}
	}

	fetch := d.ticketReconcilePRFetch
	if fetch == nil && d.githubAvailable() {
		if client, ok := d.ghRegistry.Get(host); ok {
			fetch = client.FetchPRState
		}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, groundTruthLookupTimeout)
	defer cancel()
	untracked, caps := groundTruthUntrackedLines(lookupCtx, refs, tracked, repoSlug, fetch)
	lines = append(lines, untracked...)
	if trackedLineCap {
		caps.lineCap = true
	}

	if len(lines) > groundTruthMaxLines {
		lines = lines[:groundTruthMaxLines]
		caps.lineCap = true
	}
	if caps.any() {
		d.logf("ticket reconcile ground-truth %s: annotation cap reached (lineCap=%t lookupCap=%t timeout=%t; refs=%d lines=%d)",
			repoSlug, caps.lineCap, caps.lookupCap, caps.timeout, len(refs), len(lines))
	}
	return lines
}
