package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestExtractPRRefs(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []int
	}{
		{"empty", "", nil},
		{
			"hash ref",
			"still need to merge #462 before closing",
			[]int{462},
		},
		{
			"PR word ref",
			"waiting on PR 462 to land",
			[]int{462},
		},
		{
			"github url ref",
			"see https://github.com/victorarias/attn/pull/462 for details",
			[]int{462},
		},
		{
			"dedupes and preserves first-seen order",
			"mentions #17 twice: once here #17, then PR 462, then #17 again",
			[]int{17, 462},
		},
		{
			"garbage guard drops absurdly large numbers",
			"see #123456789",
			nil,
		},
		{
			"mixed patterns ordered by position",
			"first PR 5 is done, then #3, then https://github.com/o/r/pull/9",
			[]int{5, 3, 9},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractPRRefs(tc.text)
			if !equalIntSlices(got, tc.want) {
				t.Errorf("extractPRRefs(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func groundTruthTestPR(number int, state, title string) *protocol.PR {
	return &protocol.PR{
		ID:     "github.com:victorarias/attn#" + strconv.Itoa(number),
		Host:   "github.com",
		Repo:   "victorarias/attn",
		Number: number,
		Title:  title,
		State:  state,
	}
}

func TestReconcileGroundTruthLines(t *testing.T) {
	prs := []*protocol.PR{
		groundTruthTestPR(462, "merged", "Fix the thing"),
		groundTruthTestPR(470, "closed", "Abandoned approach"),
		groundTruthTestPR(480, "open", "Still cooking"),
	}

	t.Run("merged PR is annotated", func(t *testing.T) {
		lines, _ := reconcileGroundTruthLines([]int{462}, "victorarias/attn", prs)
		if len(lines) != 1 {
			t.Fatalf("lines = %v, want 1", lines)
		}
		if !strings.Contains(lines[0], "PR #462 is merged") || !strings.Contains(lines[0], "Fix the thing") {
			t.Fatalf("unexpected line: %q", lines[0])
		}
	})

	t.Run("closed PR is annotated", func(t *testing.T) {
		lines, _ := reconcileGroundTruthLines([]int{470}, "victorarias/attn", prs)
		if len(lines) != 1 || !strings.Contains(lines[0], "PR #470 is closed") {
			t.Fatalf("lines = %v, want one closed annotation", lines)
		}
	})

	t.Run("open PR is silent", func(t *testing.T) {
		lines, _ := reconcileGroundTruthLines([]int{480}, "victorarias/attn", prs)
		if len(lines) != 0 {
			t.Fatalf("lines = %v, want none (open PR)", lines)
		}
	})

	t.Run("untracked PR number is silent", func(t *testing.T) {
		lines, _ := reconcileGroundTruthLines([]int{999}, "victorarias/attn", prs)
		if len(lines) != 0 {
			t.Fatalf("lines = %v, want none (untracked)", lines)
		}
	})

	t.Run("empty repo slug yields nil", func(t *testing.T) {
		if lines, _ := reconcileGroundTruthLines([]int{462}, "", prs); lines != nil {
			t.Fatalf("lines = %v, want nil", lines)
		}
	})

	t.Run("empty pr list yields nil", func(t *testing.T) {
		if lines, _ := reconcileGroundTruthLines([]int{462}, "victorarias/attn", nil); lines != nil {
			t.Fatalf("lines = %v, want nil", lines)
		}
	})

	t.Run("caps at 5 lines", func(t *testing.T) {
		var manyPRs []*protocol.PR
		var refs []int
		for i := 1; i <= 8; i++ {
			manyPRs = append(manyPRs, groundTruthTestPR(i, "merged", "t"))
			refs = append(refs, i)
		}
		lines, lineCap := reconcileGroundTruthLines(refs, "victorarias/attn", manyPRs)
		if len(lines) != groundTruthMaxLines {
			t.Fatalf("lines = %d, want %d (cap)", len(lines), groundTruthMaxLines)
		}
		if !lineCap {
			t.Fatalf("lineCap = false, want true when %d refs exceed the %d-line cap", len(refs), groundTruthMaxLines)
		}
	})
}

func TestReconcileGroundTruthAnnotatesMergedPR(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	repoDir := t.TempDir()
	runGitDaemon(t, repoDir, "init")
	runGitDaemon(t, repoDir, "remote", "add", "origin", "git@github.com:victorarias/attn.git")

	ticketID := "gt-ticket"
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:       ticketID,
		Title:    "Ship the fix",
		Assignee: "sess-dead",
		Status:   store.TicketStatusInReview,
		Cwd:      repoDir,
	}, "chief", time.Now()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	pr := groundTruthTestPR(462, "merged", "Fix the offset bug")
	d.store.AddPR(pr)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"medium","whats_left":"merge PR #462 pending","evidence":"last turn was still waiting on CI"}`),
			TotalCostUSD:     0.05,
			NumTurns:         2,
		}, nil
	}

	if _, err := d.reconcileJobHandler(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       ticketID,
		Title:          "Ship the fix",
		Brief:          "Land the fix.",
		StatusAtClaim:  store.TicketStatusInReview,
		SessionID:      "sess-dead",
		Agent:          "codex",
		TranscriptPath: transcript,
		CloseContext:   "found orphaned by the periodic sweep",
	})); err != nil {
		t.Fatalf("reconcileJobHandler: %v", err)
	}

	comments := reconcileComments(t, d, ticketID)
	if len(comments) != 1 {
		t.Fatalf("reconcile comments = %d, want 1", len(comments))
	}
	comment := comments[0]
	if !strings.Contains(comment, "What's left: merge PR #462 pending") {
		t.Fatalf("verdict text was altered, comment:\n%s", comment)
	}
	if !strings.Contains(comment, "Ground-truth check: PR #462 is merged") {
		t.Fatalf("missing ground-truth annotation, comment:\n%s", comment)
	}
	if !strings.Contains(comment, "Fix the offset bug") {
		t.Fatalf("annotation missing PR title, comment:\n%s", comment)
	}

	ticket, _ := d.store.GetTicket(ticketID)
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("status = %q, want in_review (annotation never moves the column)", ticket.Status)
	}
}

func TestGroundTruthUntrackedLines(t *testing.T) {
	annotated := func(state string, merged bool, title string) prStateFetcher {
		return func(string, int) (string, bool, string, error) { return state, merged, title, nil }
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name        string
		ctx         context.Context
		refs        []int
		tracked     map[int]bool
		fetch       prStateFetcher
		wantLines   []string
		wantLookups []int
		wantCaps    groundTruthCaps
	}{
		{
			name: "merged", refs: []int{462}, fetch: annotated("closed", true, "Fix the offset bug"),
			wantLines: []string{groundTruthLine(462, "merged", "Fix the offset bug")}, wantLookups: []int{462},
		},
		{
			name: "closed", refs: []int{470}, fetch: annotated("closed", false, "Abandoned approach"),
			wantLines: []string{groundTruthLine(470, "closed", "Abandoned approach")}, wantLookups: []int{470},
		},
		{name: "open stays silent", refs: []int{462}, fetch: annotated("open", false, "Still cooking"), wantLookups: []int{462}},
		{
			name: "a failed lookup stays silent", refs: []int{462},
			fetch:       func(string, int) (string, bool, string, error) { return "", false, "", errors.New("boom") },
			wantLookups: []int{462},
		},
		{name: "no GitHub client", refs: []int{462}},
		{
			name: "tracked refs are not looked up", refs: []int{1, 2, 3}, tracked: map[int]bool{1: true, 2: true},
			fetch: annotated("closed", true, "t"), wantLines: []string{groundTruthLine(3, "merged", "t")}, wantLookups: []int{3},
		},
		{
			name: "lookups are capped", refs: []int{1, 2, 3, 4, 5, 6}, fetch: annotated("closed", true, "t"),
			wantLines:   []string{groundTruthLine(1, "merged", "t"), groundTruthLine(2, "merged", "t"), groundTruthLine(3, "merged", "t")},
			wantLookups: []int{1, 2, 3}, wantCaps: groundTruthCaps{lookupCap: true},
		},
		{
			name: "a cancelled context stops before looking up", ctx: cancelled, refs: []int{1, 2},
			fetch: annotated("closed", true, "t"), wantCaps: groundTruthCaps{timeout: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			var lookups []int
			var fetch prStateFetcher
			if tc.fetch != nil {
				fetch = func(repo string, number int) (string, bool, string, error) {
					if repo != "victorarias/attn" {
						t.Errorf("looked up %d in %q, want victorarias/attn", number, repo)
					}
					lookups = append(lookups, number)
					return tc.fetch(repo, number)
				}
			}
			lines, caps := groundTruthUntrackedLines(ctx, tc.refs, tc.tracked, "victorarias/attn", fetch)
			if !slices.Equal(lines, tc.wantLines) || !slices.Equal(lookups, tc.wantLookups) || caps != tc.wantCaps {
				t.Errorf("lines %q after looking up %v with caps %+v, want %q after %v with %+v", lines, lookups, caps, tc.wantLines, tc.wantLookups, tc.wantCaps)
			}
		})
	}
}
