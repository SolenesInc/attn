package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestLegacyTicketRecoveryInventoryStartsOnlyAfterThePIDLock(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	dataRoot := shortTempDir(t)
	makeRecoveryHome(t, dataRoot)
	target, err := store.NewWithDB(filepath.Join(dataRoot, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })

	socketPath := filepath.Join(dataRoot, "attn.sock")
	d := NewForTesting(socketPath)
	_ = d.store.Close()
	d.store = target
	holder := &Daemon{pidPath: d.pidPath}
	if err := holder.acquirePIDLock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(holder.releasePIDLock)

	err = d.Start()
	if err == nil || !strings.Contains(err.Error(), "daemon already running") {
		t.Fatalf("Start() error = %v, want PID-lock refusal", err)
	}
	run, err := target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if err != nil {
		t.Fatal(err)
	}
	if run != nil {
		t.Fatalf("PID-lock loser froze recovery inventory: %#v", run)
	}
}

func TestLegacyTicketRecoveryInventoriesBothOwnedBackupRoots(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
	dataRoot := t.TempDir()
	dbRoot := t.TempDir()
	target, err := store.NewWithDB(filepath.Join(dbRoot, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	makeRecoveryHome(t, dataRoot)

	routine := createClosedTicketBackup(t, filepath.Join(dataRoot, "backups"), "routine-ticket", "Routine", time.Now().Add(-2*time.Hour))
	premigrationDir := filepath.Join(dbRoot, "backups")
	premigrationSource := createClosedTicketBackup(t, t.TempDir(), "premigration-ticket", "Premigration", time.Now().Add(-time.Hour))
	premigration := filepath.Join(premigrationDir, "attn-premigration-60-20260101-000000.db")
	if err := os.MkdirAll(premigrationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(premigrationSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(premigration, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(routine, filepath.Join(dataRoot, "backups", "attn-20260101-000000.db")); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{store: target, dataRoot: dataRoot}
	wait, err := d.prepareLegacyTicketRecovery()
	if err != nil || !wait {
		t.Fatalf("prepare wait=%v err=%v", wait, err)
	}
	sources, err := target.ListLegacyTicketRecoverySources(store.LegacyTicketRecoveryVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %#v", sources)
	}
	if sources[0].Path != filepath.Clean(routine) && sources[1].Path != filepath.Clean(routine) {
		t.Fatalf("routine source not inventoried: %#v", sources)
	}
	if sources[0].Path != filepath.Clean(premigration) && sources[1].Path != filepath.Clean(premigration) {
		t.Fatalf("premigration source not inventoried: %#v", sources)
	}
}

func TestLegacyTicketRecoveryRestoresNewestWithoutChangingSourcesOrLiveRows(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
	dataRoot := t.TempDir()
	dbRoot := t.TempDir()
	target, err := store.NewWithDB(filepath.Join(dbRoot, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	makeRecoveryHome(t, dataRoot)
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	if _, err := target.CreateTicket(store.Ticket{ID: "live-wins", Title: "Live", Description: "current"}, "you", now); err != nil {
		t.Fatal(err)
	}
	routineDir := filepath.Join(dataRoot, "backups")
	old := createClosedTicketBackup(t, routineDir, "recover-me", "Older", now.Add(-2*time.Hour))
	newSource := createClosedTicketBackup(t, t.TempDir(), "recover-me", "Newer", now.Add(-time.Hour))
	preDir := filepath.Join(dbRoot, "backups")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(preDir, "attn-premigration-60-20260102-000000.db")
	contents, err := os.ReadFile(newSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	liveBackup := createClosedTicketBackup(t, t.TempDir(), "live-wins", "Backup", now.Add(time.Hour))
	liveBackupPath := filepath.Join(preDir, "attn-premigration-60-20260103-000000.db")
	liveContents, _ := os.ReadFile(liveBackup)
	if err := os.WriteFile(liveBackupPath, liveContents, 0o600); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{store: target, dataRoot: dataRoot, done: make(chan struct{})}
	d.legacyTicketRecoveryPostOnce.Do(func() {})
	if wait, err := d.prepareLegacyTicketRecovery(); err != nil || !wait {
		t.Fatalf("prepare wait=%v err=%v", wait, err)
	}
	beforeOld, err := readLegacySnapshotIdentity(old)
	if err != nil {
		t.Fatal(err)
	}
	beforeNew, err := readLegacySnapshotIdentity(newer)
	if err != nil {
		t.Fatal(err)
	}
	job := &jobs.Job{Attempts: 1, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}
	resultAny, err := d.legacyTicketRecoveryHandler(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	result := resultAny.(legacyTicketRecoveryResult)
	if result.Counts.Recovered != 1 || result.Counts.LiveWon != 1 || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v", result)
	}
	recovered, err := target.GetTicket("recover-me")
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.Title != "Newer" || len(recovered.Activity) != 1 || len(recovered.Attachments) != 1 {
		t.Fatalf("recovered = %#v", recovered)
	}
	live, _ := target.GetTicket("live-wins")
	if live.Title != "Live" || live.Description != "current" || live.Status != store.TicketStatusTodo {
		t.Fatalf("live row changed: %#v", live)
	}
	afterOld, _ := readLegacySnapshotIdentity(old)
	afterNew, _ := readLegacySnapshotIdentity(newer)
	if !legacySnapshotIdentityMatches(beforeOld, afterOld) || !legacySnapshotIdentityMatches(beforeNew, afterNew) {
		t.Fatal("source identity changed during recovery")
	}
	if _, err := d.legacyTicketRecoveryHandler(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	again, _ := target.GetTicket("recover-me")
	if len(again.Activity) != 1 || len(again.Attachments) != 1 {
		t.Fatalf("rerun duplicated children: %#v", again)
	}
}

func TestLegacyTicketRecoveryRestoresTranscriptOnlyArchiveAndConversation(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
	dataRoot := t.TempDir()
	dbRoot := t.TempDir()
	target, err := store.NewWithDB(filepath.Join(dbRoot, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	makeRecoveryHome(t, dataRoot)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	createCodexLegacyTranscript(t, codexHome, dataRoot, "native-one", "transcript-only", "completed", "done")

	d := &Daemon{store: target, dataRoot: dataRoot, done: make(chan struct{})}
	d.legacyTicketRecoveryPostOnce.Do(func() {})
	if wait, err := d.prepareLegacyTicketRecovery(); err != nil || !wait {
		t.Fatalf("prepare wait=%v err=%v", wait, err)
	}
	resultAny, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 1, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}})
	if err != nil {
		t.Fatal(err)
	}
	result := resultAny.(legacyTicketRecoveryResult)
	if result.Counts.TranscriptRecovered != 1 || result.Counts.Recovered != 1 || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v", result)
	}
	ticket, err := target.GetTicket("transcript-only")
	if err != nil {
		t.Fatal(err)
	}
	if ticket == nil || ticket.Status != store.TicketStatusDone || ticket.ArchivedAt == nil || ticket.Description != "Please recover this prompt." {
		t.Fatalf("ticket = %#v", ticket)
	}
	if ticket.Cwd != "/work/transcript-only" || ticket.LastAgentID != "codex" || len(ticket.Activity) != 1 || len(ticket.Attachments) != 1 {
		t.Fatalf("ticket archive fields = %#v", ticket)
	}
	conversation := ticket.Attachments[0].Path
	info, err := os.Lstat(conversation)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("conversation mode = %v", info.Mode())
	}
	content, err := os.ReadFile(conversation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Please recover this prompt.") || !strings.Contains(string(content), "Recovered answer.") || strings.Contains(string(content), "private reasoning") || strings.Contains(string(content), "ticket transcript-only") {
		t.Fatalf("conversation = %s", content)
	}

	if _, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 2, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}); err != nil {
		t.Fatal(err)
	}
	again, _ := target.GetTicket("transcript-only")
	if len(again.Activity) != 1 || len(again.Attachments) != 1 {
		t.Fatalf("rerun duplicated transcript children: %#v", again)
	}
}

func TestLegacyTicketRecoveryMapsEveryUserTerminalStateWithoutChangingTickets(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
	dataRoot := t.TempDir()
	target, err := store.NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	makeRecoveryHome(t, dataRoot)
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	wantStates := map[string]string{
		"done-ticket":           garden.StatusHarvested,
		"failed-ticket":         garden.StatusWithered,
		"crashed-ticket":        garden.StatusWithered,
		"auto-abcdef1234567890": garden.StatusWithered,
	}
	for id, status := range map[string]store.TicketStatus{
		"done-ticket":           store.TicketStatusDone,
		"failed-ticket":         store.TicketStatusFailed,
		"crashed-ticket":        store.TicketStatusCrashed,
		"auto-abcdef1234567890": store.TicketStatusFailed,
	} {
		if _, err := target.CreateTicket(store.Ticket{ID: id, Title: "Title " + id, Description: "body", Status: status}, "you", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := target.EnsureAutomationTicket(store.Ticket{
		ID: "automation-ticket", Title: "Automation", Description: "scheduled work",
		Status: store.TicketStatusDone, AutomationRunID: "run-1",
	}, "automation:schedule", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}
	before := make(map[string]*store.Ticket, len(wantStates))
	for id := range wantStates {
		before[id], err = target.GetTicket(id)
		if err != nil {
			t.Fatal(err)
		}
	}

	d := &Daemon{store: target, dataRoot: dataRoot, done: make(chan struct{})}
	d.legacyTicketRecoveryPostOnce.Do(func() {})
	if wait, err := d.prepareLegacyTicketRecovery(); err != nil || !wait {
		t.Fatalf("prepare wait=%v err=%v", wait, err)
	}
	if _, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 1, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}); err != nil {
		t.Fatal(err)
	}

	for id, want := range wantStates {
		seed := recoveredSeedForTicket(t, target, id)
		if seed.Status != want || seed.Reason != "recovered from legacy ticket "+id {
			t.Fatalf("seed for %s = %#v, want state %s", id, seed, want)
		}
		after, err := target.GetTicket(id)
		if err != nil || !reflect.DeepEqual(before[id], after) {
			t.Fatalf("mapping changed ticket %s:\nbefore=%#v\nafter=%#v err=%v", id, before[id], after, err)
		}
	}
	link, err := target.LegacyTicketSeedLink("automation-ticket")
	if err != nil || link != nil {
		t.Fatalf("Automation ticket recovery link = %#v, %v", link, err)
	}
}

func recoveredSeedForTicket(t *testing.T, s *store.Store, ticketID string) garden.Seed {
	t.Helper()
	link, err := s.LegacyTicketSeedLink(ticketID)
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

func TestLegacyTicketRecoveryFenceSkipsNamedProfilesBeforeInventory(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "dev")
	d := &Daemon{store: store.New(), dataRoot: filepath.Join(t.TempDir(), "missing")}
	defer d.store.Close()
	wait, err := d.prepareLegacyTicketRecovery()
	if err != nil || wait {
		t.Fatalf("named profile prepare wait=%v err=%v", wait, err)
	}
	if _, err := os.Stat(d.dataRoot); !os.IsNotExist(err) {
		t.Fatalf("named profile touched data root: %v", err)
	}
}

func TestLegacyTicketRecoveryChangedSourceWarnsAndStaysProtected(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
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
	d.legacyTicketRecoveryPostOnce.Do(func() {})
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
	t.Setenv("ATTN_PROFILE", "")
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
	d.legacyTicketRecoveryPostOnce.Do(func() {})
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
	t.Setenv("ATTN_PROFILE", "")
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
	d.legacyTicketRecoveryPostOnce.Do(func() {})
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
