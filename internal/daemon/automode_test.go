package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func automodeShow(t *testing.T, d *Daemon) *protocol.AutoModeShowResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeShow(c, &protocol.AutoModeShowMessage{Cmd: protocol.CmdAutoModeShow})
	})
	if !resp.Ok {
		t.Fatalf("automode show: %v", protocol.Deref(resp.Error))
	}
	return resp.AutomodeShowResult
}

func automodePropose(t *testing.T, d *Daemon, kind, target, value string) protocol.Response {
	t.Helper()
	msg := &protocol.AutoModeProposeMessage{Cmd: protocol.CmdAutoModePropose, Kind: kind, Value: value}
	if target != "" {
		msg.Target = protocol.Ptr(target)
	}
	return docCall(t, func(c net.Conn) { d.handleAutoModePropose(c, msg) })
}

func TestAutoModeShowAnswersDefaultsOnAFreshProfile(t *testing.T) {
	d := newDaemonForTest(t)
	result := automodeShow(t, d)
	cfg := result.Config
	if cfg.ApprovalPolicy != automode.PolicyOnRequest || cfg.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Errorf("policy = %q/%q, want the shipped defaults", cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	if cfg.Rules == nil || cfg.ShippedRules == nil || cfg.LegacyPatterns == nil ||
		cfg.Network.AllowedDomains == nil || cfg.Network.DeniedDomains == nil {
		t.Fatalf("a config list came back nil: %+v", cfg)
	}
	if len(cfg.Rules) != len(cfg.ShippedRules) {
		t.Errorf("rules = %+v on a fresh profile, want only the shipped ones", cfg.Rules)
	}
	if cfg.Environment.Slots == nil || cfg.Environment.Notes == nil {
		t.Fatalf("the environment came back nil: %+v", cfg.Environment)
	}
	if len(result.Proposals) != 0 {
		t.Fatalf("a fresh profile has %d proposals", len(result.Proposals))
	}
}

func TestAutoModeRuleFromASessionOnlyProposes(t *testing.T) {
	d := newDaemonForTest(t)
	resp := automodePropose(t, d, automode.KindRule, "", `{"pattern":["git","push"],"decision":"allow"}`)
	if !resp.Ok {
		t.Fatalf("propose: %v", protocol.Deref(resp.Error))
	}
	if resp.AutomodeProposeResult.Proposal.State != automode.StatePending {
		t.Errorf("proposal state = %q", resp.AutomodeProposeResult.Proposal.State)
	}
	after := automodeShow(t, d)
	if len(after.Config.Rules) != len(after.Config.ShippedRules) {
		t.Fatalf("the effective rules changed to %+v", after.Config.Rules)
	}
	if len(after.Proposals) != 1 {
		t.Fatalf("proposals = %d, want the one just recorded", len(after.Proposals))
	}
	if got := after.Proposals[0].Summary; got != "allow git push" {
		t.Errorf("proposal summary = %q, want the line a reviewer reads", got)
	}
}

func TestAutoModeProposeRefusesWhatCouldNeverBePromoted(t *testing.T) {
	d := newDaemonForTest(t)
	resp := automodePropose(t, d, automode.KindRule, "", `{"pattern":["git push"],"decision":"allow"}`)
	if resp.Ok {
		t.Fatal("a shell line was accepted as a rule pattern")
	}
	if !strings.Contains(protocol.Deref(resp.Error), "one command token per entry") {
		t.Fatalf("refusal does not say what a pattern is: %q", protocol.Deref(resp.Error))
	}
	resp = automodePropose(t, d, automode.KindHost, "", `{"host":"github.com","decision":"prompt"}`)
	if resp.Ok {
		t.Fatal("a host with a decision the network has no room for was accepted")
	}
	if len(automodeShow(t, d).Proposals) != 0 {
		t.Fatal("a refused proposal reached the review list")
	}
}

func TestAutoModeEnvironmentSlotWritesAndClears(t *testing.T) {
	d := newDaemonForTest(t)
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeEnvSlot(c, &protocol.AutoModeEnvSlotMessage{
			Cmd: protocol.CmdAutoModeEnvSlot, Slot: "domains",
			Values: []string{"grafana.acme.corp", "docs.acme.corp"},
		})
	})
	if !resp.Ok {
		t.Fatalf("env slot: %v", protocol.Deref(resp.Error))
	}
	if got := envSlotValues(resp.AutomodeEnvResult.Environment, "domains"); len(got) != 2 {
		t.Fatalf("domains = %v, want both entries", got)
	}

	resp = docCall(t, func(c net.Conn) {
		d.handleAutoModeEnvSlot(c, &protocol.AutoModeEnvSlotMessage{
			Cmd: protocol.CmdAutoModeEnvSlot, Slot: "domains", Values: []string{}})
	})
	if !resp.Ok {
		t.Fatalf("clearing the slot: %v", protocol.Deref(resp.Error))
	}
	if got := envSlotValues(resp.AutomodeEnvResult.Environment, "domains"); len(got) != 0 {
		t.Fatalf("domains = %v after clearing it", got)
	}

	resp = docCall(t, func(c net.Conn) {
		d.handleAutoModeEnvSlot(c, &protocol.AutoModeEnvSlotMessage{
			Cmd: protocol.CmdAutoModeEnvSlot, Slot: "intranet", Values: []string{"acme.corp"}})
	})
	if resp.Ok {
		t.Fatal("a slot nothing reads was accepted")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "intranet") || !strings.Contains(msg, "domains") {
		t.Fatalf("refusal names neither the ask nor the choices: %q", msg)
	}
}

func TestAutoModeEnvironmentNotesKeepProseBesideTheSlots(t *testing.T) {
	d := newDaemonForTest(t)
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeEnvNotes(c, &protocol.AutoModeEnvNotesMessage{
			Cmd:   protocol.CmdAutoModeEnvNotes,
			Notes: []string{"this laptop is mine   ", "", "nothing here serves traffic", "", "  "},
		})
	})
	if !resp.Ok {
		t.Fatalf("env notes: %v", protocol.Deref(resp.Error))
	}
	want := []string{"this laptop is mine", "", "nothing here serves traffic"}
	if got := resp.AutomodeEnvResult.Environment.Notes; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("notes = %q, want %q", got, want)
	}
}

func envSlotValues(env protocol.AutoModeEnvironmentInfo, id string) []string {
	for _, slot := range env.Slots {
		if slot.ID == id {
			return slot.Values
		}
	}
	return nil
}

func TestAnEnvironmentWriteSaysTheConfigMoved(t *testing.T) {
	d := newDaemonForTest(t)
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeEnvSlot(c, &protocol.AutoModeEnvSlotMessage{
			Cmd: protocol.CmdAutoModeEnvSlot, Slot: "buckets", Values: []string{"s3://acme-artifacts"}})
	})
	if !resp.Ok {
		t.Fatalf("env slot: %v", protocol.Deref(resp.Error))
	}

	published := docFacts(t, d, FactAutoModeConfigChanged)
	if len(published) != 1 || published[0].Subject != AutoModeConfigSubject {
		t.Fatalf("automode.config.changed facts = %+v, want one naming the config", published)
	}
}

func TestAutoModeGetOffersTheEnvironmentSchema(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()
	d.handleAutoModeGet(client, &protocol.AutoModeGetMessage{Cmd: protocol.CmdAutoModeGet, RequestID: "r1"})
	var result protocol.AutoModeStateResultMessage
	nextBusMessage(t, client, &result)
	if !result.Success {
		t.Fatalf("automode_get failed: %q", protocol.Deref(result.Error))
	}
	if len(result.EnvironmentSlots) != len(automode.Slots()) {
		t.Fatalf("slots = %d, want the schema's %d", len(result.EnvironmentSlots), len(automode.Slots()))
	}
	for i, slot := range result.EnvironmentSlots {
		if slot.ID != automode.Slots()[i].ID {
			t.Errorf("slot %d is %q, want the schema's order (%q)", i, slot.ID, automode.Slots()[i].ID)
		}
		if slot.Label == "" || slot.Detail == "" || slot.Unset == "" || len(slot.ReadBy) == 0 {
			t.Errorf("slot %+v is missing something the panel renders", slot)
		}
	}
}

func TestAutoModeDenialsReadsWhatSessionsReported(t *testing.T) {
	d := newDaemonForTest(t)
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeDenials(c, &protocol.AutoModeDenialsMessage{Cmd: protocol.CmdAutoModeDenials})
	})
	if !resp.Ok {
		t.Fatalf("denials: %v", protocol.Deref(resp.Error))
	}
	if len(resp.AutomodeDenialsResult.Denials) != 0 {
		t.Fatalf("a machine that denied nothing has denials: %+v", resp.AutomodeDenialsResult.Denials)
	}

	for _, action := range []string{"bash: curl https://one.example", "write /etc/hosts"} {
		if _, _, err := d.store.RecordAutoModeDenial(store.AutoModeDenial{
			SessionID: "pi-1", Tool: "bash", Signature: action,
			Reason: "outside the envelope", Rule: "classifier-2a",
		}, time.Now()); err != nil {
			t.Fatalf("record denial: %v", err)
		}
	}
	resp = docCall(t, func(c net.Conn) {
		d.handleAutoModeDenials(c, &protocol.AutoModeDenialsMessage{Cmd: protocol.CmdAutoModeDenials})
	})
	listed := resp.AutomodeDenialsResult.Denials
	if len(listed) != 2 {
		t.Fatalf("denials = %+v, want both", listed)
	}
	if listed[0].Signature != "write /etc/hosts" {
		t.Errorf("newest denial = %q", listed[0].Signature)
	}
	if listed[0].Rule != "classifier-2a" || listed[0].SessionID != "pi-1" || listed[0].CreatedAt == "" {
		t.Errorf("denial = %+v, want session, rule and time", listed[0])
	}
}

func TestAutoModePromoteFromTheAppPutsItInForce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	resp := automodePropose(t, d, automode.KindRule, "", `{"pattern":["git","push"],"decision":"prompt"}`)
	if !resp.Ok {
		t.Fatalf("propose: %v", protocol.Deref(resp.Error))
	}
	id := resp.AutomodeProposeResult.Proposal.ID

	client := busTestClient()
	d.handleAutoModePromote(client, &protocol.AutoModePromoteMessage{ID: id, RequestID: "r1"})
	var promoted protocol.AutoModePromoteResultMessage
	nextBusMessage(t, client, &promoted)
	if !promoted.Success {
		t.Fatalf("promote failed: %q", protocol.Deref(promoted.Error))
	}
	if promoted.Config == nil || len(promoted.Config.Rules) != len(promoted.Config.ShippedRules)+1 {
		t.Fatalf("promoted config = %+v", promoted.Config)
	}
	got := automodeShow(t, d)
	if len(got.Config.Rules) != len(got.Config.ShippedRules)+1 || len(got.Proposals) != 0 {
		t.Fatalf("show after promote: rules=%+v pending=%d", got.Config.Rules, len(got.Proposals))
	}
	if line := autoModeTestRuleLine(got.Config.Rules[len(got.Config.Rules)-1]); line != "git push" {
		t.Errorf("promoted rule = %q", line)
	}
}

func TestAutoModeDiscardFromTheAppClosesWithoutApplying(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	resp := automodePropose(t, d, automode.KindHost, "", `{"host":"pypi.org","decision":"allow"}`)
	if !resp.Ok {
		t.Fatalf("propose: %v", protocol.Deref(resp.Error))
	}
	client := busTestClient()
	d.handleAutoModeDiscard(client, &protocol.AutoModeDiscardMessage{
		ID: resp.AutomodeProposeResult.Proposal.ID, RequestID: "r1",
	})
	var discarded protocol.AutoModeDiscardResultMessage
	nextBusMessage(t, client, &discarded)
	if !discarded.Success {
		t.Fatalf("discard failed: %q", protocol.Deref(discarded.Error))
	}
	got := automodeShow(t, d)
	if len(got.Config.Network.AllowedDomains) != 0 {
		t.Errorf("allowed domains = %v after a discard", got.Config.Network.AllowedDomains)
	}
	if len(got.Proposals) != 0 {
		t.Errorf("discarded proposal is still pending")
	}
}

func TestAutoModePromoteRefusesAnUnknownProposal(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()
	d.handleAutoModePromote(client, &protocol.AutoModePromoteMessage{ID: 404, RequestID: "r1"})
	var result protocol.AutoModePromoteResultMessage
	nextBusMessage(t, client, &result)
	if result.Success {
		t.Fatal("promoting a proposal that does not exist succeeded")
	}
	if !strings.Contains(protocol.Deref(result.Error), "404") {
		t.Fatalf("refusal does not name the ask: %q", protocol.Deref(result.Error))
	}
}

func TestPromotionIsNotReachableOverTheUnixSocket(t *testing.T) {
	d := newDaemonForTest(t)
	for _, cmd := range []string{protocol.CmdAutoModePromote, protocol.CmdAutoModeDiscard} {
		client, server := net.Pipe()
		go func() {
			d.handleConnection(server)
		}()
		payload := `{"cmd":"` + cmd + `","id":1,"request_id":"r1"}`
		if _, err := client.Write([]byte(payload)); err != nil {
			t.Fatalf("write %s: %v", cmd, err)
		}
		var resp protocol.Response
		if err := json.NewDecoder(client).Decode(&resp); err != nil {
			t.Fatalf("decode %s response: %v", cmd, err)
		}
		client.Close()
		if resp.Ok {
			t.Fatalf("%s was answered over the unix socket", cmd)
		}
		if got := protocol.Deref(resp.Error); !strings.Contains(got, "unknown command") {
			t.Fatalf("%s was refused for the wrong reason: %q", cmd, got)
		}
	}
}

func TestSettingsSnapshotCarriesTheAutoModeDefault(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	if got := d.settingsWithAgentAvailability()[SettingAutoModeEnabledDefault]; got != "true" {
		t.Errorf("%s = %q on a fresh database, want the shipped default", SettingAutoModeEnabledDefault, got)
	}
	if _, err := d.store.SetAutoModeEnabledDefault(false, time.Now().UTC()); err != nil {
		t.Fatalf("set default: %v", err)
	}
	if got := d.settingsWithAgentAvailability()[SettingAutoModeEnabledDefault]; got != "false" {
		t.Errorf("%s = %q after turning it off", SettingAutoModeEnabledDefault, got)
	}
	if err := d.validateSetting(SettingAutoModeEnabledDefault, "true"); err == nil {
		t.Error("set_setting accepted the daemon-computed auto mode default")
	}
}

func TestAutoModeDenialFromADriverBecomesARowANotificationAndAFact(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"state_reporting": true, "auto_mode": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-denial", Label: "envelope work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-denial", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	sendPluginMethod(t, client, 3, "session.report_automode_denial", pluginReportAutoModeDenialParams{
		SessionID: "pi-denial",
		RunID:     "run-1",
		Tool:      "bash",
		Action:    "bash: curl https://example.com",
		Reason:    "the user never asked to reach that host",
		Rule:      "classifier-2a",
		At:        "2026-08-17T10:00:00Z",
	})

	denials, err := d.store.ListAutoModeDenials(10)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(denials) != 1 {
		t.Fatalf("denials = %d, want the one that was reported", len(denials))
	}
	got := denials[0]
	if got.SessionID != "pi-denial" || got.Tool != "bash" || got.Rule != "classifier-2a" {
		t.Errorf("denial row = %+v", got)
	}
	if got.Signature != "bash: curl https://example.com" {
		t.Errorf("signature = %q, want the blocked call", got.Signature)
	}
	if !got.CreatedAt.Equal(time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("created_at = %s, want the time the session refused it", got.CreatedAt)
	}

	notes, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notifications = %d, want the denial's", len(notes))
	}
	note := notes[0]
	if note.Kind != notificationKindAutoModeDenied || note.SourceID != "pi-denial" {
		t.Errorf("notification = %+v", note)
	}
	if !strings.Contains(note.Title, "envelope work") {
		t.Errorf("title does not name the session: %q", note.Title)
	}
	if !strings.Contains(note.Body, "curl https://example.com") {
		t.Errorf("body does not say what was blocked: %q", note.Body)
	}
	if !strings.Contains(note.Detail, "never asked to reach that host") ||
		!strings.Contains(note.Detail, "classifier-2a") {
		t.Errorf("detail does not carry the reason and who decided: %q", note.Detail)
	}

	published := docFacts(t, d, FactAutoModeDenied)
	if len(published) != 1 || published[0].Subject != "pi-denial" {
		t.Fatalf("automode.denied facts = %+v, want one naming the session", published)
	}
}

func TestAutoModeDenialFromAnUnownedRunIsRefused(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"auto_mode": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-denial", Label: "pi", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-denial", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	response := sendPluginMethodResponse(t, client, 3, "session.report_automode_denial",
		pluginReportAutoModeDenialParams{SessionID: "pi-denial", RunID: "run-other", Action: "bash: git push --force"})
	if response.Error == nil {
		t.Fatal("a denial for a run the plugin does not own was accepted")
	}

	response = sendPluginMethodResponse(t, client, 4, "session.report_automode_denial",
		pluginReportAutoModeDenialParams{SessionID: "pi-denial", RunID: "run-1", Action: "   "})
	if response.Error == nil {
		t.Fatal("a denial with no action named was accepted")
	}

	denials, err := d.store.ListAutoModeDenials(10)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(denials) != 0 {
		t.Fatalf("refused denials reached the log: %+v", denials)
	}
}

// A pi session answers an approval once and reports what it was told; the answer is
// already the user's, so it is recorded and applied in one move rather than queued.
func TestReportedAmendmentsFromPiLandInTheConfig(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"auto_mode": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-amend", Label: "sunny otter", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-amend", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	sendPluginMethod(t, client, 3, "session.report_execpolicy_amendment",
		pluginReportExecPolicyAmendmentParams{
			SessionID: "pi-amend", RunID: "run-1",
			Pattern: []string{"cargo", "build"}, Decision: automode.DecisionAllow,
		})
	sendPluginMethod(t, client, 4, "session.report_network_amendment",
		pluginReportNetworkAmendmentParams{
			SessionID: "pi-amend", RunID: "run-1", Host: "crates.io", Decision: automode.HostAllow,
		})

	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	stored := automode.StripShippedRules(cfg.Rules)
	if len(stored) != 1 || stored[0].Describe() != "cargo build" {
		t.Fatalf("rules = %v, want the reported one in force", cfg.Rules)
	}
	if got := cfg.Network.AllowedDomains; len(got) != 1 || got[0] != "crates.io" {
		t.Fatalf("allowed domains = %v, want the reported host", got)
	}

	pending, err := d.store.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("a reported amendment left something for the user to promote: %+v", pending)
	}
	promoted, err := d.store.ListAutoModeProposals(automode.StatePromoted)
	if err != nil {
		t.Fatalf("list promoted: %v", err)
	}
	if len(promoted) != 2 {
		t.Fatalf("promoted = %d, want a row per report so the user can read them back", len(promoted))
	}
	if !strings.Contains(promoted[0].ProposedBy, "sunny otter") {
		t.Errorf("proposed_by = %q, want the session that answered", promoted[0].ProposedBy)
	}
}

func TestReportedAmendmentsFromAnUnownedRunAreRefused(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"auto_mode": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-amend", Label: "pi", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-amend", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	response := sendPluginMethodResponse(t, client, 3, "session.report_execpolicy_amendment",
		pluginReportExecPolicyAmendmentParams{
			SessionID: "pi-amend", RunID: "run-other",
			Pattern: []string{"cargo", "build"}, Decision: automode.DecisionAllow,
		})
	if response.Error == nil {
		t.Fatal("an amendment for a run the plugin does not own was accepted")
	}
	response = sendPluginMethodResponse(t, client, 4, "session.report_network_amendment",
		pluginReportNetworkAmendmentParams{
			SessionID: "pi-amend", RunID: "run-1", Host: "crates.io", Decision: "prompt",
		})
	if response.Error == nil {
		t.Fatal("a host decision the network has no room for was accepted")
	}

	cfg, err := d.store.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(automode.StripShippedRules(cfg.Rules)) != 0 || len(cfg.Network.AllowedDomains) != 0 {
		t.Fatalf("a refused amendment reached the config: %+v", cfg)
	}
}

// The plugin swallows the JSON-RPC error it gets back, so the daemon log is the one
// place a human can read why an answered approval card never became a rule.
func TestARefusedPluginRequestNamesItselfInTheDaemonLog(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	logPath := attachPluginTestLogger(t, d)
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"auto_mode": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-amend", Label: "pi", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-amend", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	response := sendPluginMethodResponse(t, client, 3, "session.report_network_amendment",
		pluginReportNetworkAmendmentParams{
			SessionID: "pi-amend", RunID: "run-other", Host: "crates.io", Decision: automode.HostAllow,
		})
	if response.Error == nil {
		t.Fatal("an amendment for a run the plugin does not own was accepted")
	}

	assertLogContains(t, logPath,
		"plugin request session.report_network_amendment from plugin pi-plugin failed:",
		`does not own active run "run-other"`)
}

func autoModeTestRuleLine(rule protocol.AutoModeRuleInfo) string {
	tokens := make([]string, 0, len(rule.Pattern))
	for _, alternatives := range rule.Pattern {
		tokens = append(tokens, strings.Join(alternatives, "|"))
	}
	return strings.Join(tokens, " ")
}

func autoModeEdit(t *testing.T, d *Daemon, edit func(*wsClient)) protocol.AutoModeConfigResultMessage {
	t.Helper()
	client := busTestClient()
	edit(client)
	var result protocol.AutoModeConfigResultMessage
	nextBusMessage(t, client, &result)
	return result
}

func TestAutoModeEnvSlotFromTheAppAnswersWithTheStoredConfig(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()
	d.handleAutoModeEnvSlotWS(client, &protocol.AutoModeEnvSlotMessage{
		Cmd:       protocol.CmdAutoModeEnvSlot,
		Slot:      "registry",
		Values:    []string{"registry.acme.corp"},
		RequestID: protocol.Ptr("r1"),
	})
	var result protocol.AutoModeEnvSetResultMessage
	nextBusMessage(t, client, &result)
	if !result.Success || result.Config == nil {
		t.Fatalf("env slot failed: %q", protocol.Deref(result.Error))
	}
	if got := envSlotValues(result.Config.Environment, "registry"); len(got) != 1 || got[0] != "registry.acme.corp" {
		t.Fatalf("registry = %v", got)
	}
}

func TestAutoModeRuleEditFromTheAppRoundTrips(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))

	added := autoModeEdit(t, d, func(c *wsClient) {
		d.handleAutoModeRuleAdd(c, &protocol.AutoModeRuleAddMessage{
			Cmd: protocol.CmdAutoModeRuleAdd, Pattern: []string{"git", "status"},
			Decision: protocol.Ptr(automode.DecisionAllow), RequestID: "r1",
		})
	})
	if !added.Success || added.Config == nil {
		t.Fatalf("add failed: %q", protocol.Deref(added.Error))
	}
	stored := added.Config.Rules[len(added.Config.Rules)-1]
	if autoModeTestRuleLine(stored) != "git status" || stored.Decision != automode.DecisionAllow {
		t.Fatalf("rules after add = %+v", added.Config.Rules)
	}
	if got := automodeShow(t, d).Config.Rules; len(got) != len(added.Config.Rules) {
		t.Fatalf("show after a direct add: %+v", got)
	}

	removed := autoModeEdit(t, d, func(c *wsClient) {
		d.handleAutoModeRuleRemoveWS(c, &protocol.AutoModeRuleRemoveMessage{
			Cmd: protocol.CmdAutoModeRuleRemove, Pattern: [][]string{{"git"}, {"status"}},
			RequestID: protocol.Ptr("r2"),
		})
	})
	if !removed.Success || removed.Config == nil {
		t.Fatalf("remove failed: %q", protocol.Deref(removed.Error))
	}
	if len(removed.Config.Rules) != len(removed.Config.ShippedRules) {
		t.Fatalf("rules after remove = %+v", removed.Config.Rules)
	}
}

func TestAutoModeHostEditFromTheAppRoundTrips(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))

	added := autoModeEdit(t, d, func(c *wsClient) {
		d.handleAutoModeHostAdd(c, &protocol.AutoModeHostAddMessage{
			Cmd: protocol.CmdAutoModeHostAdd, Host: "crates.io",
			Decision: automode.HostAllow, RequestID: "r1",
		})
	})
	if !added.Success || added.Config == nil {
		t.Fatalf("add failed: %q", protocol.Deref(added.Error))
	}
	if got := added.Config.Network.AllowedDomains; len(got) != 1 || got[0] != "crates.io" {
		t.Fatalf("allowed domains = %v", got)
	}

	removed := autoModeEdit(t, d, func(c *wsClient) {
		d.handleAutoModeHostRemoveWS(c, &protocol.AutoModeHostRemoveMessage{
			Cmd: protocol.CmdAutoModeHostRemove, Host: "crates.io",
			Decision: automode.HostAllow, RequestID: protocol.Ptr("r2"),
		})
	})
	if !removed.Success || len(removed.Config.Network.AllowedDomains) != 0 {
		t.Fatalf("remove failed: %q %+v", protocol.Deref(removed.Error), removed.Config)
	}
}

func TestAutoModePolicySetFromTheAppHoldsWhatItIsNotTold(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))

	set := autoModeEdit(t, d, func(c *wsClient) {
		d.handleAutoModePolicySetWS(c, &protocol.AutoModePolicySetMessage{
			Cmd:            protocol.CmdAutoModePolicySet,
			ApprovalPolicy: protocol.Ptr(automode.PolicyNever),
			RequestID:      protocol.Ptr("r1"),
		})
	})
	if !set.Success || set.Config == nil {
		t.Fatalf("policy set failed: %q", protocol.Deref(set.Error))
	}
	if set.Config.ApprovalPolicy != automode.PolicyNever ||
		set.Config.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Fatalf("policy = %q/%q, want only the approval policy moved",
			set.Config.ApprovalPolicy, set.Config.SandboxMode)
	}

	refused := autoModeEdit(t, d, func(c *wsClient) {
		d.handleAutoModePolicySetWS(c, &protocol.AutoModePolicySetMessage{
			Cmd: protocol.CmdAutoModePolicySet, SandboxMode: protocol.Ptr("open"), RequestID: protocol.Ptr("r2"),
		})
	})
	if refused.Success {
		t.Fatal("an unknown sandbox mode was accepted")
	}
	if got := automodeShow(t, d).Config; got.ApprovalPolicy != automode.PolicyNever ||
		got.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Fatalf("a refused edit moved the config: %q/%q", got.ApprovalPolicy, got.SandboxMode)
	}
}

func TestAutoModeStateNamesWhatShipped(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()
	d.handleAutoModeGet(client, &protocol.AutoModeGetMessage{Cmd: protocol.CmdAutoModeGet, RequestID: "r1"})
	var read protocol.AutoModeStateResultMessage
	nextBusMessage(t, client, &read)
	if !read.Success {
		t.Fatalf("automode_get failed: %q", protocol.Deref(read.Error))
	}
	if len(read.Config.ShippedRules) != len(automode.ShippedRules()) {
		t.Fatalf("shipped_rules = %+v, want the built-in set", read.Config.ShippedRules)
	}
	shippedDomains := automode.ShippedDeniedDomains(config.WSPort())
	if len(read.Config.ShippedDeniedDomains) != len(shippedDomains) {
		t.Fatalf("shipped_denied_domains = %v, want %v", read.Config.ShippedDeniedDomains, shippedDomains)
	}
	if len(read.Config.Network.DeniedDomains) != len(shippedDomains) {
		t.Errorf("denied domains = %v, want the shipped ones in force", read.Config.Network.DeniedDomains)
	}

	refused := autoModeEdit(t, d, func(c *wsClient) {
		d.handleAutoModeRuleRemoveWS(c, &protocol.AutoModeRuleRemoveMessage{
			Cmd:       protocol.CmdAutoModeRuleRemove,
			Pattern:   patternTokenStrings(automode.ShippedRules()[0]),
			RequestID: protocol.Ptr("r2"),
		})
	})
	if refused.Success {
		t.Fatal("a shipped rule was removed from the app")
	}
	if !strings.Contains(protocol.Deref(refused.Error), "built-in") {
		t.Fatalf("shipped removal refusal = %q", protocol.Deref(refused.Error))
	}
}

func patternTokenStrings(rule automode.Rule) [][]string {
	tokens := make([][]string, 0, len(rule.Pattern))
	for _, token := range rule.Pattern {
		tokens = append(tokens, token.Alternatives)
	}
	return tokens
}

// Legacy patterns only ever arrive from the migration, so what the app needs from
// the daemon is the route: the command answers a config result, not "unknown".
func TestDismissingALegacyPatternIsRoutedFromTheApp(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	client := busTestClient()
	client.setIdentity("daemon-test", protocol.ProtocolVersion,
		[]string{protocol.CapabilityWorkspaceSessions})
	d.handleClientMessage(client, []byte(
		`{"cmd":"automode_legacy_dismiss","pattern":"*curl*","request_id":"r1"}`))

	var result protocol.AutoModeConfigResultMessage
	nextBusMessage(t, client, &result)
	if result.Event != protocol.EventAutoModeConfigResult {
		t.Fatalf("answer = %+v (%s), want an automode config result", result, protocol.Deref(result.Error))
	}
	if result.Success {
		t.Fatal("dismissing a pattern that is not on the list was accepted")
	}
	if got := protocol.Deref(result.Error); !strings.Contains(got, "*curl*") {
		t.Fatalf("refusal = %q, want it to name the pattern", got)
	}
}

func unixAutoModeCall(t *testing.T, d *Daemon, payload string) protocol.Response {
	t.Helper()
	client, server := net.Pipe()
	go func() { d.handleConnection(server) }()
	if _, err := client.Write([]byte(payload)); err != nil {
		t.Fatalf("write %s: %v", payload, err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response to %s: %v", payload, err)
	}
	client.Close()
	return resp
}

func TestNoAutoModeWriteIsReachableOverTheUnixSocket(t *testing.T) {
	d := newDaemonForTest(t)
	if _, err := d.store.AddAutoModeRule(automode.Rule{
		Pattern: automode.Tokens("git", "status"), Decision: automode.DecisionAllow,
	}, time.Now()); err != nil {
		t.Fatalf("seed a rule: %v", err)
	}
	for _, payload := range []string{
		`{"cmd":"automode_rule_add","pattern":["git","push"],"request_id":"r1"}`,
		`{"cmd":"automode_rule_remove","pattern":[["git"],["status"]],"request_id":"r1"}`,
		`{"cmd":"automode_host_add","host":"crates.io","decision":"allow","request_id":"r1"}`,
		`{"cmd":"automode_host_remove","host":"crates.io","decision":"allow","request_id":"r1"}`,
		`{"cmd":"automode_policy_set","approval_policy":"never","request_id":"r1"}`,
		`{"cmd":"automode_legacy_dismiss","pattern":"*curl*","request_id":"r1"}`,
		`{"cmd":"automode_model_set","models":["a/one"],"request_id":"r1"}`,
	} {
		resp := unixAutoModeCall(t, d, payload)
		if resp.Ok {
			t.Fatalf("%s was answered over the unix socket", payload)
		}
		if got := protocol.Deref(resp.Error); !strings.Contains(got, "unknown command") {
			t.Fatalf("%s was refused for the wrong reason: %q", payload, got)
		}
	}
	cfg := automodeShow(t, d).Config
	if len(cfg.Rules) != len(cfg.ShippedRules)+1 {
		t.Fatalf("rules = %+v after the socket edit attempts", cfg.Rules)
	}
	if cfg.ApprovalPolicy != automode.PolicyOnRequest {
		t.Fatalf("approval policy = %q after the socket edit attempts", cfg.ApprovalPolicy)
	}
}

// The CLI's reversal is a proposal like every other amendment: an agent reaching the
// socket can ask to drop a rule, and nothing moves until a human promotes it.
func TestTakingARuleAwayOverTheUnixSocketOnlyProposes(t *testing.T) {
	d := newDaemonForTest(t)
	if _, err := d.store.AddAutoModeRule(automode.Rule{
		Pattern: automode.Tokens("git", "status"), Decision: automode.DecisionAllow,
	}, time.Now()); err != nil {
		t.Fatalf("seed a rule: %v", err)
	}
	before := automodeShow(t, d).Config

	for _, tc := range []struct {
		kind, value, summary string
	}{
		{automode.KindRuleRemove, `{"pattern":["git","status"]}`, "remove rule git status"},
		{automode.KindHostRemove, `{"host":"crates.io","decision":"allow"}`, "remove allow crates.io"},
		{automode.KindPolicy, `{"approval_policy":"never"}`, "approval never"},
	} {
		resp := automodePropose(t, d, tc.kind, "", tc.value)
		if !resp.Ok {
			t.Fatalf("%s proposal: %q", tc.kind, protocol.Deref(resp.Error))
		}
		if got := resp.AutomodeProposeResult.Proposal.Summary; got != tc.summary {
			t.Errorf("%s summary = %q, want %q", tc.kind, got, tc.summary)
		}
	}

	after := automodeShow(t, d)
	if len(after.Proposals) != 3 {
		t.Fatalf("pending = %d, want the three proposals", len(after.Proposals))
	}
	if len(after.Config.Rules) != len(before.Rules) ||
		after.Config.ApprovalPolicy != before.ApprovalPolicy {
		t.Fatalf("a proposal changed the config: %+v", after.Config)
	}
}

func promoteAutoMode(t *testing.T, d *Daemon, id int) {
	t.Helper()
	client := busTestClient()
	d.handleAutoModePromote(client, &protocol.AutoModePromoteMessage{ID: id, RequestID: "r1"})
	var promoted protocol.AutoModePromoteResultMessage
	nextBusMessage(t, client, &promoted)
	if !promoted.Success {
		t.Fatalf("promote %d: %q", id, protocol.Deref(promoted.Error))
	}
}

// The app is where a proposal becomes policy, so promoting each kind is what proves
// the reversal the CLI proposed can actually be applied.
func TestPromotingEachAmendmentKindFromTheApp(t *testing.T) {
	d := newDaemonForTest(t)
	if _, err := d.store.AddAutoModeRule(automode.Rule{
		Pattern: automode.Tokens("git", "status"), Decision: automode.DecisionAllow,
	}, time.Now()); err != nil {
		t.Fatalf("seed a rule: %v", err)
	}
	if _, err := d.store.AddAutoModeHost(automode.HostAmendment{
		Host: "crates.io", Decision: automode.HostAllow,
	}, time.Now()); err != nil {
		t.Fatalf("seed a host: %v", err)
	}
	for _, tc := range []struct{ kind, value string }{
		{automode.KindRuleRemove, `{"pattern":["git","status"]}`},
		{automode.KindHostRemove, `{"host":"crates.io","decision":"allow"}`},
		{automode.KindPolicy, `{"approval_policy":"never","allow_local_binding":true}`},
	} {
		resp := automodePropose(t, d, tc.kind, "", tc.value)
		if !resp.Ok {
			t.Fatalf("%s proposal: %q", tc.kind, protocol.Deref(resp.Error))
		}
		promoteAutoMode(t, d, int(resp.AutomodeProposeResult.Proposal.ID))
	}

	cfg := automodeShow(t, d).Config
	if len(cfg.Rules) != len(cfg.ShippedRules) {
		t.Errorf("rules = %+v, want the promoted removal applied", cfg.Rules)
	}
	if len(cfg.Network.AllowedDomains) != 0 {
		t.Errorf("allowed = %v, want the promoted removal applied", cfg.Network.AllowedDomains)
	}
	if cfg.ApprovalPolicy != automode.PolicyNever || !cfg.Network.AllowLocalBinding {
		t.Errorf("policy = %q, local binding = %t", cfg.ApprovalPolicy, cfg.Network.AllowLocalBinding)
	}
}

func TestPolicySetFromTheAppCarriesLocalBinding(t *testing.T) {
	d := newDaemonForTest(t)
	result := autoModeEdit(t, d, func(c *wsClient) {
		d.handleAutoModePolicySetWS(c, &protocol.AutoModePolicySetMessage{
			Cmd:               protocol.CmdAutoModePolicySet,
			AllowLocalBinding: protocol.Ptr(true),
			RequestID:         protocol.Ptr("r1"),
		})
	})
	if !result.Success {
		t.Fatalf("policy set: %q", protocol.Deref(result.Error))
	}
	if !result.Config.Network.AllowLocalBinding {
		t.Fatalf("network = %+v, want local binding on", result.Config.Network)
	}
	if result.Config.ApprovalPolicy != automode.PolicyOnRequest {
		t.Errorf("approval policy = %q, want the setting it was not told about held",
			result.Config.ApprovalPolicy)
	}
}
