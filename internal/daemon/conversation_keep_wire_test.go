package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
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
			closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
	t.Setenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS", "0")
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	first := w.Launched(delegated.SessionID)
	first.Prompted()
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
	lifeMove(t, cli, "", delegated.SeedID, "wither", "abandoned", "")
	released := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	if released == nil || protocol.Deref(released.DeleteAfter) == "" {
		t.Fatalf("released copy: %+v", released)
	}
	// Replant retains the copy even after Claude has removed its own transcript.
	sessionRecoveryDeleteTranscript(t, first.ConversationID)
	lifeMove(t, cli, "", delegated.SeedID, "replant", "", "")
	retained := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	if retained == nil || retained.DeleteAfter != nil {
		t.Fatalf("replanted copy: %+v", retained)
	}
	lifeMove(t, cli, "", delegated.SeedID, "wither", "abandoned again", "")
	plantSeedAs(t, cli, "", "trigger the next keep pass")
	deleted := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	if deleted == nil || protocol.Deref(deleted.DeletedAt) == "" {
		t.Fatalf("deleted copy: %+v", deleted)
	}
	lifeMove(t, cli, "", delegated.SeedID, "replant", "", "")
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
	kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	if kept == nil {
		t.Fatal("quiet conversation was not copied")
	}
	// The harness owns existing files, even when it recreates a shorter transcript.
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
	subfiles, err := filepath.Glob(filepath.Join(strings.TrimSuffix(originalPath, ".jsonl"), "subagents", "*.jsonl"))
	if err != nil || len(subfiles) != 1 {
		t.Fatalf("subagent files: %v, %v", subfiles, err)
	}
	suboriginal, err := os.ReadFile(subfiles[0])
	if err != nil {
		t.Fatal(err)
	}
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
	spawned, secondWorkspace, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("elsewhere"), func(msg *protocol.SpawnSessionMessage) {
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
	app.TypeLine(second, "continue the work")
	resumed.Prompted()
	resumed.Reply("second session's longer answer to keep with the original conversation <!-- attn:state=waiting_input -->")
	closePane(app, seedResumePane(t, w, secondWorkspace, second))
	firstCopy := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation
	secondCopy := lifeShow(t, cli, secondSeed).Seed.Continuation.KeptConversation
	if firstCopy == nil || secondCopy == nil || *firstCopy != *secondCopy {
		t.Fatalf("shared copy: first %+v, second %+v", firstCopy, secondCopy)
	}
	lifeMove(t, cli, "", delegated.SeedID, "wither", "first work abandoned", "")
	kept := lifeShow(t, cli, secondSeed).Seed.Continuation.KeptConversation
	if kept == nil || kept.DeleteAfter != nil {
		t.Fatalf("copy released while other work remains open: %+v", kept)
	}
	sessionRecoveryDeleteTranscript(t, first.ConversationID)
	if err := os.RemoveAll(strings.TrimSuffix(originalPath, ".jsonl")); err != nil {
		t.Fatal(err)
	}
	if result := seedResumeRequest(app, secondSeed); !result.Success {
		t.Fatalf("shared conversation restore: %+v", result)
	}
	seedResumeContinues(t, w, first, second)
	if got, err := os.ReadFile(subfiles[0]); err != nil || string(got) != string(suboriginal) {
		t.Fatalf("subagent output from original directory lost: %q, %v", got, err)
	}
}
