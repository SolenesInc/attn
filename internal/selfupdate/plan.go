package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/config"
)

const (
	BuildTimeoutEnv      = "ATTN_UPDATE_BUILD_TIMEOUT"
	defaultBuildTimeout  = time.Hour
	killedBuildExitLimit = 10 * time.Second
)

func BuildTimeoutFromEnv() time.Duration {
	if limit, err := time.ParseDuration(os.Getenv(BuildTimeoutEnv)); err == nil && limit > 0 {
		return limit
	}
	return defaultBuildTimeout
}

// Plan is how one install gets and applies a build. Dir is where make runs.
type Plan struct {
	Kind       Kind
	Repository string
	Dir        string
	Instance   string
	Root       string
}

// InstallRoot is the app tree this instance installs to, the one
// install-staged replaces. It is the only directory an update may copy or
// remove, so an executable anywhere else is refused.
func InstallRoot(executable, instance string) (string, error) {
	root := config.AppPathForInstance(instance)
	if !isInside(resolved(executable), resolved(root)) {
		return "", fmt.Errorf("attn cannot update itself here: %s is not inside this install's app, %s. Run %s", executable, root, installCommand(instance))
	}
	return root, nil
}

func resolved(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}

func isInside(path, dir string) bool {
	relative, err := filepath.Rel(dir, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func installCommand(instance string) string {
	if instance == "" {
		return "make install"
	}
	return "make install INSTANCE=" + instance
}

func NewPlan(install Install, dataDir, originWorktree string) (Plan, error) {
	switch install.Kind {
	case Unknown:
		return Plan{}, install.RefusalNamingChannel()
	case Outpost:
		return Plan{}, errors.New("an outpost updates from its home; update attn from the home's app")
	case ReleaseApp, ReleaseBinary:
		return newReleasePlan(install, dataDir)
	}
	root, err := InstallRoot(install.Facts.Executable, install.Facts.Instance)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Kind: install.Kind, Instance: install.Facts.Instance, Root: root}
	if install.Kind == NamedInstance {
		if originWorktree == "" {
			return Plan{}, fmt.Errorf("instance %s has no recorded origin worktree. Run make install INSTANCE=%s from its worktree", plan.Instance, plan.Instance)
		}
		if _, err := os.Stat(originWorktree); err != nil {
			return Plan{}, fmt.Errorf("instance %s's origin worktree %s is gone: %w", plan.Instance, originWorktree, err)
		}
		plan.Dir, plan.Repository = originWorktree, originWorktree
		return plan, nil
	}
	plan.Repository = install.Facts.Repository
	if plan.Repository == "" {
		return Plan{}, errors.New("this build records no repository path. Run make install from your checkout")
	}
	if _, err := os.Stat(plan.Repository); err != nil {
		return Plan{}, fmt.Errorf("the repository this build came from, %s, is gone: %w", plan.Repository, err)
	}
	if output, err := exec.Command("git", "-C", plan.Repository, "remote", "get-url", "origin").CombinedOutput(); err != nil {
		return Plan{}, fmt.Errorf("the repository %s has no origin remote: %s", plan.Repository, lastLine(output, err))
	}
	plan.Dir = filepath.Join(dataDir, "update-source")
	return plan, nil
}

// Checkout puts Dir at the build to make and returns its commit. A source
// install always builds origin's main; a named instance builds its worktree
// as it stands and returns no commit.
func (p Plan) Checkout(ctx context.Context, env []string, log io.Writer) (string, error) {
	if p.Kind != Source {
		return "", nil
	}
	if err := runIn(ctx, p.Repository, env, log, "git", "fetch", "--no-tags", "origin", "main"); err != nil {
		return "", fmt.Errorf("could not fetch main from origin in %s: %w", p.Repository, err)
	}
	head, err := exec.CommandContext(ctx, "git", "-C", p.Repository, "rev-parse", "FETCH_HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("read the fetched commit in %s: %w", p.Repository, err)
	}
	commit := strings.TrimSpace(string(head))
	if _, err := os.Stat(filepath.Join(p.Dir, ".git")); err == nil {
		if err := runIn(ctx, p.Dir, env, log, "git", "checkout", "--force", "--detach", commit); err != nil {
			return "", fmt.Errorf("move %s to %s: %w", p.Dir, commit, err)
		}
		if err := runIn(ctx, p.Dir, env, log, "git", "clean", "-fd"); err != nil {
			return "", fmt.Errorf("clean %s: %w", p.Dir, err)
		}
		return commit, nil
	}
	if err := runIn(ctx, p.Repository, env, log, "git", "worktree", "prune"); err != nil {
		return "", fmt.Errorf("prune worktrees in %s: %w", p.Repository, err)
	}
	if err := runIn(ctx, p.Repository, env, log, "git", "worktree", "add", "--force", "--detach", p.Dir, commit); err != nil {
		return "", fmt.Errorf("create the build worktree %s: %w", p.Dir, err)
	}
	return commit, nil
}

func (p Plan) Build(ctx context.Context, env []string, log io.Writer) error {
	return runIn(ctx, p.Dir, append(env, "REPOSITORY_PATH="+p.Repository), log, "make", p.makeArgs("build-app")...)
}

func (p Plan) makeArgs(target string) []string {
	if p.Instance != "" {
		return []string{target, "INSTANCE=" + p.Instance}
	}
	return []string{target}
}

func (p Plan) StagedDaemon() string {
	if p.Kind == ReleaseApp {
		return filepath.Join(p.Dir, BundleName, "Contents", "MacOS", "attnd")
	}
	return filepath.Join(p.Dir, "attnd")
}

type StagedBuild struct {
	Version           string `json:"version"`
	ProtocolVersion   string `json:"protocolVersion"`
	SourceFingerprint string `json:"sourceFingerprint"`
}

func ReadStagedBuild(ctx context.Context, daemon string) (StagedBuild, error) {
	output, err := exec.CommandContext(ctx, daemon, "--build-info-json").Output()
	if err != nil {
		return StagedBuild{}, fmt.Errorf("read the build's identity from %s: %w", daemon, err)
	}
	var build StagedBuild
	if err := json.Unmarshal(output, &build); err != nil {
		return StagedBuild{}, fmt.Errorf("read the build's identity from %s: %w", daemon, err)
	}
	return build, nil
}

// CheckDatabase asks the staged build whether it can open this database.
func CheckDatabase(ctx context.Context, daemon, dbPath string, build fmt.Stringer) error {
	output, err := exec.CommandContext(ctx, daemon, "--check-schema", dbPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("build %s refuses this database: %s", build, lastLine(output, err))
	}
	return nil
}

func runIn(ctx context.Context, dir string, env []string, log io.Writer, name string, args ...string) error {
	fmt.Fprintf(log, ">>> %s %s (in %s)\n", name, strings.Join(args, " "), dir)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var tail tailBuffer
	cmd.Stdout = io.MultiWriter(log, &tail)
	cmd.Stderr = io.MultiWriter(log, &tail)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = killedBuildExitLimit
	if err := cmd.Run(); err != nil {
		return errors.New(lastLine(tail.data, err))
	}
	return nil
}

var makeChatter = regexp.MustCompile(`^make(\[\d+\])?: (Entering|Leaving) directory`)

func lastLine(output []byte, err error) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return err.Error()
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" && !makeChatter.MatchString(line) {
			return line
		}
	}
	return err.Error()
}

type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	if len(b.data) > 4096 {
		b.data = b.data[len(b.data)-4096:]
	}
	return len(p), nil
}

// Files the helper and the next daemon start share in the data folder.

func RestartMarkerPath(dataDir string) string { return filepath.Join(dataDir, "update-restarting") }

func failurePath(dataDir string) string { return filepath.Join(dataDir, "update-failure.json") }

type Failure struct {
	Run   string `json:"run"`
	Cause string `json:"cause"`
}

func WriteFailure(dataDir string, failure Failure) error {
	data, err := json.Marshal(failure)
	if err != nil {
		return err
	}
	return os.WriteFile(failurePath(dataDir), data, 0o600)
}

// TakeFailure returns what the helper reported about this run, once.
func TakeFailure(dataDir, run string) string {
	path := failurePath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_ = os.Remove(path)
	var failure Failure
	if json.Unmarshal(data, &failure) != nil || failure.Run != run {
		return ""
	}
	return failure.Cause
}
