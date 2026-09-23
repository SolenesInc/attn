package ptybackend

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/ptyhost"
)

func sharedHostTestRoot(t *testing.T, prefix string) (binary, root string) {
	t.Helper()
	binary = os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return binary, root
}

func stopHostsAtCleanup(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		var pids []int
		for _, path := range ptyhost.HostRegistryPaths(root) {
			if entry, err := ptyhost.ReadHostRegistry(path); err == nil && entry.HostPID > 0 {
				_ = syscall.Kill(entry.HostPID, syscall.SIGTERM)
				pids = append(pids, entry.HostPID)
			}
		}
		_ = waitForPIDsGone(3*time.Second, pids...)
	})
}

func liveHostPIDs(root string) []int {
	var pids []int
	for _, path := range ptyhost.HostRegistryPaths(root) {
		if entry, err := ptyhost.ReadHostRegistry(path); err == nil && pidExists(entry.HostPID) {
			pids = append(pids, entry.HostPID)
		}
	}
	slices.Sort(pids)
	return pids
}

func hostExecutable(t *testing.T, root string, pid int) string {
	t.Helper()
	for _, path := range ptyhost.HostRegistryPaths(root) {
		if entry, err := ptyhost.ReadHostRegistry(path); err == nil && entry.HostPID == pid {
			return entry.Executable
		}
	}
	t.Fatalf("no host registry for pid %d", pid)
	return ""
}

func copyExecutable(t *testing.T, from, to string, suffix []byte) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, append(data, suffix...), 0o700); err != nil {
		t.Fatal(err)
	}
}

func spawnCat(t *testing.T, backend *WorkerBackend, id, cwd string) {
	t.Helper()
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: id, CWD: cwd, Agent: "lifecycle-probe", ExternalCommand: []string{"/bin/cat"}, Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatalf("Spawn(%s): %v", id, err)
	}
}

func TestSharedHost_RetiredHostRelaunchAcceptsInputAndResize(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-retire-")
	stopHostsAtCleanup(t, root)
	previousIdle := sharedHostIdleTimeout
	sharedHostIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { sharedHostIdleTimeout = previousIdle })

	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-retire", BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	if err := backend.Probe(context.Background()); err != nil {
		t.Fatalf("startup validation: %v", err)
	}
	spawnCat(t, backend, "before-retirement", root)
	if err := backend.Input(context.Background(), "before-retirement", []byte("warm\n")); err != nil {
		t.Fatal(err)
	}
	if err := backend.Remove(context.Background(), "before-retirement"); err != nil {
		t.Fatal(err)
	}
	retired := liveHostPIDs(root)
	if len(retired) != 1 {
		t.Fatalf("hosts after validation = %v, want the validation host", retired)
	}
	if !waitForPIDsGone(5*time.Second, retired...) {
		t.Fatalf("idle validation host %v did not retire", retired)
	}

	spawnCat(t, backend, "after-retirement", root)
	hostPID := backend.WorkerPIDs(context.Background())["after-retirement"]
	if hostPID <= 0 || hostPID == retired[0] {
		t.Fatalf("relaunched host pid = %d, retired = %d", hostPID, retired[0])
	}
	_, stream, err := backend.Attach(context.Background(), "after-retirement", "retire")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := backend.Resize(context.Background(), "after-retirement", 101, 33, 0, 0); err != nil {
		t.Fatalf("resize on the relaunched host: %v", err)
	}
	if err := backend.Input(context.Background(), "after-retirement", []byte("__AFTER_RETIREMENT__\n")); err != nil {
		t.Fatalf("input on the relaunched host: %v", err)
	}
	waitForStreamText(t, stream, "__AFTER_RETIREMENT__")
	info, err := backend.SessionInfo(context.Background(), "after-retirement")
	if err != nil {
		t.Fatal(err)
	}
	if info.Cols != 101 || info.Rows != 33 {
		t.Fatalf("size = %dx%d, want 101x33", info.Cols, info.Rows)
	}
	if err := backend.Remove(context.Background(), "after-retirement"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedHost_DaemonStaysPinnedWhenTheBundleIsReplaced(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-pin-")
	stopHostsAtCleanup(t, root)
	bundle := filepath.Join(root, "bundle-host")
	copyExecutable(t, binary, bundle, nil)
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-pin", BinaryPath: bundle})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	if err := os.WriteFile(bundle, []byte("#!/bin/sh\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: "replaced-before-pin", CWD: root, Agent: "lifecycle-probe", ExternalCommand: []string{"/bin/cat"},
	}); err == nil || !strings.Contains(err.Error(), ptyhost.ErrArtifactChanged.Error()) {
		t.Fatalf("spawn after the bundle changed = %v, want the startup artifact refused", err)
	}

	copyExecutable(t, binary, bundle, nil)
	pinned, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-pin", BinaryPath: bundle})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pinned.Shutdown(context.Background()) })
	if err := pinned.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("#!/bin/sh\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spawnCat(t, pinned, "after-replacement", root)
	executable := hostExecutable(t, root, pinned.WorkerPIDs(context.Background())["after-replacement"])
	if !strings.HasPrefix(executable, ptyhost.ArtifactsDir(root, "d-pin")) {
		t.Fatalf("host runs %s, want the pinned artifact copy", executable)
	}
}

func TestSharedHost_PromotionLeavesExistingSessionsUninterrupted(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-promote-")
	stopHostsAtCleanup(t, root)
	cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-promote", BinaryPath: binary}
	first, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	spawnCat(t, first, "before", root)
	oldHost := first.WorkerPIDs(context.Background())["before"]
	beforeInfo, err := first.SessionInfo(context.Background(), "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	candidate := filepath.Join(root, "candidate-host")
	copyExecutable(t, binary, candidate, []byte{0})
	cfg.BinaryPath = candidate
	second, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Shutdown(context.Background()) })
	if report, err := second.Recover(context.Background()); err != nil || report.Recovered != 1 {
		t.Fatalf("recover = %+v, %v", report, err)
	}
	if !second.SharedArtifactReady() || !second.SharedCandidatePending() {
		t.Fatal("new daemon did not start on its last-known-good artifact with the candidate pending")
	}
	_, stream, err := second.Attach(context.Background(), "before", "promotion")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	spawnCat(t, second, "during", root)
	if got := second.WorkerPIDs(context.Background())["during"]; got != oldHost {
		t.Fatalf("pre-promotion launch used host %d, want last-known-good host %d", got, oldHost)
	}

	if err := second.ValidateSharedCandidate(context.Background(), false); err != nil {
		t.Fatalf("candidate validation: %v", err)
	}
	spawnCat(t, second, "after", root)
	newHost := second.WorkerPIDs(context.Background())["after"]
	if newHost == oldHost {
		t.Fatal("post-promotion launch reused the previous artifact's host")
	}
	if !strings.HasPrefix(hostExecutable(t, root, newHost), ptyhost.ArtifactsDir(root, "d-promote")) {
		t.Fatal("promoted host does not run from the artifact cache")
	}
	if err := second.Input(context.Background(), "before", []byte("__AFTER_PROMOTION__\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "__AFTER_PROMOTION__")
	afterInfo, err := second.SessionInfo(context.Background(), "before")
	if err != nil {
		t.Fatal(err)
	}
	pids := second.WorkerPIDs(context.Background())
	if afterInfo.PID != beforeInfo.PID || pids["before"] != oldHost || pids["during"] != oldHost {
		t.Fatalf("promotion moved existing sessions: before child %d->%d, hosts %v, old host %d", beforeInfo.PID, afterInfo.PID, pids, oldHost)
	}
	oldArtifact, err := ptyhost.HashArtifact(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, stored := ptyhost.StoredArtifact(ptyhost.ArtifactsDir(root, "d-promote"), oldArtifact); !stored {
		t.Fatal("an artifact with a live host was collected")
	}

	third, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !third.SharedArtifactReady() || third.SharedCandidatePending() {
		t.Fatal("an ordinary restart did not reuse the promotion receipt")
	}
	for _, id := range []string{"before", "during", "after"} {
		if err := second.Remove(context.Background(), id); err != nil {
			t.Fatalf("Remove(%s): %v", id, err)
		}
	}
}

func TestSharedHost_RejectedCandidateKeepsLastKnownGood(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-reject-")
	stopHostsAtCleanup(t, root)
	cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-reject", BinaryPath: binary}
	good, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := good.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = good.Shutdown(context.Background())

	broken := filepath.Join(root, "broken-host")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var rejections []SharedArtifactRejection
	cfg.BinaryPath = broken
	cfg.OnSharedArtifactRejected = func(r SharedArtifactRejection) { rejections = append(rejections, r) }
	daemon, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Shutdown(context.Background()) })
	if err := daemon.ValidateSharedCandidate(context.Background(), false); err == nil {
		t.Fatal("broken candidate passed validation")
	}
	goodID, err := ptyhost.HashArtifact(binary)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejections) != 1 || rejections[0].FallbackID != goodID || rejections[0].Reason == "" {
		t.Fatalf("rejections = %+v, want one naming the last-known-good fallback", rejections)
	}
	if !daemon.SharedArtifactReady() {
		t.Fatal("rejection dropped the last-known-good artifact")
	}
	spawnCat(t, daemon, "on-last-known-good", root)
	executable := hostExecutable(t, root, daemon.WorkerPIDs(context.Background())["on-last-known-good"])
	if executable != ptyhost.ArtifactPath(ptyhost.ArtifactsDir(root, "d-reject"), goodID) {
		t.Fatalf("new session runs %s, want the last-known-good artifact", executable)
	}

	restarted, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.SharedCandidatePending() {
		t.Fatal("an unchanged rejected candidate is pending again after restart")
	}
	if err := restarted.ValidateSharedCandidate(context.Background(), false); err == nil || len(rejections) != 1 {
		t.Fatalf("restart revalidated the rejected candidate: err=%v rejections=%d", err, len(rejections))
	}
	if err := restarted.Probe(context.Background()); err != nil {
		t.Fatalf("explicit retry with a last-known-good fallback = %v", err)
	}
	if len(rejections) != 2 {
		t.Fatalf("explicit retry did not rerun validation: rejections=%d", len(rejections))
	}
	if err := daemon.Remove(context.Background(), "on-last-known-good"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedHost_KeystrokesReachTheChildAcrossDaemonReplacement(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-typist-")
	stopHostsAtCleanup(t, root)
	cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-typist", BinaryPath: binary}
	first, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Spawn(context.Background(), SpawnOptions{
		ID: "typist", CWD: root, Agent: "lifecycle-probe", Cols: 80, Rows: 24,
		ExternalCommand: []string{"/bin/sh", "-c", "stty raw -echo && printf __TYPIST_READY__ && exec cat"},
	}); err != nil {
		t.Fatal(err)
	}
	keys := "abcdefghijklmnopqrstuvwxyz0123456789"
	half := len(keys) / 2
	var received bytes.Buffer
	_, stream, err := first.Attach(context.Background(), "typist", "typist-before")
	if err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "__TYPIST_READY__")
	for _, key := range keys[:half] {
		if err := first.Input(context.Background(), "typist", []byte{byte(key)}); err != nil {
			t.Fatal(err)
		}
	}
	received.Write(collectStreamUntil(t, stream, keys[:half]))
	_ = stream.Close()
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	second, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Shutdown(context.Background()) })
	if _, err := second.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, stream, err = second.Attach(context.Background(), "typist", "typist-after", AttachOptions{OmitReplay: true})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for _, key := range keys[half:] {
		if err := second.Input(context.Background(), "typist", []byte{byte(key)}); err != nil {
			t.Fatal(err)
		}
	}
	received.Write(collectStreamUntil(t, stream, keys[half:]))
	if got := strings.TrimPrefix(received.String(), "__TYPIST_READY__"); got != keys {
		t.Fatalf("child received %q, want every keystroke in order: %q", got, keys)
	}
	if err := second.Remove(context.Background(), "typist"); err != nil {
		t.Fatal(err)
	}
}

func collectStreamUntil(t *testing.T, stream Stream, suffix string) []byte {
	t.Helper()
	deadline := time.After(8 * time.Second)
	var output bytes.Buffer
	for !bytes.HasSuffix(output.Bytes(), []byte(suffix)) {
		select {
		case event := <-stream.Events():
			if event.Kind == OutputEventKindOutput {
				output.Write(event.Data)
			}
		case <-deadline:
			t.Fatalf("output = %q, want suffix %q", output.String(), suffix)
		}
	}
	return output.Bytes()
}

func TestSharedHost_OperatesARetainedOlderArtifact(t *testing.T) {
	retained := os.Getenv("ATTN_TEST_RETAINED_PTY_HOST")
	if retained == "" {
		t.Skip("set ATTN_TEST_RETAINED_PTY_HOST to the oldest retained attn-pty-host build")
	}
	_, root := sharedHostTestRoot(t, "attn-host-retained-")
	stopHostsAtCleanup(t, root)
	cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-retained", BinaryPath: retained}
	first, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	spawnCat(t, first, "retained", root)
	hostPID := first.WorkerPIDs(context.Background())["retained"]
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	current, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Shutdown(context.Background()) })
	if report, err := current.Recover(context.Background()); err != nil || report.Recovered != 1 {
		t.Fatalf("recover = %+v, %v", report, err)
	}
	_, stream, err := current.Attach(context.Background(), "retained", "retained")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := current.Resize(context.Background(), "retained", 90, 30, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := current.Input(context.Background(), "retained", []byte("__RETAINED_HOST__\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "__RETAINED_HOST__")
	spawnCat(t, current, "retained-second", root)
	if got := current.WorkerPIDs(context.Background())["retained-second"]; got != hostPID {
		t.Fatalf("second session host = %d, want the retained host %d", got, hostPID)
	}
	if err := current.ValidateSharedCandidate(context.Background(), true); err == nil || !strings.Contains(err.Error(), "validation probe") {
		t.Fatalf("validating a pre-probe artifact = %v, want a clean rejection", err)
	}
	if err := current.Input(context.Background(), "retained", []byte("__AFTER_REJECTION__\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "__AFTER_REJECTION__")
	for _, id := range []string{"retained", "retained-second"} {
		if err := current.Remove(context.Background(), id); err != nil {
			t.Fatalf("Remove(%s): %v", id, err)
		}
	}
	if err := current.ValidateSharedCandidate(context.Background(), true); err == nil {
		t.Fatal("pre-probe artifact passed validation")
	}
	if !waitForPIDsGone(3*time.Second, hostPID) {
		t.Fatal("an empty host rejected before its probe kept running")
	}
}

func TestSharedHost_UnusedHostRetiresOnItsOwn(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-unused-")
	stopHostsAtCleanup(t, root)
	previousIdle := sharedHostIdleTimeout
	sharedHostIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { sharedHostIdleTimeout = previousIdle })
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-unused", BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	host, err := backend.ensureSharedHost(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !waitForPIDsGone(5*time.Second, host.HostPID) {
		t.Fatal("a host that never received a terminal did not retire")
	}
}

func TestSharedHost_ValidationPassesWhenTheDaemonSharesTheHostSnapshotFormat(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-native-")
	stopHostsAtCleanup(t, root)
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-native", BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	artifact, err := backend.launchArtifact()
	if err != nil {
		t.Fatal(err)
	}
	host, err := backend.ensureSharedHost(context.Background(), &artifact)
	if err != nil {
		t.Fatal(err)
	}
	info, err := backend.sharedHostInfo(context.Background(), incarnationOfHost(host))
	if err != nil {
		t.Fatal(err)
	}
	previous := buildinfo.SnapshotFormat
	buildinfo.SnapshotFormat = info.SnapshotFormat
	t.Cleanup(func() { buildinfo.SnapshotFormat = previous })
	if err := backend.Probe(context.Background()); err != nil {
		t.Fatalf("validation with native snapshots: %v", err)
	}
}

func TestSharedHost_RecoveryRemovesAnAbandonedProbe(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-abandoned-")
	stopHostsAtCleanup(t, root)
	cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-abandoned", BinaryPath: binary}
	first, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.spawn(context.Background(), SpawnOptions{
		ID: probeSessionPrefix + "left-behind", CWD: root, Agent: "probe", ExternalCommand: []string{"/bin/cat"}, Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatal(err)
	}
	spawnCat(t, first, "user-terminal", root)
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	second, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Shutdown(context.Background()) })
	report, err := second.Recover(context.Background())
	if err != nil || report.Recovered != 1 || report.Pruned != 1 {
		t.Fatalf("recover = %+v, %v; want the user terminal recovered and the probe pruned", report, err)
	}
	if ids := second.SessionIDs(context.Background()); len(ids) != 1 || ids[0] != "user-terminal" {
		t.Fatalf("recovered sessions = %v", ids)
	}
	if err := second.Remove(context.Background(), "user-terminal"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedHost_ProbeChildThatExitsRejectsTheBuildWithoutReportingTheProbe(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-mute-")
	stopHostsAtCleanup(t, root)
	cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-mute", BinaryPath: binary}
	good, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := good.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = good.Shutdown(context.Background())

	mute := filepath.Join(root, "mute-probe-host")
	script := "#!/bin/sh\nif [ \"$1\" = " + ptyhost.ProbeChildFlag + " ]; then exit 0; fi\nexec '" + binary + "' \"$@\"\n"
	if err := os.WriteFile(mute, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var rejections int
	cfg.BinaryPath = mute
	cfg.OnSharedArtifactRejected = func(SharedArtifactRejection) { rejections++ }
	backend, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	exits := make(chan string, 8)
	backend.SetExitHandler(func(info ExitInfo) { exits <- info.ID })

	if err := backend.ValidateSharedCandidate(context.Background(), false); err == nil {
		t.Fatal("a build whose probe child never answers passed validation")
	}
	if rejections != 1 || backend.SharedCandidatePending() {
		t.Fatalf("a build whose probe child exits was not rejected: rejections=%d pending=%v", rejections, backend.SharedCandidatePending())
	}

	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: "exits-at-once", CWD: root, Agent: "lifecycle-probe", ExternalCommand: []string{"/bin/sh", "-c", "exit 0"}, Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatal(err)
	}
	for id := range exits {
		if isProbeSession(id) {
			t.Fatalf("the validation probe %s was reported as a session exit", id)
		}
		if id == "exits-at-once" {
			break
		}
	}
}

func TestSharedHost_InterruptedProbeLeavesNoTerminalBehind(t *testing.T) {
	binary, root := sharedHostTestRoot(t, "attn-host-stalled-")
	stopHostsAtCleanup(t, root)
	started := filepath.Join(root, "probe-started")
	if err := syscall.Mkfifo(started, 0o600); err != nil {
		t.Fatal(err)
	}
	stalled := filepath.Join(root, "stalled-probe-host")
	script := "#!/bin/sh\nif [ \"$1\" = " + ptyhost.ProbeChildFlag + " ]; then echo started > '" + started + "'; exec sleep 30; fi\nexec '" + binary + "' \"$@\"\n"
	if err := os.WriteFile(stalled, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var rejections int
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-stalled", BinaryPath: stalled,
		OnSharedArtifactRejected: func(SharedArtifactRejection) { rejections++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checked := make(chan error, 1)
	go func() { checked <- backend.ValidateSharedCandidate(ctx, false) }()
	if _, err := os.ReadFile(started); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-checked; err == nil {
		t.Fatal("an interrupted check passed")
	}
	if rejections != 0 || !backend.SharedCandidatePending() {
		t.Fatalf("an interrupted check was recorded: rejections=%d pending=%v", rejections, backend.SharedCandidatePending())
	}
	for _, path := range ptyhost.HostRegistryPaths(root) {
		entry, err := ptyhost.ReadHostRegistry(path)
		if err != nil {
			continue
		}
		info, err := backend.sharedHostInfo(context.Background(), incarnationOfHost(entry))
		if err != nil {
			t.Fatal(err)
		}
		if len(info.SessionIDs) != 0 {
			t.Fatalf("the interrupted probe left sessions %v on its host", info.SessionIDs)
		}
	}
}
