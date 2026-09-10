package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/transcript"
)

func TestWritePrivateFileReplacesPublicFileWithOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.png")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(path, []byte("private")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("capture permissions = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "private" {
		t.Fatalf("capture contents = %q, want private", data)
	}
}

func writeCopilotSessionState(t *testing.T, homeDir, sessionID, cwd string, startTime time.Time, withStart, withAssistant bool, modTime time.Time) string {
	t.Helper()

	sessionDir := filepath.Join(homeDir, ".copilot", "session-state", sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}

	workspace := fmt.Sprintf("id: %s\ncwd: %s\n", sessionID, cwd)
	if err := os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(workspace), 0o644); err != nil {
		t.Fatalf("write workspace.yaml: %v", err)
	}

	lines := ""
	if withStart {
		lines += fmt.Sprintf(
			`{"type":"session.start","data":{"sessionId":"%s","startTime":"%s"}}`+"\n",
			sessionID,
			startTime.UTC().Format(time.RFC3339Nano),
		)
	}
	if withAssistant {
		lines += `{"type":"assistant.message","data":{"content":"ok"}}` + "\n"
	} else {
		lines += `{"type":"user.message","data":{"content":"hi"}}` + "\n"
	}

	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(lines), 0o644); err != nil {
		t.Fatalf("write events.jsonl: %v", err)
	}
	if err := os.Chtimes(eventsPath, modTime, modTime); err != nil {
		t.Fatalf("chtimes events.jsonl: %v", err)
	}

	return eventsPath
}

func TestFindCopilotTranscript_PrefersClosestStartTime(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv(toolhome.EnvVar, homeDir)

	cwd := "/repo/project"
	startedAt := time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC)

	expected := writeCopilotSessionState(
		t,
		homeDir,
		"session-a",
		cwd,
		startedAt.Add(5*time.Second),
		true,
		true,
		startedAt.Add(1*time.Minute),
	)
	_ = writeCopilotSessionState(
		t,
		homeDir,
		"session-b",
		cwd,
		startedAt.Add(-30*time.Minute),
		true,
		true,
		startedAt.Add(2*time.Minute),
	)

	got := transcript.FindCopilotTranscript(cwd, startedAt)
	if got != expected {
		t.Fatalf("FindCopilotTranscript() = %q, want %q", got, expected)
	}
}

func TestFindCopilotTranscript_FallsBackToNewestModTime(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv(toolhome.EnvVar, homeDir)

	cwd := "/repo/project"
	startedAt := time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC)

	_ = writeCopilotSessionState(
		t,
		homeDir,
		"session-a",
		cwd,
		startedAt,
		false,
		true,
		startedAt.Add(1*time.Minute),
	)
	expected := writeCopilotSessionState(
		t,
		homeDir,
		"session-b",
		cwd,
		startedAt,
		false,
		true,
		startedAt.Add(2*time.Minute),
	)

	got := transcript.FindCopilotTranscript(cwd, startedAt)
	if got != expected {
		t.Fatalf("FindCopilotTranscript() = %q, want %q", got, expected)
	}
}

func TestResolveCopilotTranscript_PrefersResumeSessionPath(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv(toolhome.EnvVar, homeDir)

	startedAt := time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC)
	resumeID := "resume-session-id"
	expected := writeCopilotSessionState(
		t,
		homeDir,
		resumeID,
		"/repo/from-resume",
		startedAt,
		true,
		true,
		startedAt.Add(30*time.Second),
	)

	got := transcript.FindCopilotTranscriptForResume(resumeID)
	if got == "" {
		got = transcript.FindCopilotTranscript("/repo/other", startedAt)
	}
	if got != expected {
		t.Fatalf("copilot transcript resolution = %q, want %q", got, expected)
	}
}

func TestResolveCopilotTranscript_FallsBackWhenResumePathMissing(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv(toolhome.EnvVar, homeDir)

	cwd := "/repo/project"
	startedAt := time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC)
	expected := writeCopilotSessionState(
		t,
		homeDir,
		"fallback-session",
		cwd,
		startedAt.Add(3*time.Second),
		true,
		true,
		startedAt.Add(1*time.Minute),
	)

	got := transcript.FindCopilotTranscriptForResume("missing-resume-id")
	if got == "" {
		got = transcript.FindCopilotTranscript(cwd, startedAt)
	}
	if got != expected {
		t.Fatalf("copilot transcript resolution = %q, want %q", got, expected)
	}
}

func TestParseDirectLaunchArgs_ResumePickerWithFlagAfterResume(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--resume", "--yolo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !parsed.resumePicker {
		t.Fatalf("expected resume picker to be enabled")
	}
	if parsed.resumeID != "" {
		t.Fatalf("expected empty resume id, got %q", parsed.resumeID)
	}
	if !parsed.yoloMode {
		t.Fatalf("expected yolo flag to be preserved")
	}
}

func TestParseDirectLaunchArgs_ResumeIDStillAccepted(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--resume", "abc123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.resumePicker {
		t.Fatalf("expected resume picker to be disabled")
	}
	if parsed.resumeID != "abc123" {
		t.Fatalf("expected resume id abc123, got %q", parsed.resumeID)
	}
}

func TestParseDirectLaunchArgs_LabelAndYolo(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"-s", "my-label", "--yolo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.label != "my-label" {
		t.Fatalf("expected label my-label, got %q", parsed.label)
	}
	if !parsed.yoloMode {
		t.Fatal("expected yolo mode to be enabled")
	}
}

func TestParseDirectLaunchArgs_MemberNamesTheSession(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--member", "trellis"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.member != "trellis" {
		t.Fatalf("member = %q, want trellis", parsed.member)
	}
	if parsed.label != "Trellis" {
		t.Fatalf("label = %q, want the member's name", parsed.label)
	}
}

func TestParseDirectLaunchArgs_LabelOverridesTheMemberName(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--member", "trellis", "-s", "crew slice 1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.member != "trellis" || parsed.label != "crew slice 1" {
		t.Fatalf("member/label = %q/%q, want trellis/crew slice 1", parsed.member, parsed.label)
	}
}

func TestParseDirectLaunchArgs_RejectsUnrecognizedArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "foo"},
		{"--"},
		{"--help"},
		{"random"},
		{"-s"},
		{"--member"},
	} {
		if _, err := parseDirectLaunchArgs(args); err == nil {
			t.Fatalf("expected error for args %#v, got nil", args)
		}
	}
}

func TestIsVersionCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no args", args: []string{"attn"}, want: false},
		{name: "long flag", args: []string{"attn", "--version"}, want: true},
		{name: "subcommand", args: []string{"attn", "version"}, want: true},
		{name: "other flag", args: []string{"attn", "--help"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVersionCommand(tt.args); got != tt.want {
				t.Fatalf("isVersionCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestIsProtocolVersionCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no args", args: []string{"attn"}, want: false},
		{name: "protocol flag", args: []string{"attn", "--protocol-version"}, want: true},
		{name: "version flag", args: []string{"attn", "--version"}, want: false},
		{name: "subcommand", args: []string{"attn", "version"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProtocolVersionCommand(tt.args); got != tt.want {
				t.Fatalf("isProtocolVersionCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestIsBuildInfoJSONCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no args", args: []string{"attn"}, want: false},
		{name: "build info flag", args: []string{"attn", "--build-info-json"}, want: true},
		{name: "protocol flag", args: []string{"attn", "--protocol-version"}, want: false},
		{name: "version flag", args: []string{"attn", "--version"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBuildInfoJSONCommand(tt.args); got != tt.want {
				t.Fatalf("isBuildInfoJSONCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestDetectPresence(t *testing.T) {
	t.Run("outside attn", func(t *testing.T) {
		t.Setenv("ATTN_INSIDE_APP", "")
		t.Setenv("ATTN_SESSION_ID", "stale-session")

		sessionID, present := detectPresence()
		if present || sessionID != "" {
			t.Fatalf("detectPresence() = (%q, %v), want empty session and false", sessionID, present)
		}
	})

	t.Run("inside attn", func(t *testing.T) {
		t.Setenv("ATTN_INSIDE_APP", "1")
		t.Setenv("ATTN_SESSION_ID", " session-1 ")

		sessionID, present := detectPresence()
		if !present || sessionID != "session-1" {
			t.Fatalf("detectPresence() = (%q, %v), want session-1 and true", sessionID, present)
		}
	})
}

func TestParseDirectLaunchArgs_InitialPromptFile(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--initial-prompt-file", "/tmp/brief.md"})
	if err != nil {
		t.Fatalf("parseDirectLaunchArgs() error = %v", err)
	}
	if parsed.initialPromptFile != "/tmp/brief.md" {
		t.Fatalf("initialPromptFile = %q, want /tmp/brief.md", parsed.initialPromptFile)
	}
}

func TestConsumeOneShotBoolEnvRemovesLaunchCapability(t *testing.T) {
	t.Setenv("ATTN_TEST_ONE_SHOT", "1")
	if !consumeOneShotBoolEnv("ATTN_TEST_ONE_SHOT") {
		t.Fatal("consumeOneShotBoolEnv() = false, want true")
	}
	if _, ok := os.LookupEnv("ATTN_TEST_ONE_SHOT"); ok {
		t.Fatal("one-shot launch capability remained in the environment")
	}
}

func TestConsumeOneShotEnvRemovesLaunchPin(t *testing.T) {
	t.Setenv("ATTN_TEST_ONE_SHOT_VALUE", " pinned ")
	if got := consumeOneShotEnv("ATTN_TEST_ONE_SHOT_VALUE"); got != "pinned" {
		t.Fatalf("consumeOneShotEnv() = %q, want pinned", got)
	}
	if _, ok := os.LookupEnv("ATTN_TEST_ONE_SHOT_VALUE"); ok {
		t.Fatal("one-shot launch pin remained in the environment")
	}
}

func TestReadInitialPromptFileRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(path, []byte("delegated brief"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	prompt, err := readInitialPromptFile(path)
	if err != nil {
		t.Fatalf("readInitialPromptFile() error = %v", err)
	}
	if prompt != "delegated brief" {
		t.Fatalf("prompt = %q", prompt)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("prompt file still exists: %v", err)
	}
}

type fakeNotebookGuideClient struct {
	result *protocol.NotebookGuideResult
	err    error
	gotID  string
}

func (f *fakeNotebookGuideClient) NotebookGuide(sessionID string) (*protocol.NotebookGuideResult, error) {
	f.gotID = sessionID
	return f.result, f.err
}

func TestResolveChiefNotebookRoot(t *testing.T) {
	t.Run("chief returns root", func(t *testing.T) {
		c := &fakeNotebookGuideClient{result: &protocol.NotebookGuideResult{Root: "/nb", SessionIsChief: true}}
		if got := resolveChiefNotebookRoot(c, "s1"); got != "/nb" {
			t.Fatalf("root = %q, want /nb", got)
		}
		if c.gotID != "s1" {
			t.Fatalf("session id = %q, want s1", c.gotID)
		}
	})
	t.Run("non-chief returns empty", func(t *testing.T) {
		c := &fakeNotebookGuideClient{result: &protocol.NotebookGuideResult{Root: "/nb", SessionIsChief: false}}
		if got := resolveChiefNotebookRoot(c, "s1"); got != "" {
			t.Fatalf("root = %q, want empty for non-chief", got)
		}
	})
	t.Run("lookup error returns empty", func(t *testing.T) {
		c := &fakeNotebookGuideClient{err: errors.New("daemon down")}
		if got := resolveChiefNotebookRoot(c, "s1"); got != "" {
			t.Fatalf("root = %q, want empty on error", got)
		}
	})
}

func TestParseDelegateArgsBuildsExplicitRequest(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "source-session")
	parsed, err := parseDelegateArgs([]string{"--brief", "Investigate this", "--cwd", "/repo", "--new-worktree", "--branch", "feat/parser", "--from", "origin/main", "--role", "builder", "--model", "default", "--effort", "high"})
	if err != nil {
		t.Fatal(err)
	}
	msg := parsed.request
	if protocol.Deref(msg.SourceSessionID) != "source-session" || msg.Assignment.Kind != protocol.DelegateAssignmentKindNew || protocol.Deref(msg.Assignment.Brief) != "Investigate this" {
		t.Fatalf("%+v", msg)
	}
	if msg.Cwd != "/repo" || msg.Checkout == nil || msg.Checkout.Kind != protocol.DelegateCheckoutKindNewWorktree || msg.Checkout.Branch != "feat/parser" || protocol.Deref(msg.Checkout.From) != "origin/main" {
		t.Fatalf("%+v", msg)
	}
	if msg.Model == nil || *msg.Model != "" || protocol.Deref(msg.Effort) != "high" {
		t.Fatalf("%+v", msg)
	}
}

func TestParseDelegateArgsRejectsRetiredAndConflictingInputs(t *testing.T) {
	for _, args := range [][]string{
		{"--brief", "Task", "--cwd", "/repo", "--workspace", "old", "--agent", "codex"},
		{"--brief", "Task", "--seed", "s-abc123", "--cwd", "/repo", "--agent", "codex"},
		{"--brief", "Task", "--cwd", "/repo", "--handover", "--agent", "codex"},
		{"--brief", "Task", "--cwd", "/repo", "--choice", "hard", "--agent", "codex"},
		{"--brief", "Task", "--cwd", "/repo", "--role", "builder", "--fallback"},
		{"--brief", "Task", "--cwd", "/repo", "--branch", "feat/ignored", "--agent", "codex"},
		{"--brief", "Task", "--cwd", "/repo", "--existing-branch", "feat/ignored", "--agent", "codex"},
		{"--brief", "Task", "--cwd", "/repo", "--from", "origin/next", "--agent", "codex"},
		{"--brief", "Task", "--cwd", "/repo", "--worktree-path", "/tmp/ignored", "--agent", "codex"},
		{"--brief", "Task", "--cwd", "/repo", "--allow-worktree-reuse", "--agent", "codex"},
	} {
		if _, err := parseDelegateArgs(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestApplyLegacyBuildInfoOverrides_UsesLegacyMainInjectionWhenNeeded(t *testing.T) {
	previousBuildinfoVersion := buildinfo.Version
	previousBuildinfoBuildTime := buildinfo.BuildTime
	previousBuildinfoSourceFingerprint := buildinfo.SourceFingerprint
	previousBuildinfoGitCommit := buildinfo.GitCommit
	previousVersion := version
	previousBuildTime := buildTime
	previousSourceFingerprint := sourceFingerprint
	previousGitCommit := gitCommit
	t.Cleanup(func() {
		buildinfo.Version = previousBuildinfoVersion
		buildinfo.BuildTime = previousBuildinfoBuildTime
		buildinfo.SourceFingerprint = previousBuildinfoSourceFingerprint
		buildinfo.GitCommit = previousBuildinfoGitCommit
		version = previousVersion
		buildTime = previousBuildTime
		sourceFingerprint = previousSourceFingerprint
		gitCommit = previousGitCommit
	})

	buildinfo.Version = "dev"
	buildinfo.BuildTime = "unknown"
	buildinfo.SourceFingerprint = "unknown"
	buildinfo.GitCommit = "unknown"
	version = "1.2.3"
	buildTime = "2026-04-06T00:00:00Z"
	sourceFingerprint = "tree:abc123"
	gitCommit = "1234567890abcdef"

	applyLegacyBuildInfoOverrides()

	if buildinfo.Version != "1.2.3" {
		t.Fatalf("buildinfo.Version = %q, want legacy main.version value", buildinfo.Version)
	}
	if buildinfo.BuildTime != "2026-04-06T00:00:00Z" {
		t.Fatalf("buildinfo.BuildTime = %q, want legacy main.buildTime value", buildinfo.BuildTime)
	}
	if buildinfo.SourceFingerprint != "tree:abc123" {
		t.Fatalf("buildinfo.SourceFingerprint = %q, want legacy main.sourceFingerprint value", buildinfo.SourceFingerprint)
	}
	if buildinfo.GitCommit != "1234567890abcdef" {
		t.Fatalf("buildinfo.GitCommit = %q, want legacy main.gitCommit value", buildinfo.GitCommit)
	}
}

func TestApplyLegacyBuildInfoOverrides_PreservesInjectedBuildinfo(t *testing.T) {
	previousBuildinfoVersion := buildinfo.Version
	previousBuildinfoBuildTime := buildinfo.BuildTime
	previousBuildinfoSourceFingerprint := buildinfo.SourceFingerprint
	previousBuildinfoGitCommit := buildinfo.GitCommit
	previousVersion := version
	previousBuildTime := buildTime
	previousSourceFingerprint := sourceFingerprint
	previousGitCommit := gitCommit
	t.Cleanup(func() {
		buildinfo.Version = previousBuildinfoVersion
		buildinfo.BuildTime = previousBuildinfoBuildTime
		buildinfo.SourceFingerprint = previousBuildinfoSourceFingerprint
		buildinfo.GitCommit = previousBuildinfoGitCommit
		version = previousVersion
		buildTime = previousBuildTime
		sourceFingerprint = previousSourceFingerprint
		gitCommit = previousGitCommit
	})

	buildinfo.Version = "9.9.9"
	buildinfo.BuildTime = "2026-04-06T12:34:56Z"
	buildinfo.SourceFingerprint = "git:new"
	buildinfo.GitCommit = "fedcba0987654321"
	version = "1.2.3"
	buildTime = "2001-01-01T00:00:00Z"
	sourceFingerprint = "tree:old"
	gitCommit = "0123456789abcdef"

	applyLegacyBuildInfoOverrides()

	if buildinfo.Version != "9.9.9" {
		t.Fatalf("buildinfo.Version = %q, want primary buildinfo injection to win", buildinfo.Version)
	}
	if buildinfo.BuildTime != "2026-04-06T12:34:56Z" {
		t.Fatalf("buildinfo.BuildTime = %q, want primary buildinfo injection to win", buildinfo.BuildTime)
	}
	if buildinfo.SourceFingerprint != "git:new" {
		t.Fatalf("buildinfo.SourceFingerprint = %q, want primary buildinfo injection to win", buildinfo.SourceFingerprint)
	}
	if buildinfo.GitCommit != "fedcba0987654321" {
		t.Fatalf("buildinfo.GitCommit = %q, want primary buildinfo injection to win", buildinfo.GitCommit)
	}
}

func TestParseOpenArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantPath    string
		wantSession string
		wantErr     bool
	}{
		{name: "session after path", args: []string{"README.md", "--session", "sess-1"}, wantPath: "README.md", wantSession: "sess-1"},
		{name: "session=after path", args: []string{"README.md", "--session=sess-2"}, wantPath: "README.md", wantSession: "sess-2"},
		{name: "session before path", args: []string{"--session", "sess-3", "README.md"}, wantPath: "README.md", wantSession: "sess-3"},
		{name: "path only", args: []string{"README.md"}, wantPath: "README.md", wantSession: ""},
		{name: "no path", args: []string{"--session", "sess-4"}, wantErr: true},
		{name: "empty", args: []string{}, wantErr: true},
		{name: "extra positional", args: []string{"a.md", "b.md"}, wantErr: true},
		{name: "unknown flag", args: []string{"README.md", "--nope"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, session, err := parseOpenArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseOpenArgs(%v) = (%q, %q, nil), want error", tc.args, path, session)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOpenArgs(%v) error = %v", tc.args, err)
			}
			if path != tc.wantPath || session != tc.wantSession {
				t.Fatalf("parseOpenArgs(%v) = (%q, %q), want (%q, %q)", tc.args, path, session, tc.wantPath, tc.wantSession)
			}
		})
	}
}

func TestSeedOpenTargetClassification(t *testing.T) {
	if !isSeedOpenTarget("s-7k3f9m") {
		t.Fatal("seed id should route to open_seed")
	}
	if !isSeedOpenTarget("s-not-valid") {
		t.Fatal("seed-looking input should route to open_seed so validation fails loudly")
	}
	if isSeedOpenTarget("./s-7k3f9m") {
		t.Fatal("explicit relative path should remain a file target")
	}
}

func TestStopFacts(t *testing.T) {
	cases := []struct {
		name         string
		payload      string
		wantStatuses []string
		wantNames    []string
		wantCrons    int
	}{
		{
			name:         "workflow running (parent yields mid-run)",
			payload:      `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[{"id":"wv9p74ip7","type":"workflow","status":"running","name":"hello-parallel"}],"session_crons":[]}`,
			wantStatuses: []string{"running"},
		},
		{
			name:         "workflow plus background shells running",
			payload:      `{"background_tasks":[{"type":"workflow","status":"running"},{"type":"shell","status":"running"},{"type":"shell","status":"running"}]}`,
			wantStatuses: []string{"running", "running", "running"},
		},
		{
			name:    "empty background_tasks (workflow finished)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[]}`,
		},
		{
			name:    "fields absent (e.g. another agent)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false}`,
		},
		{
			name:         "task present but not running is still reported",
			payload:      `{"background_tasks":[{"type":"workflow","status":"completed"}]}`,
			wantStatuses: []string{"completed"},
		},
		{
			name:      "recurring cron pending",
			payload:   `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo persist-probe"}]}`,
			wantCrons: 1,
		},
		{
			name:      "one-shot reminder pending",
			payload:   `{"hook_event_name":"Stop","stop_hook_active":false,"session_crons":[{"id":"5e9a0f21","schedule":"18 14 * * *","recurring":false,"prompt":"echo oneshot-fired"}]}`,
			wantCrons: 1,
		},
		{
			name:      "recurring plus one-shot pending",
			payload:   `{"session_crons":[{"id":"43f0809f","schedule":"*/30 * * * *","recurring":true,"prompt":"echo recurring-probe"},{"id":"2b1dec68","schedule":"15 9 20 6 *","recurring":false,"prompt":"echo oneshot-probe"}]}`,
			wantCrons: 2,
		},
		{
			name:         "background running and cron pending are reported together",
			payload:      `{"background_tasks":[{"type":"shell","status":"running"}],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo x"}]}`,
			wantStatuses: []string{"running"},
			wantCrons:    1,
		},
		{
			name:         "a task's description travels as its name (captured from Claude Code 2.1.257)",
			payload:      `{"background_tasks":[{"id":"bzd8fe67e","type":"shell","status":"running","description":"Sleep for 90 seconds in background","command":"sleep 90"},{"id":"a2c193c118d82726c","type":"subagent","status":"running","description":"Run sleep 75 then report completion","agent_type":"general-purpose"}],"session_crons":[]}`,
			wantStatuses: []string{"running", "running"},
			wantNames:    []string{"Sleep for 90 seconds in background", "Run sleep 75 then report completion"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input hookInput
			if err := json.Unmarshal([]byte(tc.payload), &input); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			facts := stopFacts(input)
			var statuses, names []string
			for _, task := range facts.BackgroundTasks {
				statuses = append(statuses, task.Status)
				names = append(names, protocol.Deref(task.Name))
			}
			if !slices.Equal(statuses, tc.wantStatuses) {
				t.Fatalf("background task statuses = %q, want %q", statuses, tc.wantStatuses)
			}
			if tc.wantNames != nil && !slices.Equal(names, tc.wantNames) {
				t.Fatalf("background task names = %q, want %q", names, tc.wantNames)
			}
			if facts.PendingSessionCrons != tc.wantCrons {
				t.Fatalf("PendingSessionCrons = %d, want %d", facts.PendingSessionCrons, tc.wantCrons)
			}
		})
	}
}

func TestParseTicketIDArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want ticketIDArgs
	}{
		{name: "id only", args: []string{"tk"}, want: ticketIDArgs{TicketID: "tk"}},
		{
			name: "flags after id",
			args: []string{"tk", "--session", "s1", "--json"},
			want: ticketIDArgs{TicketID: "tk", Session: "s1", JSON: true},
		},
		{
			name: "flags before id",
			args: []string{"--session", "s1", "tk"},
			want: ticketIDArgs{TicketID: "tk", Session: "s1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTicketIDArgs("ticket subscribe", tc.args)
			if err != nil {
				t.Fatalf("parseTicketIDArgs(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Fatalf("parseTicketIDArgs(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseTicketIDArgsErrors(t *testing.T) {
	cases := map[string][]string{
		"no args":         {},
		"two positionals": {"tk", "extra"},
		"unknown flag":    {"tk", "--bogus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTicketIDArgs("ticket subscribe", args); err == nil {
				t.Fatalf("parseTicketIDArgs(%v) = nil error, want error", args)
			}
		})
	}
}

func TestParseJournalAppendArgsEntryAndFileMutuallyExclusive(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "entry.md")
	if err := os.WriteFile(tmp, []byte("from file"), 0o644); err != nil {
		t.Fatalf("write temp entry file: %v", err)
	}
	_, err := parseJournalAppendArgs([]string{"--entry", "inline", "--entry-file", tmp})
	if err == nil || !strings.Contains(err.Error(), "only one of --entry or --entry-file") {
		t.Fatalf("parseJournalAppendArgs() error = %v, want mutually-exclusive error", err)
	}
}

func TestParseJournalAppendArgsMissingEntryErrors(t *testing.T) {
	_, err := parseJournalAppendArgs(nil)
	if err == nil || !strings.Contains(err.Error(), "--entry or --entry-file is required") {
		t.Fatalf("parseJournalAppendArgs() error = %v, want missing-entry error", err)
	}
}

func TestParseJournalAppendArgsEntryFileReadsAndTrims(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "entry.md")
	if err := os.WriteFile(tmp, []byte("  from file  \n"), 0o644); err != nil {
		t.Fatalf("write temp entry file: %v", err)
	}
	parsed, err := parseJournalAppendArgs([]string{"--entry-file", tmp})
	if err != nil {
		t.Fatalf("parseJournalAppendArgs() error = %v", err)
	}
	if parsed.entry != "from file" {
		t.Fatalf("entry = %q, want %q", parsed.entry, "from file")
	}
}

func TestParseJournalAppendArgsDatePassthrough(t *testing.T) {
	parsed, err := parseJournalAppendArgs([]string{"--entry", "hi", "--date", "2026-07-05", "--session", "s1", "--json"})
	if err != nil {
		t.Fatalf("parseJournalAppendArgs() error = %v", err)
	}
	if parsed.date != "2026-07-05" || parsed.sessionID != "s1" || !parsed.jsonOut || parsed.entry != "hi" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestParseJournalAppendArgsSessionDefaultsToEnv(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "env-session")
	parsed, err := parseJournalAppendArgs([]string{"--entry", "hi"})
	if err != nil {
		t.Fatalf("parseJournalAppendArgs() error = %v", err)
	}
	if parsed.sessionID != "env-session" {
		t.Fatalf("sessionID = %q, want env-session", parsed.sessionID)
	}
}

func TestParseSessionRenameArgs(t *testing.T) {
	got, err := parseSessionRenameArgs([]string{"pi resume support"}, "sess-own")
	if err != nil || got.sessionID != "sess-own" || got.name != "pi resume support" {
		t.Fatalf("own session = %+v, %v; want sess-own and the name", got, err)
	}
	got, err = parseSessionRenameArgs([]string{"--session", "sess-2", "  review store tripwires "}, "sess-own")
	if err != nil || got.sessionID != "sess-2" || got.name != "review store tripwires" {
		t.Fatalf("explicit session = %+v, %v", got, err)
	}
	got, err = parseSessionRenameArgs([]string{"pi resume support", "--session", "sess-3"}, "sess-own")
	if err != nil || got.sessionID != "sess-3" {
		t.Fatalf("flag after the name = %+v, %v; want sess-3", got, err)
	}
	if _, err := parseSessionRenameArgs([]string{"a", "b"}, "sess-own"); err == nil {
		t.Fatal("two positional words must be refused, so an unquoted name fails loudly")
	}
	if _, err := parseSessionRenameArgs([]string{"name"}, ""); err == nil || !strings.Contains(err.Error(), "--session") {
		t.Fatalf("no session anywhere = %v, want an error naming --session", err)
	}
	if _, err := parseSessionRenameArgs([]string{"   "}, "sess-own"); err == nil {
		t.Fatal("a blank name must be refused")
	}
}
