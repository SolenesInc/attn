package appbuild

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// `bun x tsc` re-resolves the package on every run, measured at 2.1s against 0.77s for
// a direct exec of an installed compiler; per-app installs cost ~30MB each.

const (
	// Must match the frontend's version so one repo has one TypeScript.
	TypeScriptVersion = "5.8.3"

	// Must match the version the frontend resolves; TestReactTypesPinMatchesTheFrontend
	// fails when the two drift.
	ReactTypesVersion = "19.2.7"

	toolchainDirName = "toolchain"

	toolchainStamp = ".attn-typescript-version"
)

func toolchainPins() string {
	return fmt.Sprintf("typescript@%s @types/react@%s", TypeScriptVersion, ReactTypesVersion)
}

// DefaultNPMRegistry is set explicitly rather than inherited: an exported
// NPM_CONFIG_REGISTRY for a corporate mirror measured here as a 401 on every install.
const DefaultNPMRegistry = "https://registry.npmjs.org/"

func npmRegistryEnv(environ []string) []string {
	registry := strings.TrimSpace(os.Getenv("ATTN_NPM_REGISTRY"))
	if registry == "" {
		registry = DefaultNPMRegistry
	}
	out := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if strings.HasPrefix(entry, "NPM_CONFIG_REGISTRY=") || strings.HasPrefix(entry, "npm_config_registry=") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "NPM_CONFIG_REGISTRY="+registry)
}

type Toolchain struct {
	Bun string
	TSC string
}

func ResolveToolchain(toolchainRoot string, log func(string)) (Toolchain, error) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		return Toolchain{}, fmt.Errorf("bun is not on PATH, and an app is built with bun (bundle) and TypeScript (typecheck); install it from https://bun.sh and try again")
	}
	tsc, err := ensureTypeScript(bun, filepath.Join(toolchainRoot, toolchainDirName), log)
	if err != nil {
		return Toolchain{}, err
	}
	return Toolchain{Bun: bun, TSC: tsc}, nil
}

// The check-and-install runs under a lock on the toolchain directory: an `attn app dev`
// loop racing a manual apply would otherwise run two installs into one node_modules.
func ensureTypeScript(bun, dir string, log func(string)) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating the app toolchain directory %s: %w", dir, err)
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return "", err
	}
	defer unlock()

	tsc := filepath.Join(dir, "node_modules", ".bin", "tsc")
	reactTypes := filepath.Join(dir, "node_modules", "@types", "react")
	if installedPins(dir) == toolchainPins() {
		_, tscErr := os.Stat(tsc)
		_, typesErr := os.Stat(reactTypes)
		if tscErr == nil && typesErr == nil {
			return tsc, nil
		}
	}

	if log != nil {
		log(fmt.Sprintf("installing TypeScript %s and React's types %s into %s (once per machine)", TypeScriptVersion, ReactTypesVersion, dir))
	}
	pkg := fmt.Sprintf("{\n  \"name\": \"attn-app-toolchain\",\n  \"private\": true,\n  \"dependencies\": { \"typescript\": \"%s\", \"@types/react\": \"%s\" }\n}\n",
		TypeScriptVersion, ReactTypesVersion)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		return "", fmt.Errorf("writing the app toolchain package.json: %w", err)
	}
	cmd := exec.Command(bun, "install", "--no-save")
	cmd.Dir = dir
	cmd.Env = npmRegistryEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("installing %s into %s failed (%v); apply needs a typechecker and React's declarations, and these are the ones it uses. Output:\n%s",
			toolchainPins(), dir, err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(tsc); err != nil {
		return "", fmt.Errorf("installing TypeScript %s into %s reported success but left no compiler at %s", TypeScriptVersion, dir, tsc)
	}
	if _, err := os.Stat(reactTypes); err != nil {
		return "", fmt.Errorf("installing @types/react %s into %s reported success but left no declarations at %s; the app SDK re-exports React's types and cannot resolve them without it",
			ReactTypesVersion, dir, reactTypes)
	}
	if err := os.WriteFile(filepath.Join(dir, toolchainStamp), []byte(toolchainPins()+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("recording the installed toolchain versions: %w", err)
	}
	return tsc, nil
}

func installedPins(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, toolchainStamp))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func lockDir(dir string) (func(), error) {
	path := filepath.Join(dir, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening the app toolchain lock %s: %w", path, err)
	}
	deadline := time.Now().Add(toolchainLockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("another apply has held the app toolchain lock %s for over %s; if no apply is running, delete that file", path, toolchainLockWait)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// toolchainLockWait is a tripwire, not a budget: one `bun install` of a single package,
// measured under 10s cold and instant after, so only a stuck holder reaches two minutes.
const toolchainLockWait = 2 * time.Minute
