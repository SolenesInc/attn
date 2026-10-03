package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"github.com/victorarias/attn/internal/transcript"
)

func TestKeptClaudeConversationRestoresForSeedResumeAndLedgerReopen(t *testing.T) {
	for _, entry := range []string{"seed resume", "ledger reopen"} {
		t.Run(entry, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
			first := w.Launched(delegated.SessionID)
			first.Prompted()
			first.Subagent("subagent's full answer")
			first.Reply("the conversation remembers <!-- attn:state=waiting_input -->")
			path := transcript.FindClaudeTranscript(first.ConversationID)
			subdir := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
			subs, err := os.ReadDir(subdir)
			if err != nil || len(subs) == 0 {
				t.Fatalf("subagent transcripts: %v, %v", subs, err)
			}
			subpath := filepath.Join(subdir, subs[0].Name())
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			suboriginal, err := os.ReadFile(subpath)
			if err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-40 * 24 * time.Hour)
			for _, file := range []string{path, subpath} {
				if err := os.Chtimes(file, old, old); err != nil {
					t.Fatal(err)
				}
			}
			closePane(app, sessionPane{session: delegated.SessionID})
			testworld.AwaitTaskDone(app, "conversation_keep")
			kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
			if kept == nil || kept.Bytes <= 0 || protocol.Deref(kept.DeleteAfter) != "" {
				t.Fatalf("open seed's kept conversation: %+v", kept)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Dir(subdir)); err != nil {
				t.Fatal(err)
			}
			before := time.Now().Add(-time.Second)
			if entry == "seed resume" {
				if result := seedResumeRequest(app, delegated.SeedID); !result.Success {
					t.Fatalf("resume: %+v", result)
				}
			} else {
				if result := reopenOverTheWebSocket(app, delegated.SessionID); !result.Success {
					t.Fatalf("reopen: %+v", result)
				}
			}
			seedResumeContinues(t, w, first, delegated.SessionID)
			for restored, want := range map[string][]byte{path: original, subpath: suboriginal} {
				got, err := os.ReadFile(restored)
				if err != nil || string(got) != string(want) {
					t.Errorf("restored %s = %q, %v; want %q", restored, got, err, want)
				}
				info, err := os.Stat(restored)
				if err != nil || info.ModTime().Before(before) {
					t.Errorf("restored modification time: %v, %v", info, err)
				}
			}
		})
	}
}

func TestKeptConversationReleaseReplantAndDeletionAreVisible(t *testing.T) {
	t.Setenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS", "")
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	// Lifecycle writes protect against cleanup, so their keep pass can skip retirement.
	// Notes queue an unprotected pass; normal grace preserves release/replant checks.
	keepPass := func() {
		t.Helper()
		if _, err := cli.SeedNote("", delegated.SeedID, "Record the abandoned work", "", "", false, nil); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitTaskDone(app, "conversation_keep")
	}
	first := w.Launched(delegated.SessionID)
	first.Prompted()
	closePane(app, sessionPane{session: delegated.SessionID})
	testworld.AwaitTaskDone(app, "conversation_keep")
	lifeMove(t, cli, "", delegated.SeedID, "wither", "abandoned", "")
	keepPass()
	released := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	if released == nil || protocol.Deref(released.DeleteAfter) == "" {
		t.Fatalf("released copy: %+v", released)
	}
	// Replant retains the copy even after Claude has removed its own transcript.
	sessionRecoveryDeleteTranscript(t, first.ConversationID)
	lifeMove(t, cli, "", delegated.SeedID, "replant", "", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	retained := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	if retained == nil || retained.DeleteAfter != nil {
		t.Fatalf("replanted copy: %+v", retained)
	}
	lifeMove(t, cli, "", delegated.SeedID, "wither", "abandoned again", "")
	keepPass()
	t.Setenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS", "0")
	keepPass()
	deleted := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	if deleted == nil || protocol.Deref(deleted.DeletedAt) == "" {
		t.Fatalf("deleted copy: %+v", deleted)
	}
	lifeMove(t, cli, "", delegated.SeedID, "replant", "", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	date := strings.Split(protocol.Deref(deleted.DeletedAt), "T")[0]
	result := seedResumeRequest(app, delegated.SeedID)
	if result.Success || !strings.Contains(protocol.Deref(result.Error), "attn deleted its copy") || !strings.Contains(protocol.Deref(result.Error), date) {
		t.Fatalf("expired resume: %+v, want refusal naming deletion %s", result, date)
	}
	verdict := reopenVerdict(t, cli, delegated.SessionID)
	if verdict.Reopenable || !strings.Contains(protocol.Deref(verdict.Reason), date) {
		t.Fatalf("expired reopen verdict: %+v", verdict)
	}
}

func TestLiveClaudeIsKeptOnlyWhenQuietAndNeverReplacedByASmallerSource(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	first := w.Launched(delegated.SessionID)
	first.Prompted()
	first.Reply("original full conversation <!-- attn:state=waiting_input -->")
	lifeMove(t, cli, delegated.SessionID, delegated.SeedID, "park", "", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept != nil {
		t.Fatalf("active conversation was copied: %+v", kept)
	}
	path := transcript.FindClaudeTranscript(first.ConversationID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	lifeMove(t, cli, delegated.SessionID, delegated.SeedID, "tend", "", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	if kept == nil {
		t.Fatal("quiet conversation was not copied")
	}
	// The harness owns existing files, even when it recreates a shorter transcript.
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	closePane(app, sessionPane{session: delegated.SessionID})
	testworld.AwaitTaskDone(app, "conversation_keep")
	if next := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; next.CopiedAt != kept.CopiedAt {
		t.Fatalf("smaller source replaced the copy: %+v", next)
	}
	if result := seedResumeRequest(app, delegated.SeedID); !result.Success {
		t.Fatalf("resume existing: %+v", result)
	}
	seedResumeContinues(t, w, first, delegated.SessionID)
	if got, err := os.ReadFile(path); err != nil || string(got) != "{}\n" {
		t.Fatalf("existing transcript overwritten: %q, %v", got, err)
	}
	closePane(app, sessionPane{session: delegated.SessionID})
	testworld.AwaitTaskDone(app, "conversation_keep")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if result := seedResumeRequest(app, delegated.SeedID); !result.Success {
		t.Fatalf("restore larger copy: %+v", result)
	}
	seedResumeContinues(t, w, first, delegated.SessionID)
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("restored copy lost original content: %q, %v", got, err)
	}
}

func TestCodexConversationNeedsNoKeptCopy(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	first := w.Launched(delegated.SessionID)
	first.Prompted()
	closePane(app, sessionPane{session: delegated.SessionID})
	testworld.AwaitTaskDone(app, "conversation_keep")
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept != nil {
		t.Fatalf("Codex copied: %+v", kept)
	}
}

func TestSessionsSharingAConversationKeepOneCopyUntilAllTheirWorkCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	first := w.Launched(delegated.SessionID)
	first.Prompted()
	first.Subagent("original subagent output")
	first.Reply("first session's answer <!-- attn:state=waiting_input -->")
	originalPath := transcript.FindClaudeTranscript(first.ConversationID)
	older, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	subfiles, err := filepath.Glob(filepath.Join(strings.TrimSuffix(originalPath, ".jsonl"), "subagents", "*.jsonl"))
	if err != nil || len(subfiles) != 1 {
		t.Fatalf("subagent files: %v, %v", subfiles, err)
	}
	suboriginal, err := os.ReadFile(subfiles[0])
	if err != nil {
		t.Fatal(err)
	}
	closePane(app, sessionPane{session: delegated.SessionID})
	testworld.AwaitTaskDone(app, "conversation_keep")
	spawned, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("elsewhere"), func(msg *protocol.SpawnSessionMessage) {
		msg.ResumeSessionID = protocol.Ptr(first.ConversationID)
	})
	if !spawned.Success {
		t.Fatalf("shared spawn: %+v", spawned)
	}
	second := spawned.ID
	resumed := w.Launched(second)
	if resumed.ConversationID != first.ConversationID || !resumed.Resumed {
		t.Fatalf("shared resume: %+v", resumed)
	}
	secondSeed := plantSeedAs(t, cli, "", "continue the same conversation")
	lifeMove(t, cli, second, secondSeed, "tend", "", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	app.TypeLine(second, "continue the work")
	resumed.Prompted()
	resumed.Reply("second session's longer answer to keep with the original conversation <!-- attn:state=waiting_input -->")
	closePane(app, sessionPane{session: second})
	testworld.AwaitTaskDone(app, "conversation_keep")
	firstCopy := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	secondCopy := lifeShow(t, cli, secondSeed).Seed.Continuation.KeptConversation
	if firstCopy == nil || secondCopy == nil || *firstCopy != *secondCopy {
		t.Fatalf("shared copy: first %+v, second %+v", firstCopy, secondCopy)
	}
	lifeMove(t, cli, "", delegated.SeedID, "wither", "first work abandoned", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	kept := lifeShow(t, cli, secondSeed).Seed.Continuation.KeptConversation
	if kept == nil || kept.DeleteAfter != nil {
		t.Fatalf("copy released while other work remains open: %+v", kept)
	}
	mainfiles, err := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(originalPath)), "*", first.ConversationID+".jsonl"))
	if err != nil || len(mainfiles) != 2 {
		t.Fatalf("cross-directory main transcripts: %v, %v", mainfiles, err)
	}
	newerPath := mainfiles[0]
	if newerPath == originalPath {
		newerPath = mainfiles[1]
	}
	newer, err := os.ReadFile(newerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(newerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(strings.TrimSuffix(originalPath, ".jsonl")); err != nil {
		t.Fatal(err)
	}
	partialRestore := seedResumeRequest(app, secondSeed)
	if !partialRestore.Success {
		t.Fatalf("restore newer transcript while older survives: %+v", partialRestore)
	}
	continued := w.Launched(second)
	if got, err := os.ReadFile(newerPath); err != nil || string(got) != string(newer) {
		t.Fatalf("newer transcript replaced with stale surviving history: %q, %v", got, err)
	}
	if got, err := os.ReadFile(subfiles[0]); err != nil || string(got) != string(suboriginal) {
		t.Fatalf("missing auxiliary file not restored with surviving main: %q, %v", got, err)
	}
	if err := os.RemoveAll(strings.TrimSuffix(originalPath, ".jsonl")); err != nil {
		t.Fatal(err)
	}
	app.TypeLine(second, "continue after the original auxiliary files were pruned")
	continued.Prompted()
	continued.Reply("the new archive must retain missing old auxiliary files <!-- attn:state=waiting_input -->")
	closePane(app, sessionPane{session: second})
	testworld.AwaitTaskDone(app, "conversation_keep")
	sessionRecoveryDeleteTranscript(t, first.ConversationID)
	if err := os.RemoveAll(strings.TrimSuffix(originalPath, ".jsonl")); err != nil {
		t.Fatal(err)
	}
	fullRestore := seedResumeRequest(app, secondSeed)
	if !fullRestore.Success {
		t.Fatalf("shared conversation restore: %+v", fullRestore)
	}
	seedResumeContinues(t, w, first, second)
	if got, err := os.ReadFile(subfiles[0]); err != nil || string(got) != string(suboriginal) {
		t.Fatalf("subagent output from original directory lost: %q, %v", got, err)
	}
	latest, err := os.ReadFile(newerPath)
	if err != nil {
		t.Fatal(err)
	}
	closePane(app, sessionPane{session: second})
	testworld.AwaitTaskDone(app, "conversation_keep")
	if err := os.WriteFile(originalPath, older, 0600); err != nil {
		t.Fatal(err)
	}
	third, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("third-directory"), func(msg *protocol.SpawnSessionMessage) {
		msg.ResumeSessionID = protocol.Ptr(first.ConversationID)
	})
	if !third.Success {
		t.Fatalf("third-directory resume: %+v", third)
	}
	w.Launched(third.ID)
	mainfiles, err = filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(originalPath)), "*", first.ConversationID+".jsonl"))
	if err != nil || len(mainfiles) != 3 {
		t.Fatalf("third-directory main transcripts: %v, %v", mainfiles, err)
	}
	for _, path := range mainfiles {
		if path == originalPath || path == newerPath {
			continue
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != string(latest) {
			t.Fatalf("new directory resumed stale surviving history: %q, %v", got, err)
		}
	}
}

func TestOpenWorkKeepsLatestClaudeHistoryFromAnUnassignedSession(t *testing.T) {
	for _, oldSource := range []string{"present", "removed"} {
		t.Run(oldSource, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
			first := w.Launched(delegated.SessionID)
			first.Prompted()
			first.Reply("the seed's original history <!-- attn:state=waiting_input -->")
			originalPath := transcript.FindClaudeTranscript(first.ConversationID)
			closePane(app, sessionPane{session: delegated.SessionID})
			testworld.AwaitTaskDone(app, "conversation_keep")
			spawned, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("unassigned-directory"), func(msg *protocol.SpawnSessionMessage) {
				msg.ResumeSessionID = protocol.Ptr(first.ConversationID)
			})
			if !spawned.Success {
				t.Fatalf("plain conversation resume: %+v", spawned)
			}
			second := w.Launched(spawned.ID)
			app.TypeLine(spawned.ID, "continue without tending any seed")
			second.Prompted()
			second.Subagent("the unassigned session's complete auxiliary answer")
			second.Reply("newer history belongs to the shared conversation <!-- attn:state=waiting_input -->")
			projects := filepath.Dir(filepath.Dir(originalPath))
			mains, err := filepath.Glob(filepath.Join(projects, "*", first.ConversationID+".jsonl"))
			if err != nil || len(mains) != 2 {
				t.Fatalf("cross-directory transcripts: %v, %v", mains, err)
			}
			newerPath := mains[0]
			if newerPath == originalPath {
				newerPath = mains[1]
			}
			latest, err := os.ReadFile(newerPath)
			if err != nil {
				t.Fatal(err)
			}
			auxiliary, err := filepath.Glob(filepath.Join(strings.TrimSuffix(newerPath, ".jsonl"), "subagents", "*.jsonl"))
			if err != nil || len(auxiliary) != 1 {
				t.Fatalf("new session's auxiliary transcript: %v, %v", auxiliary, err)
			}
			auxiliaryBytes, err := os.ReadFile(auxiliary[0])
			if err != nil {
				t.Fatal(err)
			}
			if oldSource == "removed" {
				if err := os.Remove(originalPath); err != nil {
					t.Fatal(err)
				}
			}
			closePane(app, sessionPane{session: spawned.ID})
			testworld.AwaitTaskDone(app, "conversation_keep")
			if seed := lifeShow(t, cli, delegated.SeedID).Seed; protocol.Deref(seed.LastExecutionID) != delegated.SessionID {
				t.Fatalf("plain resume changed seed's execution: %+v", seed)
			}
			sessionRecoveryDeleteTranscript(t, first.ConversationID)
			for _, path := range mains {
				if err := os.RemoveAll(strings.TrimSuffix(path, ".jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.RemoveAll(filepath.Join(filepath.Dir(projects), "file-history", first.ConversationID)); err != nil {
				t.Fatal(err)
			}
			if result := seedResumeRequest(app, delegated.SeedID); !result.Success {
				t.Fatalf("seed resume after pruning: %+v", result)
			}
			seedResumeContinues(t, w, first, delegated.SessionID)
			for restored, want := range map[string][]byte{originalPath: latest, newerPath: latest, auxiliary[0]: auxiliaryBytes} {
				if got, err := os.ReadFile(restored); err != nil || string(got) != string(want) {
					t.Fatalf("latest unassigned-session history lost at %s: %q, %v", restored, got, err)
				}
			}
		})
	}
}

func TestOpenWorkKeepsDistinctClaudeConversationsInOneBatch(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	type conversation struct {
		seed, session, native, original, latest, auxiliary string
		continued                                          string
		mainBytes, auxiliaryBytes                          []byte
	}
	var conversations []conversation
	for _, marker := range []string{"first", "second"} {
		delegated := seedResumeDelegate(t, w, fakeagent.Claude, "batch-original-"+marker)
		first := w.Launched(delegated.SessionID)
		first.Prompted()
		first.Reply(marker + " conversation's original history <!-- attn:state=waiting_input -->")
		conversations = append(conversations, conversation{
			seed: delegated.SeedID, session: delegated.SessionID,
			native: first.ConversationID, original: transcript.FindClaudeTranscript(first.ConversationID),
		})
	}
	for _, c := range conversations {
		closePane(app, sessionPane{session: c.session})
		testworld.AwaitTaskDone(app, "conversation_keep")
	}
	for i := range conversations {
		c := &conversations[i]
		spawned, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("batch-newer"), func(msg *protocol.SpawnSessionMessage) {
			msg.ResumeSessionID = protocol.Ptr(c.native)
		})
		if !spawned.Success {
			t.Fatalf("plain resume of %s: %+v", c.native, spawned)
		}
		c.continued = spawned.ID
		second := w.Launched(spawned.ID)
		app.TypeLine(spawned.ID, "continue the distinct conversation without tending a seed")
		second.Prompted()
		second.Subagent("auxiliary history belongs to " + c.native)
		second.Reply("newer history belongs only to " + c.native + " <!-- attn:state=waiting_input -->")
		mains, err := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(c.original)), "*", c.native+".jsonl"))
		if err != nil || len(mains) != 2 {
			t.Fatalf("main transcripts for %s: %v, %v", c.native, mains, err)
		}
		c.latest = mains[0]
		if c.latest == c.original {
			c.latest = mains[1]
		}
		c.mainBytes, err = os.ReadFile(c.latest)
		if err != nil {
			t.Fatal(err)
		}
		auxiliary, err := filepath.Glob(filepath.Join(strings.TrimSuffix(c.latest, ".jsonl"), "subagents", "*.jsonl"))
		if err != nil || len(auxiliary) != 1 {
			t.Fatalf("auxiliary transcript for %s: %v, %v", c.native, auxiliary, err)
		}
		c.auxiliary = auxiliary[0]
		c.auxiliaryBytes, err = os.ReadFile(c.auxiliary)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range conversations {
		closePane(app, sessionPane{session: c.continued})
		testworld.AwaitTaskDone(app, "conversation_keep")
	}
	for _, c := range conversations {
		sessionRecoveryDeleteTranscript(t, c.native)
		for _, main := range []string{c.original, c.latest} {
			if err := os.RemoveAll(strings.TrimSuffix(main, ".jsonl")); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range conversations {
		if result := seedResumeRequest(app, c.seed); !result.Success {
			t.Fatalf("resume batched conversation %s: %+v", c.native, result)
		}
		if resumed := w.Launched(c.session); !resumed.Resumed || resumed.ConversationID != c.native {
			t.Fatalf("batched resume mixed conversation identity: %+v", resumed)
		}
		for restored, want := range map[string][]byte{c.original: c.mainBytes, c.latest: c.mainBytes, c.auxiliary: c.auxiliaryBytes} {
			if got, err := os.ReadFile(restored); err != nil || string(got) != string(want) {
				t.Fatalf("batched history for %s lost or mixed at %s: %q, %v", c.native, restored, got, err)
			}
		}
	}
}

func TestCrossDirectoryClaudeResumeIgnoresLargerSymlinkedTranscripts(t *testing.T) {
	for _, symlink := range []string{"transcript", "project directory"} {
		t.Run(symlink, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
			first := w.Launched(delegated.SessionID)
			first.Prompted()
			first.Reply("the actual conversation <!-- attn:state=waiting_input -->")
			originalPath := transcript.FindClaudeTranscript(first.ConversationID)
			original, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			closePane(app, sessionPane{session: delegated.SessionID})
			testworld.AwaitTaskDone(app, "conversation_keep")
			outside := w.Path("private-project")
			if err := os.MkdirAll(outside, 0700); err != nil {
				t.Fatal(err)
			}
			privatePath := filepath.Join(outside, first.ConversationID+".jsonl")
			private := strings.Repeat("private content must not enter a conversation\n", len(original)+1)
			if err := os.WriteFile(privatePath, []byte(private), 0600); err != nil {
				t.Fatal(err)
			}
			projects := filepath.Dir(filepath.Dir(originalPath))
			decoy := filepath.Join(projects, "aaa-private-decoy")
			if symlink == "project directory" {
				if err := os.Symlink(outside, decoy); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(decoy, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(privatePath, filepath.Join(decoy, first.ConversationID+".jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			resumed, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("third-directory"), func(msg *protocol.SpawnSessionMessage) {
				msg.ResumeSessionID = protocol.Ptr(first.ConversationID)
			})
			if !resumed.Success {
				t.Fatalf("cross-directory resume: %+v", resumed)
			}
			w.Launched(resumed.ID)
			paths, err := filepath.Glob(filepath.Join(projects, "*", first.ConversationID+".jsonl"))
			if err != nil || len(paths) != 3 {
				t.Fatalf("resumed main transcripts: %v, %v", paths, err)
			}
			for _, path := range paths {
				if path == originalPath || path == filepath.Join(decoy, first.ConversationID+".jsonl") {
					continue
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
					t.Fatalf("resume copied a symlinked private file: %q, %v", got, err)
				}
			}
			if got, err := os.ReadFile(privatePath); err != nil || string(got) != private {
				t.Fatalf("private transcript changed: %q, %v", got, err)
			}
		})
	}
}

func TestKeptConversationRefusesSymlinkedProviderDirectories(t *testing.T) {
	for _, stage := range []string{"copy", "restore", "reload"} {
		t.Run(stage, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
			first := w.Launched(delegated.SessionID)
			first.Prompted()
			first.Reply("keep this conversation <!-- attn:state=waiting_input -->")
			path := transcript.FindClaudeTranscript(first.ConversationID)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if stage != "copy" {
				closePane(app, sessionPane{session: delegated.SessionID})
				testworld.AwaitTaskDone(app, "conversation_keep")
				if lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation == nil {
					t.Fatal("conversation was not kept")
				}
			}
			if stage == "reload" {
				if result := seedResumeRequest(app, delegated.SeedID); !result.Success {
					t.Fatalf("resume before reload: %+v", result)
				}
				seedResumeContinues(t, w, first, delegated.SessionID)
			}
			outside := t.TempDir()
			if stage == "copy" {
				if err := os.WriteFile(filepath.Join(outside, filepath.Base(path)), []byte("private host data"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			project := filepath.Dir(path)
			if err := os.Rename(project, project+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, project); err != nil {
				t.Fatal(err)
			}
			if stage == "copy" {
				closePane(app, sessionPane{session: delegated.SessionID})
				testworld.AwaitTaskDone(app, "conversation_keep")
				if err := os.Remove(project); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(project + "-original"); err != nil {
					t.Fatal(err)
				}
				if result := seedResumeRequest(app, delegated.SeedID); !result.Success {
					t.Fatalf("resume safely kept relocated conversation: %+v", result)
				}
				seedResumeContinues(t, w, first, delegated.SessionID)
				if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
					t.Fatalf("kept symlinked private data instead of the real conversation: %q, %v", got, err)
				}
				if got, err := os.ReadFile(filepath.Join(outside, filepath.Base(path))); err != nil || string(got) != "private host data" {
					t.Fatalf("private host file changed: %q, %v", got, err)
				}
			} else {
				if stage == "reload" {
					result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: delegated.SessionID}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == delegated.SessionID })
					if result.Success || !strings.Contains(protocol.Deref(result.Error), "could not be prepared") {
						t.Fatalf("reload must refuse instead of fresh-spawning: %+v", result)
					}
				} else if result := seedResumeRequest(app, delegated.SeedID); result.Success {
					t.Fatalf("restored through symlink: %+v", result)
				}
				entries, err := os.ReadDir(outside)
				if err != nil || len(entries) != 0 {
					t.Fatalf("restore wrote outside provider directories: %v, %v", entries, err)
				}
			}
		})
	}
}

func TestConversationRetirementWaitsForAnActiveWorktreeSweep(t *testing.T) {
	t.Setenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS", "0")
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	pulls := newMergedPullRequests(t)
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	first := w.Launched(delegated.SessionID)
	first.Prompted()
	closePane(app, sessionPane{session: delegated.SessionID})
	testworld.AwaitTaskDone(app, "conversation_keep")
	lifeMove(t, cli, "", delegated.SeedID, "wither", "finished", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept == nil || kept.DeleteAfter == nil {
		t.Fatalf("released copy: %+v", kept)
	}

	shop := sweepRepoOnGitHub(t)
	path := createWorktree(t, app, shop, "feat-reclaim")
	runGit(t, shop, "update-ref", "refs/remotes/origin/main", "main")
	refreshWorktrees(t, cli)
	answer := pulls.awaitSweepAsking(t)
	if _, err := cli.SeedNote("", delegated.SeedID, "Record the completed work", "", "", false, nil); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept == nil || kept.DeletedAt != nil {
		t.Errorf("retired copy while the worktree sweep was active: %+v", kept)
	}
	close(answer)
	if swept := sweepAwaitSwept(app, path); swept.Action != "removed" {
		t.Errorf("sweep action = %s; want removed", swept.Action)
	}
}
