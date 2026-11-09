package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/testworld"
)

const updateReleaseVersion = "2.0.0"

type releaseServer struct {
	mu        sync.Mutex
	assets    map[string][]byte
	truncated map[string]bool
	source    string
}

func (s *releaseServer) put(name string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assets[name] = content
}

func (s *releaseServer) truncate(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.truncated[name] = true
}

func (s *releaseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/latest" {
		http.Redirect(w, r, "/tag/v"+updateReleaseVersion, http.StatusFound)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/tag/") {
		return
	}
	name := filepath.Base(r.URL.Path)
	s.mu.Lock()
	content, found := s.assets[name]
	truncated := s.truncated[name]
	s.mu.Unlock()
	if !found {
		http.NotFound(w, r)
		return
	}
	if truncated {
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Write(content[:len(content)/2])
		return
	}
	w.Write(content)
}

func newReleaseServer(t *testing.T) *releaseServer {
	t.Helper()
	server := &releaseServer{assets: map[string][]byte{}, truncated: map[string]bool{}}
	running := httptest.NewServer(server)
	t.Cleanup(running.Close)
	server.source = running.URL + "/download"
	return server
}

func releaseBuild(t *testing.T, server *releaseServer, version string) (attn, attnd []byte) {
	t.Helper()
	binary := testworld.AttnUpdateBinary(t, version, "release", server.source)
	return []byte(mustRead(t, binary)), []byte(mustRead(t, filepath.Join(filepath.Dir(binary), "attnd")))
}

type releaseBinaryInstall struct {
	stack  *testworld.Stack
	server *releaseServer
	dir    string
}

func publishLinuxRelease(t *testing.T, server *releaseServer, attnd []byte) {
	t.Helper()
	attn, _ := releaseBuild(t, server, updateReleaseVersion)
	arch := runtime.GOARCH
	files := map[string][]byte{"attn-linux-" + arch: attn, "attnd-linux-" + arch: attnd, ptyhost.BinaryName + "-linux-" + arch: []byte("the released host")}
	var sums strings.Builder
	for name, content := range files {
		server.put(name, content)
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(content), name)
	}
	server.put("SHA256SUMS-linux-"+arch, []byte(sums.String()))
}

func newReleaseBinaryInstall(t *testing.T, vars ...string) *releaseBinaryInstall {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("attn publishes release binaries only for Linux")
	}
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Harness(protocol.SessionAgentShell)))
	server := newReleaseServer(t)
	_, attnd := releaseBuild(t, server, updateReleaseVersion)
	publishLinuxRelease(t, server, attnd)
	install := &releaseBinaryInstall{stack: stack, server: server, dir: filepath.Join(stack.Dir, "dot-local-bin")}
	if err := os.MkdirAll(install.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldAttn, oldAttnd := releaseBuild(t, server, "1.0.0")
	writeFixtureFile(t, filepath.Join(install.dir, "attn"), string(oldAttn), 0o755)
	writeFixtureFile(t, filepath.Join(install.dir, "attnd"), string(oldAttnd), 0o755)
	writeFixtureFile(t, filepath.Join(install.dir, "someone-elses-tool"), "keep me", 0o755)
	stack.RunFrom(filepath.Join(install.dir, "attn"))
	stack.Vars = append(stack.Vars, vars...)
	return install
}

func (i *releaseBinaryInstall) snapshot(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(i.dir)
	if err != nil {
		t.Fatal(err)
	}
	var listing strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&listing, "%s %x\n", entry.Name(), sha256.Sum256([]byte(mustRead(t, filepath.Join(i.dir, entry.Name())))))
	}
	return listing.String()
}

func startReleaseStack(t *testing.T, stack *testworld.Stack) *testworld.Peer {
	t.Helper()
	carrier := &sourceInstall{stack: stack}
	return carrier.start(t)
}

func TestAReleaseBinaryUpdatesOnlyItsOwnFilesAndKeepsTerminals(t *testing.T) {
	install := newReleaseBinaryInstall(t)
	carrier := &sourceInstall{stack: install.stack}
	carrier.expectRestart()
	app := startReleaseStack(t, install.stack)
	shell := install.stack.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), install.stack.Path("shell"))
	app.TypeLine(shell, "kept=yes; printf '__before_%s\\n' \"$kept\"")
	app.AwaitScreen(shell, "__before_yes")

	startUpdate(t, app)
	app = carrier.restarted()
	done := finalRun(app)
	t.Logf("final run: %+v", done)
	if done.Phase != protocol.UpdatePhaseDone || !strings.HasPrefix(done.TargetBuild, updateReleaseVersion+" ") || done.InstallKind != "release_binary" {
		t.Fatalf("final run = %+v", done)
	}
	if status := updateStatus(t, install.stack); status.Version != updateReleaseVersion {
		t.Fatalf("status after the update = %+v", status)
	}
	if host := mustRead(t, filepath.Join(install.dir, ptyhost.BinaryName)); host != "the released host" {
		t.Fatalf("host = %q", host)
	}
	if mustRead(t, filepath.Join(install.dir, "someone-elses-tool")) != "keep me" {
		t.Fatal("the update touched a neighbor")
	}
	entries, err := os.ReadDir(install.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".previous") || strings.HasSuffix(entry.Name(), ".new") {
			t.Fatalf("left behind %s", entry.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(install.stack.Dir, "update-release")); !os.IsNotExist(err) {
		t.Fatalf("the staging folder stayed: %v", err)
	}
	daemonPID := strings.TrimSpace(mustRead(t, filepath.Join(install.stack.Dir, "attn.pid")))
	if cwd, err := os.Readlink("/proc/" + daemonPID + "/cwd"); err != nil || strings.HasSuffix(cwd, " (deleted)") {
		t.Fatalf("the new daemon's working directory = %q, %v", cwd, err)
	}
	app.Watch(shell)
	app.TypeLine(shell, "printf '__after_%s\\n' \"$kept\"")
	app.AwaitScreen(shell, "__after_yes")
}

func TestABadReleaseDownloadIsRefusedAndChangesNothing(t *testing.T) {
	arch := runtime.GOARCH
	attnd := "attnd-linux-" + arch
	for _, bad := range []struct {
		name   string
		damage func(t *testing.T, server *releaseServer)
		want   string
	}{
		{"tampered", func(t *testing.T, server *releaseServer) {
			content := append([]byte{}, server.assets[attnd]...)
			content[len(content)/2] ^= 0xff
			server.put(attnd, content)
		}, "does not match release"},
		{"truncated", func(t *testing.T, server *releaseServer) { server.truncate(attnd) }, "unexpected EOF"},
		{"no checksums", func(t *testing.T, server *releaseServer) {
			delete(server.assets, "SHA256SUMS-linux-"+arch)
		}, "404"},
		{"not in the checksums", func(t *testing.T, server *releaseServer) {
			server.put("SHA256SUMS-linux-"+arch, []byte("abc  some-other-file\n"))
		}, "lists no checksum"},
	} {
		t.Run(bad.name, func(t *testing.T) {
			install := newReleaseBinaryInstall(t)
			app := startReleaseStack(t, install.stack)
			before := install.snapshot(t)
			bad.damage(t, install.server)

			startUpdate(t, app)
			failed := awaitRunPhase(app, protocol.UpdatePhaseFailed)
			t.Logf("refused: %s", protocol.Deref(failed.Outcome))
			if !strings.Contains(protocol.Deref(failed.Outcome), bad.want) {
				t.Fatalf("outcome = %q, want %q", protocol.Deref(failed.Outcome), bad.want)
			}
			if after := install.snapshot(t); after != before {
				t.Fatalf("the install changed:\n%s\nwas:\n%s", after, before)
			}
			if status := updateStatus(t, install.stack); status.Version != "1.0.0" {
				t.Fatalf("version = %s", status.Version)
			}
			if _, err := os.Stat(filepath.Join(install.stack.Dir, "update-release")); !os.IsNotExist(err) {
				t.Fatalf("a refused download left its staging folder: %v", err)
			}
		})
	}
}

func TestAReleaseBinaryThatFailsToStartGoesBackToTheOldFiles(t *testing.T) {
	install := newReleaseBinaryInstall(t)
	real := testworld.AttnUpdateBinary(t, updateReleaseVersion, "release", install.server.source)
	broken := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in --build-info-json|--check-schema|_update-apply) exec %q \"$@\";; esac\n%q daemon stop\necho the new build cannot start >&2\nexit 1\n", filepath.Join(filepath.Dir(real), "attnd"), filepath.Join(filepath.Dir(real), "attnd"))
	publishLinuxRelease(t, install.server, []byte(broken))
	carrier := &sourceInstall{stack: install.stack}
	carrier.expectRestart()
	app := startReleaseStack(t, install.stack)
	before := install.snapshot(t)

	startUpdate(t, app)
	app = carrier.restarted()
	failed := finalRun(app)
	t.Logf("failed run: %+v", failed)
	if failed.Phase != protocol.UpdatePhaseFailed || !strings.Contains(protocol.Deref(failed.Outcome), "went back") {
		t.Fatalf("final run = %+v", failed)
	}
	if after := install.snapshot(t); !sameExceptAddedHost(before, after) {
		t.Fatalf("the old files did not come back:\n%s\nwas:\n%s", after, before)
	}
	if status := updateStatus(t, install.stack); status.Version != "1.0.0" {
		t.Fatalf("running version = %s", status.Version)
	}
}

func sameExceptAddedHost(before, after string) bool {
	var kept []string
	for _, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(line, ptyhost.BinaryName+" ") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n") == before
}

type releaseAppInstall struct {
	stack            *testworld.Stack
	server           *releaseServer
	bundle, reopened string
	carrier          *sourceInstall
}

const fakeCodesign = `#!/bin/sh
if [ "$1" = "-d" ]; then
  echo "Executable=$3/Contents/MacOS/attnd"
  echo 'designated => identifier "com.attn" and anchor apple generic and certificate leaf[subject.OU] = TEAM1234'
  exit 0
fi
for last; do :; done
if [ -e "$last/BAD_SIGNATURE" ]; then
  echo "$last: a sealed resource is missing or invalid" >&2
  exit 1
fi
`

func writeBundle(t *testing.T, bundle string, attn, attnd []byte, reopened string) {
	t.Helper()
	macos := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(macos, "attn"), string(attn), 0o755)
	writeFixtureFile(t, filepath.Join(macos, "attnd"), string(attnd), 0o755)
	writeFixtureFile(t, filepath.Join(bundle, "bin", "attn-app"), "#!/bin/sh\necho reopened > "+reopened+"\n", 0o755)
}

func appArchiveOf(t *testing.T, dir string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	root := filepath.Dir(dir)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name, _ = filepath.Rel(root, path)
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if !info.IsDir() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, err = archive.Write(content)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	archive.Close()
	zipped.Close()
	return buffer.Bytes()
}

func newReleaseAppInstall(t *testing.T, damage func(bundle string)) *releaseAppInstall {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("this path replaces codesign with a fake; the macOS VM covers the real one")
	}
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Harness(protocol.SessionAgentShell)))
	server := newReleaseServer(t)
	install := &releaseAppInstall{stack: stack, server: server, bundle: filepath.Join(stack.Dir, "Applications", "attn.app")}
	install.reopened = filepath.Join(stack.Dir, "reopened")
	if err := exec.Command("mkfifo", install.reopened).Run(); err != nil {
		t.Fatal(err)
	}
	oldAttn, oldAttnd := releaseBuild(t, server, "1.0.0")
	writeBundle(t, install.bundle, oldAttn, oldAttnd, install.reopened)
	newAttn, newAttnd := releaseBuild(t, server, updateReleaseVersion)
	publishDir := filepath.Join(t.TempDir(), "attn.app")
	writeBundle(t, publishDir, newAttn, newAttnd, install.reopened)
	if damage != nil {
		damage(publishDir)
	}
	server.put("attn_aarch64.app.tar.gz", appArchiveOf(t, publishDir))
	tools := filepath.Join(stack.Dir, "tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(tools, "codesign"), fakeCodesign, 0o755)
	stack.RunFrom(filepath.Join(install.bundle, "Contents", "MacOS", "attn"))
	stack.Vars = append(stack.Vars, "PATH="+tools+":"+os.Getenv("PATH"))
	install.carrier = &sourceInstall{stack: stack}
	return install
}

func TestAReleaseAppSwapsItsBundleAndReopens(t *testing.T) {
	install := newReleaseAppInstall(t, nil)
	install.carrier.expectRestart()
	app := startReleaseStack(t, install.stack)
	shell := install.stack.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), install.stack.Path("shell"))
	app.TypeLine(shell, "kept=yes; printf '__before_%s\\n' \"$kept\"")
	app.AwaitScreen(shell, "__before_yes")

	startUpdate(t, app)
	app = install.carrier.restarted()
	done := finalRun(app)
	t.Logf("final run: %+v", done)
	if done.Phase != protocol.UpdatePhaseDone || done.InstallKind != "release_app" {
		t.Fatalf("final run = %+v", done)
	}
	if mustRead(t, install.reopened) != "reopened\n" {
		t.Fatal("the app did not reopen")
	}
	if status := updateStatus(t, install.stack); status.Version != updateReleaseVersion {
		t.Fatalf("status = %+v", status)
	}
	if _, err := os.Stat(filepath.Join(install.stack.Dir, "update-release")); !os.IsNotExist(err) {
		t.Fatalf("the old bundle stayed: %v", err)
	}
	app.Watch(shell)
	app.TypeLine(shell, "printf '__after_%s\\n' \"$kept\"")
	app.AwaitScreen(shell, "__after_yes")
}

func TestAReleaseAppWithABadSignatureIsRefusedAndChangesNothing(t *testing.T) {
	install := newReleaseAppInstall(t, func(bundle string) {
		writeFixtureFile(t, filepath.Join(bundle, "BAD_SIGNATURE"), "x", 0o644)
	})
	app := startReleaseStack(t, install.stack)
	before := mustRead(t, filepath.Join(install.bundle, "Contents", "MacOS", "attnd"))

	startUpdate(t, app)
	failed := awaitRunPhase(app, protocol.UpdatePhaseFailed)
	t.Logf("refused: %s", protocol.Deref(failed.Outcome))
	if !strings.Contains(protocol.Deref(failed.Outcome), "signature") || !strings.Contains(protocol.Deref(failed.Outcome), "sealed resource") {
		t.Fatalf("outcome = %q", protocol.Deref(failed.Outcome))
	}
	if mustRead(t, filepath.Join(install.bundle, "Contents", "MacOS", "attnd")) != before {
		t.Fatal("a refused download changed the bundle")
	}
	if status := updateStatus(t, install.stack); status.Version != "1.0.0" {
		t.Fatalf("version = %s", status.Version)
	}
}
