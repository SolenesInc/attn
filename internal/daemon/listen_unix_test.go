package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListenUnixAtomicallyAcceptsAsSoonAsThePathExists(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "attn.sock")
	listener, err := listenUnixAtomically(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, err := os.Stat(path + ".listen"); !os.IsNotExist(err) {
		t.Fatalf("staging path still present: %v", err)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial after the path appeared: %v", err)
	}
	conn.Close()

	listener.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("close must leave the path to the daemon's own shutdown removal: %v", err)
	}
}

func TestListenUnixAtomicallyReplacesAStaleSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "attn.sock")
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()

	listener, err := listenUnixAtomically(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial replaced socket: %v", err)
	}
	conn.Close()
}
