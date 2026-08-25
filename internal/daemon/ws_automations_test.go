package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestAutomationDefinitionsGetWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDefinitionsGetWS(client, &protocol.AutomationDefinitionsGetMessage{
		Cmd:       protocol.CmdAutomationDefinitionsGet,
		RequestID: protocol.Ptr("defs-1"),
	})

	var res protocol.AutomationDefinitionsResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "defs-1" {
		t.Fatalf("definitions_get result = %+v, want success for defs-1", res)
	}
	if len(res.Definitions) != 1 || res.Definitions[0].ID != def.ID {
		t.Fatalf("definitions = %+v, want one summary for %s", res.Definitions, def.ID)
	}
	summary := res.Definitions[0]
	if summary.TriggerType != "manual" || !summary.Enabled {
		t.Fatalf("summary = %+v, want manual+enabled", summary)
	}
}

func TestAutomationRunsGetWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, time.Now(), store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunsGetWS(client, &protocol.AutomationRunsGetMessage{
		Cmd:          protocol.CmdAutomationRunsGet,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("runs-1"),
	})

	var res protocol.AutomationRunsResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "runs-1" {
		t.Fatalf("runs_get result = %+v, want success for runs-1", res)
	}
	if res.Truncated != nil && *res.Truncated {
		t.Fatalf("runs_get result truncated=%v, want unset/false for one run", res.Truncated)
	}
	if len(res.Runs) != 1 || res.Runs[0].ID != run.ID {
		t.Fatalf("runs = %+v, want one summary for %s", res.Runs, run.ID)
	}
	if res.Runs[0].OccurrenceKey == nil || *res.Runs[0].OccurrenceKey != "manual:request-1" {
		t.Fatalf("run occurrence_key = %v, want manual:request-1", res.Runs[0].OccurrenceKey)
	}
}

func TestAutomationRunsGetWSResultTruncatesAtCap(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < automationRunSummaryListCap+1; i++ {
		requestID := fmt.Sprintf("request-%d", i)
		if _, _, err := s.ClaimManualAutomationRun(def.ID, requestID, "", `{}`, def.Revision, `{}`, time.Now(), store.AutomationRunReservation{
			RunID: fmt.Sprintf("run-%d", i), OccurrenceID: fmt.Sprintf("occ-%d", i),
			TicketID: fmt.Sprintf("ticket-%d", i), SessionID: fmt.Sprintf("session-%d", i),
			WorkspaceID: fmt.Sprintf("workspace-%d", i), PaneID: fmt.Sprintf("pane-%d", i),
		}); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunsGetWS(client, &protocol.AutomationRunsGetMessage{
		Cmd:          protocol.CmdAutomationRunsGet,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("runs-cap"),
	})

	var res protocol.AutomationRunsResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success {
		t.Fatalf("runs_get result = %+v, want success", res)
	}
	if len(res.Runs) != automationRunSummaryListCap {
		t.Fatalf("runs = %d, want capped at %d", len(res.Runs), automationRunSummaryListCap)
	}
	if res.Truncated == nil || !*res.Truncated {
		t.Fatalf("truncated = %v, want true", res.Truncated)
	}
}

func TestAutomationSetEnabledWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(client, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: def.ID,
		Enabled:      false,
		RequestID:    protocol.Ptr("set-1"),
	})

	var res protocol.AutomationSetEnabledResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "set-1" {
		t.Fatalf("set_enabled result = %+v, want success for set-1", res)
	}
	if res.Definition == nil || res.Definition.Enabled {
		t.Fatalf("set_enabled definition = %+v, want a disabled summary", res.Definition)
	}

	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(client2, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: "does-not-exist",
		Enabled:      true,
		RequestID:    protocol.Ptr("set-2"),
	})
	var errRes protocol.AutomationSetEnabledResultMessage
	readNotebookWSEvent(t, client2.send, &errRes)
	if errRes.Success || errRes.Error == nil {
		t.Fatalf("set_enabled unknown definition result = %+v, want success=false with error", errRes)
	}
}

func TestAutomationDeleteWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDeleteWS(client, &protocol.AutomationDeleteMessage{
		Cmd:          protocol.CmdAutomationDelete,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("delete-1"),
	})

	var res protocol.AutomationDeleteResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "delete-1" {
		t.Fatalf("delete result = %+v, want success for delete-1", res)
	}

	if got, err := s.GetAutomationDefinition(def.ID); err != nil || got != nil {
		t.Fatalf("expected the definition to be soft-deleted, got %#v err=%v", got, err)
	}

	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDeleteWS(client2, &protocol.AutomationDeleteMessage{
		Cmd:          protocol.CmdAutomationDelete,
		DefinitionID: "does-not-exist",
		RequestID:    protocol.Ptr("delete-2"),
	})
	var errRes protocol.AutomationDeleteResultMessage
	readNotebookWSEvent(t, client2.send, &errRes)
	if errRes.Success || errRes.Error == nil {
		t.Fatalf("delete unknown definition result = %+v, want success=false with error", errRes)
	}
}

func TestAutomationCleanupWSResultCorrelatesRequest(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-ws", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run := claimTerminalAutomationRun(t, s, def, "cleanup-ws-1", time.Now(), automationResolvedLocationJSON(t, mainRepo, worktree))

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationCleanupWS(client, &protocol.AutomationCleanupMessage{
		Cmd:          protocol.CmdAutomationCleanup,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("cleanup-1"),
	})

	var res protocol.AutomationCleanupResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "cleanup-1" {
		t.Fatalf("cleanup result = %+v, want success for cleanup-1", res)
	}
	if len(res.Cleaned) != 1 || res.Cleaned[0] != run.ID {
		t.Fatalf("cleanup result Cleaned = %v, want [%s]", res.Cleaned, run.ID)
	}
	if len(res.KeptDirty) != 0 {
		t.Fatalf("cleanup result KeptDirty = %v, want none", res.KeptDirty)
	}
	if len(res.KeptActive) != 0 {
		t.Fatalf("cleanup result KeptActive = %v, want none", res.KeptActive)
	}

	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationCleanupWS(client2, &protocol.AutomationCleanupMessage{
		Cmd:          protocol.CmdAutomationCleanup,
		DefinitionID: "does-not-exist",
		RequestID:    protocol.Ptr("cleanup-2"),
	})
	var errRes protocol.AutomationCleanupResultMessage
	readNotebookWSEvent(t, client2.send, &errRes)
	if errRes.Success || errRes.Error == nil {
		t.Fatalf("cleanup unknown definition result = %+v, want success=false with error", errRes)
	}
}

func TestAutomationRunWSResultCorrelatesRequest(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	run, _, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, "{}", now); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "request-1",
	})

	var res protocol.AutomationRunResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.RequestID == nil || *res.RequestID != "request-1" {
		t.Fatalf("run result = %+v, want success for request-1", res)
	}
	if res.Run == nil || res.Run.ID != run.ID {
		t.Fatalf("run result run = %+v, want %s", res.Run, run.ID)
	}
	if res.Run.TicketID == nil || *res.Run.TicketID != run.TicketID || res.Run.SessionID == nil || *res.Run.SessionID != run.SessionID {
		t.Fatalf("run result ticket/session = %+v, want %s/%s", res.Run, run.TicketID, run.SessionID)
	}
}

func TestAutomationRunWSRejectsNonManualTrigger(t *testing.T) {
	d, _, def, _ := setupScheduledDaemon(t, "* * * * *", "fresh", "latest")

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "request-1",
	})

	var res protocol.AutomationRunResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "cannot be run manually") {
		t.Fatalf("run result = %+v, want success=false with a manual-trigger rejection", res)
	}
}

func TestAutomationRunWSMutualExclusion(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "request-1",
		PRURL:        protocol.Ptr("https://github.com/owner/repo/pull/1"),
		InputJson:    protocol.Ptr(`{}`),
	})

	var res protocol.AutomationRunResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "mutually exclusive") {
		t.Fatalf("run result = %+v, want success=false mutual-exclusion error", res)
	}
}

func TestAutomationRunWSRoutesPRURL(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "request-1",
		PRURL:        protocol.Ptr("https://github.com/owner/repo/pull/1"),
	})

	var res protocol.AutomationRunResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("run result = %+v, want success=false (pr_url routed to the pull-request path)", res)
	}
	if strings.Contains(*res.Error, "mutually exclusive") || strings.Contains(*res.Error, "cannot be run manually") {
		t.Fatalf("run result error = %q, want automationRunPullRequest's own validation error, not a manual-path error", *res.Error)
	}
}

func TestAutomationValidateAndApplyAgreeOnCorpus(t *testing.T) {
	const template = `api_version: attn.dev/automations/v1alpha1
id: %s
name: Corpus case
trigger: {type: manual}
prompt: Do the thing.
launch: {driver: %s}
location: {type: directory, path: "%s"}
`
	cases := []struct {
		name      string
		id        string
		driver    string
		mutate    func(raw string) string
		wantValid bool
		wantErr   string
	}{
		{name: "valid codex baseline", id: "corpus-valid-codex", driver: "codex", wantValid: true},
		{name: "valid claude baseline", id: "corpus-valid-claude", driver: "claude", wantValid: true},
		{
			name:      "driver outside the automatic-approval allowlist is rejected beyond parse",
			id:        "corpus-shell-driver",
			driver:    "shell",
			wantValid: false,
			wantErr:   "does not support automation automatic approval",
		},
		{
			name:      "unresolvable agent is rejected beyond parse",
			id:        "corpus-fake-driver",
			driver:    "totally-not-a-real-agent",
			wantValid: false,
			wantErr:   "not available",
		},
		{
			name:   "missing prompt is rejected at parse",
			id:     "corpus-missing-prompt",
			driver: "codex",
			mutate: func(raw string) string {
				return strings.Replace(raw, "prompt: Do the thing.\n", "", 1)
			},
			wantValid: false,
			wantErr:   "prompt is required",
		},
		{
			name:   "bad api_version is rejected at parse",
			id:     "corpus-bad-api-version",
			driver: "codex",
			mutate: func(raw string) string {
				return strings.Replace(raw, "attn.dev/automations/v1alpha1", "attn.dev/automations/v0", 1)
			},
			wantValid: false,
			wantErr:   "api_version must be",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := store.New()
			d := &Daemon{store: s, wsHub: newWSHub()}
			raw := fmt.Sprintf(template, tc.id, tc.driver, t.TempDir())
			if tc.mutate != nil {
				raw = tc.mutate(raw)
			}

			validateClient := &wsClient{send: make(chan outboundMessage, 4)}
			d.handleAutomationValidateWS(validateClient, &protocol.AutomationValidateMessage{
				Cmd:            protocol.CmdAutomationValidate,
				DefinitionYaml: raw,
				RequestID:      protocol.Ptr("validate"),
			})
			var validateRes protocol.AutomationValidateResultMessage
			readNotebookWSEvent(t, validateClient.send, &validateRes)

			applyClient := &wsClient{send: make(chan outboundMessage, 4)}
			d.handleAutomationApplyWS(applyClient, &protocol.AutomationApplyMessage{
				Cmd:            protocol.CmdAutomationApply,
				DefinitionYaml: raw,
				RequestID:      protocol.Ptr("apply"),
			})
			var applyRes protocol.AutomationApplyResultMessage
			readNotebookWSEvent(t, applyClient.send, &applyRes)

			if validateRes.Success != applyRes.Success {
				t.Fatalf("validate/apply disagree on %q: validate success=%v (err=%v), apply success=%v (err=%v)",
					tc.name, validateRes.Success, validateRes.Error, applyRes.Success, applyRes.Error)
			}
			if validateRes.Success != tc.wantValid {
				t.Fatalf("success = %v, want %v (validate error=%v, apply error=%v)", validateRes.Success, tc.wantValid, validateRes.Error, applyRes.Error)
			}
			if !tc.wantValid {
				if validateRes.Error == nil || !strings.Contains(*validateRes.Error, tc.wantErr) {
					t.Fatalf("validate error = %v, want to contain %q", validateRes.Error, tc.wantErr)
				}
				if applyRes.Error == nil || !strings.Contains(*applyRes.Error, tc.wantErr) {
					t.Fatalf("apply error = %v, want to contain %q", applyRes.Error, tc.wantErr)
				}
				if applyRes.ErrorCode == nil || *applyRes.ErrorCode != "validation" {
					t.Fatalf("apply error_code = %v, want %q", applyRes.ErrorCode, "validation")
				}
			}
		})
	}
}

func TestAutomationCommandApplyOverSocketIsUnguarded(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	colliding := strings.Replace(raw, "Check locally.", "Something else entirely.", 1)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleAutomationCommand(serverConn, protocol.CmdAutomationApply,
		&protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, DefinitionYaml: colliding})

	var res protocol.AutomationApplyResultMessage
	if err := json.NewDecoder(clientConn).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("socket apply result = %+v, want success (unguarded last-writer-wins)", res)
	}
	overwritten, err := s.GetAutomationDefinition(original.ID)
	if err != nil || overwritten == nil || !strings.Contains(overwritten.SpecJSON, "Something else entirely.") {
		t.Fatalf("definition after unguarded socket apply = %#v err=%v, want the new content live", overwritten, err)
	}
}

func TestAutomationApplyWSRefusesIDMismatch(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	renamed := strings.Replace(raw, "id: manual-check", "id: manual-check-renamed", 1)
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(client, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   renamed,
		ExpectedID:       protocol.Ptr(original.ID),
		ExpectedRevision: protocol.Ptr(original.Revision),
		RequestID:        protocol.Ptr("apply-id-mismatch"),
	})

	var res protocol.AutomationApplyResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "does not match the definition being edited") {
		t.Fatalf("apply result = %+v, want success=false with an id-mismatch error", res)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "id_mismatch" {
		t.Fatalf("apply result error_code = %v, want %q", res.ErrorCode, "id_mismatch")
	}

	stillOriginal, err := s.GetAutomationDefinition(original.ID)
	if err != nil || stillOriginal == nil || stillOriginal.Revision != original.Revision {
		t.Fatalf("original definition after refused apply = %#v err=%v, want unchanged", stillOriginal, err)
	}
	if got, err := s.GetAutomationDefinition("manual-check-renamed"); err != nil || got != nil {
		t.Fatalf("renamed id after refused apply = %#v err=%v, want no definition created", got, err)
	}
}

func TestAutomationApplyWSRefusesCreateOverLiveDefinition(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	colliding := strings.Replace(raw, "Check locally.", "Something else entirely.", 1)
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(client, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   colliding,
		ExpectedRevision: protocol.Ptr(0),
		RequestID:        protocol.Ptr("apply-create-collision"),
	})

	var res protocol.AutomationApplyResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "already exists") {
		t.Fatalf("apply result = %+v, want success=false with an already-exists error", res)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "id_collision" {
		t.Fatalf("apply result error_code = %v, want %q", res.ErrorCode, "id_collision")
	}
	survivor, err := s.GetAutomationDefinition(original.ID)
	if err != nil || survivor == nil {
		t.Fatalf("definition after refused create = %#v err=%v, want it to survive", survivor, err)
	}
	if survivor.Revision != original.Revision || !strings.Contains(survivor.SpecJSON, "Check locally.") {
		t.Fatalf("definition after refused create = revision %d spec_json %q, want the original untouched",
			survivor.Revision, survivor.SpecJSON)
	}

	if err := d.automationDelete(context.Background(), original.ID); err != nil {
		t.Fatal(err)
	}
	resurrectClient := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(resurrectClient, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   colliding,
		ExpectedRevision: protocol.Ptr(0),
		RequestID:        protocol.Ptr("apply-create-resurrect"),
	})
	var resurrectRes protocol.AutomationApplyResultMessage
	readNotebookWSEvent(t, resurrectClient.send, &resurrectRes)
	if !resurrectRes.Success {
		t.Fatalf("resurrect apply result = %+v, want success", resurrectRes)
	}
	resurrected, err := s.GetAutomationDefinition(original.ID)
	if err != nil || resurrected == nil || !strings.Contains(resurrected.SpecJSON, "Something else entirely.") {
		t.Fatalf("definition after resurrect = %#v err=%v, want the new content live", resurrected, err)
	}
}

func TestAutomationApplyWSRefusesStaleRevision(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	concurrentlyApplied := strings.Replace(raw, "Manual check", "Manual check (renamed elsewhere)", 1)
	if _, err := d.automationApply(concurrentlyApplied); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(client, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   strings.Replace(raw, "Manual check", "Manual check (from the stale editor)", 1),
		ExpectedID:       protocol.Ptr(original.ID),
		ExpectedRevision: protocol.Ptr(original.Revision),
		RequestID:        protocol.Ptr("apply-stale-revision"),
	})

	var res protocol.AutomationApplyResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "changed elsewhere") {
		t.Fatalf("apply result = %+v, want success=false with a changed-elsewhere error", res)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "revision_conflict" {
		t.Fatalf("apply result error_code = %v, want %q", res.ErrorCode, "revision_conflict")
	}

	stored, err := s.GetAutomationDefinition(original.ID)
	if err != nil || stored == nil || stored.Name != "Manual check (renamed elsewhere)" {
		t.Fatalf("definition after refused stale apply = %#v err=%v, want the concurrent apply's name to survive", stored, err)
	}
}

func TestAutomationApplyWSRefusesEditOfDeletedDefinition(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	original, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.automationDelete(context.Background(), original.ID); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(client, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   strings.Replace(raw, "Manual check", "Manual check (from the stale editor)", 1),
		ExpectedID:       protocol.Ptr(original.ID),
		ExpectedRevision: protocol.Ptr(original.Revision),
		RequestID:        protocol.Ptr("apply-edit-deleted"),
	})

	var res protocol.AutomationApplyResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "deleted") || !strings.Contains(*res.Error, "New") {
		t.Fatalf("apply result = %+v, want success=false with an error naming the deletion and pointing at New", res)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "deleted_elsewhere" {
		t.Fatalf("apply result error_code = %v, want %q", res.ErrorCode, "deleted_elsewhere")
	}

	if live, err := s.GetAutomationDefinition(original.ID); err != nil || live != nil {
		t.Fatalf("definition after refused edit-of-deleted apply = %#v err=%v, want still absent from the live set", live, err)
	}
	stillDeleted, err := s.GetAutomationDefinitionIncludingDeleted(original.ID)
	if err != nil || stillDeleted == nil || stillDeleted.DeletedAt == nil {
		t.Fatalf("definition after refused edit-of-deleted apply = %#v err=%v, want still soft-deleted", stillDeleted, err)
	}
	if stillDeleted.Revision != original.Revision {
		t.Fatalf("deleted definition revision = %d, want unchanged %d", stillDeleted.Revision, original.Revision)
	}
}

func TestAutomationDefinitionGetWSStarterTemplate(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDefinitionGetWS(client, &protocol.AutomationDefinitionGetMessage{
		Cmd:          protocol.CmdAutomationDefinitionGet,
		DefinitionID: "",
		RequestID:    protocol.Ptr("get-starter"),
	})

	var res protocol.AutomationDefinitionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.SpecYaml == nil {
		t.Fatalf("definition_get(\"\") result = %+v, want success with spec_yaml set", res)
	}
	if res.Definition != nil {
		t.Fatalf("starter template result definition = %+v, want absent (new-definition case)", res.Definition)
	}
	if !strings.Contains(*res.SpecYaml, "id: my-automation") {
		t.Fatalf("starter template spec_yaml = %q, want the StarterDefinition placeholder", *res.SpecYaml)
	}
	if res.SpecJson == nil {
		t.Fatalf("starter template result spec_json = nil, want the StarterDefinition encoded as JSON")
	}
	var starterSpec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(*res.SpecJson), &starterSpec); err != nil {
		t.Fatalf("starter template spec_json = %q, want valid JSON: %v", *res.SpecJson, err)
	}
	if !reflect.DeepEqual(starterSpec, automation.StarterDefinition) {
		t.Fatalf("starter template spec_json decodes to %#v, want automation.StarterDefinition %#v", starterSpec, automation.StarterDefinition)
	}
}

func TestAutomationDefinitionGetWSUnknownID(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDefinitionGetWS(client, &protocol.AutomationDefinitionGetMessage{
		Cmd:          protocol.CmdAutomationDefinitionGet,
		DefinitionID: "does-not-exist",
		RequestID:    protocol.Ptr("get-missing"),
	})

	var res protocol.AutomationDefinitionResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("definition_get(unknown) result = %+v, want success=false with an error", res)
	}
}

func TestAutomationDefinitionGetWSAfterToggleDerivesFromSpecJSON(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	setClient := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(setClient, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: def.ID,
		Enabled:      false,
		RequestID:    protocol.Ptr("toggle-off"),
	})
	var setRes protocol.AutomationSetEnabledResultMessage
	readNotebookWSEvent(t, setClient.send, &setRes)
	if !setRes.Success || setRes.Definition == nil || setRes.Definition.Enabled {
		t.Fatalf("set_enabled result = %+v, want a disabled summary", setRes)
	}

	getClient := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationDefinitionGetWS(getClient, &protocol.AutomationDefinitionGetMessage{
		Cmd:          protocol.CmdAutomationDefinitionGet,
		DefinitionID: def.ID,
		RequestID:    protocol.Ptr("get-after-toggle"),
	})
	var getRes protocol.AutomationDefinitionResultMessage
	readNotebookWSEvent(t, getClient.send, &getRes)
	if !getRes.Success || getRes.SpecYaml == nil || getRes.Definition == nil || getRes.SpecJson == nil {
		t.Fatalf("definition_get after toggle = %+v, want success with spec_yaml, spec_json, and definition set", getRes)
	}
	fetched := *getRes.SpecYaml
	if strings.Contains(fetched, "enabled:") {
		t.Fatalf("derived spec_yaml must never carry an enabled key:\n%s", fetched)
	}

	var fromWire, stored automation.DefinitionSpec
	if err := json.Unmarshal([]byte(*getRes.SpecJson), &fromWire); err != nil {
		t.Fatalf("definition_get spec_json = %q, want valid JSON: %v", *getRes.SpecJson, err)
	}
	if err := json.Unmarshal([]byte(def.SpecJSON), &stored); err != nil {
		t.Fatalf("stored spec_json = %q, want valid JSON: %v", def.SpecJSON, err)
	}
	if !reflect.DeepEqual(fromWire, stored) {
		t.Fatalf("definition_get spec_json decodes to %#v, want the stored spec %#v", fromWire, stored)
	}

	applyClient := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationApplyWS(applyClient, &protocol.AutomationApplyMessage{
		Cmd:              protocol.CmdAutomationApply,
		DefinitionYaml:   fetched,
		ExpectedID:       protocol.Ptr(def.ID),
		ExpectedRevision: protocol.Ptr(getRes.Definition.Revision),
		RequestID:        protocol.Ptr("save-after-get"),
	})
	var applyRes protocol.AutomationApplyResultMessage
	readNotebookWSEvent(t, applyClient.send, &applyRes)
	if !applyRes.Success {
		t.Fatalf("re-applying the fetched (disabled) yaml failed: %+v", applyRes)
	}

	final, err := s.GetAutomationDefinition(def.ID)
	if err != nil || final == nil || final.Enabled {
		t.Fatalf("definition after re-applying fetched yaml = %#v err=%v, want it to stay disabled (apply never toggles enabled)", final, err)
	}
}

// Not synctest-able: a goroutine waiting on d.automationMu is not durably blocked, so a bubble's clock never reaches the release.
func TestAutomationSetEnabledWSDeadlineAbortsWithoutMutating(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub(), wsAutomationMutationTimeout: 50 * time.Millisecond}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !def.Enabled {
		t.Fatalf("fixture definition = %#v, want enabled", def)
	}

	d.automationMu.Lock()
	released := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.automationMu.Unlock()
		close(released)
	}()

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(client, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: def.ID,
		Enabled:      false,
		RequestID:    protocol.Ptr("set-deadline"),
	})

	var res protocol.AutomationSetEnabledResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil || !strings.Contains(*res.Error, "deadline exceeded") {
		t.Fatalf("set_enabled result = %+v, want success=false with a deadline error", res)
	}

	<-released

	stored, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled {
		t.Fatalf("definition after deadline abort = %#v, want still enabled (no late flip)", stored)
	}

	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationSetEnabledWS(client2, &protocol.AutomationSetEnabledMessage{
		Cmd:          protocol.CmdAutomationSetEnabled,
		DefinitionID: def.ID,
		Enabled:      false,
		RequestID:    protocol.Ptr("set-after"),
	})
	var res2 protocol.AutomationSetEnabledResultMessage
	readNotebookWSEvent(t, client2.send, &res2)
	if !res2.Success {
		t.Fatalf("set_enabled after lock freed = %+v, want success", res2)
	}
	stored2, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored2.Enabled {
		t.Fatalf("definition after uncontended set_enabled = %#v, want disabled", stored2)
	}
}

// Not synctest-able: both retries queue on d.automationMu, and a mutex wait is invisible to a bubble.
func TestAutomationRunWSRetryWithSameRequestIDDoesNotDuplicate(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	run, _, err := s.ClaimManualAutomationRun(def.ID, "retry-request", "", `{}`, def.Revision, `{}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", TicketID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, "{}", now); err != nil {
		t.Fatal(err)
	}

	d.automationMu.Lock()
	released := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.automationMu.Unlock()
		close(released)
	}()

	client1 := &wsClient{send: make(chan outboundMessage, 4)}
	client2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAutomationRunWS(client1, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "retry-request",
	})
	d.handleAutomationRunWS(client2, &protocol.AutomationRunMessage{
		Cmd:          protocol.CmdAutomationRun,
		DefinitionID: def.ID,
		RequestID:    "retry-request",
	})

	var res1, res2 protocol.AutomationRunResultMessage
	readNotebookWSEvent(t, client1.send, &res1)
	readNotebookWSEvent(t, client2.send, &res2)
	<-released

	if !res1.Success || !res2.Success {
		t.Fatalf("run results = %+v / %+v, want both success", res1, res2)
	}
	if res1.Run == nil || res2.Run == nil || res1.Run.ID != run.ID || res2.Run.ID != run.ID {
		t.Fatalf("runs = %+v / %+v, want both %s", res1.Run, res2.Run, run.ID)
	}

	runs, err := s.ListAutomationRuns(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs for definition = %d, want exactly 1 (no duplicate claim from the retried request_id)", len(runs))
	}
}

func TestAutomationCommandValidateHasNoPayload(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleAutomationCommand(serverConn, protocol.CmdAutomationValidate,
		&protocol.AutomationValidateMessage{Cmd: protocol.CmdAutomationValidate, DefinitionYaml: raw})

	var res protocol.AutomationValidateResultMessage
	if err := json.NewDecoder(clientConn).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Error != nil {
		t.Fatalf("validate result = %+v, want success with no error", res)
	}
}

func TestAutomationCommandSetEnabledTogglesColumn(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !def.Enabled {
		t.Fatalf("fixture definition = %#v, want enabled", def)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleAutomationCommand(serverConn, protocol.CmdAutomationSetEnabled,
		&protocol.AutomationSetEnabledMessage{Cmd: protocol.CmdAutomationSetEnabled, DefinitionID: def.ID, Enabled: false})

	var encoded map[string]any
	if err := json.NewDecoder(clientConn).Decode(&encoded); err != nil {
		t.Fatal(err)
	}
	if success, _ := encoded["success"].(bool); !success {
		t.Fatalf("automation_set_enabled did not succeed: %+v", encoded)
	}

	stored, err := s.GetAutomationDefinition(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatalf("definition after socket automation_set_enabled(false) = %#v, want disabled", stored)
	}
}

func TestAutomationCommandDefinitionGetMissingDefinitionOverSocket(t *testing.T) {
	s := store.New()
	d := &Daemon{store: s, wsHub: newWSHub()}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleAutomationCommand(serverConn, protocol.CmdAutomationDefinitionGet,
		&protocol.AutomationDefinitionGetMessage{Cmd: protocol.CmdAutomationDefinitionGet, DefinitionID: "no-such-definition"})

	var res protocol.AutomationDefinitionResultMessage
	if err := json.NewDecoder(clientConn).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Error == nil {
		t.Fatalf("definition_get(missing) over socket = %+v, want success=false with an error", res)
	}
}
