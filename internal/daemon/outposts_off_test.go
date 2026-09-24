package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
)

type outpostsOffDaemon struct {
	t          *testing.T
	d          *Daemon
	client     *wsClient
	endpointID string
	sshLog     string
}

func startOutpostsOffDaemon(t *testing.T) *outpostsOffDaemon {
	t.Helper()
	shimDir := t.TempDir()
	sshLog := filepath.Join(shimDir, "ssh.log")
	if err := os.WriteFile(filepath.Join(shimDir, "ssh"), []byte("#!/bin/sh\necho \"$@\" >> '"+sshLog+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ATTN_REMOTE_SOCKET_PATH", "/home/tester/.attn/harness/run-1/attn.sock")
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", strconv.Itoa(port))

	d := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	endpoint, err := d.store.AddEndpoint("gpu-box", "gpu.example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	go d.Start()
	t.Cleanup(func() {
		d.Stop()
		d.stopEventBus()
		d.sessionInputs().stopRetries()
	})
	waitForSocket(t, d.socketPath, 10*time.Second)
	<-d.Started()
	waitFor(t, "the daemon to finish recovering", func() bool { return !d.isRecovering() })

	client := newWorkspaceProtocolTestClient()
	client.setIdentity("tauri-app", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})
	return &outpostsOffDaemon{t: t, d: d, client: client, endpointID: endpoint.ID, sshLog: sshLog}
}

func (w *outpostsOffDaemon) send(command map[string]any) [][]byte {
	w.t.Helper()
	data, err := json.Marshal(command)
	if err != nil {
		w.t.Fatal(err)
	}
	w.d.handleClientMessage(w.client, data)
	return drainClientPayloads(w.t, w.client)
}

func (w *outpostsOffDaemon) endpointActionError(command map[string]any) string {
	w.t.Helper()
	for _, payload := range w.send(command) {
		if eventName(w.t, payload) != protocol.EventEndpointActionResult {
			continue
		}
		var result protocol.EndpointActionResultMessage
		decodeInto(w.t, payload, &result)
		if result.Success {
			w.t.Fatalf("%s succeeded, want a refusal", command["cmd"])
		}
		return protocol.Deref(result.Error)
	}
	w.t.Fatalf("%s was not answered with endpoint_action_result", command["cmd"])
	return ""
}

func (w *outpostsOffDaemon) commandError(command map[string]any) string {
	w.t.Helper()
	for _, payload := range w.send(command) {
		if eventName(w.t, payload) != protocol.EventCommandError {
			continue
		}
		var event protocol.WebSocketEvent
		decodeInto(w.t, payload, &event)
		return protocol.Deref(event.Error)
	}
	w.t.Fatalf("%s was not refused with command_error", command["cmd"])
	return ""
}

func (w *outpostsOffDaemon) listedEndpoints() []protocol.EndpointInfo {
	w.t.Helper()
	for _, payload := range w.send(map[string]any{"cmd": protocol.CmdListEndpoints}) {
		if eventName(w.t, payload) == protocol.EventEndpointsUpdated {
			var updated protocol.EndpointsUpdatedMessage
			decodeInto(w.t, payload, &updated)
			return updated.Endpoints
		}
	}
	w.t.Fatal("list_endpoints was not answered with endpoints_updated")
	return nil
}

func (w *outpostsOffDaemon) assertSSHNeverRan() {
	w.t.Helper()
	if data, err := os.ReadFile(w.sshLog); !errors.Is(err, os.ErrNotExist) {
		w.t.Fatalf("ssh ran against the endpoint host: %q (%v)", data, err)
	}
}

func TestADaemonListsSavedEndpointsAsUnsupportedAndNeverConnects(t *testing.T) {
	w := startOutpostsOffDaemon(t)

	if got := w.d.endpointInfos(); len(got) != 1 || got[0].Status != hub.StatusUnsupported {
		t.Fatalf("endpoints = %+v, want the saved endpoint listed as unsupported", got)
	}
	enabled := w.send(map[string]any{"cmd": protocol.CmdUpdateEndpoint, "endpoint_id": w.endpointID, "enabled": true})
	if len(enabled) == 0 {
		t.Fatal("update_endpoint was not answered")
	}
	listed := w.listedEndpoints()
	if len(listed) != 1 || listed[0].ID != w.endpointID || listed[0].Status != hub.StatusUnsupported ||
		protocol.Deref(listed[0].StatusMessage) != hub.UnsupportedReason {
		t.Fatalf("listed endpoints = %+v, want gpu-box unsupported with the release reason", listed)
	}
	w.assertSSHNeverRan()
}

func TestEveryEndpointCommandRefusesWithoutAnSSHCallOrALocalFallback(t *testing.T) {
	w := startOutpostsOffDaemon(t)

	for _, command := range []map[string]any{
		{"cmd": protocol.CmdAddEndpoint, "name": "new-box", "ssh_target": "new.example.test"},
		{"cmd": protocol.CmdBootstrapEndpoint, "endpoint_id": w.endpointID},
		{"cmd": protocol.CmdSetEndpointRemoteWeb, "endpoint_id": w.endpointID, "enabled": true},
	} {
		if got := w.endpointActionError(command); !strings.Contains(got, hub.UnsupportedReason) {
			t.Errorf("%s refused with %q, want the release reason", command["cmd"], got)
		}
	}
	if saved := w.d.store.ListEndpoints(); len(saved) != 1 {
		t.Fatalf("saved endpoints = %+v, want only the original", saved)
	}

	for _, command := range []map[string]any{
		{"cmd": protocol.CmdSpawnSession, "id": "remote-launch", "cwd": "/srv/repo", "agent": "claude", "cols": 80, "rows": 24, "endpoint_id": w.endpointID},
		{"cmd": protocol.CmdBrowseDirectory, "input_path": "/srv", "endpoint_id": w.endpointID},
		{"cmd": protocol.CmdGetRecentLocations, "endpoint_id": w.endpointID},
	} {
		if got := w.commandError(command); !strings.Contains(got, hub.UnsupportedReason) {
			t.Errorf("%s refused with %q, want the release reason", command["cmd"], got)
		}
	}
	w.send(map[string]any{"cmd": protocol.CmdRemoveEndpoint, "endpoint_id": w.endpointID})
	if saved := w.d.store.ListEndpoints(); len(saved) != 0 {
		t.Fatalf("saved endpoints after remove = %+v, want none", saved)
	}
	unknown := w.commandError(map[string]any{
		"cmd": protocol.CmdSpawnSession, "id": "ghost-launch", "cwd": "/srv/repo", "agent": "claude", "cols": 80, "rows": 24, "endpoint_id": w.endpointID,
	})
	if unknown != "endpoint not found: "+w.endpointID {
		t.Errorf("spawn on a removed endpoint refused with %q, want endpoint not found", unknown)
	}
	for _, id := range []string{"remote-launch", "ghost-launch"} {
		if w.d.store.Get(id) != nil {
			t.Errorf("%s was spawned locally instead of refused", id)
		}
	}
	w.assertSSHNeverRan()
}
