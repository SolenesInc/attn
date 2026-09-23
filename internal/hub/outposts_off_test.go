package hub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func installSSHShim(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "ssh.log")
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ATTN_REMOTE_SOCKET_PATH", "/home/tester"+remoteHarnessRootMarker+"run-1/attn.sock")
	return log
}

func assertSSHNeverRan(t *testing.T, log string) {
	t.Helper()
	if data, err := os.ReadFile(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ssh ran against the endpoint host: %q (%v)", data, err)
	}
}

func savedEndpoints(t *testing.T) (*store.Store, []store.EndpointRecord) {
	t.Helper()
	endpointStore := store.New()
	var records []store.EndpointRecord
	for _, name := range []string{"gpu-box", "dev-box"} {
		record, err := endpointStore.AddEndpoint(name, name+".example.test", "")
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, *record)
	}
	return endpointStore, records
}

func TestStartedManagerRunsNoEndpointLoopAndListsEveryEndpointAsUnsupported(t *testing.T) {
	log := installSSHShim(t)
	endpointStore, records := savedEndpoints(t)
	var published []protocol.EndpointInfo
	manager := NewManager(endpointStore, func(info protocol.EndpointInfo) { published = append(published, info) }, nil, nil, nil, nil)

	manager.Start(context.Background())
	if _, err := manager.UpdateEndpoint(records[0].ID, store.EndpointUpdate{Enabled: protocol.Ptr(true)}); err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}

	for id, runtime := range manager.runtimes {
		if runtime.cancel != nil {
			t.Errorf("endpoint %s has a running connection loop", id)
		}
	}
	listed := manager.List()
	if len(listed) != len(records) {
		t.Fatalf("List = %+v, want both saved endpoints", listed)
	}
	for _, info := range append(listed, published...) {
		if info.Status != StatusUnsupported || protocol.Deref(info.StatusMessage) != UnsupportedReason {
			t.Errorf("endpoint %s reports %q (%q), want unsupported with the release reason", info.Name, info.Status, protocol.Deref(info.StatusMessage))
		}
	}
	manager.Stop()
	assertSSHNeverRan(t, log)
}

func TestEveryRemoteOperationRefusesAnUnsupportedEndpointBeforeSideEffects(t *testing.T) {
	log := installSSHShim(t)
	endpointStore, records := savedEndpoints(t)
	manager := NewManager(endpointStore, nil, nil, nil, nil, nil)
	manager.Start(context.Background())
	defer manager.Stop()
	id := records[0].ID
	manager.ReplaceRemoteSessions(id, []protocol.Session{{ID: "remote-session"}})
	ctx := context.Background()

	operations := map[string]func() error{
		"bootstrap":  func() error { return manager.BootstrapEndpoint(id) },
		"remote web": func() error { return manager.SetEndpointRemoteWeb(ctx, id, true) },
		"forward":    func() error { return manager.ForwardEndpointCommand(ctx, id, []byte(`{"cmd":"spawn_session"}`)) },
		"pty":        func() error { return manager.ForwardPTYCommand(ctx, "remote-session", []byte(`{"cmd":"pty_input"}`)) },
		"close": func() error {
			return manager.ForwardSessionClose(ctx, id, "remote-session", []byte(`{"cmd":"close_session"}`))
		},
		"rename": func() error {
			return manager.ForwardSessionRename(ctx, id, "remote-session", []byte(`{"cmd":"rename_session"}`))
		},
		"browser control": func() error {
			_, err := manager.ForwardBrowserControl(ctx, id, protocol.BrowserControlMessage{RequestID: protocol.Ptr("req-1")})
			return err
		},
		"refusal check": func() error { return manager.EndpointRefusal(id) },
	}
	for name, operation := range operations {
		err := operation()
		var unsupported *UnsupportedEndpointError
		if !errors.As(err, &unsupported) || !errors.Is(err, ErrOutpostsOff) {
			t.Errorf("%s = %v, want the unsupported-endpoint refusal", name, err)
			continue
		}
		if unsupported.EndpointID != id || unsupported.Name != "gpu-box" {
			t.Errorf("%s refusal names %+v, want gpu-box (%s)", name, unsupported, id)
		}
	}
	if manager.runtimes[id].pendingBootstrap || manager.runtimes[id].cancel != nil {
		t.Fatal("a refused bootstrap still armed an install or started a loop")
	}
	assertSSHNeverRan(t, log)
}

func TestAddingAnEndpointIsRefusedWithoutSavingIt(t *testing.T) {
	endpointStore := store.New()
	manager := NewManager(endpointStore, nil, nil, nil, nil, nil)

	err := manager.AddEndpoint("gpu-box")

	if !errors.Is(err, ErrOutpostsOff) {
		t.Fatalf("AddEndpoint = %v, want the release refusal", err)
	}
	if saved := endpointStore.ListEndpoints(); len(saved) != 0 {
		t.Fatalf("saved endpoints = %+v, want none", saved)
	}
}

func TestRemovingAnUnsupportedEndpointNeverReachesItsHost(t *testing.T) {
	log := installSSHShim(t)
	endpointStore, records := savedEndpoints(t)
	manager := NewManager(endpointStore, nil, nil, nil, nil, nil)
	manager.Start(context.Background())

	if err := manager.RemoveEndpoint(records[0].ID); err != nil {
		t.Fatalf("RemoveEndpoint: %v", err)
	}
	manager.Stop()

	if saved := endpointStore.ListEndpoints(); len(saved) != 1 || saved[0].ID != records[1].ID {
		t.Fatalf("saved endpoints = %+v, want only %s", saved, records[1].ID)
	}
	assertSSHNeverRan(t, log)
}
