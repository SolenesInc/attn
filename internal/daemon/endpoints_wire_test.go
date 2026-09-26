package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEndpointsAreEditedCanonicallyAndSurviveARestart(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	refuseSSH(t)
	w := newWorld(t)
	app := w.App()

	gpu := addEndpoint(t, app, "gpu-box", "user@example", nil)
	if got := awaitEndpoint(app, gpu); got.Name != "gpu-box" || got.SshTarget != "user@example" || !protocol.Deref(got.Enabled) || got.Instance != nil {
		t.Fatalf("added endpoint = %+v, want enabled on the default instance", got)
	}
	for _, instance := range []struct{ sent, stored string }{
		{"dev", "dev"},
		{"DEV", "dev"},
		{"default", ""},
		{"DEFAULT", ""},
		{"  default  ", ""},
	} {
		id := addEndpoint(t, app, "box-"+instance.sent, "user@box", protocol.Ptr(instance.sent))
		if got := protocol.Deref(awaitEndpoint(app, id).Instance); got != instance.stored {
			t.Errorf("added with instance %q, the endpoint runs instance %q; want %q", instance.sent, got, instance.stored)
		}
		removeEndpoint(t, app, id)
	}
	refused := testworld.Request(app, protocol.AddEndpointMessage{
		Cmd: protocol.CmdAddEndpoint, Name: "bad", SshTarget: "user@bad", Instance: protocol.Ptr("with space"),
	}, protocol.EventEndpointActionResult, func(r protocol.EndpointActionResultMessage) bool { return r.Action == "add" })
	if refused.Success || refused.Error == nil {
		t.Errorf("adding an endpoint with an invalid instance = %+v, want it refused", refused)
	}

	doomed := addEndpoint(t, app, "doomed", "user@doomed", nil)
	removeEndpoint(t, app, doomed)

	updateEndpoint(t, app, protocol.UpdateEndpointMessage{
		EndpointID: gpu, Name: protocol.Ptr("gpu-box-2"), SshTarget: protocol.Ptr("dev@example"),
		Enabled: protocol.Ptr(false), Instance: protocol.Ptr("DEV"),
	})
	live := awaitEndpoints(app, func(endpoints []protocol.EndpointInfo) bool {
		found := findEndpoint(endpoints, gpu)
		return found != nil && found.Name == "gpu-box-2"
	})
	if len(live) != 1 || live[0].SshTarget != "dev@example" || protocol.Deref(live[0].Enabled) || protocol.Deref(live[0].Instance) != "dev" {
		t.Fatalf("endpoints after the update = %+v, want only gpu-box-2 at dev@example, disabled, instance dev", live)
	}

	w.restart()
	app = w.App()
	app.Send(protocol.ListEndpointsMessage{Cmd: protocol.CmdListEndpoints})
	listed := awaitEndpoints(app, func(endpoints []protocol.EndpointInfo) bool { return len(endpoints) > 0 })
	if len(listed) != 1 || listed[0].ID != gpu || listed[0].Name != "gpu-box-2" || protocol.Deref(listed[0].Instance) != "dev" || protocol.Deref(listed[0].Enabled) {
		t.Fatalf("endpoints after a restart = %+v, want only the updated gpu-box-2", listed)
	}

	updateEndpoint(t, app, protocol.UpdateEndpointMessage{EndpointID: gpu, Instance: protocol.Ptr("")})
	if cleared := awaitEndpoint(app, gpu, func(e protocol.EndpointInfo) bool { return e.Instance == nil }); cleared.Name != "gpu-box-2" {
		t.Errorf("clearing the instance changed the endpoint to %+v", cleared)
	}
}

func refuseSSH(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nexit 255\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func addEndpoint(t *testing.T, app *testworld.Peer, name, target string, instance *string) string {
	t.Helper()
	result := testworld.Request(app, protocol.AddEndpointMessage{
		Cmd: protocol.CmdAddEndpoint, Name: name, SshTarget: target, Instance: instance,
	}, protocol.EventEndpointActionResult, func(r protocol.EndpointActionResultMessage) bool { return r.Action == "add" })
	if !result.Success {
		t.Fatalf("add endpoint %s refused: %s", name, protocol.Deref(result.Error))
	}
	return protocol.Deref(result.EndpointID)
}

func updateEndpoint(t *testing.T, app *testworld.Peer, update protocol.UpdateEndpointMessage) {
	t.Helper()
	update.Cmd = protocol.CmdUpdateEndpoint
	result := testworld.Request(app, update, protocol.EventEndpointActionResult, func(r protocol.EndpointActionResultMessage) bool {
		return r.Action == "update" && protocol.Deref(r.EndpointID) == update.EndpointID
	})
	if !result.Success {
		t.Fatalf("update endpoint %s refused: %s", update.EndpointID, protocol.Deref(result.Error))
	}
}

func removeEndpoint(t *testing.T, app *testworld.Peer, id string) {
	t.Helper()
	result := testworld.Request(app, protocol.RemoveEndpointMessage{Cmd: protocol.CmdRemoveEndpoint, EndpointID: id},
		protocol.EventEndpointActionResult, func(r protocol.EndpointActionResultMessage) bool {
			return r.Action == "remove" && protocol.Deref(r.EndpointID) == id
		})
	if !result.Success {
		t.Fatalf("remove endpoint %s refused: %s", id, protocol.Deref(result.Error))
	}
}

func awaitEndpoint(app *testworld.Peer, id string, match ...func(protocol.EndpointInfo) bool) protocol.EndpointInfo {
	app.T.Helper()
	listed := awaitEndpoints(app, func(endpoints []protocol.EndpointInfo) bool {
		found := findEndpoint(endpoints, id)
		return found != nil && (len(match) == 0 || match[0](*found))
	})
	return *findEndpoint(listed, id)
}

func awaitEndpoints(app *testworld.Peer, match func([]protocol.EndpointInfo) bool) []protocol.EndpointInfo {
	app.T.Helper()
	return testworld.Await(app, protocol.EventEndpointsUpdated, func(m protocol.EndpointsUpdatedMessage) bool {
		return match(m.Endpoints)
	}).Endpoints
}

func findEndpoint(endpoints []protocol.EndpointInfo, id string) *protocol.EndpointInfo {
	for i := range endpoints {
		if endpoints[i].ID == id {
			return &endpoints[i]
		}
	}
	return nil
}
