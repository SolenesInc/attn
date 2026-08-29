package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"pgregory.net/rapid"
)

func newLegacySalvageDaemon(t *testing.T) (*Daemon, *store.Store, string) {
	t.Helper()
	t.Setenv("ATTN_PROFILE", "")
	dataRoot := t.TempDir()
	makeRecoveryHome(t, dataRoot)
	target, err := store.NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	d := &Daemon{store: target, dataRoot: dataRoot, done: make(chan struct{})}
	d.legacyTicketRecoveryPostOnce.Do(func() {})
	return d, target, dataRoot
}

func addOrphanDelegation(t *testing.T, s *store.Store, requestID, ticketID, requestJSON string, now time.Time) {
	t.Helper()
	record, claimed, err := s.ClaimDelegationOperation(requestID, "op-"+requestID, "session-"+requestID, "", ticketID, requestJSON, now)
	if err != nil || !claimed {
		t.Fatalf("claim delegation: %#v claimed=%v err=%v", record, claimed, err)
	}
	result := &protocol.DelegateResult{SessionID: "session-" + requestID, WorkspaceID: "workspace-1", Directory: "/repo", Placement: "reuse"}
	if err := s.UpdateDelegationOperation(record.Operation.OperationID, protocol.DelegationOperationStateCompleted,
		"ready", "workspace-1", ticketID, "", result, nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func runLegacyRecoveryOnce(t *testing.T, d *Daemon) legacyTicketRecoveryResult {
	t.Helper()
	if wait, err := d.prepareLegacyTicketRecovery(); err != nil || !wait {
		t.Fatalf("prepare wait=%v err=%v", wait, err)
	}
	value, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 1, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}})
	if err != nil {
		t.Fatal(err)
	}
	return value.(legacyTicketRecoveryResult)
}

func TestLegacyDelegationSalvageIsDeterministicCreateOnlyAndOwnerOnly(t *testing.T) {
	d, target, dataRoot := newLegacySalvageDaemon(t)
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	addOrphanDelegation(t, target, "request-b", "ticket-b", ` {"brief":"second","cmd":"delegate"} `, now)
	addOrphanDelegation(t, target, "request-a", "ticket-a", ` {"brief":"first","cmd":"delegate"} `, now.Add(time.Minute))

	result := runLegacyRecoveryOnce(t, d)
	path := filepath.Join(dataRoot, "legacy-ticket-delegations.json")
	if result.Counts.DelegationsSalvaged != 2 || !reflect.DeepEqual(result.Artifacts, []string{path}) {
		t.Fatalf("result = %#v", result)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode = %v err=%v", info, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Version    int `json:"version"`
		Operations []struct {
			Row     store.LegacyDelegationOperation `json:"row"`
			Sources []legacyDelegationProvenance    `json:"sources"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(content, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Version != 1 || len(artifact.Operations) != 2 || artifact.Operations[0].Row.TicketID != "ticket-a" || artifact.Operations[1].Row.TicketID != "ticket-b" {
		t.Fatalf("artifact = %#v", artifact)
	}
	if artifact.Operations[0].Row.RequestJSON != ` {"brief":"first","cmd":"delegate"} ` || len(artifact.Operations[0].Sources) != 1 || artifact.Operations[0].Sources[0].Family != "live" {
		t.Fatalf("raw operation or provenance changed: %#v", artifact.Operations[0])
	}
	for _, id := range []string{"ticket-a", "ticket-b"} {
		if ticket, err := target.GetTicket(id); err != nil || ticket != nil {
			t.Fatalf("salvage created ticket %s: %#v, %v", id, ticket, err)
		}
		if link, err := target.LegacyTicketSeedLink(id); err != nil || link != nil {
			t.Fatalf("salvage created seed link %s: %#v, %v", id, link, err)
		}
	}

	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	run, err := target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if err != nil {
		t.Fatal(err)
	}
	again := legacyTicketRecoveryResult{}
	if err := d.salvageLegacyDelegations(context.Background(), run, &again); err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(path)
	if err != nil || !os.SameFile(beforeInfo, afterInfo) {
		t.Fatalf("exact adoption replaced the file: before=%v after=%v err=%v", beforeInfo, afterInfo, err)
	}
	againContent, _ := os.ReadFile(path)
	if string(content) != string(againContent) {
		t.Fatal("exact adoption changed artifact bytes")
	}
	notifications, err := target.ListNotifications()
	if err != nil || len(notifications) != 1 || !strings.Contains(notifications[0].Detail, path) {
		t.Fatalf("notifications = %#v, %v", notifications, err)
	}
}

func TestLegacyDelegationSalvageConflictAndWriteFailureDoNotRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		occupant []byte
		writeErr error
	}{
		{name: "different occupant", occupant: []byte("keep me")},
		{name: "write failure", writeErr: errors.New("disk unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, target, dataRoot := newLegacySalvageDaemon(t)
			addOrphanDelegation(t, target, "request-1", "lost-ticket", `{"brief":"save me"}`, time.Now())
			path := filepath.Join(dataRoot, "legacy-ticket-delegations.json")
			if tc.occupant != nil {
				if err := os.WriteFile(path, tc.occupant, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			if tc.writeErr != nil {
				d.legacyRecoveryArtifactWrite = func(string, []byte) error {
					calls++
					return tc.writeErr
				}
			}
			result := runLegacyRecoveryOnce(t, d)
			if len(result.Warnings) == 0 {
				t.Fatalf("result = %#v", result)
			}
			run, err := target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
			if err != nil || run.State != store.LegacyTicketRecoveryWarned {
				t.Fatalf("run = %#v, %v", run, err)
			}
			if tc.occupant != nil {
				got, _ := os.ReadFile(path)
				if string(got) != string(tc.occupant) {
					t.Fatalf("occupant overwritten: %q", got)
				}
			}
			if _, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 2, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}); err != nil {
				t.Fatal(err)
			}
			if tc.writeErr != nil && calls != 1 {
				t.Fatalf("write calls = %d, want one terminal attempt", calls)
			}
			notifications, err := target.ListNotifications()
			if err != nil || len(notifications) != 1 {
				t.Fatalf("notifications = %#v, %v", notifications, err)
			}
		})
	}
}

func TestLegacyNotebookRecoveryAttachesProvenFilesAndDumpsUnboundFragments(t *testing.T) {
	d, target, dataRoot := newLegacySalvageDaemon(t)
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	if _, err := target.CreateTicket(store.Ticket{
		ID: "proven-ticket", Title: "Proven", Description: "original", Status: store.TicketStatusDone,
	}, "you", now); err != nil {
		t.Fatal(err)
	}
	before, err := target.GetTicket("proven-ticket")
	if err != nil {
		t.Fatal(err)
	}
	notebookRoot := t.TempDir()
	target.SetSetting(SettingNotebookRoot, notebookRoot)
	for ticketID, filename := range map[string]string{"proven-ticket": "proof.md", "orphan-ticket": "orphan.txt"} {
		dir := filepath.Join(notebookRoot, "tickets", ticketID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filename), []byte("evidence for "+ticketID), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	provenDir := filepath.Join(notebookRoot, "tickets", "proven-ticket")
	nestedDir := filepath.Join(provenDir, "nested")
	if err := os.Mkdir(nestedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "hidden.md"), []byte("must not be read"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("must not be followed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(provenDir, "linked.md")); err != nil {
		t.Fatal(err)
	}

	result := runLegacyRecoveryOnce(t, d)
	if result.Counts.NotebookAttachments != 1 || result.Counts.FragmentsSalvaged != 3 {
		t.Fatalf("result = %#v", result)
	}
	after, err := target.GetTicket("proven-ticket")
	if err != nil {
		t.Fatal(err)
	}
	if before.Title != after.Title || before.Description != after.Description || before.Status != after.Status ||
		!before.CreatedAt.Equal(after.CreatedAt) || !before.UpdatedAt.Equal(after.UpdatedAt) ||
		!reflect.DeepEqual(before.Activity, after.Activity) || len(after.Attachments) != 1 {
		t.Fatalf("Notebook recovery rewrote ticket data:\nbefore=%#v\nafter=%#v", before, after)
	}
	seed := recoveredSeedForTicket(t, target, "proven-ticket")
	if seed.Status != garden.StatusHarvested {
		t.Fatalf("proven seed = %#v", seed)
	}
	if ticket, err := target.GetTicket("orphan-ticket"); err != nil || ticket != nil {
		t.Fatalf("unbound Notebook directory created a ticket: %#v, %v", ticket, err)
	}
	fragmentPath := filepath.Join(dataRoot, "legacy-ticket-recovery", "fragments.json")
	content, err := os.ReadFile(fragmentPath)
	if err != nil || !strings.Contains(string(content), `"kind": "notebook_unbound"`) ||
		!strings.Contains(string(content), "orphan.txt") ||
		!strings.Contains(string(content), "nested directory ignored") ||
		!strings.Contains(string(content), "symlink ignored") {
		t.Fatalf("fragments = %s, %v", content, err)
	}
	fileInfo, err := os.Stat(fragmentPath)
	dirInfo, dirErr := os.Stat(filepath.Dir(fragmentPath))
	if err != nil || dirErr != nil || fileInfo.Mode().Perm() != 0o600 || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("fragment permissions: file=%v dir=%v err=%v/%v", fileInfo, dirInfo, err, dirErr)
	}
	notifications, err := target.ListNotifications()
	if err != nil || len(notifications) != 1 || !strings.Contains(notifications[0].Detail, fragmentPath) {
		t.Fatalf("notifications = %#v, %v", notifications, err)
	}
}

func TestLegacyTranscriptPartialReceiptBecomesAFragmentNotATicket(t *testing.T) {
	d, target, dataRoot := newLegacySalvageDaemon(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	createCodexLegacyTranscript(t, codexHome, dataRoot, "native-partial", "partial-ticket", "in_progress", "working")

	result := runLegacyRecoveryOnce(t, d)
	if result.Counts.Recovered != 0 || result.Counts.FragmentsSalvaged != 1 {
		t.Fatalf("result = %#v", result)
	}
	if ticket, err := target.GetTicket("partial-ticket"); err != nil || ticket != nil {
		t.Fatalf("partial receipt created a ticket: %#v, %v", ticket, err)
	}
	path := filepath.Join(dataRoot, "legacy-ticket-recovery", "fragments.json")
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), `"kind": "transcript_partial_receipt"`) || !strings.Contains(string(content), `"state": "working"`) {
		t.Fatalf("fragments = %s, %v", content, err)
	}
}

func TestLegacyAutomationShapedTranscriptTicketNeedsRelationalProvenanceToBeExcluded(t *testing.T) {
	d, target, dataRoot := newLegacySalvageDaemon(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	ticketID := "auto-abcdef1234567890"
	createCodexLegacyTranscript(t, codexHome, dataRoot, "native-user-work", ticketID, "completed", "done")

	result := runLegacyRecoveryOnce(t, d)
	if result.Counts.TranscriptRecovered != 1 || result.Counts.Automation != 0 {
		t.Fatalf("result = %#v", result)
	}
	ticket, err := target.GetTicket(ticketID)
	if err != nil || ticket == nil || ticket.Status != store.TicketStatusDone {
		t.Fatalf("automation-shaped user ticket = %#v, %v", ticket, err)
	}
	if seed := recoveredSeedForTicket(t, target, ticketID); seed.Status != garden.StatusHarvested {
		t.Fatalf("seed = %#v", seed)
	}
}

func TestLegacyFragmentWriteFailureIsTerminalAndProtectsItsFrozenSource(t *testing.T) {
	d, target, dataRoot := newLegacySalvageDaemon(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	createCodexLegacyTranscript(t, codexHome, dataRoot, "native-undumped", "undumped-ticket", "in_progress", "working")
	calls := 0
	d.legacyRecoveryArtifactWrite = func(string, []byte) error {
		calls++
		return errors.New("disk unavailable")
	}

	result := runLegacyRecoveryOnce(t, d)
	if calls != 1 || len(result.Warnings) == 0 {
		t.Fatalf("result = %#v calls=%d", result, calls)
	}
	sources, err := target.ListLegacyTicketRecoverySources(store.LegacyTicketRecoveryVersion)
	if err != nil || len(sources) != 1 || sources[0].State != "protected" {
		t.Fatalf("sources = %#v, %v", sources, err)
	}
	run, err := target.GetLegacyTicketRecoveryRun(store.LegacyTicketRecoveryVersion)
	if err != nil || run.State != store.LegacyTicketRecoveryWarned {
		t.Fatalf("run = %#v, %v", run, err)
	}
	if _, err := d.legacyTicketRecoveryHandler(context.Background(), &jobs.Job{Attempts: 2, MaxAttempts: 3, CommitGuard: &jobs.CommitGuard{}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("fragment writer retried after terminal warning: calls=%d", calls)
	}
}

func TestLegacyFragmentNormalizationIgnoresSourceOrderAndDuplicates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		values := rapid.SliceOfN(rapid.IntRange(0, 20), 0, 100).Draw(t, "values")
		fragments := make([]legacyRecoveryFragment, 0, len(values))
		for _, value := range values {
			fragments = append(fragments, legacyRecoveryFragment{
				Kind: "property", TicketID: fmt.Sprintf("ticket-%d", value), Detail: fmt.Sprintf("value-%d", value),
			})
		}
		reversed := append([]legacyRecoveryFragment(nil), fragments...)
		slices.Reverse(reversed)
		left := normalizeLegacyRecoveryFragments(fragments)
		right := normalizeLegacyRecoveryFragments(reversed)
		if !reflect.DeepEqual(left, right) {
			t.Fatalf("normalization depends on source order:\nleft=%#v\nright=%#v", left, right)
		}
		if !reflect.DeepEqual(left, normalizeLegacyRecoveryFragments(left)) {
			t.Fatalf("normalization is not idempotent: %#v", left)
		}
	})
}
