package testworld

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

var formatBinaries sync.Map

func AttnBinaryWithSnapshotFormat(t testing.TB, format string) string {
	t.Helper()
	build, _ := formatBinaries.LoadOrStore(format, sync.OnceValues(func() (string, error) {
		if processDir == "" {
			return "", fmt.Errorf("testworld.AttnBinaryWithSnapshotFormat builds into the process directory that testworld.Main creates; call it from TestMain")
		}
		return buildPair(filepath.Join(processDir, "bin", "attn-format-"+format), "-w -X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat="+format)
	}))
	binary, err := build.(func() (string, error))()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func (s *Stack) StartBinary(binary string, vars ...string) {
	s.T.Helper()
	s.binary = binary
	s.start(append([]string{"ATTN_PTY_WORKER_BINARY=" + binary}, vars...)...)
}

func (s *Stack) StartBinaryHeldAt(binary string, p *Pause) {
	s.T.Helper()
	s.binary = binary
	s.launch("ATTN_PTY_WORKER_BINARY=" + binary)
	p.Await()
}

func (s *Stack) StartWith(vars ...string) {
	s.T.Helper()
	s.start(vars...)
}
