package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func TestSanitizeSessionTitle(t *testing.T) {
	longMultibyte := strings.Repeat("é", 60) // multibyte rune: a byte-based cut would corrupt/mis-truncate this
	wantLongMultibyte := strings.Repeat("é", maxSessionTitleRunes)

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "Fix login flow", "Fix login flow"},
		{"double_quotes", `"Fix login flow"`, "Fix login flow"},
		{"backticks", "`Fix login flow`", "Fix login flow"},
		{"curly_quotes", "“Fix login flow”", "Fix login flow"},
		{"title_prefix", "Title: Fix login flow", "Fix login flow"},
		{"title_prefix_lower", "title: fix login flow", "fix login flow"},
		{"multiline_first_nonempty", "\n  \nFix login flow\nsecond line", "Fix login flow"},
		{"internal_whitespace_collapsed", "Fix   login\tflow", "Fix login flow"},
		{"trailing_punctuation", "Fix login flow.", "Fix login flow"},
		{"trailing_punctuation_mixed", "Fix login flow!;", "Fix login flow"},
		{"over_limit_multibyte", longMultibyte, wantLongMultibyte},
		{"empty", "", ""},
		{"whitespace_only", "   \n\t  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeSessionTitle(tc.raw)
			if got != tc.want {
				t.Errorf("sanitizeSessionTitle(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDefaultSessionLabel(t *testing.T) {
	cases := []struct {
		name      string
		cwd       string
		sessionID string
		want      string
	}{
		{"normal_path", "/Users/victor/projects/attn", "sess-1", "attn"},
		{"empty_cwd", "", "sess-1", "sess-1"},
		{"dot_cwd", ".", "sess-1", "sess-1"},
		{"root_cwd", string(filepath.Separator), "sess-1", "sess-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultSessionLabel(tc.cwd, tc.sessionID)
			if got != tc.want {
				t.Errorf("defaultSessionLabel(%q, %q) = %q, want %q", tc.cwd, tc.sessionID, got, tc.want)
			}
		})
	}
}

func writeSessionTitleTranscript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"type":"session_meta","cwd":"/tmp"}`,
		`{"type":"user","origin":{"kind":"human"},"message":{"content":"fix the login flow"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"looking into it"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func writeSessionTitleTranscriptNoUserContent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"type":"session_meta","cwd":"/tmp"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","text":"some tool output"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func seedSessionTitleSession(t *testing.T, d *Daemon, id, directory, label string) *protocol.Session {
	t.Helper()
	if label == "" {
		label = defaultSessionLabel(directory, id)
	}
	session := &protocol.Session{
		ID:        id,
		Label:     label,
		Agent:     protocol.SessionAgentClaude,
		Directory: directory,
		State:     protocol.SessionStateIdle,
	}
	d.store.Add(session)
	return session
}

func newSessionTitleDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	installSessionTitleRunner(t, d)
	return d
}

func installSessionTitleRunner(t *testing.T, d *Daemon) *jobs.Runner {
	t.Helper()
	runner := jobs.New(jobs.Options{
		Store:        newTestJobStore(t, d),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
		BackoffBase:  time.Millisecond,
	})
	if err := runner.RegisterWith(sessionTitleKind, d.sessionTitleHandler,
		jobs.HandlerConfig{Timeout: sessionTitleTimeout}); err != nil {
		t.Fatalf("register session_title: %v", err)
	}
	d.jobQueue = runner
	return runner
}

func runSessionTitleJobs(t *testing.T, d *Daemon) {
	t.Helper()
	runner := d.jobQueueRef()
	if runner == nil {
		return
	}
	all, err := runner.List()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	for _, job := range all {
		if job.Kind != sessionTitleKind {
			continue
		}
		job.CommitGuard = &jobs.CommitGuard{}
		_, _ = d.sessionTitleHandler(context.Background(), job)
		runner.Remove(job.ID)
	}
}

func TestSessionTitle_FailedRunRetriesOnTheJobsRunner(t *testing.T) {
	d := newDaemonForTest(t)
	runner := installSessionTitleRunner(t, d)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("exit status 1")
		}
		return "Fix login flow", nil
	}
	settled := make(chan jobs.State, 4)
	runner.OnChange(func(jobID string) {
		if job, _ := runner.Get(jobID); job != nil && (job.State == jobs.StateDone || job.State == jobs.StateDead) {
			settled <- job.State
		}
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)

	d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
	if state := <-settled; state != jobs.StateDone {
		t.Fatalf("job state = %s, want done after a retry", state)
	}
	if calls != 2 {
		t.Fatalf("exec calls = %d, want 2 (one failure, one retry)", calls)
	}
	if got := d.store.Get("sess-1"); got == nil || got.Label != "Fix login flow" {
		t.Fatalf("session label = %+v, want %q", got, "Fix login flow")
	}
}

func TestSessionTitle_GivesUpAfterTheAttemptCap(t *testing.T) {
	d := newDaemonForTest(t)
	runner := installSessionTitleRunner(t, d)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "", errors.New("exit status 1")
	}
	dead := make(chan struct{}, 1)
	runner.OnTerminalFailure(func(*jobs.Job) { dead <- struct{}{} })
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)

	d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
	<-dead
	if calls != sessionTitleAttempts {
		t.Fatalf("exec calls = %d, want %d", calls, sessionTitleAttempts)
	}
	d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
	if calls != sessionTitleAttempts {
		t.Fatalf("a dead title job was re-armed: exec calls = %d", calls)
	}
}

type failingSaveJobStore struct {
	jobs.Store
	fail bool
}

func (s *failingSaveJobStore) Save(j *jobs.Job) error {
	if s.fail {
		return errors.New("disk full")
	}
	return s.Store.Save(j)
}

func TestSessionTitle_EnqueueFailureLeavesTheAttemptAvailable(t *testing.T) {
	d := newDaemonForTest(t)
	store := &failingSaveJobStore{Store: newTestJobStore(t, d), fail: true}
	runner := jobs.New(jobs.Options{Store: store, Log: func(string, ...interface{}) {}})
	if err := runner.RegisterWith(sessionTitleKind, d.sessionTitleHandler, jobs.HandlerConfig{Timeout: sessionTitleTimeout}); err != nil {
		t.Fatalf("register: %v", err)
	}
	d.jobQueue = runner
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	d.rememberSessionTitleInitialPrompt("sess-1", "investigate the retry queue")
	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "Investigate retry queue", nil
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-1", "investigate the retry queue", sessionInputOrigin{})
	runSessionTitleJobs(t, d)
	if calls != 0 {
		t.Fatalf("exec calls after a failed enqueue = %d, want 0", calls)
	}
	d.sessionTitleMu.Lock()
	_, attempted := d.sessionTitleAttempted["sess-1"]
	_, fingerprint := d.sessionTitleInitialPrompt["sess-1"]
	d.sessionTitleMu.Unlock()
	if attempted || !fingerprint {
		t.Fatalf("after a failed enqueue attempted=%v fingerprint=%v, want the attempt and the initial-prompt marker back", attempted, fingerprint)
	}

	store.fail = false
	d.maybeGenerateSessionTitleFromPrompt("sess-1", "investigate the retry queue", sessionInputOrigin{})
	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls once the store recovered = %d, want 1", calls)
	}
	if got := d.store.Get("sess-1"); got == nil || got.Label != "Investigate retry queue" {
		t.Fatalf("session label = %+v, want %q", got, "Investigate retry queue")
	}
}

func TestSessionTitle_CancelledRunDoesNotCommitTheLabel(t *testing.T) {
	d := newDaemonForTest(t)
	runner := installSessionTitleRunner(t, d)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	wantLabel := defaultSessionLabel(directory, "sess-1")

	started := make(chan struct{})
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		close(started)
		<-ctx.Done()
		return "Fix login flow", nil
	}
	settled := make(chan jobs.State, 4)
	runner.OnChange(func(jobID string) {
		if job, _ := runner.Get(jobID); job != nil && job.State != jobs.StateRunning && job.State != jobs.StateQueued {
			settled <- job.State
		}
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)

	d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
	<-started
	job, err := runner.GetByKey(sessionTitleKind, "sess-1")
	if err != nil || job == nil {
		t.Fatalf("title job: %v %v", job, err)
	}
	runner.Cancel(job.ID)
	if state := <-settled; state == jobs.StateDone {
		t.Fatalf("a cancelled title run reported %s", state)
	}
	if got := d.store.Get("sess-1"); got == nil || got.Label != wantLabel {
		t.Fatalf("session label = %+v, want unchanged %q after a cancel at commit", got, wantLabel)
	}
}

func TestMaybeGenerateSessionTitle_HappyPath(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)
	runSessionTitleJobs(t, d)

	if calls != 1 {
		t.Fatalf("exec calls = %d, want 1", calls)
	}
	got := d.store.Get("sess-1")
	if got == nil || got.Label != "Fix login flow" {
		t.Fatalf("session label = %+v, want %q", got, "Fix login flow")
	}
}

func TestMaybeGenerateSessionTitle_CustomLabelSkipped(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "user renamed me")
	transcriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)
	runSessionTitleJobs(t, d)

	if calls != 0 {
		t.Fatalf("exec calls = %d, want 0 (custom label must never be clobbered)", calls)
	}
	got := d.store.Get("sess-1")
	if got == nil || got.Label != "user renamed me" {
		t.Fatalf("session label = %+v, want unchanged", got)
	}
}

func TestMaybeGenerateSessionTitle_ExecError(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)
	wantLabel := defaultSessionLabel(directory, "sess-1")

	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		return "", errors.New("boom")
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)
	runSessionTitleJobs(t, d)

	got := d.store.Get("sess-1")
	if got == nil || got.Label != wantLabel {
		t.Fatalf("session label = %+v, want unchanged %q", got, wantLabel)
	}
}

func TestMaybeGenerateSessionTitle_UnusableOutput(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)
	wantLabel := defaultSessionLabel(directory, "sess-1")

	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		return "   \n\t  ", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)
	runSessionTitleJobs(t, d)

	got := d.store.Get("sess-1")
	if got == nil || got.Label != wantLabel {
		t.Fatalf("session label = %+v, want unchanged %q", got, wantLabel)
	}
}

func TestMaybeGenerateSessionTitle_OneAttemptPerSession(t *testing.T) {
	directory := t.TempDir()
	transcriptPath := writeSessionTitleTranscript(t)

	t.Run("after_success", func(t *testing.T) {
		d := newSessionTitleDaemon(t)
		seedSessionTitleSession(t, d, "sess-1", directory, "")
		calls := 0
		d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
			calls++
			return "Fix login flow", nil
		}
		d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		runSessionTitleJobs(t, d)
		if calls != 1 {
			t.Fatalf("first call: exec calls = %d, want 1", calls)
		}
		d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		runSessionTitleJobs(t, d)
		if calls != 1 {
			t.Fatalf("second call: exec calls = %d, want still 1 (label no longer default)", calls)
		}
	})

	t.Run("after_failure", func(t *testing.T) {
		d := newSessionTitleDaemon(t)
		seedSessionTitleSession(t, d, "sess-1", directory, "")
		calls := 0
		d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
			calls++
			return "", errors.New("boom")
		}
		d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		runSessionTitleJobs(t, d)
		if calls != 1 {
			t.Fatalf("first call: exec calls = %d, want 1", calls)
		}
		d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		runSessionTitleJobs(t, d)
		if calls != 1 {
			t.Fatalf("second call after failure: exec calls = %d, want still 1 (attempted-guard)", calls)
		}
	})
}

func TestMaybeGenerateSessionTitle_RenameRace(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)

	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		d.store.UpdateSessionLabel("sess-1", "user choice")
		return "llm title", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)
	runSessionTitleJobs(t, d)

	got := d.store.Get("sess-1")
	if got == nil || got.Label != "user choice" {
		t.Fatalf("session label = %+v, want %q (must not clobber the in-flight rename)", got, "user choice")
	}
}

func TestMaybeGenerateSessionTitle_EmptyTranscriptNotMarkedAttempted(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	emptyTranscriptPath := writeSessionTitleTranscriptNoUserContent(t)
	goodTranscriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", emptyTranscriptPath)
	runSessionTitleJobs(t, d)
	if calls != 0 {
		t.Fatalf("exec calls after empty transcript = %d, want 0", calls)
	}

	d.maybeGenerateSessionTitle("sess-1", goodTranscriptPath)
	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls after good transcript = %d, want 1 (empty transcript must not have marked attempted)", calls)
	}
	got := d.store.Get("sess-1")
	if got == nil || got.Label != "Fix login flow" {
		t.Fatalf("session label = %+v, want %q", got, "Fix login flow")
	}
}

// exec re-entering maybeGenerateSessionTitle stands in for a second Stop that raced past
// the early attempted-check; a guard mutex held across exec would deadlock here.
func TestMaybeGenerateSessionTitle_ConcurrentAttemptRunsExecOnce(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		if calls == 1 {
			d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		}
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)
	runSessionTitleJobs(t, d)

	if calls != 1 {
		t.Fatalf("exec calls = %d, want 1 (concurrent caller must not double-run the paid LLM call)", calls)
	}
	got := d.store.Get("sess-1")
	if got == nil || got.Label != "Fix login flow" {
		t.Fatalf("session label = %+v, want %q", got, "Fix login flow")
	}
}

func TestMaybeGenerateSessionTitle_CrewMemberNeverTitled(t *testing.T) {
	d := newCrewDaemon(t)
	installSessionTitleRunner(t, d)
	home := filepath.Join(d.dataRoot, crew.HomesDirName, "trellis")
	seedSessionTitleSession(t, d, "sess-trellis", home, "")
	if _, err := d.claimCrewBinding("trellis", "sess-trellis"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	transcriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-trellis", transcriptPath)
	runSessionTitleJobs(t, d)

	if calls != 0 {
		t.Fatalf("exec calls = %d, want 0 (a bound member is named by definition)", calls)
	}
	if got := d.store.Get("sess-trellis"); got == nil || got.Label != "trellis" {
		t.Fatalf("session label = %+v, want %q", got, "trellis")
	}
}

func TestMaybeGenerateSessionTitle_CrewBindingRace(t *testing.T) {
	d := newCrewDaemon(t)
	installSessionTitleRunner(t, d)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)

	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		if _, err := d.claimCrewBinding("trellis", "sess-1"); err != nil {
			t.Errorf("claim: %v", err)
		}
		return "llm title", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)
	runSessionTitleJobs(t, d)

	wantLabel := defaultSessionLabel(directory, "sess-1")
	if got := d.store.Get("sess-1"); got == nil || got.Label != wantLabel {
		t.Fatalf("session label = %+v, want unchanged %q (a session that became a member must not be titled)", got, wantLabel)
	}
}

func TestMaybeGenerateSessionTitleFromPrompt_TitlesBeforeStop(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")

	calls := 0
	var gotBrief string
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		gotBrief = conversation
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-1", "  fix the login flow\n", userConversationInput())
	runSessionTitleJobs(t, d)
	if calls != 1 || !strings.Contains(gotBrief, "fix the login flow") {
		t.Fatalf("exec calls = %d brief = %q, want 1 call with the trimmed prompt", calls, gotBrief)
	}
	if got := d.store.Get("sess-1"); got == nil || got.Label != "Fix login flow" {
		t.Fatalf("session label = %+v, want %q", got, "Fix login flow")
	}

	d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls after Stop = %d, want 1", calls)
	}
}

func TestMaybeGenerateSessionTitleFromPrompt_EmptyPromptLeavesStopPath(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-1", "   ", userConversationInput())
	runSessionTitleJobs(t, d)
	if calls != 0 {
		t.Fatalf("exec calls after empty prompt = %d, want 0", calls)
	}
	d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls after Stop = %d, want 1", calls)
	}
}

func TestMaybeGenerateSessionTitleFromPrompt_CapsLongPrompt(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")

	var gotBrief string
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		gotBrief = conversation
		return "Long prompt", nil
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-1", strings.Repeat("é", sessionTitleBriefCharCap+10), userConversationInput())
	runSessionTitleJobs(t, d)
	if n := strings.Count(gotBrief, "é"); n != sessionTitleBriefCharCap {
		t.Fatalf("brief prompt runes = %d, want %d", n, sessionTitleBriefCharCap)
	}
}

func TestMaybeGenerateSessionTitleFromPrompt_UncorrelatedReceiptLeavesAttemptAvailable(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-1", "ticket nudge: please reconcile", maintenanceInput("tickets"))
	runSessionTitleJobs(t, d)
	d.maybeGenerateSessionTitleFromPrompt("sess-1", "another maintenance prompt", maintenanceInput("other-maintenance"))
	runSessionTitleJobs(t, d)
	if calls != 0 {
		t.Fatalf("exec calls after maintenance and peer receipts = %d, want 0", calls)
	}
	if got := d.store.Get("sess-1"); got == nil || got.Label != defaultSessionLabel(directory, "sess-1") {
		t.Fatalf("session label = %+v, want the default label untouched", got)
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-1", "fix the login flow", userConversationInput())
	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls after user turn = %d, want 1 (receipts must not consume the attempt)", calls)
	}
}

func TestMaybeGenerateSessionTitleFromPrompt_InitialPromptTitlesWithoutCorrelation(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	d.rememberSessionTitleInitialPrompt("sess-1", "investigate the retry queue")

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "Investigate retry queue", nil
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-1", "unrelated injected text", sessionInputOrigin{})
	runSessionTitleJobs(t, d)
	if calls != 0 {
		t.Fatalf("exec calls after non-matching uncorrelated prompt = %d, want 0", calls)
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-1", "investigate the retry queue", sessionInputOrigin{})
	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls after the initial prompt = %d, want 1", calls)
	}
	if got := d.store.Get("sess-1"); got == nil || got.Label != "Investigate retry queue" {
		t.Fatalf("session label = %+v, want %q", got, "Investigate retry queue")
	}
}

func TestSpawnPipeline_InitialPromptMarkerBeatsEarlyPromptHook(t *testing.T) {
	d := newSessionTitleDaemon(t)
	addTestWorkspace(d, "workspace-title", t.TempDir())

	titled := make(chan string, 1)
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		titled <- conversation
		return "One-shot investigation", nil
	}
	backend := &fakeSpawnBackend{}
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		d.maybeGenerateSessionTitleFromPrompt(opts.ID, "investigate the retry queue", sessionInputOrigin{})
	}
	d.ptyBackend = backend

	client := &wsClient{send: make(chan outboundMessage, 8), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		ID:            "sess-oneshot",
		Cwd:           t.TempDir(),
		WorkspaceID:   "workspace-title",
		Agent:         "claude",
		Cols:          80,
		Rows:          24,
		InitialPrompt: protocol.Ptr("investigate the retry queue"),
	})

	runSessionTitleJobs(t, d)
	select {
	case brief := <-titled:
		if !strings.Contains(brief, "investigate the retry queue") {
			t.Fatalf("titled from brief %q, want the initial prompt", brief)
		}
	default:
		t.Fatal("the early prompt hook did not reach the title exec; marker missing at backend-spawn time")
	}
	if got := d.store.Get("sess-oneshot"); got == nil || got.Label != "One-shot investigation" {
		t.Fatalf("session label = %+v, want %q", got, "One-shot investigation")
	}
}

func TestSpawnPipeline_FailedLaunchRollsBackInitialPromptMarker(t *testing.T) {
	d := newSessionTitleDaemon(t)
	addTestWorkspace(d, "workspace-title", t.TempDir())
	d.ptyBackend = &fakeSpawnBackend{spawnErr: errors.New("boom")}

	client := &wsClient{send: make(chan outboundMessage, 8), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		ID:            "sess-failed",
		Cwd:           t.TempDir(),
		WorkspaceID:   "workspace-title",
		Agent:         "claude",
		Cols:          80,
		Rows:          24,
		InitialPrompt: protocol.Ptr("investigate the retry queue"),
	})

	d.sessionTitleMu.Lock()
	_, held := d.sessionTitleInitialPrompt["sess-failed"]
	d.sessionTitleMu.Unlock()
	if held {
		t.Fatal("failed launch left the initial-prompt marker behind")
	}
}

func TestMaybeGenerateSessionTitle_StopPathClearsInitialPromptMarker(t *testing.T) {
	d := newSessionTitleDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	d.rememberSessionTitleInitialPrompt("sess-1", "a prompt the hook never carried")
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
	runSessionTitleJobs(t, d)

	d.sessionTitleMu.Lock()
	_, held := d.sessionTitleInitialPrompt["sess-1"]
	d.sessionTitleMu.Unlock()
	if held {
		t.Fatal("Stop-path title left the initial-prompt marker behind")
	}
}

func TestSpawnPipeline_AlreadyLiveSpawnPreservesInitialPromptMarker(t *testing.T) {
	d := newSessionTitleDaemon(t)
	addTestWorkspace(d, "workspace-title", t.TempDir())
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "One-shot investigation", nil
	}

	client := &wsClient{send: make(chan outboundMessage, 8), attachedStreams: make(map[string]ptybackend.Stream)}
	spawn := &protocol.SpawnSessionMessage{
		ID:            "sess-dup",
		Cwd:           t.TempDir(),
		WorkspaceID:   "workspace-title",
		Agent:         "claude",
		Cols:          80,
		Rows:          24,
		InitialPrompt: protocol.Ptr("investigate the retry queue"),
	}
	d.handleSpawnSession(client, spawn)
	backend.mu.Lock()
	backend.sessionIDs = []string{"sess-dup"}
	backend.mu.Unlock()

	duplicate := *spawn
	duplicate.InitialPrompt = nil
	d.handleSpawnSession(client, &duplicate)

	d.maybeGenerateSessionTitleFromPrompt("sess-dup", "investigate the retry queue", sessionInputOrigin{})
	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls after the original receipt = %d, want 1 (the no-op spawn must not disturb the marker)", calls)
	}
	if got := d.store.Get("sess-dup"); got == nil || got.Label != "One-shot investigation" {
		t.Fatalf("session label = %+v, want %q", got, "One-shot investigation")
	}
}

func TestTitleProviderAgent_PrefersSessionAgentThenFallsBack(t *testing.T) {
	cases := []struct{ agent, want string }{
		{"claude", "claude"},
		{"codex", "codex"},
		{"copilot", "copilot"},
		{"pi", "claude"},
		{"shell", "claude"},
		{"unknown-agent", "claude"},
		{"", "claude"},
	}
	for _, tc := range cases {
		if got := titleProviderAgent(tc.agent); got != tc.want {
			t.Errorf("titleProviderAgent(%q) = %q, want %q", tc.agent, got, tc.want)
		}
	}
}

func TestSpawnPipeline_InitialPromptTitlesAtSpawn(t *testing.T) {
	d := newSessionTitleDaemon(t)
	addTestWorkspace(d, "workspace-title", t.TempDir())
	d.ptyBackend = &fakeSpawnBackend{}

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		calls++
		return "One-shot investigation", nil
	}
	client := &wsClient{send: make(chan outboundMessage, 8), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		ID:            "sess-spawn-title",
		Cwd:           t.TempDir(),
		WorkspaceID:   "workspace-title",
		Agent:         "claude",
		Cols:          80,
		Rows:          24,
		InitialPrompt: protocol.Ptr("investigate the retry queue"),
	})

	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls after spawn = %d, want 1 (no UserPromptSubmit hook needed)", calls)
	}
	if got := d.store.Get("sess-spawn-title"); got == nil || got.Label != "One-shot investigation" {
		t.Fatalf("session label = %+v, want %q", got, "One-shot investigation")
	}

	d.maybeGenerateSessionTitleFromPrompt("sess-spawn-title", "investigate the retry queue", userConversationInput())
	runSessionTitleJobs(t, d)
	if calls != 1 {
		t.Fatalf("exec calls after the hook echo = %d, want still 1", calls)
	}
}
