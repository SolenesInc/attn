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
	if len(result.Config.Models) != 0 {
		t.Errorf("models = %v on a fresh profile, want none until the user names one", result.Config.Models)
	}
	if result.Config.Allow == nil || result.Config.HardDeny == nil {
		t.Fatalf("a config list came back nil: %+v", result.Config)
	}
	if result.Config.Environment.Slots == nil || result.Config.Environment.Notes == nil {
		t.Fatalf("the environment came back nil: %+v", result.Config.Environment)
	}
	if len(result.Proposals) != 0 {
		t.Fatalf("a fresh profile has %d proposals", len(result.Proposals))
	}
}

func TestAutoModeAllowOnlyProposes(t *testing.T) {
	d := newDaemonForTest(t)
	resp := automodePropose(t, d, automode.KindAllow, "", "git push origin*")
	if !resp.Ok {
		t.Fatalf("propose: %v", protocol.Deref(resp.Error))
	}
	if resp.AutomodeProposeResult.Proposal.State != automode.StatePending {
		t.Errorf("proposal state = %q", resp.AutomodeProposeResult.Proposal.State)
	}
	after := automodeShow(t, d)
	if len(after.Config.Allow) != 0 {
		t.Fatalf("effective allow list changed to %v", after.Config.Allow)
	}
	if len(after.Proposals) != 1 {
		t.Fatalf("proposals = %d, want the one just recorded", len(after.Proposals))
	}
}

func TestAutoModeProposeRefusesABroadAllowByName(t *testing.T) {
	d := newDaemonForTest(t)
	resp := automodePropose(t, d, automode.KindAllow, "", "*")
	if resp.Ok {
		t.Fatal("a broad allow proposal was accepted")
	}
	if !strings.Contains(protocol.Deref(resp.Error), "broad allow pattern") {
		t.Fatalf("refusal does not name the limit: %q", protocol.Deref(resp.Error))
	}
	if len(automodeShow(t, d).Proposals) != 0 {
		t.Fatal("the refused proposal reached the review list")
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
	resp := automodePropose(t, d, automode.KindAllow, "", "git push origin*")
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
	if promoted.Config == nil || len(promoted.Config.Allow) != 1 {
		t.Fatalf("promoted config = %+v", promoted.Config)
	}
	if got := automodeShow(t, d); len(got.Config.Allow) != 1 || len(got.Proposals) != 0 {
		t.Fatalf("show after promote: allow=%v pending=%d", got.Config.Allow, len(got.Proposals))
	}
}

func TestAutoModeDiscardFromTheAppClosesWithoutApplying(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	resp := automodePropose(t, d, automode.KindModel, automode.TargetModels, "opencode-go/other-model")
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
	if len(got.Config.Models) != 0 {
		t.Errorf("models = %v after a discard", got.Config.Models)
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

func automodePatternAdd(t *testing.T, d *Daemon, list, pattern string) protocol.AutoModePatternResultMessage {
	t.Helper()
	client := busTestClient()
	d.handleAutoModePatternAdd(client, &protocol.AutoModePatternAddMessage{
		Cmd: protocol.CmdAutoModePatternAdd, List: list, Pattern: pattern, RequestID: "r1",
	})
	var result protocol.AutoModePatternResultMessage
	nextBusMessage(t, client, &result)
	return result
}

func automodePatternRemove(t *testing.T, d *Daemon, list, pattern string) protocol.AutoModePatternResultMessage {
	t.Helper()
	client := busTestClient()
	d.handleAutoModePatternRemove(client, &protocol.AutoModePatternRemoveMessage{
		Cmd: protocol.CmdAutoModePatternRemove, List: list, Pattern: pattern, RequestID: "r1",
	})
	var result protocol.AutoModePatternResultMessage
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

func TestAutoModePatternEditFromTheAppRoundTrips(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))

	added := automodePatternAdd(t, d, automode.ListAllow, "git status*")
	if !added.Success || added.Config == nil {
		t.Fatalf("add failed: %q", protocol.Deref(added.Error))
	}
	if len(added.Config.Allow) != 1 || added.Config.Allow[0] != "git status*" {
		t.Fatalf("allow after add = %v", added.Config.Allow)
	}
	if got := automodeShow(t, d); len(got.Config.Allow) != 1 || got.Config.Allow[0] != "git status*" {
		t.Fatalf("show after a direct add: %v", got.Config.Allow)
	}

	removed := automodePatternRemove(t, d, automode.ListAllow, "git status*")
	if !removed.Success || removed.Config == nil {
		t.Fatalf("remove failed: %q", protocol.Deref(removed.Error))
	}
	if len(removed.Config.Allow) != 0 {
		t.Fatalf("allow after remove = %v", removed.Config.Allow)
	}
}

func TestAutoModeStateNamesTheShippedHardDenies(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	added := automodePatternAdd(t, d, automode.ListHardDeny, "*terraform apply*")
	if !added.Success {
		t.Fatalf("add hard deny: %q", protocol.Deref(added.Error))
	}
	shipped := automode.ShippedHardDeny(config.WSPort())
	if len(added.Config.ShippedHardDeny) != len(shipped) {
		t.Fatalf("shipped_hard_deny = %v, want %v", added.Config.ShippedHardDeny, shipped)
	}
	if len(added.Config.HardDeny) != len(shipped)+1 {
		t.Fatalf("hard_deny = %v, want the shipped list plus one", added.Config.HardDeny)
	}

	client := busTestClient()
	d.handleAutoModeGet(client, &protocol.AutoModeGetMessage{
		Cmd: protocol.CmdAutoModeGet, RequestID: "r2",
	})
	var read protocol.AutoModeStateResultMessage
	nextBusMessage(t, client, &read)
	if !read.Success || len(read.Config.ShippedHardDeny) != len(shipped) {
		t.Fatalf("automode_get shipped_hard_deny = %v", read.Config.ShippedHardDeny)
	}
}

func TestAutoModePatternEditRefusalsReachTheApp(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))

	broad := automodePatternAdd(t, d, automode.ListAllow, "*")
	if broad.Success {
		t.Fatal("a broad allow was accepted from the app")
	}
	if !strings.Contains(protocol.Deref(broad.Error), "must name something") {
		t.Fatalf("broad allow refusal = %q", protocol.Deref(broad.Error))
	}

	shipped := automode.ShippedHardDeny(config.WSPort())[0]
	refused := automodePatternRemove(t, d, automode.ListHardDeny, shipped)
	if refused.Success {
		t.Fatal("a shipped hard deny was removed from the app")
	}
	if !strings.Contains(protocol.Deref(refused.Error), "built-in") {
		t.Fatalf("shipped removal refusal = %q", protocol.Deref(refused.Error))
	}
	found := false
	for _, pattern := range automodeShow(t, d).Config.HardDeny {
		if pattern == shipped {
			found = true
		}
	}
	if !found {
		t.Fatalf("%q left the hard deny list after a refused removal", shipped)
	}
}

func TestPatternEditingIsNotReachableOverTheUnixSocket(t *testing.T) {
	d := newDaemonForTest(t)
	for _, cmd := range []string{protocol.CmdAutoModePatternAdd, protocol.CmdAutoModePatternRemove} {
		client, server := net.Pipe()
		go func() {
			d.handleConnection(server)
		}()
		payload := `{"cmd":"` + cmd + `","list":"allow","pattern":"git status*","request_id":"r1"}`
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
	if got := automodeShow(t, d); len(got.Config.Allow) != 0 {
		t.Fatalf("allow = %v after a socket edit attempt", got.Config.Allow)
	}
}
