package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func newFsDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	return d
}

func trustedFsClient(bufSize int) *wsClient {
	client := &wsClient{send: make(chan outboundMessage, bufSize), trustedTauriOrigin: true}
	client.setBrowserHostAuthenticated(true)
	client.setIdentity("tauri-app", "test", nil)
	return client
}

func fsWatch(t *testing.T, d *Daemon, client *wsClient, requestID, root string) protocol.FsWatchResultMessage {
	t.Helper()
	d.handleFsWatch(client, requestID, root)
	var res protocol.FsWatchResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

func fsUnwatch(t *testing.T, d *Daemon, client *wsClient, requestID, root string) protocol.FsUnwatchResultMessage {
	t.Helper()
	d.handleFsUnwatch(client, requestID, root)
	var res protocol.FsUnwatchResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

func waitForFsChangeWithRoot(t *testing.T, ch chan outboundMessage, origin string) protocol.FsChangedMessage {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			var ev protocol.FsChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err != nil {
				continue
			}
			if ev.Event == protocol.EventFsChanged && ev.Origin == origin {
				return ev
			}
		case <-deadline:
			t.Fatalf("no fs_changed with origin %q was broadcast", origin)
			return protocol.FsChangedMessage{}
		}
	}
}

func assertNoFsChangedForRoot(t *testing.T, ch chan outboundMessage, root, origin string, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	var drained []outboundMessage
	for {
		select {
		case msg := <-ch:
			var ev protocol.FsChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err == nil &&
				ev.Event == protocol.EventFsChanged && ev.Root == root && ev.Origin == origin {
				t.Fatalf("unexpected fs_changed(root=%q origin=%q)", root, origin)
			}
			drained = append(drained, msg)
		case <-deadline:
			for _, m := range drained {
				ch <- m
			}
			return
		}
	}
}

func TestFsWatchRefcountedAcrossClients(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()

	clientA := trustedFsClient(8)
	clientB := trustedFsClient(8)
	if res := fsWatch(t, d, clientA, "wa", root); !res.Success {
		t.Fatalf("fs_watch A = %+v", res)
	}
	if res := fsWatch(t, d, clientB, "wb", root); !res.Success {
		t.Fatalf("fs_watch B = %+v", res)
	}

	d.dropFsWatchClient(clientA)

	if err := os.WriteFile(filepath.Join(root, "still-watched.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := waitForFsChangeWithRoot(t, clientB.send, originExternal)
	if ev.Root != root || !slices.Contains(ev.Paths, "still-watched.txt") {
		t.Fatalf("fs_changed after dropping one client = %+v", ev)
	}

	if res := fsUnwatch(t, d, clientB, "ub", root); !res.Success {
		t.Fatalf("fs_unwatch B = %+v", res)
	}
	if err := os.WriteFile(filepath.Join(root, "no-longer-watched.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertNoFsChangedForRoot(t, clientB.send, root, originExternal, 700*time.Millisecond)
}
