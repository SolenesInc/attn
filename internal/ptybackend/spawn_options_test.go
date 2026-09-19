package ptybackend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSpawnOptionsWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "link")
	if err := os.Symlink(root, symlink); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "gone"), dangling); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing")

	tests := []struct {
		name    string
		opts    SpawnOptions
		wantErr string
	}{
		{name: "base directory", opts: SpawnOptions{CWD: root}},
		{name: "blank cwd", opts: SpawnOptions{}, wantErr: "missing cwd"},
		{name: "missing base", opts: SpawnOptions{CWD: missing}, wantErr: missing},
		{name: "base file", opts: SpawnOptions{CWD: file}, wantErr: "is not a directory"},
		{name: "valid override", opts: SpawnOptions{CWD: missing, ExternalCWD: root}},
		{name: "missing override", opts: SpawnOptions{CWD: root, ExternalCWD: missing}, wantErr: missing},
		{name: "blank override", opts: SpawnOptions{CWD: root, ExternalCWD: "  "}},
		{name: "directory symlink", opts: SpawnOptions{CWD: symlink}},
		{name: "dangling symlink", opts: SpawnOptions{CWD: dangling}, wantErr: dangling},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSpawnOptions(tt.opts)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateSpawnOptions() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateSpawnOptions() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestConcreteBackendsRejectMissingWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")

	embedded := NewEmbedded(nil)
	if err := embedded.Spawn(t.Context(), SpawnOptions{ID: "embedded", CWD: missing, Agent: "shell"}); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("embedded spawn error = %v, want missing working directory %q", err, missing)
	}

	launcher := filepath.Join(root, "record-launch.sh")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n: > \"$ATTN_PTYBACKEND_TEST_MARKER\"\nexit 17\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		new  func(WorkerBackendConfig) (*WorkerBackend, error)
	}{
		{name: "dedicated", new: NewWorker},
		{name: "shared", new: NewSharedHost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(root, tc.name+"-launched")
			t.Setenv("ATTN_PTYBACKEND_TEST_MARKER", marker)
			backend, err := tc.new(WorkerBackendConfig{
				DataRoot:         filepath.Join(root, tc.name),
				DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				BinaryPath:       launcher,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.Spawn(t.Context(), SpawnOptions{ID: tc.name, CWD: missing, Agent: "shell"}); err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("spawn error = %v, want missing working directory %q", err, missing)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("worker executable was invoked before working directory validation: %v", err)
			}
			if ids := backend.SessionIDs(t.Context()); len(ids) != 0 {
				t.Fatalf("sessions = %v, want none", ids)
			}
		})
	}
}
