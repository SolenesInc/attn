package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnUpgradeRecoversClosedTicketsAsSeeds(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	var backups map[string]string
	legacyRecoveryUpgrade(t, func(dir string) {
		backups = legacyRecoveryBackupBodies(t, legacyRecoveryOlderData(t, dir))
	}, func(t *testing.T, w *world) {
		cli := w.Client()
		if feed := listNotifications(w.App()).Notifications; len(feed) != 0 {
			t.Errorf("a clean upgrade left notifications %+v, want no recovery warning", feed)
		}

		seeds := legacyRecoverySeedsByTicket(t, cli)
		for id, want := range map[string]string{
			"done-ticket": "harvested", "failed-ticket": "withered", "crashed-ticket": "withered", "auto-abcdef1234567890": "withered",
			"recover-me": "harvested", "routine-ticket": "harvested", "transcript-only": "harvested",
		} {
			if got := seeds[id].Status; got != want {
				t.Errorf("the seed recovered from %s is %q, want %s", id, got, want)
			}
		}
		if _, planted := seeds["automation-ticket"]; planted {
			t.Error("an Automation ticket was recovered as a seed")
		}
		for id, want := range map[string]protocol.TicketStatus{
			"done-ticket": protocol.TicketStatusDone, "failed-ticket": protocol.TicketStatusFailed,
			"crashed-ticket": protocol.TicketStatusCrashed, "auto-abcdef1234567890": protocol.TicketStatusFailed,
		} {
			if ticket := showTicket(t, cli, id); ticket.Status != want || ticket.Title != "Title "+id {
				t.Errorf("recovery changed %s to %s %q", id, ticket.Status, ticket.Title)
			}
		}

		recovered := showTicket(t, cli, "recover-me")
		if recovered.Title != "Newer" || len(recovered.Activity) != 1 {
			t.Errorf("recover-me = %q with activity %q, want the newest backup's copy", recovered.Title, activityLines(recovered))
		}
		if live := showTicket(t, cli, "live-wins"); live.Title != "Live" || live.Description != "current" || live.Status != protocol.TicketStatusWorking {
			t.Errorf("the live ticket became %q %q %s, want it untouched by its newer backup", live.Title, live.Description, live.Status)
		}
		transcript := showTicket(t, cli, "transcript-only")
		if transcript.Status != protocol.TicketStatusDone || transcript.ArchivedAt == nil || transcript.Description != "Please recover this prompt." ||
			transcript.Cwd != "/work/transcript-only" || transcript.LastAgentID != "codex" {
			t.Errorf("the ticket recovered from a transcript = %+v", transcript)
		}
		conversation := legacyRecoveryConversation(t, cli, seeds["transcript-only"].ID)
		if info, err := os.Lstat(conversation); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("the recovered conversation %s = %v, %v; want a private regular file", conversation, info, err)
		}
		body, err := os.ReadFile(conversation)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "Please recover this prompt.") || !strings.Contains(string(body), "Recovered answer.") ||
			strings.Contains(string(body), "private reasoning") || strings.Contains(string(body), "ticket transcript-only") {
			t.Errorf("the recovered conversation reads %q, want the exchange without reasoning or tool output", body)
		}
		for path, before := range backups {
			if after, _ := os.ReadFile(path); string(after) != before {
				t.Errorf("recovery changed backup %s", path)
			}
		}

		w.restart()
		w.finishStartupWork()
		cli = w.Client()
		if again := showTicket(t, cli, "recover-me"); len(again.Activity) != 1 {
			t.Errorf("after a restart recover-me has activity %q, want it recovered once", activityLines(again))
		}
		if again := legacyRecoverySeedsByTicket(t, cli); len(again) != len(seeds) {
			t.Errorf("after a restart there are %d recovered seeds, want %d", len(again), len(seeds))
		}
	})
}

func TestANamedInstanceNeverRecoversLegacyTickets(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "dev")
	legacyRecoveryUpgrade(t, func(dir string) {
		legacyRecoveryOlderData(t, dir)
	}, func(t *testing.T, w *world) {
		cli := w.Client()
		seeds := legacyRecoverySeedsByTicket(t, cli)
		for _, id := range []string{"recover-me", "routine-ticket", "transcript-only", "done-ticket"} {
			if ticket, err := cli.ShowTicket("", id); id != "done-ticket" && err == nil {
				t.Errorf("a named instance restored %s from a backup or transcript: %+v", id, ticket)
			}
			if seed, planted := seeds[id]; planted {
				t.Errorf("a named instance recovered %s as seed %s", id, seed.ID)
			}
		}
		if _, err := os.Stat(filepath.Join(w.Dir, "legacy-ticket-recovery")); !os.IsNotExist(err) {
			t.Errorf("a named instance wrote recovery files: %v", err)
		}
	})
}

func TestASeedRecoveredWithoutItsConversationLinksItOnALaterStart(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	prepared := prepareWorld(t)
	legacyRecoveryOlderData(t, prepared.Dir)
	legacyRecoveryStart(t, prepared, func(t *testing.T, w *world) {
		seed := legacyRecoverySeedsByTicket(t, w.Client())["recover-me"]
		if attached := legacyRecoveryConversationNotes(t, w.Client(), seed.ID); len(attached) != 0 {
			t.Fatalf("the seed recovered from a backup has conversation notes %+v before its ticket carries one", attached)
		}
	})
	conversation := legacyRecoveryConversationOnTicket(t, prepared.Dir, "recover-me")

	legacyRecoveryStart(t, prepared, func(t *testing.T, w *world) {
		cli := w.Client()
		seed := legacyRecoverySeedsByTicket(t, cli)["recover-me"]
		if linked := legacyRecoveryConversation(t, cli, seed.ID); linked != conversation {
			t.Fatalf("the seed links %s, want its ticket's recovered conversation %s", linked, conversation)
		}
		w.advance(time.Second)
		if _, err := cli.SeedNote("", seed.ID, "", "", "detach", false, &protocol.SeedArtifactReference{
			Kind: "markdown_file", Path: protocol.Ptr(conversation),
		}); err != nil {
			t.Fatal(err)
		}

		w.restart()
		w.finishStartupWork()
		notes := legacyRecoveryConversationNotes(t, w.Client(), seed.ID)
		if len(notes) != 2 || notes[0].Kind != "detach" || notes[1].Kind != "attach" {
			t.Errorf("after the user detached the conversation and attn restarted, the seed's conversation notes are %+v, want one attach then the detach", notes)
		}
	})
}

func legacyRecoveryUpgrade(t *testing.T, before func(dir string), script func(t *testing.T, w *world)) {
	t.Helper()
	prepared := prepareWorld(t)
	before(prepared.Dir)
	legacyRecoveryStart(t, prepared, script)
}

func legacyRecoveryStart(t *testing.T, prepared *testworld.World, script func(t *testing.T, w *world)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true}
		w.start()
		w.finishStartupWork()
		script(t, w)
	})
}

func legacyRecoveryOlderData(t *testing.T, dir string) []string {
	t.Helper()
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	database := filepath.Join(t.TempDir(), "attn.db")
	t.Setenv("ATTN_DB_PATH", database)
	live, err := store.NewWithDB(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := live.CreateTicket(store.Ticket{ID: "live-wins", Title: "Live", Description: "current", Status: store.TicketStatusWorking, Assignee: "someone"}, "you", now); err != nil {
		t.Fatal(err)
	}
	for id, status := range map[string]store.TicketStatus{
		"done-ticket": store.TicketStatusDone, "failed-ticket": store.TicketStatusFailed,
		"crashed-ticket": store.TicketStatusCrashed, "auto-abcdef1234567890": store.TicketStatusFailed,
	} {
		if _, err := live.CreateTicket(store.Ticket{ID: id, Title: "Title " + id, Description: "body", Status: status}, "you", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := live.EnsureAutomationTicket(store.Ticket{
		ID: "automation-ticket", Title: "Automation", Description: "scheduled work", Status: store.TicketStatusDone, AutomationRunID: "run-1",
	}, "automation:schedule", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}

	routineDir, premigrationDir := filepath.Join(dir, "backups"), store.BackupDirForDatabase(database)
	backups := []string{
		legacyRecoveryBackup(t, filepath.Join(routineDir, "attn-20260101-000000.db"), "recover-me", "Older", now.Add(-2*time.Hour)),
		legacyRecoveryBackup(t, filepath.Join(routineDir, "attn-20260101-000100.db"), "routine-ticket", "Routine", now.Add(-2*time.Hour)),
		legacyRecoveryBackup(t, filepath.Join(premigrationDir, "attn-premigration-60-20260102-000000.db"), "recover-me", "Newer", now.Add(-time.Hour)),
		legacyRecoveryBackup(t, filepath.Join(premigrationDir, "attn-premigration-60-20260103-000000.db"), "live-wins", "Backup", now.Add(time.Hour)),
	}
	if err := os.Symlink(backups[1], filepath.Join(routineDir, "attn-20260101-000200.db")); err != nil {
		t.Fatal(err)
	}
	legacyRecoveryCodexRollout(t, dir, "native-one", "transcript-only")
	return backups
}

func legacyRecoveryBackup(t *testing.T, path, ticketID, title string, updatedAt time.Time) string {
	t.Helper()
	source, err := store.NewWithDB(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.CreateTicket(store.Ticket{ID: ticketID, Title: title, Description: "body"}, "you", updatedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := source.SetTicketStatus(ticketID, store.TicketStatusDone, "agent", "finished", updatedAt); err != nil {
		t.Fatal(err)
	}
	written, err := source.BackupNow(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(written)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func legacyRecoveryCodexRollout(t *testing.T, dataDir, native, ticketID string) {
	t.Helper()
	path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "08", "rollout-"+native+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(dataDir, "workspace-contexts", native, "context.md")
	var body strings.Builder
	for _, line := range []any{
		map[string]any{"timestamp": "2026-08-01T10:00:00Z", "type": "session_meta", "payload": map[string]any{"id": native, "cwd": "/work/" + ticketID}},
		map[string]any{"timestamp": "2026-08-01T10:00:01Z", "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "attn checked out this workspace's shared context for this session at \"" + contextPath + "\"."}},
		}},
		map[string]any{"timestamp": "2026-08-01T10:00:02Z", "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "Please recover this prompt."}},
		map[string]any{"timestamp": "2026-08-01T10:00:03Z", "type": "event_msg", "payload": map[string]any{"type": "agent_reasoning", "text": "private reasoning"}},
		map[string]any{"timestamp": "2026-08-01T10:00:04Z", "type": "event_msg", "payload": map[string]any{"type": "agent_message", "message": "Recovered answer."}},
		map[string]any{"timestamp": "2026-08-01T10:00:05Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "name": "exec", "call_id": "call-" + native,
			"input": `const r = await tools.exec_command({cmd: "attn ticket status completed --comment 'finished'"}); text(r.output);`,
		}},
		map[string]any{"timestamp": "2026-08-01T10:00:06Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call_output", "call_id": "call-" + native, "output": "ticket " + ticketID + " → done",
		}},
	} {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func legacyRecoverySeedsByTicket(t *testing.T, cli *client.Client) map[string]protocol.Seed {
	t.Helper()
	listed, err := cli.SeedList("", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	seeds := map[string]protocol.Seed{}
	for _, seed := range listed.Seeds {
		if ticketID, ok := strings.CutPrefix(protocol.Deref(seed.Reason), "recovered from legacy ticket "); ok {
			seeds[ticketID] = seed
		}
	}
	return seeds
}

func legacyRecoveryConversation(t *testing.T, cli *client.Client, seedID string) string {
	t.Helper()
	notes, err := cli.SeedNotes("", seedID, 0)
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(notes.Notes, func(n protocol.SeedNote) bool { return n.Artifact != nil && n.Artifact.Path != nil })
	if index < 0 {
		t.Fatalf("seed %s has notes %+v, want one attaching the recovered conversation", seedID, notes.Notes)
	}
	return *notes.Notes[index].Artifact.Path
}

func legacyRecoveryConversationOnTicket(t *testing.T, dataDir, ticketID string) string {
	t.Helper()
	path := filepath.Join(dataDir, "legacy-ticket-recovery", "conversations", "codex", ticketID+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Recovered conversation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := store.NewWithDB(os.Getenv("ATTN_DB_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.AddTicketAttachment(store.TicketAttachment{
		TicketID: ticketID, Filename: filepath.Base(path), Path: path, Note: "Recovered human and assistant conversation",
	}, "attn", time.Now()); err != nil {
		t.Fatal(err)
	}
	return path
}

func legacyRecoveryConversationNotes(t *testing.T, cli *client.Client, seedID string) []protocol.SeedNote {
	t.Helper()
	notes, err := cli.SeedNotes("", seedID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(notes.Notes, func(n protocol.SeedNote) bool { return n.Artifact == nil })
}

func legacyRecoveryBackupBodies(t *testing.T, paths []string) map[string]string {
	t.Helper()
	bodies := make(map[string]string, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bodies[path] = string(body)
	}
	return bodies
}
