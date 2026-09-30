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
			closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
	t.Setenv("ATTN_CONVERSATION_KEEP_GRACE_DAYS", "0")
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	first := w.Launched(delegated.SessionID)
	first.Prompted()
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
	testworld.AwaitTaskDone(app, "conversation_keep")
	lifeMove(t, cli, "", delegated.SeedID, "wither", "abandoned", "")
	testworld.AwaitTaskDone(app, "conversation_keep")
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
	testworld.AwaitTaskDone(app, "conversation_keep")
	plantSeedAs(t, cli, "", "trigger the next keep pass")
	testworld.AwaitTaskDone(app, "conversation_keep")
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
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
	testworld.AwaitTaskDone(app, "conversation_keep")
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
	testworld.AwaitTaskDone(app, "conversation_keep")
	app.TypeLine(second, "continue the work")
	resumed.Prompted()
	resumed.Reply("second session's longer answer to keep with the original conversation <!-- attn:state=waiting_input -->")
	closePane(app, seedResumePane(t, w, secondWorkspace, second))
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
	closePane(app, seedResumePane(t, w, protocol.Deref(partialRestore.WorkspaceID), second))
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
	closePane(app, seedResumePane(t, w, protocol.Deref(fullRestore.WorkspaceID), second))
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
			closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
			if stage != "copy" {
				closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
				closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
				testworld.AwaitTaskDone(app, "conversation_keep")
				if kept := lifeShow(t, cli, delegated.SeedID).Seed.Continuation.KeptConversation; kept != nil {
					t.Fatalf("copied through symlink: %+v", kept)
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
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
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
