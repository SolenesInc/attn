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
)

type Request struct {
	Build         bridge.Build
	Channel       string
	Repository    string
	ReleaseSource string
	Cache         string
	Platform      string
}

func Acquire(ctx context.Context, req Request, phase func(string)) (string, error) {
	arch, ok := strings.CutPrefix(req.Platform, "linux/")
	if !ok || arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("outpost platform %s is unsupported; need linux/amd64 or linux/arm64", req.Platform)
	}
	if req.Channel != "source" && req.Channel != "release" {
		return "", fmt.Errorf("home install channel %q cannot provide an outpost build; need source or release", req.Channel)
	}
	if req.Channel == "source" && strings.HasPrefix(req.Build.SourceFingerprint, "tree:") {
		if err := req.checkWorktree(ctx); err != nil {
			return "", err
		}
	}
	identity, _ := json.Marshal(req.Build)
	key := fmt.Sprintf("%x", sha256.Sum256(identity))
	dir := filepath.Join(req.Cache, key, arch)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "attn")
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, req.prune(key, arch)
	}
	staging, err := os.MkdirTemp(dir, ".build-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	output := filepath.Join(staging, "attn")
	switch req.Channel {
	case "release":
		phase("downloading")
		url := strings.TrimRight(req.ReleaseSource, "/") + "/v" + strings.TrimPrefix(req.Build.Version, "v") + "/attn-linux-" + arch
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		response, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("download %s: %w", url, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("download %s: %s", url, response.Status)
		}
		file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if err != nil {
			return "", err
		}
		size, copyErr := io.Copy(file, response.Body)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("download %s: %w", url, copyErr)
		}
		if closeErr != nil {
			return "", closeErr
		}
		if size == 0 {
			return "", fmt.Errorf("download %s returned an empty binary", url)
		}
	case "source":
		phase("building")
		repo := req.Repository
		if repo == "" {
			return "", fmt.Errorf("source build has no recorded repository path")
		}
		source := repo
		gitCommit := ""
		if commit, ok := strings.CutPrefix(req.Build.SourceFingerprint, "git:"); ok {
			gitCommit = commit
			archive := filepath.Join(staging, "source.tar")
			if _, err := command(ctx, repo, "git", "archive", "--format=tar", "--output="+archive, commit); err != nil {
				return "", fmt.Errorf("export home build %s from %s: %w", req.Build.SourceFingerprint, repo, err)
			}
			source = filepath.Join(staging, "source")
			if err := os.Mkdir(source, 0700); err != nil {
				return "", err
			}
			if _, err := command(ctx, staging, "tar", "-xf", archive, "-C", source); err != nil {
				return "", err
			}
		} else if !strings.HasPrefix(req.Build.SourceFingerprint, "tree:") {
			return "", fmt.Errorf("source fingerprint %q names neither a git commit nor a worktree", req.Build.SourceFingerprint)
		}
		target := "x86_64-linux-gnu"
		if arch == "arm64" {
			target = "aarch64-linux-gnu"
		}
		buildArgs := []string{"build", "GOOS=linux", "GOARCH=" + arch, "CGO_ENABLED=1", "CC=zig cc -target " + target, "CXX=zig c++ -target " + target, "OUTPUT=" + output, "VERSION=" + req.Build.Version, "CHANNEL=source", "SOURCE_FINGERPRINT=" + req.Build.SourceFingerprint, "REPOSITORY_PATH=" + repo}
		if gitCommit != "" {
			buildArgs = append(buildArgs, "GIT_COMMIT="+gitCommit)
		}
		_, err := command(ctx, source, "make", buildArgs...)
		if err != nil {
			return "", fmt.Errorf("build outpost %s: %w", req.Platform, err)
		}
		if strings.HasPrefix(req.Build.SourceFingerprint, "tree:") {
			if err := req.checkWorktree(ctx); err != nil {
				return "", err
			}
		}
	}
	if err := os.Rename(output, path); err != nil {
		return "", err
	}
	return path, req.prune(key, arch)
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
