package main_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
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

func enableAnalyticsOnPair(t *testing.T, home *testworld.Stack, remote *testworld.Peer) {
	t.Helper()
	mark := remote.EventCount()
	requireStdout(t, home.Attn("settings", "set", "analytics.enabled", "true"), "true")
	testworld.AwaitSince(remote, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "analytics.subject_secret"
	})
	testworld.AwaitSince(remote, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["analytics.enabled"] == "true"
	})
}

func disconnectOutpost(pair *testworld.OutpostPair) {
	mark := pair.App.EventCount()
	pair.Disconnect()
	outpostStateSince(pair.App, "outpost", protocol.OutpostStateLost, mark)
}

func recordOnOutpost(t *testing.T, outpost *testworld.Stack, remote *testworld.Peer) analytics.Record {
	t.Helper()
	requireStdout(t, outpost.Attn("settings", "get", "theme"))
	rows := cliRecords(t, remote)
	if len(rows) == 0 || rows[0].Name != "settings get" {
		t.Fatalf("the outpost recorded no settings get, it holds %+v", rows)
	}
	return rows[0]
}

func holdsForwarded(rows []analytics.Record, want analytics.Record) bool {
	return slices.ContainsFunc(rows, func(row analytics.Record) bool {
		return strings.HasPrefix(string(row.Stream), string(want.Stream)+"_m") && row.Seq == want.Seq
	})
}

func holdsSeq(rows []analytics.Record, want analytics.Record) bool {
	return slices.ContainsFunc(rows, func(row analytics.Record) bool { return row.Stream == want.Stream && row.Seq == want.Seq })
}

func awaitChanged(remote *testworld.Peer, mark int, key string) {
	testworld.AwaitSince(remote, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == key
	})
}

func clearAnalyticsAt(t *testing.T, app *testworld.Peer) {
	t.Helper()
	result := testworld.Request(app, protocol.ClearAnalyticsRecordsMessage{Cmd: protocol.CmdClearAnalyticsRecords, RequestID: "clear"},
		protocol.EventClearAnalyticsRecordsResult, func(r protocol.ClearAnalyticsRecordsResultMessage) bool { return r.RequestID == "clear" })
	if !result.Success {
		t.Fatalf("clear failed: %+v", result)
	}
}

func forgetInstallAt(t *testing.T, app *testworld.Peer) {
	t.Helper()
	result := testworld.Request(app, protocol.ForgetAnalyticsInstallMessage{Cmd: protocol.CmdForgetAnalyticsInstall, RequestID: "forget"},
		protocol.EventForgetAnalyticsInstallResult, func(r protocol.ForgetAnalyticsInstallResultMessage) bool { return r.RequestID == "forget" })
	if !result.Success {
		t.Fatalf("forget failed: %+v", result)
	}
}

func TestWhatAnOfflineOutpostRecordedWhileTheHomeWasOffIsNeverSent(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	forwarded := outpost.PauseEvery(pausepoint.AnalyticsForwarded)
	pair.Start()
	remote := outpost.App()
	enableAnalyticsOnPair(t, home, remote)
	disconnectOutpost(pair)
	requireStdout(t, home.Attn("settings", "set", "analytics.enabled", "false"), "false")
	offInterval := recordOnOutpost(t, outpost, remote)
	requireStdout(t, home.Attn("settings", "set", "analytics.enabled", "true"), "true")

	mark := remote.EventCount()
	pair.Reconnect()
	stopRetries := home.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	forwarded.Await()
	forwarded.Release()
	awaitChanged(remote, mark, "analytics.cleared_at")
	if holdsForwarded(analyticsRows(t, pair.App), offInterval) {
		t.Errorf("the home holds %s %d, recorded while it was off", offInterval.Stream, offInterval.Seq)
	}
	if holdsSeq(analyticsRows(t, remote), offInterval) {
		t.Errorf("the outpost still holds %s %d after it learned the home had been off", offInterval.Stream, offInterval.Seq)
	}

	next := recordOnOutpost(t, outpost, remote)
	disconnectOutpost(pair)
	pair.Reconnect()
	forwarded.Await()
	forwarded.Release()
	if !holdsForwarded(analyticsRows(t, pair.App), next) {
		t.Errorf("the home never received %s %d, recorded after the outpost caught up", next.Stream, next.Seq)
	}
}

func TestAnOutpostThatReconnectsToAHomeLeftOffDropsWhatItRecorded(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	remote := outpost.App()
	enableAnalyticsOnPair(t, home, remote)
	disconnectOutpost(pair)
	requireStdout(t, home.Attn("settings", "set", "analytics.enabled", "false"), "false")
	offInterval := recordOnOutpost(t, outpost, remote)

	mark := remote.EventCount()
	pair.Reconnect()
	stopRetries := home.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	awaitChanged(remote, mark, "analytics.cleared_at")
	if holdsSeq(analyticsRows(t, remote), offInterval) {
		t.Errorf("the outpost still holds %s %d though the home stayed off", offInterval.Stream, offInterval.Seq)
	}
}

func TestAClearAtTheHomeRemovesWhatAnOfflineOutpostHolds(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	forwarded := outpost.PauseAt(pausepoint.AnalyticsForwarded)
	pair.Start()
	remote := outpost.App()
	enableAnalyticsOnPair(t, home, remote)
	home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	disconnectOutpost(pair)
	held := recordOnOutpost(t, outpost, remote)
	clearAnalyticsAt(t, pair.App)

	home.Stop()
	copyRead := home.PauseAt(pausepoint.OutboxSessionRead)
	mark := remote.EventCount()
	pair.Reconnect()
	home.Start()
	homeApp := home.App()
	stopRetries := home.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	copyRead.Await()
	forwarded.Await()
	if holdsForwarded(analyticsRows(t, homeApp), held) {
		t.Errorf("the home took %s %d back after clearing it, forwarded before the clear reached the outpost", held.Stream, held.Seq)
	}
	copyRead.Release()
	awaitChanged(remote, mark, "analytics.cleared_at")
	forwarded.Release()
	if holdsSeq(analyticsRows(t, remote), held) {
		t.Errorf("the outpost still holds %s %d after the home cleared", held.Stream, held.Seq)
	}
}

func TestAForwardInFlightDuringAClearLeavesNothingAtTheHome(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	importing := home.PauseAt(pausepoint.AnalyticsForwardImporting)
	forwarded := outpost.PauseAt(pausepoint.AnalyticsForwarded)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	remote := outpost.App()
	enableAnalyticsOnPair(t, home, remote)
	disconnectOutpost(pair)
	held := recordOnOutpost(t, outpost, remote)
	pair.Reconnect()
	stopRetries := home.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	importing.Await()
	clearAnalyticsAt(t, pair.App)
	importing.Release()
	forwarded.Await()
	forwarded.Release()
	if holdsForwarded(analyticsRows(t, pair.App), held) {
		t.Errorf("the home holds %s %d after clearing while it arrived", held.Stream, held.Seq)
	}
}

func TestAForgottenInstallNeverUploadsAForwardCaughtInTheMiddle(t *testing.T) {
	for _, side := range []string{"home importing", "outpost sending"} {
		t.Run(side, func(t *testing.T) {
			ingest := testworld.StartFakeIngest(t)
			link := ingest.Link(t)
			home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
			home.Vars = append(home.Vars, "ATTN_ANALYTICS_INGEST_URL="+link.URL)
			var caught *testworld.Pause
			if side == "home importing" {
				caught = home.PauseAt(pausepoint.AnalyticsForwardImporting)
			} else {
				caught = outpost.PauseAt(pausepoint.AnalyticsForwardRead)
			}
			forwarded := outpost.PauseAt(pausepoint.AnalyticsForwarded)
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			remote := outpost.App()
			enableAnalyticsOnPair(t, home, remote)

			stored, release := link.HoldNextEventsReply()
			defer release()
			requireStdout(t, home.Attn("settings", "set", "analytics.sharing_enabled", "true"), "true")
			<-stored
			first := ingest.Installs()
			release()
			home.AwaitJobsSettled()
			requireStdout(t, home.Attn("settings", "set", "analytics.sharing_enabled", "false"), "false")

			disconnectOutpost(pair)
			held := recordOnOutpost(t, outpost, remote)
			pair.Reconnect()
			stopRetries := home.FireTimers(bridge.RetryTimer)
			defer stopRetries()
			caught.Await()
			mark := remote.EventCount()
			forgetInstallAt(t, pair.App)
			awaitChanged(remote, mark, "analytics.subject_secret")
			caught.Release()
			forwarded.Await()
			forwarded.Release()
			if holdsForwarded(analyticsRows(t, pair.App), held) {
				t.Errorf("the home holds %s %d, hashed with the forgotten secret", held.Stream, held.Seq)
			}

			requireStdout(t, home.Attn("settings", "get", "theme"))
			before := len(ingest.Batches())
			storedNew, releaseNew := link.HoldNextEventsReply()
			defer releaseNew()
			requireStdout(t, home.Attn("settings", "set", "analytics.sharing_enabled", "true"), "true")
			<-storedNew
			now := ingest.Installs()
			if len(now) != 1 || slices.Contains(first, now[0]) {
				t.Fatalf("want one new install, had %v and now %v", first, now)
			}
			for _, batch := range ingest.Batches()[before:] {
				if holdsForwarded(batch.Records, held) {
					t.Errorf("the new install uploaded %s %d from before the forget", held.Stream, held.Seq)
				}
			}
		})
	}
}
