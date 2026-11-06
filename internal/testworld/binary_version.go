package testworld

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost"
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
	fingerprint := "unknown"
	if channel != "unknown" {
		fingerprint = "git:" + version
	}
	build := exec.Command("go", "build", "-ldflags", "-w -X github.com/victorarias/attn/internal/buildinfo.SourceFingerprint="+fingerprint+" -X github.com/victorarias/attn/internal/buildinfo.Version="+version+" -X github.com/victorarias/attn/internal/buildinfo.Channel="+channel+" -X github.com/victorarias/attn/internal/buildinfo.ReleaseSource="+releaseSource, "-o", binary, "github.com/victorarias/attn/cmd/attn")
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

// InstallBuild puts a build where the user's update puts it: attn and its host side by side,
// each renamed over the previous file, so running processes keep the old image while every
// later exec, such as an agent's hook, runs the new one.
func (s *Stack) InstallBuild(attn, host string) {
	s.T.Helper()
	dir := filepath.Join(s.Dir, "install")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.T.Fatal(err)
	}
	installed := filepath.Join(dir, "attn")
	replaceFile(s.T, attn, installed)
	if host != "" {
		replaceFile(s.T, host, filepath.Join(dir, ptyhost.BinaryName))
	}
	link := filepath.Join(s.Dir, "bin", "attn")
	if err := os.Remove(link); err != nil {
		s.T.Fatal(err)
	}
	if err := os.Symlink(installed, link); err != nil {
		s.T.Fatal(err)
	}
	s.binary = installed
}

// replaceFile copies in a child process: a write descriptor open in this process would leak into any exec running
// beside it, and the kernel refuses to run a file open for writing.
func replaceFile(t testing.TB, from, to string) {
	t.Helper()
	staged := to + ".new"
	if output, err := exec.Command("cp", from, staged).CombinedOutput(); err != nil {
		t.Fatalf("copy %s: %v\n%s", from, err, output)
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, to); err != nil {
		t.Fatal(err)
	}
}

// StartRelease runs the installed published build. It speaks an older protocol than this test
// binary, so talk to it through ReleaseApp.
func (s *Stack) StartRelease() {
	s.T.Helper()
	s.launch()
}

// ReleaseApp connects to a daemon of a published build, without App's check that the daemon
// speaks this test binary's protocol.
func (s *Stack) ReleaseApp() *Peer {
	s.T.Helper()
	p := s.ConnectApp()
	p.Initial = Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	return p
}
