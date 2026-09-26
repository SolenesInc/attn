package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func scheduledDefinitionYAML(dir, cron, continuity, catchUp, prompt string) string {
	return fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: nightly
name: Nightly
trigger:
  type: scheduled
  schedule: {cron: %q, time_zone: UTC}
  continuity: %s
  catch_up: %s
prompt: %s
launch: {driver: codex}
location: {type: directory, path: %s}
`, cron, continuity, catchUp, prompt, dir)
}

func setupScheduledDaemon(t *testing.T, cron, continuity, catchUp string) (*Daemon, *store.Store, *store.AutomationDefinition, string) {
	t.Helper()
	dir := t.TempDir()
	spec, canonical, err := automation.ParseDefinitionYAML([]byte(scheduledDefinitionYAML(dir, cron, continuity, catchUp, "Sweep.")))
	if err != nil {
		t.Fatalf("parse definition: %v", err)
	}
	s := store.New()
	def, err := s.UpsertAutomationDefinition(spec.ID, spec.Name, string(canonical), time.Now())
	if err != nil {
		t.Fatalf("upsert definition: %v", err)
	}
	d := newHomeDaemonForTest(t, s)
	return d, s, def, dir
}

func TestObserveDueScheduleClaimRejectionLeavesCursorForRetry(t *testing.T) {
	d, s, def, dir := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	staleDefinition := *def

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0)

	editedSpec, editedCanonical, err := automation.ParseDefinitionYAML([]byte(scheduledDefinitionYAML(dir, "* * * * *", "fresh", "latest", "Different sweep.")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition(editedSpec.ID, editedSpec.Name, string(editedCanonical), now0); err != nil {
		t.Fatal(err)
	}

	delivered := 0
	d.automationDeliveryHook = func(*store.AutomationRun) error { delivered++; return nil }
	due := now0.Add(60 * time.Second)
	d.observeDueSchedule(staleDefinition, spec, now0.Add(70*time.Second))

	if delivered != 0 {
		t.Fatalf("delivered=%d, want 0 on claim rejection", delivered)
	}
	cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(now0) {
		t.Fatalf("cursor=%v ok=%v err=%v, want unchanged at %v after rejected claim", cursor, ok, err, now0)
	}
	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs=%d, want 0 after rejected claim", len(runs))
	}

	freshDefinition, err := s.GetAutomationDefinition(def.ID)
	if err != nil || freshDefinition == nil {
		t.Fatalf("fresh definition: %v", err)
	}
	var freshSpec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(freshDefinition.SpecJSON), &freshSpec); err != nil {
		t.Fatal(err)
	}
	retryAt := now0.Add(75 * time.Second)
	d.observeDueSchedule(*freshDefinition, freshSpec, retryAt)

	if delivered != 1 {
		t.Fatalf("delivered=%d, want 1 after retry with fresh definition", delivered)
	}
	runs, err = s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs=%d, want 1", len(runs))
	}
	wantKey := automation.ScheduledOccurrenceKey(due)
	occurrence, err := s.GetAutomationOccurrence(runs[0].OccurrenceID)
	if err != nil || occurrence == nil || occurrence.OccurrenceKey != wantKey {
		t.Fatalf("occurrence=%#v err=%v, want key %s for the same intended instant", occurrence, err, wantKey)
	}
	cursor, ok, err = s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(retryAt) {
		t.Fatalf("cursor=%v ok=%v err=%v, want advanced to %v after the successful retry", cursor, ok, err, retryAt)
	}
}

func TestObserveDueScheduleClaimRejectionRetryFiresNewestDueInstant(t *testing.T) {
	d, s, def, dir := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	staleDefinition := *def

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0)

	editedSpec, editedCanonical, err := automation.ParseDefinitionYAML([]byte(scheduledDefinitionYAML(dir, "* * * * *", "fresh", "latest", "Different sweep.")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition(editedSpec.ID, editedSpec.Name, string(editedCanonical), now0); err != nil {
		t.Fatal(err)
	}

	delivered := 0
	d.automationDeliveryHook = func(*store.AutomationRun) error { delivered++; return nil }
	d.observeDueSchedule(staleDefinition, spec, now0.Add(90*time.Second))
	if delivered != 0 {
		t.Fatalf("delivered=%d, want 0 on claim rejection", delivered)
	}

	freshDefinition, err := s.GetAutomationDefinition(def.ID)
	if err != nil || freshDefinition == nil {
		t.Fatalf("fresh definition: %v", err)
	}
	var freshSpec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(freshDefinition.SpecJSON), &freshSpec); err != nil {
		t.Fatal(err)
	}
	retryAt := now0.Add(150 * time.Second)
	d.observeDueSchedule(*freshDefinition, freshSpec, retryAt)

	if delivered != 1 {
		t.Fatalf("delivered=%d, want exactly 1 after the one-minute-later retry", delivered)
	}
	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs=%d, want 1: the failed instant must be superseded, not replayed alongside", len(runs))
	}
	wantKey := automation.ScheduledOccurrenceKey(now0.Add(120 * time.Second))
	occurrence, err := s.GetAutomationOccurrence(runs[0].OccurrenceID)
	if err != nil || occurrence == nil || occurrence.OccurrenceKey != wantKey {
		t.Fatalf("occurrence=%#v err=%v, want key %s for the newest due instant", occurrence, err, wantKey)
	}
	cursor, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !cursor.Equal(retryAt) {
		t.Fatalf("cursor=%v ok=%v err=%v, want advanced to %v after the successful retry", cursor, ok, err, retryAt)
	}
}

func claimPendingScheduledRun(t *testing.T, s *store.Store, def *store.AutomationDefinition, intended, observed time.Time) *store.AutomationRun {
	t.Helper()
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	effective, err := automation.Effective(spec, def.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, err := json.Marshal(effective)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(automation.NewScheduledInput(intended, observed))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{store: s}
	reservation, err := d.newAutomationRunReservation()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimScheduledAutomationRun(def.ID, automation.ScheduledOccurrenceKey(intended), "", def.Revision, string(payload), string(snapshotJSON), observed, reservation)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestScheduledPendingRunRecoversOnRestart(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	intended := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	run := claimPendingScheduledRun(t, s, def, intended, intended.Add(time.Second))
	if _, _, err := s.SetAutomationEnabled(def.ID, false, intended.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var broadcastIDs []string
	d.automationsBroadcastHook = func(msg *protocol.AutomationsChangedMessage) {
		mu.Lock()
		broadcastIDs = append(broadcastIDs, msg.DefinitionIds...)
		mu.Unlock()
	}

	d.recoverAutomations()

	got, err := s.GetAutomationRun(run.ID)
	if err != nil || got == nil || got.State != "failed" || !strings.Contains(got.LastError, "definition is disabled") {
		t.Fatalf("recovery did not attempt scheduled run: run=%#v err=%v", got, err)
	}
	mu.Lock()
	ids := append([]string(nil), broadcastIDs...)
	mu.Unlock()
	found := false
	for _, id := range ids {
		if id == def.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("scheduled run's failed transition did not broadcast automations_changed: broadcasts=%v", ids)
	}
}

func TestScheduledPendingRunRetriedOnNextDeliveryPass(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	intended := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	run := claimPendingScheduledRun(t, s, def, intended, intended.Add(time.Second))

	attempts := 0
	d.automationDeliveryHook = func(r *store.AutomationRun) error {
		attempts++
		if attempts == 1 {
			return &retryableAutomationDeliveryError{cause: fmt.Errorf("transient launch failure")}
		}
		return markAutomationRunDeliveredForTest(s, r.ID, `{}`, time.Now())
	}

	if err := d.deliverObservedAutomationRun(run); err == nil {
		t.Fatal("expected the first (retryable) delivery attempt to return an error")
	}
	stillPending, err := s.GetAutomationRun(run.ID)
	if err != nil || stillPending == nil || stillPending.State != store.AutomationRunStatePending || attempts != 1 {
		t.Fatalf("after first (retryable) delivery attempt: run=%#v attempts=%d err=%v", stillPending, attempts, err)
	}

	if err := d.deliverObservedAutomationRun(stillPending); err != nil {
		t.Fatalf("second delivery attempt: %v", err)
	}
	delivered, err := s.GetAutomationRun(run.ID)
	if err != nil || delivered == nil || delivered.State != store.AutomationRunStateDelivered || attempts != 2 {
		t.Fatalf("after second delivery attempt: run=%#v attempts=%d err=%v, want delivered", delivered, attempts, err)
	}
}

func TestScheduledPendingRunDeliversImmutableSnapshotAfterDefinitionEdit(t *testing.T) {
	d, s, def, dir := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	intended := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	run := claimPendingScheduledRun(t, s, def, intended, intended.Add(time.Second))

	editedSpec, editedCanonical, err := automation.ParseDefinitionYAML([]byte(scheduledDefinitionYAML(dir, "* * * * *", "fresh", "latest", "Different sweep.")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition(editedSpec.ID, editedSpec.Name, string(editedCanonical), intended.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var capturedPrompt string
	d.automationDeliveryHook = func(r *store.AutomationRun) error {
		var snap automation.Snapshot
		if err := json.Unmarshal([]byte(r.SnapshotJSON), &snap); err != nil {
			return err
		}
		capturedPrompt = snap.Prompt
		return markAutomationRunDeliveredForTest(s, r.ID, "{}", time.Now())
	}
	if err := d.deliverObservedAutomationRun(run); err != nil {
		t.Fatal(err)
	}

	if capturedPrompt != "Sweep." {
		t.Fatalf("delivered prompt=%q, want original %q", capturedPrompt, "Sweep.")
	}
}

func TestObserveDueScheduleBroadcastsAtClaimTimeEvenWhenDeliveryFailsRetryably(t *testing.T) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")
	var broadcasts []string
	d.automationsBroadcastHook = func(msg *protocol.AutomationsChangedMessage) {
		broadcasts = append(broadcasts, msg.DefinitionIds...)
	}
	d.automationDeliveryHook = func(run *store.AutomationRun) error {
		return &retryableAutomationDeliveryError{cause: fmt.Errorf("session not ready yet")}
	}

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0)
	d.observeDueSchedules(now0.Add(70 * time.Second))

	if len(broadcasts) == 0 {
		t.Fatal("no automations_changed broadcast fired at claim time")
	}
	for _, id := range broadcasts {
		if id != def.ID {
			t.Fatalf("broadcast for unexpected definition %q, want %q", id, def.ID)
		}
	}
	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil || len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("runs=%#v err=%v, want exactly one pending run despite the retryable delivery failure", runs, err)
	}
}

func deliverSingletonRunForTest(d *Daemon, s *store.Store, run *store.AutomationRun) error {
	var snapshot automation.Snapshot
	if err := json.Unmarshal([]byte(run.SnapshotJSON), &snapshot); err != nil {
		return err
	}
	occurrence, err := s.GetAutomationOccurrence(run.OccurrenceID)
	if err != nil {
		return err
	}
	if occurrence == nil {
		return fmt.Errorf("occurrence missing")
	}
	req := automation.WorkRequest{
		RunID: run.ID, DefinitionID: run.DefinitionID, SubjectKey: occurrence.SubjectKey,
		ContinuityKey: "singleton", Provider: occurrence.Provider, Prompt: snapshot.Prompt,
		Context: json.RawMessage(occurrence.PayloadJSON), Launch: snapshot.Launch, Location: snapshot.Location,
		IDs: automation.DeliveryIDs{SeedID: run.SeedID, SessionID: run.SessionID, WorkspaceID: run.WorkspaceID, PaneID: run.PaneID},
	}
	if err := d.validateAutomationContinuation(req); err != nil {
		return err
	}
	continuation, _, err := d.ensureAutomationSeed(req)
	if err != nil {
		return err
	}
	if continuation {
		if err := d.ensureAutomationOccurrenceNote(req); err != nil {
			return err
		}
	}
	d.ptyBackend = &fakeSpawnBackend{sessionIDs: []string{run.SessionID}}
	return markAutomationRunDeliveredForTest(s, run.ID, "{}", time.Now())
}

func TestScheduledSingletonHoldsLaterOccurrenceBehindPendingOrigin(t *testing.T) {
	t.Run("origin failed before planting its seed", func(t *testing.T) {
		scheduledSingletonHoldsLaterOccurrence(t, false)
	})
	t.Run("origin failed after planting its seed", func(t *testing.T) {
		scheduledSingletonHoldsLaterOccurrence(t, true)
	})
}

func scheduledSingletonHoldsLaterOccurrence(t *testing.T, originPlantsSeedBeforeFailing bool) {
	d, s, def, _ := setupScheduledDaemon(t, "* * * * *", "singleton", "latest")
	originReady := false
	var delivered []*store.AutomationRun
	d.automationDeliveryHook = func(run *store.AutomationRun) error {
		binding, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton")
		if err != nil || binding == nil {
			return fmt.Errorf("binding=%#v err=%v", binding, err)
		}
		if binding.OriginRunID == run.ID {
			if !originReady {
				if originPlantsSeedBeforeFailing {
					req := automation.WorkRequest{RunID: run.ID, DefinitionID: run.DefinitionID, ContinuityKey: "singleton", Prompt: "Sweep.", IDs: automation.DeliveryIDs{SeedID: run.SeedID, SessionID: run.SessionID, WorkspaceID: run.WorkspaceID, PaneID: run.PaneID}}
					if _, _, err := d.ensureAutomationSeed(req); err != nil {
						return err
					}
				}
				return &retryableAutomationDeliveryError{cause: fmt.Errorf("session not ready yet")}
			}
		} else if origin, err := s.GetAutomationRun(binding.OriginRunID); err != nil || origin == nil || origin.State != store.AutomationRunStateDelivered {
			return fmt.Errorf("continuation %s delivered while its origin %s is %s: no session to resume", run.ID, binding.OriginRunID, origin.State)
		}
		if err := deliverSingletonRunForTest(d, s, run); err != nil {
			return err
		}
		delivered = append(delivered, run)
		return nil
	}

	now0 := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	d.observeDueSchedules(now0)
	d.observeDueSchedules(now0.Add(70 * time.Second))
	d.observeDueSchedules(now0.Add(130 * time.Second))

	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil || len(runs) != 1 || runs[0].State != store.AutomationRunStatePending {
		t.Fatalf("runs=%#v err=%v, want the origin alone and still pending", runs, err)
	}
	origin := runs[0]

	originReady = true
	if err := d.deliverObservedAutomationRun(&origin); err != nil {
		t.Fatalf("origin retry: %v", err)
	}
	d.observeDueSchedules(now0.Add(190 * time.Second))

	runs, err = s.ListAutomationRuns(def.ID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs=%#v err=%v, want the origin and one held occurrence", runs, err)
	}
	for _, run := range runs {
		if run.State != store.AutomationRunStateDelivered {
			t.Fatalf("run %s state=%s last_error=%q, want delivered", run.ID, run.State, run.LastError)
		}
		if run.SeedID != origin.SeedID || run.SessionID != origin.SessionID {
			t.Fatalf("run %s ids=%s/%s, want the origin's %s/%s", run.ID, run.SeedID, run.SessionID, origin.SeedID, origin.SessionID)
		}
	}
	if len(delivered) != 2 {
		t.Fatalf("delivered %d runs, want 2", len(delivered))
	}
	notes, err := d.readNotesDomain(origin.SeedID)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0].Body, delivered[1].ID) {
		t.Fatalf("continuation notes=%#v err=%v, want one note for %s", notes, err, delivered[1].ID)
	}
}
