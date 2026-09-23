package hub

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/ptyhost"
)

const githubRepo = "victorarias/attn"

const remoteDaemonReadyTimeout = 35 * time.Second
const remoteHarnessRootMarker = "/.attn/harness/"

const (
	remoteReadyBudget    = 180 * time.Second
	appRuntimeShipBudget = 300 * time.Second
)

type RemotePlatform struct {
	GOOS                string
	GOARCH              string
	ArtifactName        string
	RuntimeArtifactName string
	PTYHostArtifactName string
	BunTarget           string
}

type Bootstrapper struct {
	logf func(format string, args ...interface{})

	versionOnce sync.Once
	version     string
	versionErr  error

	makeReady      func(ctx context.Context, sshTarget, instance, homeDaemonID string) (readyRemote, error)
	shipAppRuntime func(ctx context.Context, sshTarget, instance string, ready readyRemote) error
}

func NewBootstrapper(logf func(format string, args ...interface{})) *Bootstrapper {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	b := &Bootstrapper{logf: logf}
	b.makeReady = b.makeRemoteReady
	b.shipAppRuntime = b.shipRemoteAppRuntime
	return b
}

type readyRemote struct {
	platform          RemotePlatform
	version           string
	remoteInstallPath string
}

func (b *Bootstrapper) EnsureRemoteReady(ctx context.Context, sshTarget, instance, homeDaemonID string) error {
	readyCtx, cancelReady := context.WithTimeout(ctx, remoteReadyBudget)
	ready, err := b.makeReady(readyCtx, sshTarget, instance, homeDaemonID)
	cancelReady()
	if err != nil {
		return err
	}

	shipCtx, cancelShip := context.WithTimeout(ctx, appRuntimeShipBudget)
	defer cancelShip()
	if err := b.shipAppRuntime(shipCtx, sshTarget, instance, ready); err != nil {
		b.logf("%v", err)
	}
	return nil
}

func (b *Bootstrapper) makeRemoteReady(ctx context.Context, sshTarget, instance, homeDaemonID string) (readyRemote, error) {
	platform, err := b.detectRemotePlatform(ctx, sshTarget, instance)
	if err != nil {
		return readyRemote{}, fmt.Errorf("detect remote platform for %s: %w", sshTarget, err)
	}

	localVersion, err := b.localVersion(ctx)
	if err != nil {
		return readyRemote{}, fmt.Errorf("determine local version: %w", err)
	}

	remoteVersion, err := b.remoteVersion(ctx, sshTarget, instance)
	if err != nil {
		return readyRemote{}, fmt.Errorf("check remote version on %s: %w", sshTarget, err)
	}

	preferSourceBuild := sourceCheckoutAvailable()
	var localBinary string
	binariesUpdated := false
	if remoteVersion != localVersion || preferSourceBuild {
		localBinary, err = b.ensureLocalBinary(ctx, platform, localVersion)
		if err != nil {
			return readyRemote{}, fmt.Errorf("prepare %s binary for %s: %w", platform.ArtifactName, sshTarget, err)
		}
	}

	shouldInstall := remoteVersion != localVersion
	if !shouldInstall && preferSourceBuild {
		localHash, err := fileSHA256(localBinary)
		if err != nil {
			return readyRemote{}, fmt.Errorf("hash local binary for %s: %w", sshTarget, err)
		}
		remoteHash, err := b.remoteBinarySHA256(ctx, sshTarget, instance)
		if err != nil {
			return readyRemote{}, fmt.Errorf("hash remote binary on %s: %w", sshTarget, err)
		}
		shouldInstall = shouldInstallRemoteBinary(localVersion, remoteVersion, preferSourceBuild, localHash, remoteHash)
		if shouldInstall {
			b.logf("remote binary hash mismatch for %s: remote=%s local=%s", sshTarget, remoteHash, localHash)
		}
	}

	remoteInstallPath, err := b.resolveRemoteInstall(ctx, sshTarget, instance)
	if err != nil {
		return readyRemote{}, fmt.Errorf("resolve the install path on %s: %w", sshTarget, err)
	}
	ready := readyRemote{platform: platform, version: localVersion, remoteInstallPath: remoteInstallPath}

	if shouldInstall {
		if err := b.installRemoteBinary(ctx, sshTarget, instance, localBinary, remoteInstallPath); err != nil {
			return ready, fmt.Errorf("install attn on %s: %w", sshTarget, err)
		}
		binariesUpdated = true
	}

	_, hostWasMissing, hostErr := b.ensureRemotePTYHost(ctx, sshTarget, instance, platform, localVersion, remoteInstallPath)
	if hostErr != nil {
		b.logf("%v", hostErr)
	} else if hostWasMissing {
		binariesUpdated = true
	}

	if err := b.enrollRemote(ctx, sshTarget, instance, homeDaemonID); err != nil {
		return ready, err
	}

	if err := b.ensureRemoteDaemonRunning(ctx, sshTarget, instance, binariesUpdated); err != nil {
		return ready, fmt.Errorf("ensure remote daemon on %s: %w", sshTarget, err)
	}
	return ready, nil
}

func (b *Bootstrapper) shipRemoteAppRuntime(ctx context.Context, sshTarget, instance string, ready readyRemote) error {
	updated, err := b.ensureRemoteAppRuntime(ctx, sshTarget, instance, ready.platform, ready.version, ready.remoteInstallPath)
	if err != nil {
		return err
	}
	if !updated {
		return nil
	}
	return b.bounceRemoteAppRuntime(ctx, sshTarget, instance)
}

func (b *Bootstrapper) bounceRemoteAppRuntime(ctx context.Context, sshTarget, instance string) error {
	stdout, _, code, err := runSSHExit(ctx, sshTarget, instance, remoteAttnCommand(instance, "app", "runtime", "status", "--json"))
	if err != nil || code != 0 {
		return fmt.Errorf("could not ask %s whether its app runtime is running, so the sidecar it just received is not in use yet; `attn app runtime restart` there picks it up", sshTarget)
	}
	var status struct {
		Runtime *struct {
			Running bool `json:"running"`
		} `json:"runtime"`
	}
	if jsonErr := json.Unmarshal([]byte(stdout), &status); jsonErr != nil {
		return fmt.Errorf("app runtime status on %s returned unreadable output %q", sshTarget, stdout)
	}
	if status.Runtime == nil || !status.Runtime.Running {
		return nil
	}
	if _, _, code, err := runSSHExit(ctx, sshTarget, instance, remoteAttnCommand(instance, "app", "runtime", "restart")); err != nil || code != 0 {
		return fmt.Errorf("the app runtime on %s is still running the previous sidecar; `attn app runtime restart` there picks up the new one", sshTarget)
	}
	b.logf("restarted the app runtime on %s onto the sidecar just installed", sshTarget)
	return nil
}

const enrollmentRefusedExitCode = 3

func withoutInstanceBanner(message string) string {
	lines := strings.Split(message, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[attn instance=") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func remoteEnrollScript(instance, homeDaemonID string) string {
	return remoteAttnCommand(instance, "enrollment", "enroll", "--home", homeDaemonID, "--json")
}

func (b *Bootstrapper) enrollRemote(ctx context.Context, sshTarget, instance, homeDaemonID string) error {
	if strings.TrimSpace(homeDaemonID) == "" {
		b.logf("skipping enrollment of %s: this daemon is not a home daemon", sshTarget)
		return nil
	}
	stdout, stderr, code, err := runSSHExit(ctx, sshTarget, instance, remoteEnrollScript(instance, homeDaemonID))
	if err != nil {
		b.logf("enrollment check on %s could not run: %v", sshTarget, err)
		return nil
	}
	switch code {
	case 0:
		var result enrollment.Result
		if jsonErr := json.Unmarshal([]byte(stdout), &result); jsonErr != nil {
			b.logf("enrollment on %s returned unreadable output %q", sshTarget, stdout)
			return nil
		}
		if result.Changed() {
			b.logf("enrolled %s as an outpost of %s", sshTarget, homeDaemonID)
		}
		return nil
	case enrollmentRefusedExitCode:
		message := stderr
		if message == "" {
			message = stdout
		}
		return fmt.Errorf("%s is enrolled to another home: %s", sshTarget, withoutInstanceBanner(message))
	default:
		detail := stderr
		if detail == "" {
			detail = stdout
		}
		b.logf("enrollment of %s skipped (exit %d): %s", sshTarget, code, detail)
		return nil
	}
}

func shouldInstallRemoteBinary(localVersion, remoteVersion string, preferSourceBuild bool, localHash, remoteHash string) bool {
	if remoteVersion != localVersion {
		return true
	}
	if preferSourceBuild && remoteHash != localHash {
		return true
	}
	return false
}

func (b *Bootstrapper) detectRemotePlatform(ctx context.Context, sshTarget, instance string) (RemotePlatform, error) {
	out, err := runSSH(ctx, sshTarget, instance, "uname -sm")
	if err != nil {
		return RemotePlatform{}, err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 2 {
		return RemotePlatform{}, fmt.Errorf("unexpected uname output: %q", out)
	}
	if fields[0] != "Linux" {
		return RemotePlatform{}, fmt.Errorf("unsupported platform %q (Linux only)", out)
	}
	return remoteLinuxPlatform(fields[1])
}

func remoteLinuxPlatform(machine string) (RemotePlatform, error) {
	switch machine {
	case "x86_64", "amd64":
		return RemotePlatform{
			GOOS:                "linux",
			GOARCH:              "amd64",
			ArtifactName:        "attn-linux-amd64",
			RuntimeArtifactName: apps.RuntimeHostBinaryName + "-linux-amd64",
			PTYHostArtifactName: ptyhost.BinaryName + "-linux-amd64",
			BunTarget:           "bun-linux-x64",
		}, nil
	case "aarch64", "arm64":
		return RemotePlatform{
			GOOS:                "linux",
			GOARCH:              "arm64",
			ArtifactName:        "attn-linux-arm64",
			RuntimeArtifactName: apps.RuntimeHostBinaryName + "-linux-arm64",
			PTYHostArtifactName: ptyhost.BinaryName + "-linux-arm64",
			BunTarget:           "bun-linux-arm64",
		}, nil
	default:
		return RemotePlatform{}, fmt.Errorf("unsupported architecture %q", machine)
	}
}

func (b *Bootstrapper) remoteVersion(ctx context.Context, sshTarget, instance string) (string, error) {
	binName := remoteBinaryName(instance)
	script := fmt.Sprintf(`
ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"
if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then
  ATTN_BIN="$(command -v %s 2>/dev/null || true)"
fi
if [ -z "$ATTN_BIN" ] || [ ! -x "$ATTN_BIN" ]; then
  printf NOT_FOUND
  exit 0
fi
"$ATTN_BIN" --version 2>/dev/null || printf NOT_FOUND
`, binName, binName)
	out, err := runSSH(ctx, sshTarget, instance, script)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "NOT_FOUND" {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

func (b *Bootstrapper) localVersion(ctx context.Context) (string, error) {
	b.versionOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			b.versionErr = err
			return
		}
		out, err := exec.CommandContext(ctx, exe, "--version").Output()
		if err != nil {
			b.versionErr = err
			return
		}
		b.version = strings.TrimSpace(string(out))
		if b.version == "" {
			b.versionErr = fmt.Errorf("empty version output")
		}
	})
	return b.version, b.versionErr
}

func (b *Bootstrapper) ensureLocalBinary(ctx context.Context, platform RemotePlatform, version string) (string, error) {
	cacheKey, preferSourceBuild, err := b.localBinaryCacheKey(version)
	if err != nil {
		return "", err
	}
	cachePath := remoteBinaryCachePath(cacheKey, platform)
	if info, err := os.Stat(cachePath); err == nil && info.Mode().IsRegular() {
		return cachePath, nil
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return "", err
	}

	if preferSourceBuild {
		if err := b.buildBinaryFromSource(ctx, platform, version, cachePath); err == nil {
			return cachePath, nil
		} else {
			b.logf("source build failed for %s %s: %v", cacheKey, platform.ArtifactName, err)
		}
	}

	if version != "" && version != "dev" {
		if err := b.downloadReleaseArtifact(ctx, version, platform.ArtifactName, filepath.Dir(cachePath)); err == nil {
			return cachePath, nil
		} else {
			b.logf("release download failed for %s %s: %v", version, platform.ArtifactName, err)
		}
	}

	if err := b.buildBinaryFromSource(ctx, platform, version, cachePath); err != nil {
		return "", err
	}
	return cachePath, nil
}

func remoteBinaryCachePath(key string, platform RemotePlatform) string {
	return filepath.Join(config.DataDir(), "remotes", "binaries", key, platform.ArtifactName)
}

func (b *Bootstrapper) downloadReleaseArtifact(ctx context.Context, version, artifact, destDir string) error {
	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	cmd := exec.CommandContext(ctx, "gh", "release", "download", tag, "--repo", githubRepo, "--pattern", artifact, "--dir", destDir, "--clobber")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh release download %s: %s", tag, strings.TrimSpace(string(out)))
	}
	return nil
}

func appRuntimeCacheDir(key string) string {
	return filepath.Join(config.DataDir(), "remotes", "app-runtime", key)
}

func (b *Bootstrapper) ensureLocalAppRuntime(ctx context.Context, platform RemotePlatform, version string) (string, error) {
	var reasons []string
	if sourceCheckoutAvailable() {
		stageDir := appRuntimeCacheDir(platform.GOOS + "_" + platform.GOARCH)
		if err := b.buildAppRuntimeFromSource(ctx, platform, stageDir); err == nil {
			return filepath.Join(stageDir, apps.RuntimeHostBinaryName), nil
		} else {
			reasons = append(reasons, fmt.Sprintf("source build: %v", err))
		}
	}

	if version != "" && version != "dev" {
		cachePath := filepath.Join(appRuntimeCacheDir(version), platform.RuntimeArtifactName)
		if info, err := os.Stat(cachePath); err == nil && info.Mode().IsRegular() {
			return cachePath, nil
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return "", err
		}
		if err := b.downloadReleaseArtifact(ctx, version, platform.RuntimeArtifactName, filepath.Dir(cachePath)); err == nil {
			return cachePath, nil
		} else {
			reasons = append(reasons, fmt.Sprintf("release download: %v", err))
		}
	} else {
		reasons = append(reasons, "no published release to download it from (this hub reports version "+version+")")
	}

	return "", fmt.Errorf("no %s available (%s)", platform.RuntimeArtifactName, strings.Join(reasons, "; "))
}

func ptyHostCacheDir(key string) string {
	return filepath.Join(config.DataDir(), "remotes", "pty-host", key)
}

func (b *Bootstrapper) ensureLocalPTYHost(ctx context.Context, platform RemotePlatform, version string) (string, error) {
	var reasons []string
	if sourceCheckoutAvailable() {
		if path, err := b.buildPTYHostFromSource(ctx, platform); err == nil {
			return path, nil
		} else {
			reasons = append(reasons, fmt.Sprintf("source build: %v", err))
		}
	}

	if version != "" && version != "dev" {
		cachePath := filepath.Join(ptyHostCacheDir(version), platform.PTYHostArtifactName)
		if info, err := os.Stat(cachePath); err == nil && info.Mode().IsRegular() {
			return cachePath, nil
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return "", err
		}
		if err := b.downloadReleaseArtifact(ctx, version, platform.PTYHostArtifactName, filepath.Dir(cachePath)); err == nil {
			return cachePath, nil
		} else {
			reasons = append(reasons, fmt.Sprintf("release download: %v", err))
		}
	} else {
		reasons = append(reasons, "no published release to download it from (this hub reports version "+version+")")
	}

	return "", fmt.Errorf("no %s available (%s)", platform.PTYHostArtifactName, strings.Join(reasons, "; "))
}

func (b *Bootstrapper) buildPTYHostFromSource(ctx context.Context, platform RemotePlatform) (string, error) {
	root := sourceRoot()
	if root == "" {
		return "", errors.New("source checkout not available")
	}
	target := "build-pty-host-linux-" + platform.GOARCH
	cmd := exec.CommandContext(ctx, "make", target)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s: %s", target, strings.TrimSpace(string(out)))
	}
	path := filepath.Join(root, "dist", "pty-host", platform.GOOS+"_"+platform.GOARCH, ptyhost.BinaryName)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s did not produce %s", target, path)
	}
	return path, nil
}

func (b *Bootstrapper) buildAppRuntimeFromSource(ctx context.Context, platform RemotePlatform, stageDir string) error {
	root := sourceRoot()
	if root == "" {
		return fmt.Errorf("source checkout not available")
	}
	if platform.BunTarget == "" {
		return fmt.Errorf("no bun target for %s/%s", platform.GOOS, platform.GOARCH)
	}
	script := filepath.Join(root, "scripts", "build-app-runtime-host.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("%s is not in this checkout", script)
	}
	cmd := exec.CommandContext(ctx, "bash", script, stageDir, platform.BunTarget)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

func remoteAppRuntimePath(remoteInstallPath, instance string) string {
	return filepath.Join(filepath.Dir(remoteInstallPath), apps.RuntimeHostBinaryNameForInstance(instance))
}

func remotePTYHostPath(remoteInstallPath, instance string) string {
	return filepath.Join(filepath.Dir(remoteInstallPath), ptyhost.BinaryNameForInstance(instance))
}

func (b *Bootstrapper) ensureRemotePTYHost(ctx context.Context, sshTarget, instance string, platform RemotePlatform, version, remoteInstallPath string) (bool, bool, error) {
	remotePath := remotePTYHostPath(remoteInstallPath, instance)
	localPath, err := b.ensureLocalPTYHost(ctx, platform, version)
	if err != nil {
		return false, false, fmt.Errorf(
			"the shared PTY host is missing from %s at %s: %w. New terminals there stay on dedicated Go workers until a host is installed",
			sshTarget, remotePath, err)
	}
	localHash, err := fileSHA256(localPath)
	if err != nil {
		return false, false, fmt.Errorf("hash the local PTY host %s: %w", localPath, err)
	}
	remoteHash, err := b.remoteFileSHA256(ctx, sshTarget, instance, shellQuote(remotePath))
	if err != nil {
		return false, false, fmt.Errorf("hash the PTY host on %s at %s: %w", sshTarget, remotePath, err)
	}
	if remoteHash == localHash {
		return false, false, nil
	}
	wasMissing := remoteHash == ""
	if err := b.uploadRemoteFile(ctx, sshTarget, instance, localPath, remotePath); err != nil {
		return false, wasMissing, fmt.Errorf(
			"the shared PTY host could not be installed on %s at %s: %w. New terminals there stay on dedicated Go workers",
			sshTarget, remotePath, err)
	}
	b.logf("installed the shared PTY host on %s at %s (%s)", sshTarget, remotePath, localHash[:12])
	return true, wasMissing, nil
}

func (b *Bootstrapper) ensureRemoteAppRuntime(ctx context.Context, sshTarget, instance string, platform RemotePlatform, version, remoteInstallPath string) (bool, error) {
	remotePath := remoteAppRuntimePath(remoteInstallPath, instance)

	localPath, err := b.ensureLocalAppRuntime(ctx, platform, version)
	if err != nil {
		return false, fmt.Errorf(
			"the app runtime host is missing from %s at %s: %w. That daemon falls back to an unsuffixed %s beside it and parks its apps only if there is none; run the hub from a source checkout with bun installed, or copy %s there yourself",
			sshTarget, remotePath, err, apps.RuntimeHostBinaryName, platform.RuntimeArtifactName)
	}

	localHash, err := fileSHA256(localPath)
	if err != nil {
		return false, fmt.Errorf("hash the local app runtime host %s: %w", localPath, err)
	}
	remoteHash, err := b.remoteFileSHA256(ctx, sshTarget, instance, shellQuote(remotePath))
	if err != nil {
		return false, fmt.Errorf("hash the app runtime host on %s at %s: %w", sshTarget, remotePath, err)
	}
	if remoteHash == localHash {
		return false, nil
	}

	if err := b.uploadRemoteFile(ctx, sshTarget, instance, localPath, remotePath); err != nil {
		return false, fmt.Errorf(
			"the app runtime host could not be installed on %s at %s: %w. That daemon keeps whatever host it already resolves — an unsuffixed %s beside it, or none, in which case its apps park",
			sshTarget, remotePath, err, apps.RuntimeHostBinaryName)
	}
	b.logf("installed the app runtime host on %s at %s (%s)", sshTarget, remotePath, localHash[:12])
	return true, nil
}

func sourceRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func sourceCheckoutAvailable() bool {
	root := sourceRoot()
	if root == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return false
	}
	return true
}

func localBinaryFingerprint() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return fileSHA256(exe)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func remoteSHA256Script(pathExpr string) string {
	return fmt.Sprintf(`
if [ ! -f %[1]s ]; then
  printf NOT_FOUND
  exit 0
fi
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum %[1]s | awk '{print $1}'
  exit 0
fi
if command -v shasum >/dev/null 2>&1; then
  shasum -a 256 %[1]s | awk '{print $1}'
  exit 0
fi
printf NO_HASH_TOOL
`, pathExpr)
}

func (b *Bootstrapper) runRemoteSHA256(ctx context.Context, sshTarget, instance, script string) (string, error) {
	out, err := runSSH(ctx, sshTarget, instance, script)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(out)
	switch value {
	case "", "NOT_FOUND":
		return "", nil
	case "NO_HASH_TOOL":
		return "", fmt.Errorf("remote host has neither sha256sum nor shasum")
	default:
		return value, nil
	}
}

func (b *Bootstrapper) remoteFileSHA256(ctx context.Context, sshTarget, instance, pathExpr string) (string, error) {
	return b.runRemoteSHA256(ctx, sshTarget, instance, remoteSHA256Script(pathExpr))
}

func (b *Bootstrapper) remoteBinarySHA256(ctx context.Context, sshTarget, instance string) (string, error) {
	binName := remoteBinaryName(instance)
	resolve := fmt.Sprintf(`
ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"
if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then
  ATTN_BIN="$(command -v %s 2>/dev/null || true)"
fi
`, binName, binName)
	return b.runRemoteSHA256(ctx, sshTarget, instance, resolve+remoteSHA256Script(`"$ATTN_BIN"`))
}

func (b *Bootstrapper) localBinaryCacheKey(version string) (string, bool, error) {
	cacheVersion := strings.TrimSpace(version)
	if cacheVersion == "" {
		cacheVersion = "unknown"
	}
	if !sourceCheckoutAvailable() {
		return cacheVersion, false, nil
	}

	fingerprint, err := localBinaryFingerprint()
	if err != nil {
		return "", false, fmt.Errorf("fingerprint local binary: %w", err)
	}
	if len(fingerprint) > 12 {
		fingerprint = fingerprint[:12]
	}
	return fmt.Sprintf("source-%s-%s", cacheVersion, fingerprint), true, nil
}

func zigTargetForPlatform(platform RemotePlatform) (string, error) {
	switch {
	case platform.GOOS == "linux" && platform.GOARCH == "amd64":
		return "x86_64-linux-gnu", nil
	case platform.GOOS == "linux" && platform.GOARCH == "arm64":
		return "aarch64-linux-gnu", nil
	default:
		return "", fmt.Errorf("unsupported zig target for %s/%s", platform.GOOS, platform.GOARCH)
	}
}

func (b *Bootstrapper) buildBinaryFromSource(ctx context.Context, platform RemotePlatform, version, outputPath string) error {
	root := sourceRoot()
	if root == "" {
		return fmt.Errorf("source checkout not available for fallback build")
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return fmt.Errorf("source checkout not available for fallback build")
	}

	ldflags := "-X github.com/victorarias/attn/internal/buildinfo.Version=" + version
	if fp := buildinfo.SourceFingerprint; fp != "" && fp != "unknown" {
		ldflags += " -X github.com/victorarias/attn/internal/buildinfo.SourceFingerprint=" + fp
	}
	if gc := buildinfo.GitCommit; gc != "" && gc != "unknown" {
		ldflags += " -X github.com/victorarias/attn/internal/buildinfo.GitCommit=" + gc
	}
	if sf := buildinfo.SnapshotFormat; sf != "" && sf != "unknown" {
		ldflags += " -X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat=" + sf
	}
	if err := ensureNativeVTArchive(ctx, root, platform); err != nil {
		return err
	}

	cmd := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-ldflags",
		ldflags,
		"-o",
		outputPath,
		"./cmd/attn",
	)
	cmd.Dir = root
	env := append(os.Environ(), "GOOS="+platform.GOOS, "GOARCH="+platform.GOARCH)
	if platform.GOOS == "linux" {
		env = append(env, "CGO_ENABLED=1")
		if runtime.GOOS != "linux" {
			if _, err := exec.LookPath("zig"); err != nil {
				return fmt.Errorf(
					"zig is required to cross-compile %s with cgo from %s; install zig or use the published Linux artifact",
					platform.ArtifactName,
					runtime.GOOS,
				)
			}
			target, err := zigTargetForPlatform(platform)
			if err != nil {
				return err
			}
			env = append(env,
				"CC=zig cc -target "+target,
				"CXX=zig c++ -target "+target,
			)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cross-compile %s: %s", platform.ArtifactName, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensureNativeVTArchive(ctx context.Context, root string, platform RemotePlatform) error {
	script := filepath.Join(root, "scripts", "build-libghostty-vt.sh")
	if _, err := os.Stat(script); err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GHOSTTY_VT_GOOS="+platform.GOOS,
		"GHOSTTY_VT_GOARCH="+platform.GOARCH,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ensure native libghostty-vt for %s: %s", platform.ArtifactName, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveRemoteInstallPath(remoteHome, override, instance string) string {
	path := strings.TrimSpace(override)
	if path == "" {
		return filepath.Join(remoteHome, ".local", "bin", remoteBinaryName(instance))
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(remoteHome, path[2:])
	}
	return path
}

func (b *Bootstrapper) resolveRemoteInstall(ctx context.Context, sshTarget, instance string) (string, error) {
	remoteHome, err := runSSH(ctx, sshTarget, instance, `printf '%s' "$HOME"`)
	if err != nil {
		return "", err
	}
	return resolveRemoteInstallPath(strings.TrimSpace(remoteHome), os.Getenv("ATTN_REMOTE_ATTN_BIN"), instance), nil
}

func (b *Bootstrapper) installRemoteBinary(ctx context.Context, sshTarget, instance, localBinary, remoteInstallPath string) error {
	attnDir := remoteAttnDirShell(instance)
	if _, err := runSSH(ctx, sshTarget, instance, fmt.Sprintf("mkdir -p %s", attnDir)); err != nil {
		return err
	}
	return b.uploadRemoteFile(ctx, sshTarget, instance, localBinary, remoteInstallPath)
}

func (b *Bootstrapper) uploadRemoteFile(ctx context.Context, sshTarget, instance, localPath, remotePath string) error {
	remoteDir := filepath.Dir(remotePath)
	remoteTmpPath := filepath.Join("/tmp", fmt.Sprintf("%s.%d.%d.tmp", filepath.Base(remotePath), os.Getpid(), time.Now().UnixNano()))
	if _, err := runSSH(ctx, sshTarget, instance, fmt.Sprintf("mkdir -p %s", shellQuote(remoteDir))); err != nil {
		return err
	}
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", localPath, err)
	}

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if _, err := runSSH(cleanupCtx, sshTarget, instance, fmt.Sprintf("rm -f %s", shellQuote(remoteTmpPath))); err != nil {
			b.logf("could not remove the staging file %s on %s: %v", remoteTmpPath, sshTarget, err)
		}
	}()

	cmd := exec.CommandContext(
		ctx,
		"ssh",
		append(sshBaseArgs(sshTarget), remoteShellCommand(instance, fmt.Sprintf("cat > %s", shellQuote(remoteTmpPath))))...,
	)
	cmd.Stdin = file
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy %s over ssh: %s", filepath.Base(localPath), strings.TrimSpace(string(out)))
	}

	probe, err := runSSH(
		ctx,
		sshTarget,
		instance,
		fmt.Sprintf("if [ -f %s ]; then wc -c < %s; else printf MISSING; fi", shellQuote(remoteTmpPath), shellQuote(remoteTmpPath)),
	)
	if err != nil {
		return fmt.Errorf("check the uploaded size of %s on %s: %w", filepath.Base(localPath), sshTarget, err)
	}
	if got := strings.TrimSpace(probe); got != fmt.Sprintf("%d", info.Size()) {
		return fmt.Errorf(
			"%s arrived on %s as %s bytes, not %d — the transfer was cut short",
			filepath.Base(localPath), sshTarget, got, info.Size(),
		)
	}

	_, err = runSSH(
		ctx,
		sshTarget,
		instance,
		fmt.Sprintf("install -m 755 %s %s", shellQuote(remoteTmpPath), shellQuote(remotePath)),
	)
	return err
}

func remoteAttnDirShell(instance string) string {
	if strings.TrimSpace(instance) == "" {
		return `"$HOME/.attn"`
	}
	return `"$HOME/.attn-${ATTN_INSTANCE}"`
}

func remoteSocketConfigScript() string {
	return `
attn_instance="${ATTN_INSTANCE:-}"
if [ -n "$attn_instance" ]; then
  attn_dir="$HOME/.attn-$attn_instance"
else
  attn_dir="$HOME/.attn"
fi
config_path="${ATTN_CONFIG_PATH:-$attn_dir/config.json}"
socket_path="${ATTN_SOCKET_PATH:-}"
if [ -z "$socket_path" ] && [ -f "$config_path" ]; then
  socket_path="$(sed -n 's/.*"socket_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$config_path" | head -n 1)"
fi
if [ -z "$socket_path" ]; then
  socket_path="$attn_dir/attn.sock"
fi
case "$socket_path" in
  "~/"*) socket_path="$HOME/${socket_path#~/}" ;;
esac
pid_path="$(dirname "$socket_path")/attn.pid"
`
}

func isRemoteHarnessOverridePath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, remoteHarnessRootMarker) || strings.Contains(trimmed, "~"+remoteHarnessRootMarker)
}

func remoteHarnessCleanupEnabled() bool {
	return isRemoteHarnessOverridePath(os.Getenv("ATTN_REMOTE_SOCKET_PATH")) ||
		isRemoteHarnessOverridePath(os.Getenv("ATTN_REMOTE_DB_PATH")) ||
		isRemoteHarnessOverridePath(os.Getenv("ATTN_REMOTE_ATTN_BIN"))
}

func remoteRoutingInstance(instance string) string {
	if remoteHarnessCleanupEnabled() {
		return ""
	}
	return strings.TrimSpace(instance)
}

type remoteDaemonState struct {
	Running  bool
	Starting bool
	Stale    bool
	PID      string
}

func (b *Bootstrapper) probeRemoteDaemon(ctx context.Context, sshTarget, instance string) (remoteDaemonState, error) {
	port := config.WSPortForInstance(instance)
	script := remoteSocketConfigScript() + fmt.Sprintf(`
listener_pid="$(ss -H -ltnp "( sport = :${ATTN_WS_PORT:-%s} )" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n 1)"
if [ -n "$listener_pid" ]; then
  printf 'running %%s\n' "$listener_pid"
  exit 0
fi
if [ -S "$socket_path" ] && [ -f "$pid_path" ]; then
  pid="$(cat "$pid_path" 2>/dev/null || true)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    printf 'starting %%s\n' "$pid"
    exit 0
  fi
  printf 'stale %%s\n' "$pid"
  exit 0
fi
printf 'stopped\n'
`, port)
	out, err := runSSH(ctx, sshTarget, instance, script)
	if err != nil {
		return remoteDaemonState{}, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return remoteDaemonState{}, fmt.Errorf("empty probe response")
	}
	switch fields[0] {
	case "running":
		state := remoteDaemonState{Running: true}
		if len(fields) > 1 {
			state.PID = fields[1]
		}
		return state, nil
	case "starting":
		state := remoteDaemonState{Starting: true}
		if len(fields) > 1 {
			state.PID = fields[1]
		}
		return state, nil
	case "stale":
		state := remoteDaemonState{Stale: true}
		if len(fields) > 1 {
			state.PID = fields[1]
		}
		return state, nil
	case "stopped":
		return remoteDaemonState{}, nil
	default:
		return remoteDaemonState{}, fmt.Errorf("unexpected probe response %q", out)
	}
}

func (b *Bootstrapper) ensureRemoteDaemonRunning(ctx context.Context, sshTarget, instance string, binariesUpdated bool) error {
	state, err := b.probeRemoteDaemon(ctx, sshTarget, instance)
	if err != nil {
		return err
	}

	if state.Stale {
		if _, err := runSSH(ctx, sshTarget, instance, removeStaleRemoteSocketScript()); err != nil {
			return err
		}
		state = remoteDaemonState{}
	}

	if (state.Running || state.Starting) && binariesUpdated {
		if err := b.restartRemoteDaemon(ctx, sshTarget, instance, state.PID); err != nil {
			return err
		}
		state = remoteDaemonState{}
	}

	if !state.Running && !state.Starting {
		if err := b.startRemoteDaemon(ctx, sshTarget, instance); err != nil {
			return err
		}
	}

	deadline := time.Now().Add(remoteDaemonReadyTimeout)
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		current, err := b.probeRemoteDaemon(probeCtx, sshTarget, instance)
		cancel()
		if err == nil && current.Running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become ready")
}

func startRemoteDaemonScript(instance string) string {
	binName := remoteBinaryName(instance)
	attnDir := remoteAttnDirShell(instance)
	return fmt.Sprintf(`
mkdir -p %s
ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"
if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then
  ATTN_BIN="$(command -v %s 2>/dev/null || true)"
fi
if [ -z "$ATTN_BIN" ] || [ ! -x "$ATTN_BIN" ]; then
  printf 'missing attn binary\n' >&2
  exit 127
fi
nohup setsid "$ATTN_BIN" daemon </dev/null >>%s/daemon.log 2>&1 &
`, attnDir, binName, binName, attnDir)
}

func (b *Bootstrapper) startRemoteDaemon(ctx context.Context, sshTarget, instance string) error {
	_, err := runSSH(
		ctx,
		sshTarget,
		instance,
		startRemoteDaemonScript(instance),
	)
	return err
}

func stopRemoteDaemonScript(instance string) string {
	port := config.WSPortForInstance(instance)
	return remoteSocketConfigScript() + fmt.Sprintf(`
listener_pid="$(ss -H -ltnp "( sport = :${ATTN_WS_PORT:-%s} )" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n 1)"
pid_file_pid=""
if [ -f "$pid_path" ]; then
  pid_file_pid="$(cat "$pid_path" 2>/dev/null || true)"
fi
seen_pids=""
for pid in "$listener_pid" "$pid_file_pid"; do
  [ -n "$pid" ] || continue
  case " $seen_pids " in
    *" $pid "*) continue ;;
  esac
  seen_pids="$seen_pids $pid"
  kill "$pid" 2>/dev/null || true
done
sleep 0.5
for pid in $seen_pids; do
  kill -0 "$pid" 2>/dev/null || continue
  kill -9 "$pid" 2>/dev/null || true
done
rm -f "$socket_path"
`, port)
}

func (b *Bootstrapper) StopRemoteDaemon(ctx context.Context, sshTarget, instance string) error {
	if !remoteHarnessCleanupEnabled() {
		return nil
	}
	_, err := runSSH(ctx, sshTarget, instance, stopRemoteDaemonScript(instance))
	return err
}

func (b *Bootstrapper) restartRemoteDaemon(ctx context.Context, sshTarget, instance, pid string) error {
	if strings.TrimSpace(pid) != "" {
		_, _ = runSSH(ctx, sshTarget, instance, fmt.Sprintf("kill %s 2>/dev/null || true", shellQuote(pid)))
		time.Sleep(500 * time.Millisecond)
		_, _ = runSSH(ctx, sshTarget, instance, fmt.Sprintf("kill -9 %s 2>/dev/null || true", shellQuote(pid)))
	}
	if _, err := runSSH(ctx, sshTarget, instance, removeStaleRemoteSocketScript()); err != nil {
		return err
	}
	return b.startRemoteDaemon(ctx, sshTarget, instance)
}

func removeStaleRemoteSocketScript() string {
	return remoteSocketConfigScript() + `rm -f "$socket_path"`
}
