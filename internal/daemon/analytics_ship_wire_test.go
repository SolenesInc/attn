package daemon_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	uploadInterval     = 5 * time.Minute
	minuteLimitRefusal = `{"limit":"events_per_install_per_minute","value":10000,"observed":10200,"retry_after_s":600}`
)

func inBubbleUploadingTo(t *testing.T, link *testworld.IngestLink, script func(t *testing.T, w *world)) {
	t.Helper()
	prepared := prepareWorld(t)
	t.Setenv(config.AnalyticsIngestURLEnv, link.URL)
	inPreparedBubble(t, prepared, script)
}

func TestSharingUploadsTheOutboxToAttnIngestUntilTheUserForgetsTheInstall(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	link := ingest.Link(t)
	inBubbleUploadingTo(t, link, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "analytics.enabled", "true")
		setSetting(t, app, "analytics.share_vents", "true")
		setSetting(t, app, "analytics.sharing_enabled", "true")
		w.advance(0)
		const ventText = "attn seed tend refused a seed held by a session that ended."
		vent := recordInjectedVent(t, w, ventText)
		if vent.Send == nil || *vent.Send != protocol.VentSendQueued {
			t.Fatalf("with sharing and Send vents on a new vent's send state is %v, want queued", vent.Send)
		}
		w.advance(uploadInterval)
		testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
			return len(m.Vents) == 1 && m.Vents[0].Send != nil && *m.Vents[0].Send == protocol.VentSendSent
		})
		status := analyticsStatus(app)
		if status.LastSentAt == nil || status.LastRefusal != nil {
			t.Fatalf("after an accepted upload the status is last_sent_at %v, last_refusal %q", status.LastSentAt, protocol.Deref(status.LastRefusal))
		}
		assertServerHoldsWhatWasSent(t, clickHouse, status)
		build := analytics.CurrentBuild()
		if got, want := clickHouse.Query(t, "SELECT DISTINCT build_protocol, build_os, build_arch, app_version, app_channel FROM events"),
			strings.Join([]string{build.Protocol, build.Os, build.Arch, testworld.AppVersion, testworld.AppChannel}, "\t"); got != want {
			t.Fatalf("stored build block %q, want %q", got, want)
		}
		var props map[string]any
		if err := json.Unmarshal([]byte(clickHouse.Query(t, "SELECT props FROM events WHERE stream = 'vent'")), &props); err != nil {
			t.Fatalf("the vent row: %v", err)
		}
		if props["text"] != ventText || props["harness"] != "claude" {
			t.Errorf("the vent row's props %v lack its text or harness", props)
		}
		for _, private := range []string{"session_label", "seed_id", "transcript"} {
			if _, ok := props[private]; ok {
				t.Errorf("the vent row carries %s: %v", private, props)
			}
		}

		if kept := setVentSend(app, vent.ID, false); kept.Success || !strings.Contains(protocol.Deref(kept.Error), "already sent") {
			t.Fatalf("keeping a sent vent = success %v, error %q; want refused as already sent", kept.Success, protocol.Deref(kept.Error))
		}
		if refiled := setVentKind(app, vent.ID, protocol.VentKindTools); refiled.Success || !strings.Contains(protocol.Deref(refiled.Error), "already sent to attn's maintainers, so it stays an attn vent") {
			t.Fatalf("filing a sent vent under tools = success %v, error %q; want refused", refiled.Success, protocol.Deref(refiled.Error))
		}

		link.DropNextEventsReply()
		recordAndAdvance(w, app, uploadInterval)
		if status := analyticsStatus(app); status.LastRefusal == nil {
			t.Fatal("an upload whose reply was lost shows no refusal")
		}
		recordAndAdvance(w, app, uploadInterval)
		status = analyticsStatus(app)
		if status.LastRefusal != nil {
			t.Fatalf("the retried upload was refused: %s", *status.LastRefusal)
		}
		if duplicates := ingest.Metric(t, "attn_ingest_events_duplicate_total"); duplicates < 1 {
			t.Fatalf("the retried batch counted %d duplicates, want the lost batch's records", duplicates)
		}
		assertServerHoldsWhatWasSent(t, clickHouse, status)

		link.RefuseNextEvents(http.StatusTooManyRequests, minuteLimitRefusal)
		recordAndAdvance(w, app, uploadInterval)
		refusal := protocol.Deref(analyticsStatus(app).LastRefusal)
		if want := "events_per_install_per_minute is 10000, and this batch would make it 10200; retrying in 10m0s"; !strings.Contains(refusal, want) {
			t.Fatalf("the status shows the refusal %q, want it to contain %q", refusal, want)
		}
		before := len(link.Requests())
		recordAndAdvance(w, app, uploadInterval)
		if after := link.Requests(); len(after) != before {
			t.Fatalf("an upload went out before retry_after_s passed: %v", after[before:])
		}
		recordAndAdvance(w, app, uploadInterval)
		if status := analyticsStatus(app); status.LastRefusal != nil || len(link.Requests()) == before {
			t.Fatalf("once retry_after_s passed the next tick did not upload: refusal %q, requests %v", protocol.Deref(status.LastRefusal), link.Requests()[before:])
		}

		installs := clickHouse.Query(t, "SELECT install_id FROM installs")
		if forget := forgetInstall(app); !forget.Success {
			t.Fatalf("forget: %s", protocol.Deref(forget.Error))
		}
		if events, left := clickHouse.Query(t, "SELECT count() FROM events"), clickHouse.Query(t, "SELECT count() FROM installs"); events != "0" || left != "0" {
			t.Fatalf("after forget ClickHouse holds %s events and %s installs", events, left)
		}
		recordAndAdvance(w, app, uploadInterval)
		if now := clickHouse.Query(t, "SELECT install_id FROM installs"); now == "" || now == installs {
			t.Fatalf("after forget the next upload registered %q, want a new install (was %q)", now, installs)
		}
	})
}

func TestAttnIngestRefusingTheDaysBytesIsShownAndWaitedOut(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse, "ATTN_INGEST_BYTES_PER_DAY=1")
	link := ingest.Link(t)
	inBubbleUploadingTo(t, link, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "analytics.enabled", "true")
		setSetting(t, app, "analytics.sharing_enabled", "true")
		recordAndAdvance(w, app, uploadInterval)
		status := analyticsStatus(app)
		refusal := protocol.Deref(status.LastRefusal)
		if !strings.Contains(refusal, "bytes_per_day is 1, and this batch would make it ") || !strings.HasSuffix(refusal, "; retrying in 24h0m0s") {
			t.Fatalf("the status shows the refusal %q, want bytes_per_day named with its value and a 24h wait", refusal)
		}
		if status.LastSentAt != nil || status.Waiting == 0 {
			t.Fatalf("after a refused upload last_sent_at is %v and %d records wait", status.LastSentAt, status.Waiting)
		}
		before := len(link.Requests())
		recordAndAdvance(w, app, uploadInterval)
		if after := link.Requests(); len(after) != before {
			t.Fatalf("an upload went out before retry_after_s passed: %v", after[before:])
		}
		if events := clickHouse.Query(t, "SELECT count() FROM events"); events != "0" {
			t.Fatalf("ClickHouse holds %s events after every batch was refused", events)
		}
	})
}

func TestForgettingWhileAttnIngestIsUnreachableChangesNothing(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	link := ingest.Link(t)
	inBubbleUploadingTo(t, link, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "analytics.enabled", "true")
		setSetting(t, app, "analytics.sharing_enabled", "true")
		w.advance(0)
		installs := clickHouse.Query(t, "SELECT install_id FROM installs")
		if installs == "" {
			t.Fatal("sharing registered no install")
		}

		link.Cut()
		forget := forgetInstall(app)
		if forget.Success || !strings.Contains(protocol.Deref(forget.Error), "Nothing changed on this machine") {
			t.Fatalf("forget while offline = success %v, error %q; want refused saying nothing changed", forget.Success, protocol.Deref(forget.Error))
		}

		link.Restore()
		recordAndAdvance(w, app, uploadInterval)
		if status := analyticsStatus(app); status.LastRefusal != nil {
			t.Fatalf("after a refused forget the install no longer uploads: %s", *status.LastRefusal)
		}
		if now := clickHouse.Query(t, "SELECT install_id FROM installs"); now != installs {
			t.Fatalf("a refused forget changed the install from %q to %q", installs, now)
		}
		assertServerHoldsWhatWasSent(t, clickHouse, analyticsStatus(app))
	})
}

func TestTurningSharingOffDuringTheFirstRegistrationSendsNothing(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	link := testworld.StartIngest(t, clickHouse).Link(t)
	arrived, release := link.HoldNextRegistration()
	defer release()
	inBubbleUploadingTo(t, link, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "analytics.enabled", "true")
		sendAppAction(app, "settings_section", map[string]any{"section": "analytics"})
		setSetting(t, app, "analytics.sharing_enabled", "true")
		<-arrived
		setSetting(t, app, "analytics.sharing_enabled", "false")
		release()
		w.advance(3 * uploadInterval)
		for _, request := range link.Requests() {
			if request != "POST /v1/installs" {
				t.Fatalf("after sharing went off mid-registration attn sent %v", link.Requests())
			}
		}
		if status := analyticsStatus(app); status.LastRefusal != nil {
			t.Errorf("withdrawing consent left the refusal %q", *status.LastRefusal)
		}
	})
}

func TestRecordsMadeAfterClearingDuringAnUploadAreStillSent(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	link := testworld.StartIngest(t, clickHouse).Link(t)
	stored, release := link.HoldNextEventsReply()
	defer release()
	inBubbleUploadingTo(t, link, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "analytics.enabled", "true")
		sendAppAction(app, "settings_section", map[string]any{"section": "analytics"})
		setSetting(t, app, "analytics.sharing_enabled", "true")
		<-stored
		first := clickHouse.Query(t, "SELECT count() FROM events FINAL")
		cleared := testworld.Request(app, protocol.ClearAnalyticsRecordsMessage{Cmd: protocol.CmdClearAnalyticsRecords, RequestID: "clear"},
			protocol.EventClearAnalyticsRecordsResult, func(r protocol.ClearAnalyticsRecordsResultMessage) bool { return r.RequestID == "clear" })
		if !cleared.Success {
			t.Fatalf("clear: %s", protocol.Deref(cleared.Error))
		}
		sendAppAction(app, "settings_section", map[string]any{"section": "analytics"})
		if status := analyticsStatus(app); status.Records == 0 {
			t.Fatal("nothing was recorded after the clear")
		}
		release()
		w.advance(uploadInterval)
		status := analyticsStatus(app)
		firstCount, err := strconv.Atoi(first)
		if err != nil {
			t.Fatal(err)
		}
		if want, got := strconv.Itoa(firstCount+status.Records-status.Waiting), clickHouse.Query(t, "SELECT count() FROM events FINAL"); got != want {
			t.Fatalf("ClickHouse holds %s events, want the %d from before the clear plus the %d rows marked sent since", got, firstCount, status.Records-status.Waiting)
		}
	})
}

func TestWithAnalyticsOffNothingIsSentEvenWithSharingOnAndRecordsWaiting(t *testing.T) {
	link := testworld.UnreachableIngestLink(t)
	inBubbleUploadingTo(t, link, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "analytics.enabled", "true")
		recordAndAdvance(w, app, 0)
		if status := analyticsStatus(app); status.Waiting == 0 {
			t.Fatal("recording left nothing waiting to be sent")
		}
		setSetting(t, app, "analytics.enabled", "false")
		setSetting(t, app, "analytics.sharing_enabled", "true")
		w.advance(3 * uploadInterval)
		if requests := link.Requests(); len(requests) != 0 {
			t.Errorf("with analytics off attn sent %v", requests)
		}
	})
}

func TestTheUserSendsOrKeepsOneVentAndAWithdrawnVentIsNeverSent(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	setSetting(t, app, "analytics.enabled", "true")
	setSetting(t, app, "analytics.share_vents", "true")
	vent := recordInjectedVent(t, w, "attn vent refused my second vent in one turn.")
	if vent.Send != nil {
		t.Fatalf("with Send vents on but sharing off a new vent's send state is %v, want none", *vent.Send)
	}
	if got := streamOutbox(t, app, analytics.StreamVent); len(got) != 0 {
		t.Fatalf("with sharing off the outbox holds vent records %v", got)
	}

	if sent := setVentSend(app, vent.ID, true); !sent.Success {
		t.Fatalf("x send: %s", protocol.Deref(sent.Error))
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 1 && m.Vents[0].Send != nil && *m.Vents[0].Send == protocol.VentSendQueued
	})
	records := streamOutbox(t, app, analytics.StreamVent)
	if len(records) != 1 || records[0].Name != "vent" || records[0].Subject != "" {
		t.Fatalf("after x send the outbox holds %+v, want one vent record with no subject", records)
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal(records[0].Props, &props); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for key := range props {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if got, want := strings.Join(keys, ","), "build,experimental,harness,text,turn"; got != want {
		t.Errorf("the vent record carries %s, want exactly %s (harness_version and model when known)", got, want)
	}

	if kept := setVentSend(app, vent.ID, false); !kept.Success {
		t.Fatalf("x keep: %s", protocol.Deref(kept.Error))
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 1 && m.Vents[0].Send == nil })
	if got := streamOutbox(t, app, analytics.StreamVent); len(got) != 0 {
		t.Fatalf("after x keep the outbox holds vent records %v", got)
	}

	if sent := setVentSend(app, vent.ID, true); !sent.Success {
		t.Fatalf("x send again: %s", protocol.Deref(sent.Error))
	}
	if err := w.Client().WithdrawVent("venting", vent.ID); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	testworld.Await(app, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool {
		return len(m.Vents) == 1 && m.Vents[0].WithdrawnAt != nil && m.Vents[0].Send == nil
	})
	if got := streamOutbox(t, app, analytics.StreamVent); len(got) != 0 {
		t.Fatalf("after the agent withdrew its queued vent the outbox holds %v", got)
	}
	if sent := setVentSend(app, vent.ID, true); sent.Success || !strings.Contains(protocol.Deref(sent.Error), "withdrawn") {
		t.Fatalf("x send on a withdrawn vent = success %v, error %q; want refused as withdrawn", sent.Success, protocol.Deref(sent.Error))
	}
}

func TestAQueuedVentsRecordIsListedOnlyInItsProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "analytics.enabled", "true")
	setSetting(t, app, "analytics.sharing_enabled", "true")
	setSetting(t, app, "analytics.share_vents", "true")
	side := w.AppOn(createProfile(app, "Side").ID)
	session := w.Spawn(app, fakeagent.Claude, w.Path("home"))
	w.Launched(session)
	if _, err := w.Client().RecordVent(protocol.SessionID(session), protocol.VentKindAttn, "the home profile's private vent"); err != nil {
		t.Fatalf("vent: %v", err)
	}
	if got := streamOutbox(t, app, analytics.StreamVent); len(got) != 1 {
		t.Fatalf("the vent's own profile lists %d vent records, want 1", len(got))
	}
	if got := streamOutbox(t, side, analytics.StreamVent); len(got) != 0 {
		t.Fatalf("another profile lists the vent records %+v", got)
	}
	for _, row := range listAnalyticsRecords(side, protocol.ListAnalyticsRecordsMessage{Limit: 1000}).Records {
		if strings.Contains(row.Record, "private vent") {
			t.Fatalf("another profile's All records shows %s", row.Record)
		}
	}
}

func TestAVentWhoseRecordAttnIngestWouldRefuseIsNeverQueued(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	setSetting(t, app, "analytics.enabled", "true")
	setSetting(t, app, "analytics.sharing_enabled", "true")
	vent := recordInjectedVent(t, w, strings.Repeat("<", 10880))
	sent := setVentSend(app, vent.ID, true)
	if want := "attn-ingest takes records of up to 65536 bytes"; sent.Success || !strings.Contains(protocol.Deref(sent.Error), want) {
		t.Fatalf("x send on a vent of escaped markup = success %v, error %q; want refused naming %q", sent.Success, protocol.Deref(sent.Error), want)
	}
	if got := streamOutbox(t, app, analytics.StreamVent); len(got) != 0 {
		t.Fatalf("the refused vent left records %v", got)
	}
}

func setVentSend(app *testworld.Peer, ventID string, send bool) protocol.SetVentSendResultMessage {
	app.T.Helper()
	requestID := "send-" + strconv.FormatBool(send) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	return testworld.Request(app, protocol.SetVentSendMessage{Cmd: protocol.CmdSetVentSend, RequestID: requestID, VentID: ventID, Send: send},
		protocol.EventSetVentSendResult, func(r protocol.SetVentSendResultMessage) bool { return r.RequestID == requestID })
}

func recordAndAdvance(w *world, app *testworld.Peer, d time.Duration) {
	w.T.Helper()
	sendAppAction(app, "settings_section", map[string]any{"section": "analytics"})
	w.advance(d)
}

func recordInjectedVent(t *testing.T, w *world, text string) protocol.Vent {
	t.Helper()
	if err := w.InjectSession("venting", "venting", w.Path("venting"), protocol.SessionAgentClaude); err != nil {
		t.Fatal(err)
	}
	vent, err := w.Client().RecordVent("venting", protocol.VentKindAttn, text)
	if err != nil {
		t.Fatalf("vent: %v", err)
	}
	return *vent
}

func forgetInstall(app *testworld.Peer) protocol.ForgetAnalyticsInstallResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.ForgetAnalyticsInstallMessage{Cmd: protocol.CmdForgetAnalyticsInstall, RequestID: "forget"},
		protocol.EventForgetAnalyticsInstallResult, func(r protocol.ForgetAnalyticsInstallResultMessage) bool { return r.RequestID == "forget" })
}

func assertServerHoldsWhatWasSent(t *testing.T, clickHouse testworld.ClickHouse, status protocol.GetAnalyticsStatusResultMessage) {
	t.Helper()
	if stored, sent := clickHouse.Query(t, "SELECT count() FROM events FINAL"), status.Records-status.Waiting; stored != strconv.Itoa(sent) {
		t.Fatalf("ClickHouse holds %s events, want the %d rows this machine marked sent", stored, sent)
	}
}
