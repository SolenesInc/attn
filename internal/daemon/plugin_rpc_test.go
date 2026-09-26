package daemon

import (
	"encoding/json"
	"io"
	"net"
	"testing"
	"testing/synctest"
)

func TestDaemon_PluginConnectionGenerationDrivesDisconnectRecovery(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		clock := newFakePluginClock()
		launcher := &fakePluginLauncher{}
		d.pluginSupervisor = newTestPluginSupervisor(t, clock, launcher)
		if err := d.pluginSupervisor.Ensure(pluginManifest{Name: "supervised"}); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		initial, _ := d.pluginSupervisor.Snapshot("supervised")
		client, done := startPluginPipeGeneration(t, d, "supervised", nil, initial.Generation)
		connected, _ := d.pluginSupervisor.Snapshot("supervised")
		if connected.Phase != pluginPhaseConnected {
			t.Fatalf("phase=%q after hello, want connected", connected.Phase)
		}
		_ = client.Close()
		<-done

		clock.Advance(pluginDisconnectGrace)
		requireSupervisor(t, func() bool {
			snapshot, _ := d.pluginSupervisor.Snapshot("supervised")
			return snapshot.Phase == pluginPhaseBackoff
		}, "the expired disconnect grace did not land the plugin in backoff")
		clock.Advance(pluginRestartBackoff[0])
		requireSupervisor(t, func() bool { return launcher.count() == 2 }, "the elapsed backoff did not restart the plugin")
		restarted, _ := d.pluginSupervisor.Snapshot("supervised")
		if restarted.Generation == initial.Generation {
			t.Fatal("restart did not advance generation")
		}

		serverConn, staleConn := net.Pipe()
		staleDone := make(chan struct{})
		go func() {
			defer close(staleDone)
			d.handleConnection(serverConn)
		}()
		sendPluginHelloWithGeneration(t, staleConn, "supervised", nil, initial.Generation)
		if response := decodeJSONRPCMessage(t, staleConn); response.Error == nil {
			t.Fatal("stale generation hello succeeded")
		}
		_ = staleConn.Close()
		<-staleDone

		current, currentDone := startPluginPipeGeneration(t, d, "supervised", nil, restarted.Generation)
		d.pluginSupervisor.Stop("supervised")
		_ = current.Close()
		<-currentDone
	})
}

func startPluginPipe(t *testing.T, d *Daemon, name string, surfaces []string) (net.Conn, <-chan struct{}) {
	return startPluginPipeGeneration(t, d, name, surfaces, 1)
}

func startPluginPipeGeneration(t *testing.T, d *Daemon, name string, surfaces []string, generation uint64) (net.Conn, <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHelloWithGeneration(t, clientConn, name, surfaces, generation)
	helloResp := decodeJSONRPCMessage(t, clientConn)
	if helloResp.Error != nil {
		t.Fatalf("hello error = %#v, want nil", helloResp.Error)
	}
	return clientConn, done
}

func sendPluginHello(t *testing.T, conn net.Conn, name string) {
	sendPluginHelloWithSurfaces(t, conn, name, nil)
}

func sendPluginHelloWithSurfaces(t *testing.T, conn net.Conn, name string, surfaces []string) {
	sendPluginHelloWithGeneration(t, conn, name, surfaces, 1)
}

func sendPluginHelloWithGeneration(t *testing.T, conn net.Conn, name string, surfaces []string, generation uint64) {
	t.Helper()
	params, err := json.Marshal(pluginHelloParams{
		Name:           name,
		Version:        "0.1.0",
		AttnAPIVersion: pluginAPIVersion,
		Generation:     generation,
		Surfaces:       surfaces,
	})
	if err != nil {
		t.Fatalf("marshal hello params: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "hello",
		Params:  params,
	}); err != nil {
		t.Fatalf("encode hello: %v", err)
	}
}

func decodeJSONRPCMessage(t *testing.T, conn net.Conn) jsonRPCMessage {
	t.Helper()
	var frame []byte
	var b [1]byte
	for b[0] != '\n' {
		if _, err := io.ReadFull(conn, b[:]); err != nil {
			t.Fatalf("read JSON-RPC frame: %v", err)
		}
		frame = append(frame, b[0])
	}
	var msg jsonRPCMessage
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatalf("decode JSON-RPC message: %v", err)
	}
	return msg
}
