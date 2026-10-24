package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnalyticsIsOffByDefaultAndTheFirstEnableCreatesTheInstallSecretOnce(t *testing.T) {
	w := newWorld(t)
	secretPath := filepath.Join(w.Dir, "analytics-secret")

	app := w.App()
	settings := testworld.Request(app, protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings}, protocol.EventSettingsUpdated,
		func(protocol.SettingsUpdatedMessage) bool { return true })
	if settings.Settings["analytics.enabled"] != "false" {
		t.Fatalf("analytics.enabled by default = %v, want false", settings.Settings["analytics.enabled"])
	}
	if _, err := os.Stat(secretPath); !os.IsNotExist(err) {
		t.Fatalf("the install secret exists before analytics was ever enabled: %v", err)
	}

	setAnalyticsEnabled(app, "true")
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("enabling analytics left no install secret: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the install secret's mode = %v, want 0600", info.Mode().Perm())
	}
	first := readSecret(t, secretPath)
	if len(first) != 64 {
		t.Errorf("the install secret = %d hex characters, want 64 (32 bytes)", len(first))
	}

	setAnalyticsEnabled(app, "false")
	setAnalyticsEnabled(app, "true")
	w.restart()
	if again := readSecret(t, secretPath); again != first {
		t.Errorf("the install secret changed across a toggle and a restart: %q, then %q", first, again)
	}
}

func TestListAnalyticsRecordsPagesTheOutboxNewestFirst(t *testing.T) {
	w := newWorld(t)
	w.stop()
	at := time.Now().UTC().Truncate(time.Second)
	appended := []analytics.Record{
		{Stream: analytics.StreamBus, Seq: 100, Name: "session.state.changed", Subject: "h:1", At: at, Bytes: 0},
		{Stream: analytics.StreamBus, Seq: 101, Name: "pr.updated", Subject: "h:2", At: at.Add(time.Second)},
		{Stream: analytics.StreamApp, Seq: 1, Name: "shortcut", At: at.Add(2 * time.Second), Props: json.RawMessage(`{"id":"dock.asks"}`)},
		{Stream: analytics.StreamBus, Seq: 101, Name: "pr.updated", Subject: "h:2", At: at.Add(3 * time.Second)},
	}
	appendAnalyticsRecords(t, appended...)
	w.start()
	app := w.App()

	newest := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 2})
	if newest.Waiting != 3 {
		t.Errorf("waiting = %d, want the 3 distinct rows (the redelivered bus seq 101 is one row)", newest.Waiting)
	}
	if names := recordNames(t, newest.Records); strings.Join(names, ",") != "app/1,bus/101" {
		t.Fatalf("first page = %v, want app/1 then bus/101", names)
	}
	var shipped analytics.Record
	if err := json.Unmarshal([]byte(newest.Records[0].Record), &shipped); err != nil {
		t.Fatal(err)
	}
	if shipped.Name != "shortcut" || !shipped.At.Equal(appended[2].At) || string(shipped.Props) != `{"id":"dock.asks"}` {
		t.Errorf("the app record came back as %+v, want it as appended", shipped)
	}

	older := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 2, BeforeID: protocol.Ptr(newest.Records[1].ID)})
	if names := recordNames(t, older.Records); strings.Join(names, ",") != "bus/100" {
		t.Fatalf("second page = %v, want bus/100", names)
	}

	bus := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 10, Stream: protocol.Ptr("bus")})
	if names := recordNames(t, bus.Records); strings.Join(names, ",") != "bus/101,bus/100" {
		t.Errorf("bus stream = %v, want bus/101 then bus/100", names)
	}

	for _, refused := range []struct {
		msg  protocol.ListAnalyticsRecordsMessage
		want string
	}{
		{protocol.ListAnalyticsRecordsMessage{Limit: 1001}, "limit must be between 1 and 1000, got 1001"},
		{protocol.ListAnalyticsRecordsMessage{Limit: 10, Stream: protocol.Ptr("busses")}, `unknown analytics stream "busses"`},
	} {
		got := requestAnalyticsRecords(app, refused.msg)
		if got.Success || !strings.Contains(protocol.Deref(got.Error), refused.want) {
			t.Errorf("list %+v = success %v, error %q; want refused with %q", refused.msg, got.Success, protocol.Deref(got.Error), refused.want)
		}
	}
}

func TestTheAnalyticsOutboxForgetsRecordsAfterSevenDays(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.stop()
		now := time.Now().UTC()
		appendAnalyticsRecords(t,
			analytics.Record{Stream: analytics.StreamBus, Seq: 1, Name: "old", At: now.Add(-7*24*time.Hour - time.Minute)},
			analytics.Record{Stream: analytics.StreamBus, Seq: 2, Name: "recent", At: now.Add(-6 * 24 * time.Hour)},
		)
		w.start()
		w.advance(time.Hour + time.Minute)
		kept := listAnalyticsRecords(w.App(), protocol.ListAnalyticsRecordsMessage{Limit: 10})
		if names := recordNames(t, kept.Records); strings.Join(names, ",") != "bus/2" {
			t.Errorf("after an hour the outbox holds %v, want only the record younger than 7 days", names)
		}
	})
}

func setAnalyticsEnabled(app *testworld.Peer, value string) {
	app.T.Helper()
	requestID := uuid.NewString()
	answer := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "analytics.enabled", Value: value, RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(answer.Success) {
		app.T.Fatalf("setting analytics.enabled=%s refused: %s", value, protocol.Deref(answer.Error))
	}
}

func readSecret(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func appendAnalyticsRecords(t *testing.T, records ...analytics.Record) {
	t.Helper()
	outbox, err := store.NewWithDB(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err := outbox.AppendAnalyticsRecord(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := outbox.Close(); err != nil {
		t.Fatal(err)
	}
}

func requestAnalyticsRecords(app *testworld.Peer, msg protocol.ListAnalyticsRecordsMessage) protocol.ListAnalyticsRecordsResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	msg.Cmd, msg.RequestID = protocol.CmdListAnalyticsRecords, protocol.Ptr(requestID)
	return testworld.Request(app, msg, protocol.EventListAnalyticsRecordsResult,
		func(r protocol.ListAnalyticsRecordsResultMessage) bool { return r.RequestID == requestID })
}

func listAnalyticsRecords(app *testworld.Peer, msg protocol.ListAnalyticsRecordsMessage) protocol.ListAnalyticsRecordsResultMessage {
	app.T.Helper()
	listed := requestAnalyticsRecords(app, msg)
	if !listed.Success {
		app.T.Fatalf("list analytics records refused: %s", protocol.Deref(listed.Error))
	}
	return listed
}

func recordNames(t *testing.T, rows []protocol.AnalyticsOutboxRow) []string {
	t.Helper()
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		var record analytics.Record
		if err := json.Unmarshal([]byte(row.Record), &record); err != nil {
			t.Fatalf("outbox row %d is not a record: %v", row.ID, err)
		}
		names = append(names, string(record.Stream)+"/"+strconv.FormatInt(record.Seq, 10))
	}
	return names
}
