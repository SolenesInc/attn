package testworld

import (
	"fmt"
	"os/exec"
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
		binary := filepath.Join(processDir, "bin", "attn-format-"+format)
		cmd := exec.Command("go", "build", "-o", binary,
			"-ldflags", "-X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat="+format,
			"github.com/victorarias/attn/cmd/attn")
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("build attn with snapshot format %s: %w\n%s", format, err, output)
		}
		return binary, nil
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

func (s *Stack) StartWith(vars ...string) {
	s.T.Helper()
	s.start(vars...)
}
