package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceCleanRemovesStaleMastersAndKeepsOtherInstances(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "master-clean-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("ATTN_HARNESS_DATA_DIR", root)
	control := filepath.Join(root, "masters")
	t.Setenv("ATTN_HARNESS_CONTROL_ROOT", control)
	if err := os.MkdirAll(control, 0700); err != nil {
		t.Fatal(err)
	}
	own := []string{"clean-aaaaaaaaaaaa.sock", "clean-bbbbbbbbbbbb.sock"}
	sibling := "clean-2-cccccccccccc.sock"
	for _, name := range append(append([]string{}, own...), sibling) {
		socket, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(control, name), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		socket.SetUnlinkOnClose(false)
		if err = socket.Close(); err != nil {
			t.Fatal(err)
		}
	}
	resolved := instanceResolved{Instance: "clean", Label: "clean", DataDir: filepath.Join(root, "data"), Socket: filepath.Join(root, "data", "attn.sock"), AppPath: filepath.Join(root, "absent-app"), AppLocalData: filepath.Join(root, "app-data"), AppLock: filepath.Join(root, "app.lock")}
	var output bytes.Buffer
	if err := cleanInstance(&output, resolved); err != nil {
		t.Fatalf("clean stale masters: %v\n%s", err, output.String())
	}
	for _, name := range own {
		if _, err := os.Lstat(filepath.Join(control, name)); !os.IsNotExist(err) {
			t.Fatalf("stale master %s remains: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(control, sibling)); err != nil {
		t.Fatalf("clean removed another instance's master: %v", err)
	}
	if err := cleanInstance(&output, resolved); err != nil {
		t.Fatalf("repeat clean: %v", err)
	}
	t.Log(output.String())
}
