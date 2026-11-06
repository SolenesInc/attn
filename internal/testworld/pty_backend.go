package testworld

import (
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
)

type PTYBackend string

const (
	WorkerPTY         PTYBackend = "worker"
	SharedHostPTY     PTYBackend = "shared host"
	SharedHostOnlyPTY PTYBackend = "shared"
)

var WorkerAndSharedHost = []PTYBackend{WorkerPTY, SharedHostPTY}

func OnPTYBackend(backend PTYBackend) StackOption {
	return func(s *stackSetup) { s.backend = backend }
}

func SharedHostBinary(t testing.TB) string {
	t.Helper()
	if host := os.Getenv("ATTN_TEST_PTY_HOST"); host != "" {
		return host
	}
	return ptyhosttest.BuildWithSnapshotFormat(t, ptyhosttest.DefaultSnapshotFormat)
}

func (s *Stack) UseSharedHostBinary(host string) {
	s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
}

func (s *Stack) EnableSharedHost(app *Peer) {
	s.T.Helper()
	requestID := uuid.NewString()
	enabled := Request(app, protocol.SetSettingMessage{
		Cmd: protocol.CmdSetSetting, Key: "pty_shared_host_enabled", Value: "true", RequestID: protocol.Ptr(requestID),
	}, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.RequestID) == requestID
	})
	if !protocol.Deref(enabled.Success) {
		s.T.Fatalf("enabling the shared PTY host failed: %s", protocol.Deref(enabled.Error))
	}
}

func (s *Stack) usePTYBackend(backend PTYBackend) {
	s.T.Helper()
	switch backend {
	case SharedHostPTY:
		s.UseSharedHostBinary(SharedHostBinary(s.T))
	case SharedHostOnlyPTY:
		s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=shared", "ATTN_PTY_HOST_BINARY="+SharedHostBinary(s.T))
	default:
		s.Vars = append(s.Vars, "ATTN_PTY_BACKEND="+string(backend))
	}
}
