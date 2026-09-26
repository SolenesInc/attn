package daemon

import (
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func seedApp(t *testing.T, d *Daemon, name string, enabled bool) store.AppVersion {
	t.Helper()
	now := time.Now().UTC()
	version, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName:      name,
		ContentHash:  "sha256:" + name,
		Declaration:  `{"name":"` + name + `","subscribe":[{"events":["ticket.*"]}]}`,
		ArtifactPath: "apps/" + name + "/bundle.js",
	}, now)
	if err != nil {
		t.Fatalf("seed version for %s: %v", name, err)
	}
	seedAppConsumer(t, d, name, enabled, 0)
	return version
}

func seedAppConsumer(t *testing.T, d *Daemon, name string, enabled bool, cursor int64) {
	t.Helper()
	if err := d.store.SaveBusConsumer(store.BusConsumer{
		Name:    apps.ConsumerName(name),
		Cursor:  cursor,
		Filter:  "ticket.*",
		Enabled: enabled,
	}, time.Now()); err != nil {
		t.Fatalf("seed consumer for %s: %v", name, err)
	}
	if _, err := d.store.SetBusConsumerEnabled(apps.ConsumerName(name), enabled, time.Now()); err != nil {
		t.Fatalf("seed consumer bit for %s: %v", name, err)
	}
	if err := d.store.SetBusConsumerCursor(apps.ConsumerName(name), cursor, time.Now()); err != nil {
		t.Fatalf("seed consumer cursor for %s: %v", name, err)
	}
}

func appFacts(t *testing.T, d *Daemon, name string) []store.BusEvent {
	t.Helper()
	var out []store.BusEvent
	for _, e := range factsOf(t, d) {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func appStatus(t *testing.T, d *Daemon, name string) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleAppStatus(c, &protocol.AppStatusMessage{Cmd: protocol.CmdAppStatus, Name: name})
	})
}

func appSetEnabled(t *testing.T, d *Daemon, name string, enabled bool) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleAppSetEnabled(c, &protocol.AppSetEnabledMessage{
			Cmd: protocol.CmdAppSetEnabled, Name: name, Enabled: enabled,
		})
	})
}

func appRemove(t *testing.T, d *Daemon, name string) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleAppRemove(c, &protocol.AppRemoveMessage{Cmd: protocol.CmdAppRemove, Name: name})
	})
}

func TestAppReEnableResumesWithoutReconcileAndDeliversTheRetainedBacklogInOrder(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "approval-gate", subscribing("ticket.*"))
	runtime := startFakeAppRuntime(t, d, nil)
	if resp := appSetEnabled(t, d, "approval-gate", false); !resp.Ok {
		t.Fatalf("disable: %v", protocol.Deref(resp.Error))
	}
	d.publishFact("ticket.created", "tk-1", nil)
	d.publishFact("ticket.updated", "tk-1", nil)
	if resp := appSetEnabled(t, d, "approval-gate", true); !resp.Ok {
		t.Fatalf("enable: %v", protocol.Deref(resp.Error))
	}
	waitFor(t, "the retained backlog to be delivered", func() bool { return len(runtime.dispatchLog()) == 2 })
	log := runtime.dispatchLog()
	if got := []string{log[0].Event.Name, log[1].Event.Name}; !reflect.DeepEqual(got, []string{"ticket.created", "ticket.updated"}) {
		t.Fatalf("delivery order = %v", got)
	}
	claim, err := d.store.AppReconcilePending("approval-gate")
	if err != nil || len(claim.Requests) != 0 {
		t.Fatalf("pending = %+v, %v", claim, err)
	}
	if got := len(runtime.reconcileLog()); got != 0 {
		t.Fatalf("re-enable dispatched %d reconcile(s), want none", got)
	}
}

func TestAppVersionChangeReconcilesAtTheFrozenCursorThenDeliversTheRetainedFact(t *testing.T) {
	d := newAppDaemon(t)
	firstManifest := subscribing("ticket.*")
	firstManifest.Reconcile = true
	first := installApp(t, d, "approval-gate", firstManifest)
	runtime := startFakeAppRuntime(t, d, nil)
	if resp := appSetEnabled(t, d, "approval-gate", false); !resp.Ok {
		t.Fatalf("disable: %v", protocol.Deref(resp.Error))
	}
	d.publishFact("ticket.created", "tk-1", nil)
	retained := appFacts(t, d, "ticket.created")
	if len(retained) != 1 || retained[0].Subject != "tk-1" {
		t.Fatalf("retained ticket facts = %+v, want ticket.created for tk-1", retained)
	}
	retainedSeq := retained[0].Seq
	secondManifest := firstManifest
	secondManifest.Description = "version two"
	second := installApp(t, d, "approval-gate", secondManifest)
	if second.ID == first.ID {
		t.Fatalf("version did not move: first=%d second=%d", first.ID, second.ID)
	}
	if resp := appSetEnabled(t, d, "approval-gate", true); !resp.Ok {
		t.Fatalf("enable: %v", protocol.Deref(resp.Error))
	}
	waitFor(t, "reconcile followed by retained fact delivery", func() bool {
		return len(runtime.reconcileLog()) == 1 && len(runtime.dispatchLog()) == 1
	})
	if fence := runtime.reconcileLog()[0].Reason.ThroughSeq; fence >= retainedSeq {
		t.Fatalf("version fence %d covered retained undelivered fact %d", fence, retainedSeq)
	}
	delivered := runtime.dispatchLog()[0]
	if delivered.Event.Seq != retainedSeq || delivered.VersionID != second.ID {
		t.Fatalf("retained delivery = seq %d version %d, want seq %d version %d", delivered.Event.Seq, delivered.VersionID, retainedSeq, second.ID)
	}
}

func TestAppStatusReportsHistoryAndRecentInvocations(t *testing.T) {
	d := newDaemonForTest(t)
	version := seedApp(t, d, "approval-gate", false)
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	for i, status := range []string{"ok", "error"} {
		failure := ""
		if status == "error" {
			failure = "TypeError: undefined is not a function"
		}
		if _, err := d.store.AppendAppInvocation(store.AppInvocation{
			AppName: "approval-gate", VersionID: version.ID, EventSeq: int64(10 + i),
			EventName: "ticket.created", EventSubject: "t-1", Handler: "ticket.*",
			Status: status, Error: failure, Duration: 5 * time.Millisecond,
			StartedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append invocation: %v", err)
		}
	}

	resp := appStatus(t, d, "approval-gate")
	if !resp.Ok {
		t.Fatalf("status: %v", protocol.Deref(resp.Error))
	}
	result := resp.AppStatusResult
	if result.Versions != 1 || result.Invocations != 2 {
		t.Fatalf("history = %d version(s), %d invocation(s)", result.Versions, result.Invocations)
	}
	if len(result.Recent) != 2 || result.Recent[0].Status != "error" {
		t.Fatalf("recent = %+v, want the newest first", result.Recent)
	}
	if result.Recent[0].VersionID != int(version.ID) || protocol.Deref(result.Recent[0].EventSeq) != 11 {
		t.Fatalf("recent[0] = %+v", result.Recent[0])
	}
	if result.App.Consumer == nil || result.App.Consumer.Enabled {
		t.Fatalf("consumer = %+v, want a disabled one", result.App.Consumer)
	}
}
