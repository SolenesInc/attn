package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
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

func TestListAnalyticsRecordsNamesTheLimitItRefuses(t *testing.T) {
	w := newWorld(t)
	app := w.App()

	empty := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 1000})
	if len(empty.Records) != 0 || empty.Waiting != 0 {
		t.Errorf("a fresh outbox lists %d records with %d waiting, want none", len(empty.Records), empty.Waiting)
	}
	for _, refused := range []struct {
		msg  protocol.ListAnalyticsRecordsMessage
		want string
	}{
		{protocol.ListAnalyticsRecordsMessage{Limit: 0}, "limit must be between 1 and 1000, got 0"},
		{protocol.ListAnalyticsRecordsMessage{Limit: 1001}, "limit must be between 1 and 1000, got 1001"},
		{protocol.ListAnalyticsRecordsMessage{Limit: 10, Stream: protocol.Ptr("busses")}, `unknown analytics stream "busses": use bus, app, cli, sample or vent`},
	} {
		got := requestAnalyticsRecords(app, refused.msg)
		if got.Success || protocol.Deref(got.Error) != refused.want {
			t.Errorf("list %+v = success %v, error %q; want refused with %q", refused.msg, got.Success, protocol.Deref(got.Error), refused.want)
		}
	}
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
