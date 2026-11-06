package main_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func analyticsRows(t *testing.T, app *testworld.Peer) []analytics.Record {
	t.Helper()
	listed := testworld.Request(app, protocol.ListAnalyticsRecordsMessage{Cmd: protocol.CmdListAnalyticsRecords, RequestID: protocol.Ptr("records"), Limit: 1000},
		protocol.EventListAnalyticsRecordsResult, func(r protocol.ListAnalyticsRecordsResultMessage) bool { return r.RequestID == "records" })
	rows := make([]analytics.Record, len(listed.Records))
	for i, row := range listed.Records {
		if err := json.Unmarshal([]byte(row.Record), &rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	return rows
}

func holdsOutpostRecords(rows []analytics.Record) bool {
	return slices.ContainsFunc(rows, func(row analytics.Record) bool { return strings.Contains(string(row.Stream), "_m") })
}

func TestAnOutpostSendsNothingWhileAnalyticsIsOff(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	forwarded := outpost.PauseAt(pausepoint.AnalyticsForwarded)
	pair.Start()
	requireStdout(t, outpost.Attn("settings", "get", "theme"))
	outpost.Advance(5*time.Minute + time.Second)
	forwarded.Await()
	forwarded.Release()
	if rows := analyticsRows(t, outpost.App()); len(rows) != 0 {
		t.Errorf("with analytics off the outpost recorded %d rows", len(rows))
	}
	if rows := analyticsRows(t, home.App()); holdsOutpostRecords(rows) {
		t.Errorf("with analytics off the home holds records from its outpost: %+v", rows)
	}
}

func TestAnOutpostsAnalyticsReachTheHomeAndClearWithIt(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	forwarded := outpost.PauseAt(pausepoint.AnalyticsForwarded)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	secretArrived := remote.EventCount()
	requireStdout(t, home.Attn("settings", "set", "analytics.enabled", "true"), "true")
	testworld.AwaitSince(remote, secretArrived, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		if _, leaked := m.Settings["analytics.subject_secret"]; leaked {
			t.Fatal("the app received the subject secret")
		}
		return protocol.Deref(m.ChangedKey) == "analytics.subject_secret"
	})
	for len(analyticsRows(t, remote)) == 0 {
		requireStdout(t, outpost.Attn("settings", "get", "theme"))
	}
	home.Stop()
	home.Start()
	forwarded.Await()
	forwarded.Release()
	homeApp := home.App()
	if !holdsOutpostRecords(analyticsRows(t, homeApp)) {
		t.Fatal("the home holds no record from its outpost after it came back")
	}

	before := analyticsRows(t, remote)
	mark := remote.EventCount()
	testworld.Request(homeApp, protocol.ClearAnalyticsRecordsMessage{Cmd: protocol.CmdClearAnalyticsRecords, RequestID: "clear"},
		protocol.EventClearAnalyticsRecordsResult, func(r protocol.ClearAnalyticsRecordsResultMessage) bool { return r.RequestID == "clear" })
	testworld.AwaitSince(remote, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "analytics.cleared_at"
	})
	after := analyticsRows(t, remote)
	type position struct {
		stream analytics.Stream
		seq    int64
	}
	seen := map[position]bool{}
	for _, row := range after {
		seen[position{row.Stream, row.Seq}] = true
	}
	for _, row := range before {
		if seen[position{row.Stream, row.Seq}] {
			t.Fatalf("the outpost still holds %s %s after the home cleared", row.Stream, row.Name)
		}
	}
}

func TestAForgottenInstallSendsNothingHashedWithItsOldSecret(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	homeApp := home.App()
	requireStdout(t, home.Attn("settings", "set", "analytics.enabled", "true"), "true")
	testworld.Await(remote, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["analytics.enabled"] == "true" })
	before := analyticsRows(t, homeApp)
	mark := remote.EventCount()
	testworld.Request(homeApp, protocol.ForgetAnalyticsInstallMessage{Cmd: protocol.CmdForgetAnalyticsInstall, RequestID: "forget"},
		protocol.EventForgetAnalyticsInstallResult, func(r protocol.ForgetAnalyticsInstallResultMessage) bool { return r.RequestID == "forget" })
	testworld.AwaitSince(remote, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "analytics.subject_secret"
	})
	seen := map[int64]bool{}
	for _, row := range analyticsRows(t, homeApp) {
		seen[row.Seq] = true
	}
	for _, row := range before {
		if seen[row.Seq] {
			t.Fatalf("the home still holds %s seq %d, unsent and hashed with the forgotten secret", row.Name, row.Seq)
		}
	}
}
