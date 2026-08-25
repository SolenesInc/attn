package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/store"
)

func TestAutomationApplyContractEditRotatesContinuityBindings(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Original prompt.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	v2 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Updated prompt.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if def2.Revision == def1.Revision {
		t.Fatalf("expected a revision bump for a spec change, got %d both times", def2.Revision)
	}

	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, fresh, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after rotation")
	}
	if run2.TicketID != "ticket-2" || run2.SessionID != "session-2" {
		t.Fatalf("expected a fresh binding reservation after a contract edit, got ticket=%q session=%q (pre-edit was ticket-1/session-1)", run2.TicketID, run2.SessionID)
	}

	req := automation.WorkRequest{RunID: run2.ID, DefinitionID: def2.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot2.Prompt, Launch: snapshot2.Launch, Location: snapshot2.Location, IDs: automation.DeliveryIDs{TicketID: run2.TicketID, SessionID: run2.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("post-rotation delivery should pass the continuation check, got: %v", err)
	}
}

func TestAutomationApplyNonContractEditPreservesContinuityBindings(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Sweep.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	v2 := scheduledDefinitionYAML(dir, "*/10 * * * *", "singleton", "skip", "Sweep.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if def2.Revision == def1.Revision {
		t.Fatalf("expected a revision bump for a spec change, got %d both times", def2.Revision)
	}

	run2, fresh, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON1), now.Add(10*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected the occurrence to claim a new run")
	}
	if run2.TicketID != run1.TicketID || run2.SessionID != run1.SessionID {
		t.Fatalf("non-contract edit should preserve the binding, got ticket=%q session=%q, want ticket=%q session=%q", run2.TicketID, run2.SessionID, run1.TicketID, run1.SessionID)
	}
}

func TestAutomationApplyPreservesPinnedSnapshotOfAlreadyClaimedRun(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "fresh", "latest", "Original prompt.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}

	v2 := scheduledDefinitionYAML(dir, "*/5 * * * *", "fresh", "latest", "Updated prompt.")
	if _, err := d.automationApply(v2); err != nil {
		t.Fatalf("apply v2: %v", err)
	}

	reloaded, err := s.GetAutomationRun(run1.ID)
	if err != nil || reloaded == nil {
		t.Fatalf("reloaded run=%#v err=%v", reloaded, err)
	}
	var pinned automation.Snapshot
	if err := json.Unmarshal([]byte(reloaded.SnapshotJSON), &pinned); err != nil {
		t.Fatal(err)
	}
	if pinned.Prompt != "Original prompt." {
		t.Fatalf("apply mutated an already-claimed run's pinned snapshot: prompt=%q, want %q", pinned.Prompt, "Original prompt.")
	}
}

func TestAutomationApplyRevertAllowsFreshThreadWhenOldTicketSurvives(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	v2 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt B.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, _, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run2.ID, "{}", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run2.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run2.SessionID, AutomationRunID: run2.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	v3 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def3, err := d.automationApply(v3)
	if err != nil {
		t.Fatalf("apply v3 (revert): %v", err)
	}
	run3, fresh, err := s.ClaimScheduledAutomationRun(def3.ID, "schedule:3", "singleton", def3.Revision, `{}`, string(snapshotJSON1), now.Add(10*time.Minute), store.AutomationRunReservation{RunID: "run-3", OccurrenceID: "occ-3", TicketID: "ticket-3", SessionID: "session-3", WorkspaceID: "workspace-3", PaneID: "pane-3"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after the revert rotation")
	}

	req := automation.WorkRequest{RunID: run3.ID, DefinitionID: def3.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot1.Prompt, Launch: snapshot1.Launch, Location: snapshot1.Location, IDs: automation.DeliveryIDs{TicketID: run3.TicketID, SessionID: run3.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("revert with the old same-contract thread's ticket (T1) still alive must be allowed, got: %v", err)
	}
}

func TestAutomationApplyRevertAllowsFreshThreadEvenWhenOldTicketWasSwept(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}

	v2 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt B.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, _, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run2.ID, "{}", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run2.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run2.SessionID, AutomationRunID: run2.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	v3 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def3, err := d.automationApply(v3)
	if err != nil {
		t.Fatalf("apply v3 (revert): %v", err)
	}
	run3, fresh, err := s.ClaimScheduledAutomationRun(def3.ID, "schedule:3", "singleton", def3.Revision, `{}`, string(snapshotJSON1), now.Add(10*time.Minute), store.AutomationRunReservation{RunID: "run-3", OccurrenceID: "occ-3", TicketID: "ticket-3", SessionID: "session-3", WorkspaceID: "workspace-3", PaneID: "pane-3"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after the revert rotation")
	}

	req := automation.WorkRequest{RunID: run3.ID, DefinitionID: def3.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot1.Prompt, Launch: snapshot1.Launch, Location: snapshot1.Location, IDs: automation.DeliveryIDs{TicketID: run3.TicketID, SessionID: run3.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("T3 holds a fresh session (%s), distinct from T1's (session-1); T1's ticket being gone must not block it, got: %v", run3.SessionID, err)
	}
}

func TestValidateAutomationContinuationSelfHealsItsOwnVanishedTicket(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(dir, "*/5 * * * *", "singleton", "latest", "Prompt A.")
	def, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	spec, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := automation.Effective(spec, def.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	run1, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:1", "singleton", def.Revision, `{}`, string(snapshotJSON), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	run2, _, err := s.ClaimScheduledAutomationRun(def.ID, "schedule:2", "singleton", def.Revision, `{}`, string(snapshotJSON), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if err != nil {
		t.Fatal(err)
	}
	if run2.SessionID != run1.SessionID || run2.TicketID != run1.TicketID {
		t.Fatalf("run2 ids=%s/%s, want inherited from run1 (%s/%s)", run2.SessionID, run2.TicketID, run1.SessionID, run1.TicketID)
	}

	if removed, err := s.SweepExpiredTickets(now.Add(6*time.Hour), time.Hour); err != nil || removed != 1 {
		t.Fatalf("sweep removed=%d err=%v", removed, err)
	}

	req := automation.WorkRequest{RunID: run2.ID, DefinitionID: def.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot.Prompt, Launch: snapshot.Launch, Location: snapshot.Location, IDs: automation.DeliveryIDs{TicketID: run2.TicketID, SessionID: run2.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("expected self-heal (release dangling binding, deliver fresh), got refusal: %v", err)
	}
	if binding, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton"); err != nil || binding != nil {
		t.Fatalf("expected no active binding after self-heal, got %#v err=%v", binding, err)
	}
}

func TestAutomationApplyLocationEditRotatesContinuityBindings(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)

	v1 := scheduledDefinitionYAML(t.TempDir(), "*/5 * * * *", "singleton", "latest", "Sweep.")
	def1, err := d.automationApply(v1)
	if err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	spec1, _, err := automation.ParseDefinitionYAML([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	snapshot1, err := automation.Effective(spec1, def1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON1, err := json.Marshal(snapshot1)
	if err != nil {
		t.Fatal(err)
	}
	run1, _, err := s.ClaimScheduledAutomationRun(def1.ID, "schedule:1", "singleton", def1.Revision, `{}`, string(snapshotJSON1), now, store.AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run1.ID, "{}", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAutomationTicket(store.Ticket{ID: run1.TicketID, Title: "Nightly", Status: store.TicketStatusDone, Assignee: run1.SessionID, AutomationRunID: run1.ID}, "automation:nightly", store.TicketRoleChiefOfStaff, now); err != nil {
		t.Fatal(err)
	}

	v2 := scheduledDefinitionYAML(t.TempDir(), "*/5 * * * *", "singleton", "latest", "Sweep.")
	def2, err := d.automationApply(v2)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if def2.Revision == def1.Revision {
		t.Fatalf("expected a revision bump for a spec change, got %d both times", def2.Revision)
	}

	spec2, _, err := automation.ParseDefinitionYAML([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	snapshot2, err := automation.Effective(spec2, def2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot1.ContinuationContract().Equal(snapshot2.ContinuationContract()) {
		t.Fatal("expected a location path change to be a ContinuationContract change")
	}
	snapshotJSON2, err := json.Marshal(snapshot2)
	if err != nil {
		t.Fatal(err)
	}
	run2, fresh, err := s.ClaimScheduledAutomationRun(def2.ID, "schedule:2", "singleton", def2.Revision, `{}`, string(snapshotJSON2), now.Add(5*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected a fresh claim after rotation")
	}
	if run2.TicketID != "ticket-2" || run2.SessionID != "session-2" {
		t.Fatalf("expected a fresh binding reservation after a location edit, got ticket=%q session=%q (pre-edit was ticket-1/session-1)", run2.TicketID, run2.SessionID)
	}

	req := automation.WorkRequest{RunID: run2.ID, DefinitionID: def2.ID, ContinuityKey: "singleton", Provider: "schedule", Prompt: snapshot2.Prompt, Launch: snapshot2.Launch, Location: snapshot2.Location, IDs: automation.DeliveryIDs{TicketID: run2.TicketID, SessionID: run2.SessionID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("post-rotation delivery should pass the continuation check, got: %v", err)
	}
}
