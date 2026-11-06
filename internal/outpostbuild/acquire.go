package outpostbuild

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/ptyhost"
)

type Request struct {
	Build         bridge.Build
	Channel       string
	Repository    string
	ReleaseSource string
	Cache         string
	Platform      string
}

type Binaries struct {
	Attn string
	Host string
}

func Acquire(ctx context.Context, req Request, phase func(string)) (Binaries, error) {
	arch, ok := strings.CutPrefix(req.Platform, "linux/")
	if !ok || arch != "amd64" && arch != "arm64" {
		return Binaries{}, fmt.Errorf("outpost platform %s is unsupported; need linux/amd64 or linux/arm64", req.Platform)
	}
	if req.Channel != "source" && req.Channel != "release" {
		return Binaries{}, fmt.Errorf("home install channel %q cannot provide an outpost build; need source or release", req.Channel)
	}
	if req.Channel == "source" && strings.HasPrefix(req.Build.SourceFingerprint, "tree:") {
		if err := req.checkWorktree(ctx); err != nil {
			return Binaries{}, err
		}
	}
	identity, _ := json.Marshal(req.Build)
	key := fmt.Sprintf("%x", sha256.Sum256(identity))
	dir := filepath.Join(req.Cache, key, arch)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Binaries{}, err
	}
	cached := Binaries{Attn: filepath.Join(dir, "attn"), Host: filepath.Join(dir, ptyhost.BinaryName)}
	if nonEmptyFile(cached.Attn) && nonEmptyFile(cached.Host) {
		return cached, req.prune(key, arch)
	}
	staging, err := os.MkdirTemp(dir, ".build-*")
	if err != nil {
		return Binaries{}, err
	}
	defer os.RemoveAll(staging)
	staged := Binaries{Attn: filepath.Join(staging, "attn"), Host: filepath.Join(staging, ptyhost.BinaryName)}
	switch req.Channel {
	case "release":
		phase("downloading")
		err = req.download(ctx, arch, staged)
	case "source":
		phase("building")
		err = req.build(ctx, arch, staging, staged)
	}
	if err != nil {
		return Binaries{}, err
	}
	if err := os.Rename(staged.Host, cached.Host); err != nil {
		return Binaries{}, err
	}
	if err := os.Rename(staged.Attn, cached.Attn); err != nil {
		return Binaries{}, err
	}
	return cached, req.prune(key, arch)
}

func nonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func (req Request) download(ctx context.Context, arch string, staged Binaries) error {
	release := strings.TrimRight(req.ReleaseSource, "/") + "/v" + strings.TrimPrefix(req.Build.Version, "v") + "/"
	if err := downloadBinary(ctx, release+"attn-linux-"+arch, staged.Attn); err != nil {
		return err
	}
	return downloadBinary(ctx, release+ptyhost.BinaryName+"-linux-"+arch, staged.Host)
}

func downloadBinary(ctx context.Context, url, output string) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, response.Status)
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	size, copyErr := io.Copy(file, response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("download %s: %w", url, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if size == 0 {
		return fmt.Errorf("download %s returned an empty binary", url)
	}
	return nil
}

func (req Request) build(ctx context.Context, arch, staging string, staged Binaries) error {
	rustTarget := "x86_64-unknown-linux-gnu"
	zigTarget := "x86_64-linux-gnu"
	if arch == "arm64" {
		rustTarget = "aarch64-unknown-linux-gnu"
		zigTarget = "aarch64-linux-gnu"
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		return fmt.Errorf("building %s for outpost %s needs a Rust toolchain, and cargo is not on PATH; install Rust through rustup and run: rustup target add %s", ptyhost.BinaryName, req.Platform, rustTarget)
	}
	repo := req.Repository
	if repo == "" {
		return fmt.Errorf("source build has no recorded repository path")
	}
	source := repo
	gitCommit := ""
	if commit, ok := strings.CutPrefix(req.Build.SourceFingerprint, "git:"); ok {
		gitCommit = commit
		archive := filepath.Join(staging, "source.tar")
		if _, err := command(ctx, repo, "git", "archive", "--format=tar", "--output="+archive, commit); err != nil {
			return fmt.Errorf("export home build %s from %s: %w", req.Build.SourceFingerprint, repo, err)
		}
		source = filepath.Join(staging, "source")
		if err := os.Mkdir(source, 0700); err != nil {
			return err
		}
		if _, err := command(ctx, staging, "tar", "-xf", archive, "-C", source); err != nil {
			return err
		}
	} else if !strings.HasPrefix(req.Build.SourceFingerprint, "tree:") {
		return fmt.Errorf("source fingerprint %q names neither a git commit nor a worktree", req.Build.SourceFingerprint)
	}
	buildArgs := []string{"build", "GOOS=linux", "GOARCH=" + arch, "CGO_ENABLED=1", "CC=zig cc -target " + zigTarget, "CXX=zig c++ -target " + zigTarget, "OUTPUT=" + staged.Attn, "VERSION=" + req.Build.Version, "CHANNEL=source", "SOURCE_FINGERPRINT=" + req.Build.SourceFingerprint, "REPOSITORY_PATH=" + repo}
	if gitCommit != "" {
		buildArgs = append(buildArgs, "GIT_COMMIT="+gitCommit)
	}
	if _, err := command(ctx, source, "make", buildArgs...); err != nil {
		return fmt.Errorf("build outpost %s: %w", req.Platform, err)
	}
	if _, err := command(ctx, source, "make", "build-pty-host-linux-"+arch); err != nil {
		return fmt.Errorf("build %s for outpost %s: %w", ptyhost.BinaryName, req.Platform, err)
	}
	built := filepath.Join(source, "dist", "pty-host", "linux_"+arch, ptyhost.BinaryName)
	if _, err := command(ctx, source, "cp", built, staged.Host); err != nil {
		return err
	}
	if strings.HasPrefix(req.Build.SourceFingerprint, "tree:") {
		return req.checkWorktree(ctx)
	}
	return nil
}

func (req Request) prune(current, arch string) error {
	builds, err := os.ReadDir(req.Cache)
	if err != nil {
		return fmt.Errorf("prune outpost build cache: %w", err)
	}
	for _, build := range builds {
		if !build.IsDir() || build.Name() == current {
			continue
		}
		dir := filepath.Join(req.Cache, build.Name())
		if err := os.RemoveAll(filepath.Join(dir, arch)); err != nil {
			return fmt.Errorf("prune outpost build cache %s for %s: %w", build.Name(), arch, err)
		}
		if err := os.Remove(dir); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
			return fmt.Errorf("prune outpost build cache %s: %w", build.Name(), err)
		}
	}
	return nil
}

func command(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (req Request) checkWorktree(ctx context.Context) error {
	if req.Repository == "" {
		return fmt.Errorf("source build has no recorded repository path")
	}
	actual, err := command(ctx, req.Repository, "bash", "scripts/source-fingerprint.sh", "--field", "fingerprint")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(actual)) != req.Build.SourceFingerprint {
		return fmt.Errorf("recorded worktree %s now has fingerprint %s; home build requires %s", req.Repository, strings.TrimSpace(string(actual)), req.Build.SourceFingerprint)
	}
	return nil
}
