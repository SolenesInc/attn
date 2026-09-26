package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

func createCodexLegacyTranscript(t *testing.T, codexHome, dataRoot, native, ticketID, commandState, receiptState string) string {
	t.Helper()
	path := filepath.Join(codexHome, "sessions", "2026", "08", "rollout-"+native+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(dataRoot, "workspace-contexts", native, "context.md")
	lines := []any{
		map[string]any{"timestamp": "2026-08-01T10:00:00Z", "type": "session_meta", "payload": map[string]any{"id": native, "cwd": "/work/" + ticketID}},
		map[string]any{"timestamp": "2026-08-01T10:00:01Z", "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "attn checked out this workspace's shared context for this session at \"" + contextPath + "\"."}},
		}},
		map[string]any{"timestamp": "2026-08-01T10:00:02Z", "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "Please recover this prompt."}},
		map[string]any{"timestamp": "2026-08-01T10:00:03Z", "type": "event_msg", "payload": map[string]any{"type": "agent_reasoning", "text": "private reasoning"}},
		map[string]any{"timestamp": "2026-08-01T10:00:04Z", "type": "event_msg", "payload": map[string]any{"type": "agent_message", "message": "Recovered answer."}},
		map[string]any{"timestamp": "2026-08-01T10:00:05Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "name": "exec", "call_id": "call-" + native,
			"input": `const r = await tools.exec_command({cmd: "attn ticket status ` + commandState + ` --comment 'finished'"}); text(r.output);`,
		}},
		map[string]any{"timestamp": "2026-08-01T10:00:06Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call_output", "call_id": "call-" + native, "output": "ticket " + ticketID + " → " + receiptState,
		}},
	}
	var body strings.Builder
	for _, line := range lines {
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
	return path
}

func createClosedTicketBackup(t *testing.T, backupDir, ticketID, title string, updatedAt time.Time) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "source.db")
	s, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := updatedAt.Add(-time.Hour)
	if _, err := s.CreateTicket(store.Ticket{ID: ticketID, Title: title, Description: "body"}, "you", createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTicketStatus(ticketID, store.TicketStatusDone, "agent", "finished", updatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTicketAttachment(store.TicketAttachment{TicketID: ticketID, Filename: "proof.md", Path: "/proof.md"}, "agent", updatedAt); err != nil {
		t.Fatal(err)
	}
	path, err := s.BackupNow(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeRecoveryHome(t *testing.T, dataRoot string) {
	t.Helper()
	if err := os.Chmod(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := enrollment.EnsureDaemonID(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.Ensure(dataRoot, id); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyTicketRecoveryRejectsTranscriptReplacedWithSameSizeAndModTime(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	dataRoot := t.TempDir()
	target, err := store.NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	makeRecoveryHome(t, dataRoot)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	path := createCodexLegacyTranscript(t, codexHome, dataRoot, "native-one", "original-ticket", "completed", "done")

	d := &Daemon{store: target, dataRoot: dataRoot, done: make(chan struct{})}
	d.legacyTicketRecoveryFinishOnce.Do(func() {})
	if wait, prepareErr := d.prepareLegacyTicketRecovery(); prepareErr != nil || !wait {
		t.Fatalf("prepare wait=%v err=%v", wait, prepareErr)
	}
	sources, err := target.ListLegacyTicketRecoverySources(store.LegacyTicketRecoveryVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].SHA256 == "" {
		t.Fatalf("frozen sources = %#v", sources)
	}
	frozenInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := createCodexLegacyTranscript(t, t.TempDir(), dataRoot, "native-one", "replaced-ticket", "completed", "done")
	replacementBody, err := os.ReadFile(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(replacementBody)) != frozenInfo.Size() {
		t.Fatalf("replacement size = %d, want %d", len(replacementBody), frozenInfo.Size())
	}
	swap := path + ".replacement"
	if err := os.WriteFile(swap, replacementBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(swap, path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, frozenInfo.ModTime(), frozenInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	replacedInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if replacedInfo.Size() != frozenInfo.Size() || replacedInfo.ModTime().UnixNano() != frozenInfo.ModTime().UnixNano() {
		t.Fatalf("replacement metadata = size %d mtime %d, want size %d mtime %d",
			replacedInfo.Size(), replacedInfo.ModTime().UnixNano(), frozenInfo.Size(), frozenInfo.ModTime().UnixNano())
	}

	resultAny, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 1, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}})
	if err != nil {
		t.Fatal(err)
	}
	result := resultAny.(legacyTicketRecoveryResult)
	if result.Counts.Recovered != 0 || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "source identity changed before inspection") {
		t.Fatalf("result = %#v", result)
	}
	for _, ticketID := range []string{"original-ticket", "replaced-ticket"} {
		if ticket, getErr := target.GetTicket(ticketID); getErr != nil || ticket != nil {
			t.Fatalf("ticket %s = %#v, err=%v", ticketID, ticket, getErr)
		}
	}
}

func TestLegacyTicketRecoveryChangedSourceWarnsAndStaysProtected(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	dataRoot := t.TempDir()
	dbRoot := t.TempDir()
	target, err := store.NewWithDB(filepath.Join(dbRoot, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	makeRecoveryHome(t, dataRoot)
	path := createClosedTicketBackup(t, filepath.Join(dataRoot, "backups"), "changed", "Changed", time.Now())
	d := &Daemon{store: target, dataRoot: dataRoot, done: make(chan struct{})}
	d.legacyTicketRecoveryFinishOnce.Do(func() {})
	if _, err := d.prepareLegacyTicketRecovery(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("changed"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	resultAny, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 1, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}})
	if err != nil {
		t.Fatal(err)
	}
	result := resultAny.(legacyTicketRecoveryResult)
	if len(result.Warnings) == 0 || len(result.Protected) != 1 || !strings.Contains(result.Warnings[0], "changed") {
		t.Fatalf("result = %#v", result)
	}
	if ticket, _ := target.GetTicket("changed"); ticket != nil {
		t.Fatalf("changed source contributed ticket: %#v", ticket)
	}
}

func TestLegacyTicketRecoveryRetriesTransientIOThenWarnsOnce(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	dataRoot := t.TempDir()
	dbRoot := t.TempDir()
	target, err := store.NewWithDB(filepath.Join(dbRoot, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	makeRecoveryHome(t, dataRoot)
	createClosedTicketBackup(t, filepath.Join(dataRoot, "backups"), "retry-me", "Retry", time.Now())
	d := &Daemon{store: target, dataRoot: dataRoot, done: make(chan struct{})}
	d.legacyTicketRecoveryFinishOnce.Do(func() {})
	if _, err := d.prepareLegacyTicketRecovery(); err != nil {
		t.Fatal(err)
	}
	d.legacyTicketSnapshotIdentity = func(path string) (store.LegacyTicketRecoverySource, error) {
		return store.LegacyTicketRecoverySource{}, &os.PathError{Op: "read", Path: path, Err: syscall.EAGAIN}
	}
	first := &jobs.Job{Attempts: 1, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}
	if _, err := d.legacyTicketRecoveryHandler(context.Background(), first); err == nil {
		t.Fatal("first transient failure did not request a retry")
	}
	run, err := target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if err != nil || run.State != store.LegacyTicketRecoveryRunning {
		t.Fatalf("run after first attempt = %#v err=%v", run, err)
	}
	last := &jobs.Job{Attempts: 3, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}
	resultAny, err := d.legacyTicketRecoveryHandler(context.Background(), last)
	if err != nil {
		t.Fatal(err)
	}
	result := resultAny.(legacyTicketRecoveryResult)
	if len(result.Warnings) == 0 || len(result.Protected) != 1 {
		t.Fatalf("final result = %#v", result)
	}
	run, _ = target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if run.State != store.LegacyTicketRecoveryWarned || run.WarningNotificationID != "legacy-ticket-recovery-v2" {
		t.Fatalf("terminal run = %#v", run)
	}
	notifications, err := target.ListNotifications()
	if err != nil || len(notifications) != 1 {
		t.Fatalf("notifications=%#v err=%v", notifications, err)
	}
	if _, err := d.legacyTicketRecoveryHandler(context.Background(), last); err != nil {
		t.Fatal(err)
	}
	notifications, _ = target.ListNotifications()
	if len(notifications) != 1 {
		t.Fatalf("terminal rerun duplicated warning: %#v", notifications)
	}
}

func TestLegacyTicketRecoveryResumesCommittedItemsAfterCrash(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	dataRoot := t.TempDir()
	dbRoot := t.TempDir()
	target, err := store.NewWithDB(filepath.Join(dbRoot, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	makeRecoveryHome(t, dataRoot)
	createClosedTicketBackup(t, filepath.Join(dataRoot, "backups"), "crash-safe", "Crash safe", time.Now())
	d := &Daemon{store: target, dataRoot: dataRoot, done: make(chan struct{})}
	d.legacyTicketRecoveryFinishOnce.Do(func() {})
	if _, err := d.prepareLegacyTicketRecovery(); err != nil {
		t.Fatal(err)
	}
	run, err := target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if err != nil {
		t.Fatal(err)
	}
	job := &jobs.Job{Attempts: 1, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}
	if _, err := d.recoverLegacyTicketsFromSnapshots(context.Background(), job, run); err != nil {
		t.Fatal(err)
	}
	if recovered, _ := target.GetTicket("crash-safe"); recovered == nil {
		t.Fatal("item transaction did not commit before simulated crash")
	}
	run, _ = target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if run.State != store.LegacyTicketRecoveryRunning {
		t.Fatalf("simulated crash unexpectedly finished run: %#v", run)
	}
	if _, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 2, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}); err != nil {
		t.Fatal(err)
	}
	recovered, _ := target.GetTicket("crash-safe")
	if len(recovered.Activity) != 1 || len(recovered.Attachments) != 1 {
		t.Fatalf("resume duplicated committed children: %#v", recovered)
	}
	run, _ = target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if run.State != store.LegacyTicketRecoverySucceeded {
		t.Fatalf("resumed run = %#v", run)
	}
}

func recoveredSeedForTicket(t *testing.T, s *store.Store, ticketID string) garden.Seed {
	t.Helper()
	link, err := s.TicketSeedLink(ticketID)
	if err != nil || link == nil {
		t.Fatalf("link for %s = %#v, %v", ticketID, link, err)
	}
	schema, ok, err := s.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil || !ok {
		t.Fatalf("seed collection: ok=%v err=%v", ok, err)
	}
	doc, found, err := s.GetDocument(*schema, link.SeedID)
	if err != nil || !found {
		t.Fatalf("seed %s: found=%v err=%v", link.SeedID, found, err)
	}
	seed, err := garden.Decode(doc.Body)
	if err != nil {
		t.Fatal(err)
	}
	return seed
}
