package testworld

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
)

var (
	attnBinary = sync.OnceValues(func() (string, error) {
		return provideAttnBinaries()
	})
	fakeAgentBinary = sync.OnceValues(func() (string, error) {
		return provideBinary("ATTN_FAKEAGENT_BIN", "fakeagent", "github.com/victorarias/attn/scripts/fakeagent")
	})
)

func AttnBinary(t testing.TB) string {
	t.Helper()
	binary, err := attnBinary()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func attnBinarySnapshotFormat() string {
	binary := os.Getenv("ATTN_E2E_BIN")
	if binary == "" {
		return ptyhosttest.DefaultSnapshotFormat
	}
	output, err := exec.Command(binary, "version", "--json").Output()
	if err != nil {
		panic(fmt.Sprintf("testworld.Main: read the snapshot format of %s: %v", binary, err))
	}
	var build struct {
		SnapshotFormat string `json:"snapshotFormat"`
	}
	if err := json.Unmarshal(output, &build); err != nil {
		panic(fmt.Sprintf("testworld.Main: read the snapshot format of %s: %v", binary, err))
	}
	return build.SnapshotFormat
}

func FakeAgentBinary(t testing.TB) string {
	t.Helper()
	binary, err := fakeAgentBinary()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func provideAttnBinaries() (string, error) {
	if os.Getenv("ATTN_E2E_BIN") != "" {
		return provideBinary("ATTN_E2E_BIN", "attn", "")
	}
	if _, err := provideBinary("ATTN_E2E_BIN", "attnd", "github.com/victorarias/attn/cmd/attnd"); err != nil {
		return "", err
	}
	return stageCLI(filepath.Join(processDir, "bin"))
}

func stageCLI(dir string) (string, error) {
	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("find the repository to build the attn CLI: %w", err)
	}
	cli := filepath.Join(dir, "attn")
	script := filepath.Join(strings.TrimSpace(string(root)), "scripts", "rust-cli.sh")
	if output, err := exec.Command("bash", script, "build", "--out", cli).CombinedOutput(); err != nil {
		return "", fmt.Errorf("build the attn CLI: %w\n%s", err, output)
	}
	return cli, nil
}

func buildPair(dir, ldflags string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", filepath.Join(dir, "attnd"), "github.com/victorarias/attn/cmd/attnd")
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build attnd in %s: %w\n%s", dir, err, output)
	}
	return stageCLI(dir)
}

func provideBinary(envVar, name, pkg string) (string, error) {
	if binary := os.Getenv(envVar); binary != "" {
		info, err := os.Stat(binary)
		if err != nil {
			return "", fmt.Errorf("%s: %w", envVar, err)
		}
		if info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("%s is not executable: %s", envVar, binary)
		}
		return binary, nil
	}
	if processDir == "" {
		return "", fmt.Errorf("testworld builds %s into the process directory that testworld.Main creates; call it from TestMain", name)
	}
	binary := filepath.Join(processDir, "bin", name)
	build := exec.Command("go", "build", "-ldflags", "-X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat="+ptyhosttest.DefaultSnapshotFormat, "-o", binary, pkg)
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s: %w\n%s", name, err, output)
	}
	return binary, nil
}
