package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
)

func commandManifest(commands ...appbuild.Command) appbuild.Manifest {
	return appbuild.Manifest{
		Views:    []appbuild.View{tileView("approvals", "Pending approvals")},
		Commands: commands,
	}
}

func tileView(name, title string) appbuild.View {
	return appbuild.View{Name: name, Kind: appbuild.ViewKindTile, Title: title, Entrypoint: "src/views/" + name + ".tsx"}
}

type appCommandCaller struct {
	client *wsClient
}

func newAppCommandCaller() *appCommandCaller {
	return &appCommandCaller{client: &wsClient{send: make(chan outboundMessage, 16)}}
}

func (c *appCommandCaller) invoke(t *testing.T, d *Daemon, app, command, payload string) protocol.AppCommandResultMessage {
	t.Helper()
	msg := &protocol.AppCommandMessage{
		Cmd:       protocol.CmdAppCommand,
		RequestID: "req-1",
		App:       app,
		Command:   command,
	}
	if payload != "" {
		msg.Payload = protocol.Ptr(payload)
	}
	d.handleAppCommand(c.client, msg)
	return c.result(t)
}

func (c *appCommandCaller) result(t *testing.T) protocol.AppCommandResultMessage {
	t.Helper()
	select {
	case msg := <-c.client.send:
		var out protocol.AppCommandResultMessage
		if err := json.Unmarshal(msg.payload, &out); err != nil {
			t.Fatalf("decode the answer: %v", err)
		}
		if out.Event != protocol.EventAppCommandResult {
			t.Fatalf("the daemon answered with %q", out.Event)
		}
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon never answered the command")
		return protocol.AppCommandResultMessage{}
	}
}

func mustFail(t *testing.T, result protocol.AppCommandResultMessage, wants ...string) {
	t.Helper()
	if result.Success {
		t.Fatalf("the command succeeded, want a refusal: %+v", result)
	}
	message := protocol.Deref(result.Error)
	for _, want := range wants {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal does not say %q: %s", want, message)
		}
	}
}

func TestAppCommandRunsTheHandlerAndAnswersTheCaller(t *testing.T) {
	d := newAppDaemon(t)
	version := installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	runtime := startConfiguredAppRuntime(t, d, fakeAppRuntimeHandlers{
		command: func(_ *fakeAppRuntime, req appCommandRequest) (json.RawMessage, error) {
			return json.RawMessage(`{"approved":"tk-1"}`), nil
		},
	})

	result := newAppCommandCaller().invoke(t, d, "reviewer", "approve", `{"id":"tk-1"}`)

	if !result.Success {
		t.Fatalf("the command failed: %s", protocol.Deref(result.Error))
	}
	if protocol.Deref(result.Payload) != `{"approved":"tk-1"}` {
		t.Fatalf("the handler's answer did not reach the caller: %v", result.Payload)
	}
	log := runtime.commandLog()
	if len(log) != 1 {
		t.Fatalf("the sidecar ran %d commands", len(log))
	}
	got := log[0]
	if got.Handler != "approve" {
		t.Errorf("the sidecar was asked for handler %q, want the command's bare name", got.Handler)
	}
	if got.App != "reviewer" || got.VersionID != version.ID {
		t.Errorf("the dispatch names %s version %d, want reviewer version %d", got.App, got.VersionID, version.ID)
	}
	if string(got.Payload) != `{"id":"tk-1"}` {
		t.Errorf("payload reached the handler as %s", got.Payload)
	}

	rows := invocationsOf(t, d, "reviewer")
	if len(rows) != 1 {
		t.Fatalf("invocations = %+v, want the one command", rows)
	}
	if rows[0].Status != appInvocationStatusOK || rows[0].Handler != apps.CommandLabel("approve") {
		t.Errorf("invocation = %+v", rows[0])
	}
	if rows[0].EventName != appCommandEvent || rows[0].EventSubject != "approve" {
		t.Errorf("a command's invocation must name itself, not borrow a fact: %+v", rows[0])
	}
}

func TestAppCommandWithNoPayloadAndNoAnswerSucceeds(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "refresh"}))
	startFakeAppRuntime(t, d, nil)

	result := newAppCommandCaller().invoke(t, d, "reviewer", "refresh", "")

	if !result.Success {
		t.Fatalf("the command failed: %s", protocol.Deref(result.Error))
	}
	if result.Payload != nil {
		t.Fatalf("a handler that returned nothing carried %v", *result.Payload)
	}
}

func TestAppCommandRefusesAnAnswerOverTheLimit(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	startConfiguredAppRuntime(t, d, fakeAppRuntimeHandlers{
		command: func(*fakeAppRuntime, appCommandRequest) (json.RawMessage, error) {
			return json.RawMessage(`{"note":"` + strings.Repeat("x", appCommandPayloadLimit) + `"}`), nil
		},
	})

	result := newAppCommandCaller().invoke(t, d, "reviewer", "approve", "")

	mustFail(t, result, "approve", "reviewer", "262144", "document")
	if result.Payload != nil {
		t.Fatalf("the oversized answer reached the caller anyway")
	}
	rows := invocationsOf(t, d, "reviewer")
	if len(rows) != 1 || rows[0].Status != appInvocationStatusError {
		t.Fatalf("invocations = %+v, want one recorded failure", rows)
	}
}

func TestAppCommandCarriesAThrownHandlerBackToTheCaller(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	startConfiguredAppRuntime(t, d, fakeAppRuntimeHandlers{
		command: func(*fakeAppRuntime, appCommandRequest) (json.RawMessage, error) {
			return nil, errTest("Error: approve needs an id")
		},
	})

	mustFail(t, newAppCommandCaller().invoke(t, d, "reviewer", "approve", ""), "approve needs an id")

	rows := invocationsOf(t, d, "reviewer")
	if len(rows) != 1 || rows[0].Status != appInvocationStatusError {
		t.Fatalf("invocations = %+v, want one recorded failure", rows)
	}
}

func TestAppCommandAbandonsAHandlerThatNeverReturns(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 300 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))
	startConfiguredAppRuntime(t, d, fakeAppRuntimeHandlers{
		command: func(f *fakeAppRuntime, _ appCommandRequest) (json.RawMessage, error) {
			f.freezeLoop()
			select {}
		},
	})

	result := newAppCommandCaller().invoke(t, d, "reviewer", "approve", "")

	mustFail(t, result, "approve", "reviewer", "300ms")
	if status := appStatus(t, d, "reviewer"); status.AppStatusResult.Stall != nil {
		t.Fatalf("a failed command advanced the stall clock: %+v", status.AppStatusResult.Stall)
	}
}

func TestAppCommandQueuedBehindABusyAppRefusesInsideItsOwnBudget(t *testing.T) {
	d := newAppDaemon(t)
	d.appDispatchWait = 300 * time.Millisecond
	d.appPingWait = 50 * time.Millisecond
	installApp(t, d, "reviewer", commandManifest(appbuild.Command{Name: "approve"}))

	lane := d.appLane("reviewer")
	lane.Lock()
	defer lane.Unlock()

	result := newAppCommandCaller().invoke(t, d, "reviewer", "approve", "")

	mustFail(t, result, "approve", "reviewer", "300ms", "never got a turn", "attn app logs reviewer")
}

type errTest string

func (e errTest) Error() string { return string(e) }
