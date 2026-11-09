package testworld

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
)

// AttnSourceBinaryFrom builds a source-channel attn that records repository as where it came from.
func AttnSourceBinaryFrom(t testing.TB, version, repository string) string {
	t.Helper()
	dir := filepath.Join(processDir, "bin", fmt.Sprintf("attn-source-%x", sha256.Sum256([]byte(version+repository))))
	ldflags := "-w -X github.com/victorarias/attn/internal/buildinfo.SourceFingerprint=git:" + version +
		" -X github.com/victorarias/attn/internal/buildinfo.Version=" + version +
		" -X github.com/victorarias/attn/internal/buildinfo.Channel=source" +
		" -X github.com/victorarias/attn/internal/buildinfo.RepositoryPath=" + repository
	binary, err := buildPair(dir, ldflags)
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

// FreeWSPort stops the harness holding the WebSocket port, so a daemon attn starts by itself can bind it.
func (s *Stack) FreeWSPort() {
	_ = s.wsListener.Close()
}

// AwaitDaemonReplaced waits for the daemon the harness started to exit after attn replaced it.
// From then on the replacement is stopped through the CLI.
func (s *Stack) AwaitDaemonReplaced() {
	s.T.Helper()
	<-s.exited
	s.daemon, s.exited = nil, nil
	s.T.Cleanup(func() { s.Attn("daemon", "stop") })
}
