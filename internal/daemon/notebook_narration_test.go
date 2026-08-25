package daemon

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

func drainingConn(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	go io.Copy(io.Discard, server)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client
}

func TestParseNotebookNarrationConfig(t *testing.T) {
	t.Run("blank uses summarize tier default", func(t *testing.T) {
		config, err := parseNotebookNarrationConfig(notebookSummarizeSessionKind, "")
		if err != nil {
			t.Fatalf("parse blank: %v", err)
		}
		if config.Agent != notebookSummarizeDefaultAgent || config.Model != notebookSummarizeDefaultModel {
			t.Fatalf("config = %+v, want summarize default", config)
		}
	})

	t.Run("blank uses narrate tier default", func(t *testing.T) {
		config, err := parseNotebookNarrationConfig(notebookNarrateWorkspaceKind, "   ")
		if err != nil {
			t.Fatalf("parse blank: %v", err)
		}
		if config.Agent != notebookNarrateDefaultAgent || config.Model != notebookNarrateDefaultModel {
			t.Fatalf("config = %+v, want narrate default", config)
		}
	})

	t.Run("explicit value parses and lowercases agent", func(t *testing.T) {
		config, err := parseNotebookNarrationConfig(notebookNarrateWorkspaceKind, `{"agent":"CLAUDE","model":"claude-opus-test"}`)
		if err != nil {
			t.Fatalf("parse explicit: %v", err)
		}
		if config.Agent != "claude" || config.Model != "claude-opus-test" {
			t.Fatalf("config = %+v", config)
		}
	})

	for name, raw := range map[string]string{
		"missing model": `{"agent":"claude"}`,
		"missing agent": `{"model":"claude-test"}`,
		"unknown field": `{"agent":"claude","model":"claude-test","tier":"strong"}`,
		"unknown agent": `{"agent":"missing","model":"x"}`,
		"trailing json": `{"agent":"claude","model":"claude-test"} {}`,
	} {
		t.Run("invalid: "+name, func(t *testing.T) {
			if _, err := parseNotebookNarrationConfig(notebookSummarizeSessionKind, raw); err == nil {
				t.Fatalf("parseNotebookNarrationConfig(%q) succeeded", raw)
			}
		})
	}
}

func TestNotebookNarrationConfigForAppliesDefaultsAndSettings(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	summarize, err := d.notebookNarrationConfigFor(notebookSummarizeSessionKind)
	if err != nil {
		t.Fatalf("summarize default: %v", err)
	}
	if summarize.Model != notebookSummarizeDefaultModel {
		t.Fatalf("summarize default model = %q", summarize.Model)
	}
	narrate, err := d.notebookNarrationConfigFor(notebookNarrateWorkspaceKind)
	if err != nil {
		t.Fatalf("narrate default: %v", err)
	}
	if narrate.Model != notebookNarrateDefaultModel {
		t.Fatalf("narrate default model = %q", narrate.Model)
	}

	d.store.SetSetting(SettingNotebookNarrateWorkspace, `{"agent":"claude","model":"claude-custom"}`)
	narrate, err = d.notebookNarrationConfigFor(notebookNarrateWorkspaceKind)
	if err != nil {
		t.Fatalf("narrate override: %v", err)
	}
	if narrate.Model != "claude-custom" {
		t.Fatalf("narrate override model = %q", narrate.Model)
	}
}

func TestBuildSummarizeSessionPromptEmbedsBriefAndPaths(t *testing.T) {
	prompt := buildSummarizeSessionPrompt("/t/transcript.jsonl", "session-xyz", "/raw/sessions/session-xyz.md")
	if !strings.Contains(prompt, "You are the attn keeper, performing your session-summary duty.") {
		t.Fatal("summarize prompt dropped the verbatim brief")
	}
	for _, want := range []string{
		"TRANSCRIPT_PATH: /t/transcript.jsonl",
		"SESSION_ID: session-xyz",
		"RAW_DIGEST_PATH: /raw/sessions/session-xyz.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("summarize prompt missing %q", want)
		}
	}
}

func TestBuildNarrateWorkspacePromptEmbedsBriefPathsAndRemovalFlag(t *testing.T) {
	prompt := buildNarrateWorkspacePrompt(narrateWorkspacePromptInputs{
		WorkspaceTitle:      "Chifplace",
		WorkspaceID:         "ws-1",
		ContextSnapshotPath: "/raw/context-snapshots/ws-1.md",
		RawSessionsDir:      "/raw/sessions",
		TranscriptPaths:     []string{"/t/a.jsonl", "/t/b.jsonl"},
		JournalPath:         "/nb/journal/2026-06-15.md",
		JournalDir:          "/nb/journal",
		KnowledgeDir:        "/nb/knowledge",
		IsRemovalPass:       true,
	})
	if !strings.Contains(prompt, "You are the attn keeper, narrating this workspace's work into the journal.") {
		t.Fatal("narrate prompt dropped the verbatim brief")
	}
	for _, want := range []string{
		"WORKSPACE_TITLE: Chifplace",
		"WORKSPACE_ID: ws-1",
		"CONTEXT_SNAPSHOT_PATH: /raw/context-snapshots/ws-1.md",
		"RAW_SESSIONS_DIR: /raw/sessions",
		"- /t/a.jsonl",
		"- /t/b.jsonl",
		"JOURNAL_PATH: /nb/journal/2026-06-15.md",
		"JOURNAL_DIR: /nb/journal",
		"KNOWLEDGE_DIR: /nb/knowledge",
		"IS_REMOVAL_PASS: true",
		"ARCHIVE THE WORKSPACE'S PROJECT FOLDER (removal pass only)",
		"resource: attn:workspace/<WORKSPACE_ID>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("narrate prompt missing %q", want)
		}
	}

	active := buildNarrateWorkspacePrompt(narrateWorkspacePromptInputs{WorkspaceID: "ws-2"})
	if !strings.Contains(active, "TRANSCRIPT_PATHS: (none resolved)") {
		t.Fatal("narrate prompt missing the empty-transcripts line")
	}
	if !strings.Contains(active, "IS_REMOVAL_PASS: false") {
		t.Fatal("narrate prompt missing IS_REMOVAL_PASS: false")
	}
}

func installNotebookNarrationRunner(t *testing.T, d *Daemon) string {
	t.Helper()
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), writeFakeAgentExecutable(t))

	runner := jobs.New(jobs.Options{
		Store: newTestJobStore(t, d),
		Log:   func(string, ...interface{}) {},
	})
	if err := runner.RegisterWith(notebookSummarizeSessionKind, d.summarizeSessionHandler,
		jobs.HandlerConfig{Timeout: notebookSummarizeSessionTimeout}); err != nil {
		t.Fatalf("register summarize_session: %v", err)
	}
	if err := runner.RegisterWith(notebookNarrateWorkspaceKind, d.narrateWorkspaceHandler,
		jobs.HandlerConfig{Timeout: notebookNarrateWorkspaceTimeout}); err != nil {
		t.Fatalf("register narrate_workspace: %v", err)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.jobQueue = runner
	return root
}

func writeFakeAgentExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	return path
}

func waitForTaskState(t *testing.T, d *Daemon, kind, subject string, want jobs.State) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		task, err := d.jobQueue.GetByKey(kind, subject)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task != nil && task.State == want {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s:%s did not reach %s (last=%+v)", kind, subject, want, task)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func requireTaskState(t *testing.T, d *Daemon, kind, subject string, want jobs.State) *jobs.Job {
	t.Helper()
	synctest.Wait()
	task, err := d.jobQueue.GetByKey(kind, subject)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task == nil || task.State != want {
		t.Fatalf("task %s:%s settled at %s, want %s (last=%+v)", kind, subject, taskState(task), want, task)
	}
	return task
}

func taskState(task *jobs.Job) jobs.State {
	if task == nil {
		return "<absent>"
	}
	return task.State
}

func TestSummarizeSessionExecutorVerifiesDigestLedger(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		now := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{
			ID: "session-1", Label: "session-1", Agent: protocol.SessionAgentClaude,
			Directory: t.TempDir(), State: protocol.SessionStateIdle,
			StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		root := installNotebookNarrationRunner(t, d)

		soloBucket := filepath.Join(notebook.RawSessionsDir(root), notebookSoloSessionBucket)
		digest := filepath.Join(soloBucket, "session-1.md")

		d.summarizeSessionExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			if err := os.WriteFile(digest, []byte("# Session Digest\n"), 0o644); err != nil {
				t.Fatalf("fake write digest: %v", err)
			}
			if !strings.Contains(req.Prompt, "RAW_DIGEST_PATH: "+digest) {
				t.Fatalf("prompt RAW_DIGEST_PATH not the bucketed path:\n%s", req.Prompt)
			}
			if len(req.ExtraWritableRoots) != 1 || req.ExtraWritableRoots[0] != soloBucket {
				t.Fatalf("ExtraWritableRoots = %v, want [%s]", req.ExtraWritableRoots, soloBucket)
			}
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: "session-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "session-1", jobs.StateDone)

		if _, err := os.Stat(digest); err != nil {
			t.Fatalf("digest not written: %v", err)
		}
	})
}

func TestSummarizeSessionExecutorFailsWhenDigestMissing(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		now := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{
			ID: "session-1", Label: "session-1", Agent: protocol.SessionAgentClaude,
			Directory: t.TempDir(), State: protocol.SessionStateIdle,
			StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		installNotebookNarrationRunner(t, d)

		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{Diagnostics: "claimed done"}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: "session-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task := requireTaskFailure(t, d, notebookSummarizeSessionKind, "session-1")
		if !strings.Contains(task.LastError, "did not write digest") {
			t.Fatalf("last error = %q, want digest-missing", task.LastError)
		}
	})
}

func TestSummarizeSessionExecutorSkipsRemovedSession(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)

		executed := false
		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			executed = true
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: "gone-session"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "gone-session", jobs.StateDone)
		if executed {
			t.Fatal("executor ran the agent for a removed session")
		}
	})
}

func TestSummarizeSessionExecutorUsesCarriedPayloadWhenRowGone(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		d.narrateWorkspaceExecution = blockingExecution(t)

		carriedTranscript := filepath.Join(t.TempDir(), "final-turn.jsonl")
		if err := os.WriteFile(carriedTranscript, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("seed transcript: %v", err)
		}
		wsBucket := filepath.Join(notebook.RawSessionsDir(root), "ws-gone")
		digest := filepath.Join(wsBucket, "session-1.md")
		soloDigest := filepath.Join(notebook.RawSessionsDir(root), notebookSoloSessionBucket, "session-1.md")

		d.summarizeSessionExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			if !strings.Contains(req.Prompt, "TRANSCRIPT_PATH: "+carriedTranscript) {
				t.Fatalf("prompt did not use carried transcript path:\n%s", req.Prompt)
			}
			if !strings.Contains(req.Prompt, "RAW_DIGEST_PATH: "+digest) {
				t.Fatalf("digest not routed to workspace bucket:\n%s", req.Prompt)
			}
			if err := os.WriteFile(digest, []byte("# Final session digest\n"), 0o644); err != nil {
				t.Fatalf("fake write digest: %v", err)
			}
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{
			UniqueKey: "session-1",
			Payload: summarizeSessionPayload{
				Transcript:  carriedTranscript,
				WorkspaceID: protocol.Ptr("ws-gone"),
			},
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "session-1", jobs.StateDone)

		if _, err := os.Stat(digest); err != nil {
			t.Fatalf("digest not written to workspace bucket: %v", err)
		}
		if _, err := os.Stat(soloDigest); err == nil {
			t.Fatal("digest leaked into the _solo bucket instead of the workspace bucket")
		}
	})
}

func TestSummarizeSessionReNarratesWhenWorkspaceRemoved(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		d.narrateWorkspaceExecution = blockingExecution(t)

		carriedTranscript := filepath.Join(t.TempDir(), "turn.jsonl")
		if err := os.WriteFile(carriedTranscript, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("seed transcript: %v", err)
		}
		digest := filepath.Join(notebook.RawSessionsDir(root), "ws-gone", "session-1.md")
		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			if err := os.WriteFile(digest, []byte("# digest\n"), 0o644); err != nil {
				t.Fatalf("fake write digest: %v", err)
			}
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{
			UniqueKey: "session-1",
			Payload: summarizeSessionPayload{
				Transcript:  carriedTranscript,
				WorkspaceID: protocol.Ptr("ws-gone"),
			},
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "session-1", jobs.StateDone)

		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-gone") {
			t.Fatal("digest success for a removed workspace did not re-enqueue a narrate")
		}
	})
}

func TestSummarizeSessionDoesNotReNarrateWhenWorkspacePresent(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-live")
		root := installNotebookNarrationRunner(t, d)
		d.narrateWorkspaceExecution = blockingExecution(t)

		digest := filepath.Join(notebook.RawSessionsDir(root), "ws-live", "session-1.md")
		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			if err := os.WriteFile(digest, []byte("# digest\n"), 0o644); err != nil {
				t.Fatalf("fake write digest: %v", err)
			}
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{
			UniqueKey: "session-1",
			Payload:   summarizeSessionPayload{WorkspaceID: protocol.Ptr("ws-live")},
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "session-1", jobs.StateDone)

		task, err := d.jobQueue.GetByKey(notebookNarrateWorkspaceKind, "ws-live")
		if err != nil {
			t.Fatalf("get narrate: %v", err)
		}
		if task != nil {
			t.Fatalf("live workspace unexpectedly got a re-narrate task: %+v", task)
		}
	})
}

func TestNarrateWorkspaceExecutorActiveDayVerifiesMarker(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		d.narrateWorkspaceExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			if !strings.Contains(req.Prompt, "IS_REMOVAL_PASS: false") {
				t.Fatalf("expected active-day prompt, got removal flag set")
			}
			if len(req.ExtraWritableRoots) != 1 || req.ExtraWritableRoots[0] != root {
				t.Fatalf("ExtraWritableRoots = %v, want [%s]", req.ExtraWritableRoots, root)
			}
			journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
			body := "## Chifplace — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nDid work.\n"
			if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
				t.Fatalf("fake write journal: %v", err)
			}
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: "ws-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", jobs.StateDone)

		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		content, err := os.ReadFile(journal)
		if err != nil {
			t.Fatalf("read journal: %v", err)
		}
		if !strings.Contains(string(content), "attn:wsnarr:ws-1") {
			t.Fatalf("journal missing workspace marker:\n%s", content)
		}
	})
}

func TestNarrateWorkspaceExecutorRemovalPassDerivesFlagFromAbsentRow(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		var sawRemoval bool
		d.narrateWorkspaceExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			sawRemoval = strings.Contains(req.Prompt, "IS_REMOVAL_PASS: true")
			if !strings.Contains(req.Prompt, "WORKSPACE_TITLE: ws-removed") {
				t.Fatalf("removal prompt did not fall back title to id:\n%s", req.Prompt)
			}
			journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
			body := "## ws-removed — 2026-06-15\n<!-- attn:wsnarr:ws-removed -->\n\nRetrospective.\n"
			if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
				t.Fatalf("fake write journal: %v", err)
			}
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: "ws-removed"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookNarrateWorkspaceKind, "ws-removed", jobs.StateDone)
		if !sawRemoval {
			t.Fatal("removal pass did not derive IS_REMOVAL_PASS=true from absent workspace row")
		}
	})
}

func TestNarrateWorkspaceExecutorFailsWhenMarkerMissing(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
			if err := os.WriteFile(journal, []byte("## Other — 2026-06-15\n<!-- attn:wsnarr:other-ws -->\n"), 0o644); err != nil {
				t.Fatalf("fake write journal: %v", err)
			}
			return agentdriver.HeadlessTaskResult{Diagnostics: "wrote wrong marker"}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: "ws-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task := requireTaskFailure(t, d, notebookNarrateWorkspaceKind, "ws-1")
		if !strings.Contains(task.LastError, "did not write") {
			t.Fatalf("last error = %q, want marker-missing", task.LastError)
		}
	})
}

func requireTaskFailure(t *testing.T, d *Daemon, kind, subject string) *jobs.Job {
	t.Helper()
	synctest.Wait()
	task, err := d.jobQueue.GetByKey(kind, subject)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task == nil || task.LastError == "" || task.State == jobs.StateDone {
		t.Fatalf("task %s:%s recorded no failure (last=%+v)", kind, subject, task)
	}
	return task
}

func TestHandleStopEnqueuesNarrationForWorkspaceSession(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		installNotebookNarrationRunner(t, d)
		d.summarizeSessionExecution = blockingExecution(t)
		d.narrateWorkspaceExecution = blockingExecution(t)

		d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "session-1"})

		if !taskExists(t, d, notebookSummarizeSessionKind, "session-1") {
			t.Fatal("stop did not enqueue summarize_session")
		}
		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-1") {
			t.Fatal("stop did not enqueue narrate_workspace for the live workspace")
		}
		settleStopClassification(t)
	})
}

func TestHandleStopEnqueuesOnlyDigestForSoloSession(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		now := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{
			ID: "solo", Label: "solo", Agent: protocol.SessionAgentClaude,
			Directory: t.TempDir(), State: protocol.SessionStateIdle,
			StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		installNotebookNarrationRunner(t, d)
		d.summarizeSessionExecution = blockingExecution(t)

		d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "solo"})

		if !taskExists(t, d, notebookSummarizeSessionKind, "solo") {
			t.Fatal("stop did not enqueue summarize_session for solo session")
		}
		task, err := d.jobQueue.GetByKey(notebookNarrateWorkspaceKind, "")
		if err != nil {
			t.Fatalf("get narrate: %v", err)
		}
		if task != nil {
			t.Fatalf("solo session unexpectedly enqueued a narrate task: %+v", task)
		}
		settleStopClassification(t)
	})
}

func TestHandleStopCarriesTranscriptAndWorkspaceOnThePayload(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		installNotebookNarrationRunner(t, d)
		d.summarizeSessionExecution = blockingExecution(t)
		d.narrateWorkspaceExecution = blockingExecution(t)

		transcript := filepath.Join(t.TempDir(), "turn.jsonl")
		if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("seed transcript: %v", err)
		}

		d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "session-1", TranscriptPath: transcript})

		if !taskExists(t, d, notebookSummarizeSessionKind, "session-1") {
			t.Fatal("stop did not enqueue summarize_session")
		}
		task, err := d.jobQueue.GetByKey(notebookSummarizeSessionKind, "session-1")
		if err != nil || task == nil {
			t.Fatalf("get summarize task: %v", err)
		}
		var carried summarizeSessionPayload
		if err := task.DecodePayload(&carried); err != nil {
			t.Fatalf("decode summarize payload: %v", err)
		}
		if carried.Transcript != transcript {
			t.Fatalf("summarize payload transcript = %q, want %q", carried.Transcript, transcript)
		}
		if carried.WorkspaceID == nil || *carried.WorkspaceID != "ws-1" {
			t.Fatalf("summarize payload workspace = %v, want ws-1", carried.WorkspaceID)
		}
		settleStopClassification(t)
	})
}

func TestWorkspaceRemovalEnqueuesFinalNarrate(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		installNotebookNarrationRunner(t, d)
		d.narrateWorkspaceExecution = blockingExecution(t)

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{
			UniqueKey: "ws-1", Delay: time.Hour,
		}); err != nil {
			t.Fatalf("seed pending narrate: %v", err)
		}
		before, err := d.jobQueue.GetByKey(notebookNarrateWorkspaceKind, "ws-1")
		if err != nil || before == nil {
			t.Fatalf("get seeded task: %v", err)
		}

		d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: "ws-1"})

		if d.store.GetWorkspace("ws-1") != nil {
			t.Fatal("workspace not removed")
		}
		after, err := d.jobQueue.GetByKey(notebookNarrateWorkspaceKind, "ws-1")
		if err != nil || after == nil {
			t.Fatalf("get final task: %v", err)
		}
		if after.ScheduledAt.After(before.ScheduledAt) {
			t.Fatalf("final narrate did not override the pending debounce: before=%s after=%s",
				before.ScheduledAt, after.ScheduledAt)
		}
	})
}

func TestNarrationTriggersAreNilSafeBeforeRunner(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	d.jobQueue = nil

	d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "session-1"})
	d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: "ws-1"})

	if d.store.GetWorkspace("ws-1") != nil {
		t.Fatal("workspace not removed after nil-runner unregister")
	}
}

func blockingExecution(t *testing.T) func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return func(ctx context.Context, _ agentdriver.HeadlessTaskProvider, _ agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return agentdriver.HeadlessTaskResult{}, ctx.Err()
	}
}

func assertJournalUntouched(t *testing.T, root string) {
	t.Helper()
	journalDir := filepath.Join(root, notebook.DirJournal)
	entries, err := os.ReadDir(journalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read journal dir: %v", err)
	}
	for _, e := range entries {
		t.Fatalf("journal dir unexpectedly contains %q — a write escaped the raw tier", e.Name())
	}
}

func TestSummarizeSessionExecutorRejectsTraversalSessionID(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		now := string(protocol.TimestampNow())
		craftedID := "../../../journal/2026-06-15"
		d.store.Add(&protocol.Session{
			ID: craftedID, Label: craftedID, Agent: protocol.SessionAgentClaude,
			Directory: t.TempDir(), State: protocol.SessionStateIdle,
			StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		root := installNotebookNarrationRunner(t, d)

		ran := false
		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			ran = true
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: craftedID}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task := requireTaskFailure(t, d, notebookSummarizeSessionKind, craftedID)
		if !strings.Contains(task.LastError, "unsafe session id") {
			t.Fatalf("last error = %q, want unsafe-session-id rejection", task.LastError)
		}
		if ran {
			t.Fatal("executor spawned the agent for a traversal session id")
		}
		assertJournalUntouched(t, root)
	})
}

func TestNarrateWorkspaceExecutorRejectsTraversalWorkspaceID(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		craftedID := "../../../journal/2026-06-15"
		ran := false
		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			ran = true
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: craftedID}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task := requireTaskFailure(t, d, notebookNarrateWorkspaceKind, craftedID)
		if !strings.Contains(task.LastError, "unsafe workspace id") {
			t.Fatalf("last error = %q, want unsafe-workspace-id rejection", task.LastError)
		}
		if ran {
			t.Fatal("executor spawned the agent for a traversal workspace id")
		}
		assertJournalUntouched(t, root)
	})
}

func TestSummarizeSessionExecutorRequiresFreshDigest(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		now := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{
			ID: "session-1", Label: "session-1", Agent: protocol.SessionAgentClaude,
			Directory: t.TempDir(), State: protocol.SessionStateIdle,
			StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		root := installNotebookNarrationRunner(t, d)

		digest := filepath.Join(notebook.RawSessionsDir(root), notebookSoloSessionBucket, "session-1.md")
		if err := os.MkdirAll(filepath.Dir(digest), 0o755); err != nil {
			t.Fatalf("mkdir bucket: %v", err)
		}
		if err := os.WriteFile(digest, []byte("# Session Digest\n\nprior run\n"), 0o644); err != nil {
			t.Fatalf("seed prior digest: %v", err)
		}

		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{Diagnostics: "no-op"}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: "session-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task := requireTaskFailure(t, d, notebookSummarizeSessionKind, "session-1")
		if !strings.Contains(task.LastError, "unchanged") {
			t.Fatalf("last error = %q, want unchanged-digest rejection", task.LastError)
		}
	})
}

func TestNarrateWorkspaceExecutorRequiresFreshEntry(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
			t.Fatalf("mkdir journal: %v", err)
		}
		prior := "## ws-1 — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nactive-day entry\n\nsource: workspace:ws-1\n"
		if err := os.WriteFile(journal, []byte(prior), 0o644); err != nil {
			t.Fatalf("seed prior entry: %v", err)
		}

		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{Diagnostics: "no-op"}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: "ws-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task := requireTaskFailure(t, d, notebookNarrateWorkspaceKind, "ws-1")
		if !strings.Contains(task.LastError, "unchanged") {
			t.Fatalf("last error = %q, want unchanged-entry rejection", task.LastError)
		}
	})
}

func TestWorkspaceNarrationBlockNoPrefixCollision(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "2026-06-15.md")
	body := "## ws-10 — 2026-06-15\n<!-- attn:wsnarr:ws-10 -->\n\nsibling entry\n\nsource: workspace:ws-10\n"
	if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	got, err := workspaceNarrationBlock(journal, "ws-1")
	if err != nil {
		t.Fatalf("workspaceNarrationBlock: %v", err)
	}
	if got.present {
		t.Fatal("ws-1 ledger falsely verified off ws-10's entry (prefix collision)")
	}

	got, err = workspaceNarrationBlock(journal, "ws-10")
	if err != nil {
		t.Fatalf("workspaceNarrationBlock ws-10: %v", err)
	}
	if !got.present || !strings.Contains(got.body, "sibling entry") {
		t.Fatalf("ws-10 ledger did not find its own entry: %+v", got)
	}
}

func TestNarrateWorkspaceScopesSessionsToWorkspace(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-a", "ws-1")
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		wantDir, err := notebookWorkspaceSessionsDir(root, "ws-1")
		if err != nil {
			t.Fatalf("workspace sessions dir: %v", err)
		}
		if wantDir == notebook.RawSessionsDir(root) {
			t.Fatal("per-workspace dir collapsed to the shared flat sessions dir")
		}

		d.narrateWorkspaceExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			if !strings.Contains(req.Prompt, "RAW_SESSIONS_DIR: "+wantDir) {
				t.Fatalf("narrate prompt RAW_SESSIONS_DIR not scoped to ws-1:\n%s", req.Prompt)
			}
			journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
			body := "## ws-1 — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nwork.\n"
			if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
				t.Fatalf("fake write journal: %v", err)
			}
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: "ws-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", jobs.StateDone)
	})
}

func TestDaemon_ValidatesNotebookNarrationAgentAndExecutable(t *testing.T) {
	tempDir := t.TempDir()
	executable := filepath.Join(tempDir, "custom-claude")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", tempDir)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), "custom-claude")

	for _, key := range []string{SettingNotebookSummarizeSession, SettingNotebookNarrateWorkspace} {
		if err := d.validateSetting(key, ""); err != nil {
			t.Fatalf("%s: blank rejected: %v", key, err)
		}
		if err := d.validateSetting(key, `{"agent":"claude","model":"m"}`); err != nil {
			t.Fatalf("%s: valid config rejected: %v", key, err)
		}
		if err := d.validateSetting(key, `{"agent":"claude"`); err == nil {
			t.Fatalf("%s: invalid JSON accepted", key)
		}
	}

	d.store.SetSetting(canonicalExecutableSettingKey("claude"), "missing-claude")
	if err := d.validateSetting(SettingNotebookNarrateWorkspace, `{"agent":"claude","model":"m"}`); err == nil {
		t.Fatal("narrate setting accepted a missing configured executable")
	}
}

func taskExists(t *testing.T, d *Daemon, kind, subject string) bool {
	t.Helper()
	synctest.Wait()
	task, err := d.jobQueue.GetByKey(kind, subject)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task != nil
}
