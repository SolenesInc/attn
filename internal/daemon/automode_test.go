package daemon

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
)

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
