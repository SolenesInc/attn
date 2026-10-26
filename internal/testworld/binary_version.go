package testworld

import (
	"crypto/sha256"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var versionedBinaries sync.Map

func AttnBinaryAt(t testing.TB, version string) string {
	t.Helper()
	once, _ := versionedBinaries.LoadOrStore(version, sync.OnceValues(func() (string, error) { return buildAttnAt(version, "unknown", "") }))
	binary, err := once.(func() (string, error))()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func buildAttnAt(version, channel, releaseSource string) (string, error) {
	if processDir == "" {
		return "", fmt.Errorf("testworld.AttnBinaryAt builds into the process directory that testworld.Main creates; call it from TestMain")
	}
	binary := filepath.Join(processDir, "bin", fmt.Sprintf("attn-%x", sha256.Sum256([]byte(version+channel+releaseSource))))
	build := exec.Command("go", "build", "-ldflags", "-X github.com/victorarias/attn/internal/buildinfo.Version="+version+" -X github.com/victorarias/attn/internal/buildinfo.Channel="+channel+" -X github.com/victorarias/attn/internal/buildinfo.ReleaseSource="+releaseSource, "-o", binary, "github.com/victorarias/attn/cmd/attn")
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build attn %s: %w\n%s", version, err, output)
	}
	return binary, nil
}

func (s *Stack) RunFrom(binary string) {
	s.binary = binary
}

func AttnUpdateBinary(t testing.TB, version, channel, releaseSource string) string {
	t.Helper()
	key := version + "-" + channel + "-" + releaseSource
	once, _ := versionedBinaries.LoadOrStore(key, sync.OnceValues(func() (string, error) { return buildAttnAt(version, channel, releaseSource) }))
	binary, err := once.(func() (string, error))()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}
