package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func ledgerLine(session, action, at string) string {
	return fmt.Sprintf(
		`{"session_id":%q,"tool_call_id":"c1","tool":"bash","action":%q,"reason":"outside the envelope","rule":"classifier-2a","at":%q}`,
		session, action, at)
}

func writeSessionLedger(t *testing.T, lines ...string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), automode.DenialLedgerFileName)
	contents := ""
	for _, line := range lines {
		contents += line + "\n"
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}
	t.Setenv(automode.DenialLedgerEnvVar, path)
}

func listDenials(t *testing.T, d *Daemon) *protocol.AutoModeDenialsResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeDenials(c, &protocol.AutoModeDenialsMessage{Cmd: protocol.CmdAutoModeDenials})
	})
	if !resp.Ok {
		t.Fatalf("denials: %v", protocol.Deref(resp.Error))
	}
	return resp.AutomodeDenialsResult
}

func TestDenialsFeedRecoversWhatTheRelayNeverDelivered(t *testing.T) {
	d := newDaemonForTest(t)
	writeSessionLedger(t,
		ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"),
		ledgerLine("pi-1", "write /etc/hosts", "2026-08-18T10:00:01.000Z"),
	)

	result := listDenials(t, d)
	if len(result.Denials) != 2 {
		t.Fatalf("denials = %+v, want both recovered from the ledger", result.Denials)
	}
	if result.Denials[0].Signature != "write /etc/hosts" {
		t.Errorf("newest denial = %q", result.Denials[0].Signature)
	}
	if result.Denials[0].Rule != "classifier-2a" || result.Denials[0].SessionID != "pi-1" {
		t.Errorf("a recovered denial lost its attribution: %+v", result.Denials[0])
	}
}

func TestReconcileDoesNotDoubleWhatTheRelayDelivered(t *testing.T) {
	d := newDaemonForTest(t)
	at := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	if _, _, err := d.store.RecordAutoModeDenial(store.AutoModeDenial{
		SessionID: "pi-1", Tool: "bash", Signature: "bash: curl https://one.example",
		Reason: "outside the envelope", Rule: "classifier-2a",
	}, at); err != nil {
		t.Fatalf("record denial: %v", err)
	}
	writeSessionLedger(t, ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"))

	for round := 1; round <= 3; round++ {
		if listed := listDenials(t, d).Denials; len(listed) != 1 {
			t.Fatalf("round %d listed %d denials, want the one that happened", round, len(listed))
		}
	}
}

func TestRelayDoesNotDoubleWhatTheLedgerRecovered(t *testing.T) {
	d := newDaemonForTest(t)
	const stamp = "2026-08-18T10:00:00.123Z"
	const action = "bash: curl https://one.example"
	writeSessionLedger(t, ledgerLine("pi-1", action, stamp))
	before := listDenials(t, d).Denials
	if len(before) != 1 {
		t.Fatalf("recovered denials = %+v, want one", before)
	}
	if err := d.recordAutoModeDenial(pluginReportAutoModeDenialParams{
		SessionID: "pi-1", Tool: "bash", Action: action,
		Reason: "outside the envelope", Rule: "classifier-2a", At: stamp,
	}); err != nil {
		t.Fatalf("late relay: %v", err)
	}
	after := listDenials(t, d).Denials
	if len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("late relay changed the recovered denial: before=%+v after=%+v", before, after)
	}
	notes, err := d.store.ListNotifications()
	if err != nil || len(notes) != 1 || notes[0].Kind != notificationKindAutoModeDenied {
		t.Fatalf("late relay notifications = %+v, error = %v, want one denial notice", notes, err)
	}
}

func TestReconcileImportsARecordOnlyOnce(t *testing.T) {
	d := newDaemonForTest(t)
	writeSessionLedger(t, ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"))

	first := d.reconcileAutoModeDenialLedger()
	if first.Imported != 1 {
		t.Fatalf("the first pass imported %d records, want the one the relay lost", first.Imported)
	}
	if cursor := d.store.GetSetting(settingAutoModeDenialCursor); !strings.HasPrefix(cursor, "2026-08-18T10:00:00") {
		t.Fatalf("cursor = %q, want the record it just imported", cursor)
	}
	if again := d.reconcileAutoModeDenialLedger(); again.Imported != 0 {
		t.Errorf("a second pass imported %d records", again.Imported)
	}

}

func TestReconcileLeavesARecordTheLogHasSinceTrimmed(t *testing.T) {
	d := newDaemonForTest(t)
	writeSessionLedger(t, ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"))
	d.store.SetSetting(settingAutoModeDenialCursor, "2026-08-18T10:00:00.000Z")

	result := listDenials(t, d)
	if len(result.Denials) != 0 {
		t.Fatalf("a record already behind the cursor came back: %+v", result.Denials)
	}
}

func TestDenialsFeedNamesWhatTheLedgerLost(t *testing.T) {
	d := newDaemonForTest(t)
	writeSessionLedger(t,
		`{"type":"rotated","dropped":3,"at":"2026-08-18T09:00:00.000Z"}`,
		"{ not json",
		ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"),
	)

	result := listDenials(t, d)
	note := protocol.Deref(result.LedgerNote)
	if !strings.Contains(note, "3 older denials") {
		t.Errorf("note does not name the dropped records: %q", note)
	}
	if !strings.Contains(note, "1 ledger line could not be read") {
		t.Errorf("note does not name the unreadable line: %q", note)
	}
	if len(result.Denials) != 1 {
		t.Errorf("the readable record was lost with the unreadable one: %+v", result.Denials)
	}
}

func TestDenialsFeedIsSilentWithoutALedger(t *testing.T) {
	d := newDaemonForTest(t)
	t.Setenv(automode.DenialLedgerEnvVar, filepath.Join(t.TempDir(), automode.DenialLedgerFileName))

	result := listDenials(t, d)
	if len(result.Denials) != 0 {
		t.Fatalf("a machine that denied nothing has denials: %+v", result.Denials)
	}
	if note := protocol.Deref(result.LedgerNote); note != "" {
		t.Errorf("an absent ledger reported %q", note)
	}
}
