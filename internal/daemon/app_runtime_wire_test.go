package daemon_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAppsWithoutARuntimeBinaryRecordARuntimeErrorAndStayEnabled(t *testing.T) {
	t.Setenv("ATTN_APP_RUNTIME_HOST", filepath.Join(t.TempDir(), "not-installed"))
	w := newWorld(t)
	cli := w.Client()
	applySubscribedApp(t, cli, "greeter")
	invocations := watchAppInvocations(t, w, "greeter")

	if _, err := cli.CreateTicket("planner", "Price the order", "", ""); err != nil {
		t.Fatal(err)
	}
	failed := awaitAppInvocation(t, invocations)
	if failed.Status != "runtime_error" || !strings.Contains(protocol.Deref(failed.Error), "ATTN_APP_RUNTIME_HOST") {
		t.Errorf("the delivery = %+v, want a runtime_error saying how to point attn at a runtime", failed)
	}
	status := appStatus(t, cli, "greeter")
	if status.Stall != nil || status.App.Consumer == nil || !status.App.Consumer.Enabled {
		t.Errorf("after a missing runtime: stall %+v, consumer %+v; want the app enabled and off the auto-disable clock", status.Stall, status.App.Consumer)
	}
}

func TestAppRuntimeStatusIsHonestBeforeAndAfterARestart(t *testing.T) {
	host := writeAppRuntimeStub(t, "exec sleep 60")
	t.Setenv("ATTN_APP_RUNTIME_HOST", host)
	w := newWorld(t)
	applySubscribedApp(t, w.Client(), "greeter")
	w.restart()
	cli := w.Client()

	before := appRuntimeStatus(t, cli)
	if before.Runtime != nil {
		t.Fatalf("a daemon that has never run an app reports a runtime: %+v", before.Runtime)
	}
	if protocol.Deref(before.HostPath) != host || before.Apps != 1 || before.AppsEnabled != 1 || !strings.HasPrefix(before.LogPath, filepath.Dir(w.Socket)+string(filepath.Separator)) {
		t.Errorf("status before any runtime = %+v, want host %s, 1 of 1 apps enabled and a log under the data dir", before, host)
	}

	started, err := cli.AppRuntimeRestart()
	if err != nil {
		t.Fatalf("runtime restart: %v", err)
	}
	if started.Was != "stopped" || started.Runtime.Desired != "running" {
		t.Errorf("runtime restart = %+v, want it started from stopped", started)
	}
	if after := appRuntimeStatus(t, cli); after.Runtime == nil {
		t.Error("status reports no runtime after one was started")
	}
}

func TestARuntimeFromAStaleInstallIsRefusedAtHello(t *testing.T) {
	w := newWorld(t)
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"jsonrpc":"2.0","id":"1","method":"app_runtime.hello","params":{"generation":1,"api_version":99,"pid":7}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&answer); err != nil {
		t.Fatalf("read the answer to the hello: %v", err)
	}
	if answer.Error == nil || !strings.Contains(answer.Error.Message, "stale install") {
		t.Errorf("the hello of api version 99 was answered %+v, want a refusal naming a stale install", answer.Error)
	}
	if status := appRuntimeStatus(t, w.Client()); status.Runtime != nil {
		t.Errorf("the refused runtime shows in the status: %+v", status.Runtime)
	}
}

func TestAppLogsShowEachAppItsOwnLinesWithinTheirCeiling(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	applySubscribedApp(t, cli, "greeter")
	logPath := appRuntimeStatus(t, cli).LogPath

	empty, err := cli.AppLogs("greeter", 0)
	if err != nil || len(empty.Lines) != 0 || empty.Path != logPath {
		t.Fatalf("logs before the runtime ever ran = %+v, %v; want no lines and the path %s", empty, err, logPath)
	}

	writeAppArtifact(t, logPath, []byte(strings.Join([]string{
		"[runtime] starting, api version 1",
		"[app greeter] hello from greeter",
		"[app auditor] hello from auditor",
		"[app greeter] and again",
	}, "\n")+"\n"))
	own, err := cli.AppLogs("greeter", 0)
	if err != nil || strings.Join(own.Lines, "|") != "hello from greeter|and again" || own.Path != logPath || own.Truncated {
		t.Errorf("greeter's logs = %+v, %v; want only its two lines, untagged, from %s", own, err, logPath)
	}
	whole, err := cli.AppLogs("runtime", 0)
	if err != nil || len(whole.Lines) != 4 || !strings.Contains(whole.Lines[0], "api version 1") || whole.Path != logPath {
		t.Errorf("the runtime's logs = %+v, %v; want every line from %s", whole, err, logPath)
	}
	last, err := cli.AppLogs("greeter", 1)
	if err != nil || strings.Join(last.Lines, "|") != "and again" || !last.Truncated {
		t.Errorf("greeter's last line = %+v, %v; want the newest line and the older one reported dropped", last, err)
	}
	if _, err := cli.AppLogs("greeter", 10001); err == nil || !strings.Contains(err.Error(), "10001") ||
		!strings.Contains(err.Error(), "10000") || !strings.Contains(err.Error(), logPath) {
		t.Errorf("asking for 10001 lines = %v, want a refusal naming the ask, the ceiling and the log", err)
	}
}

func TestAParkedRuntimeStaysParkedAcrossARestartUntilTheUserRestartsIt(t *testing.T) {
	launches := filepath.Join(t.TempDir(), "launches")
	t.Setenv("ATTN_APP_RUNTIME_HOST", writeAppRuntimeStub(t, "echo started >> "+launches+"\nexit 3"))
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		applySubscribedApp(t, cli, "greeter")
		app := w.App()
		if _, err := cli.AppRuntimeRestart(); err != nil {
			t.Fatalf("start the runtime: %v", err)
		}
		w.advance(3 * time.Minute)
		testworld.Await(app, protocol.EventNotificationsUpdated, func(m protocol.NotificationsUpdatedMessage) bool {
			return protocol.Deref(m.CriticalTitle) == "Apps stopped running"
		})
		parked := appRuntimeStatus(t, cli).Runtime
		if parked == nil || parked.Phase != "parked" || parked.ParkedAt == nil || parked.RestartAttempt == 0 || !strings.Contains(protocol.Deref(parked.LastExit), "3") {
			t.Fatalf("the crash-looping runtime = %+v, want it parked with its attempts and exit 3", parked)
		}
		launched := appRuntimeLaunches(t, launches)

		w.restart()
		cli = w.Client()
		restored := appRuntimeStatus(t, cli).Runtime
		if restored == nil || restored.Phase != "parked" || protocol.Deref(restored.ParkedAt) != protocol.Deref(parked.ParkedAt) ||
			restored.RestartAttempt != parked.RestartAttempt || protocol.Deref(restored.LastExit) != protocol.Deref(parked.LastExit) {
			t.Fatalf("after a daemon restart the runtime = %+v, want the park as it was: %+v", restored, parked)
		}
		feed := listNotifications(w.App())
		if parkedNotes := appRuntimeParkedNotifications(feed); parkedNotes != 1 {
			t.Errorf("app runtime parked notifications after the restart = %d, want the original one", parkedNotes)
		}

		invocations := watchAppInvocations(t, w, "greeter")
		if _, err := cli.CreateTicket("planner", "Price the order", "", ""); err != nil {
			t.Fatal(err)
		}
		failed := awaitAppInvocation(t, invocations)
		if failed.Status != "runtime_error" || !strings.Contains(protocol.Deref(failed.Error), "parked") ||
			!strings.Contains(protocol.Deref(failed.Error), "attn app runtime restart") {
			t.Errorf("a delivery into the park = %+v, want a runtime_error saying it is parked and how to restart it", failed)
		}
		if runtime := appRuntimeStatus(t, cli).Runtime; runtime == nil || runtime.Phase != "parked" {
			t.Errorf("after a delivery into the park the runtime = %+v, want it still parked", runtime)
		}
		if status := appStatus(t, cli, "greeter"); status.Stall != nil {
			t.Errorf("the park put greeter on the auto-disable clock: %+v", status.Stall)
		}
		if got := appRuntimeLaunches(t, launches); got != launched {
			t.Errorf("the host launched %d more time(s) after it was parked", got-launched)
		}

		revived, err := cli.AppRuntimeRestart()
		if err != nil {
			t.Fatalf("runtime restart: %v", err)
		}
		if revived.Was != "parked" || revived.Runtime.Phase == "parked" || revived.Runtime.ParkedAt != nil {
			t.Errorf("runtime restart of the park = %+v, want it unparked", revived)
		}
		w.restart()
		if status := appRuntimeStatus(t, w.Client()); status.Runtime != nil && (status.Runtime.Phase == "parked" || status.Runtime.ParkedAt != nil) {
			t.Errorf("after the user restarted the runtime, a daemon restart reports %+v, want the park gone", status.Runtime)
		}
	})
}

func applySubscribedApp(t *testing.T, cli *client.Client, name string) *protocol.AppApplyResult {
	t.Helper()
	declaration := appManifestDeclaration(t, appbuild.Manifest{Name: name, Subscribe: []appbuild.Subscribe{{Events: []string{"ticket.*"}}}})
	return applyAppVersion(t, cli, name, declaration, "export default {}\n")
}

func appRuntimeStatus(t *testing.T, cli *client.Client) *protocol.AppRuntimeStatusResult {
	t.Helper()
	status, err := cli.AppRuntimeStatus()
	if err != nil {
		t.Fatalf("runtime status: %v", err)
	}
	return status
}

func writeAppRuntimeStub(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attn-app-runtime")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func appRuntimeLaunches(t *testing.T, marks string) int {
	t.Helper()
	data, err := os.ReadFile(marks)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "started")
}

func appRuntimeParkedNotifications(feed protocol.NotificationListResultMessage) int {
	count := 0
	for _, n := range feed.Notifications {
		if n.Kind == "app_runtime_parked" {
			count++
		}
	}
	return count
}

func watchAppInvocations(t *testing.T, w *world, name string) <-chan protocol.AppInvocationInfo {
	t.Helper()
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := json.NewEncoder(conn).Encode(protocol.AppWatchMessage{Cmd: protocol.CmdAppWatch, Name: name}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(conn)
	var ack protocol.Response
	if err := decoder.Decode(&ack); err != nil || !ack.Ok {
		t.Fatalf("watch %s = %+v, %v", name, ack, err)
	}
	invocations := make(chan protocol.AppInvocationInfo, 64)
	go func() {
		defer close(invocations)
		for {
			var resp protocol.Response
			if err := decoder.Decode(&resp); err != nil {
				return
			}
			if resp.AppWatchResult != nil {
				invocations <- resp.AppWatchResult.Invocation
			}
		}
	}()
	return invocations
}

func awaitAppInvocation(t *testing.T, invocations <-chan protocol.AppInvocationInfo) protocol.AppInvocationInfo {
	t.Helper()
	select {
	case invocation, ok := <-invocations:
		if !ok {
			t.Fatal("the app watch ended before an invocation")
		}
		return invocation
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("no invocation after %s", fakeagent.HangGuard)
		return protocol.AppInvocationInfo{}
	}
}
