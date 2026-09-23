package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAppPathAndExecutablesFollowThePlatformLayout(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	darwin := runtime.GOOS == "darwin"
	macRoot := filepath.Join(home, "Applications")
	tests := []struct {
		instance      string
		appPath       string
		appExecutable string
		appDaemon     string
	}{
		{
			instance:      "",
			appPath:       pick(darwin, filepath.Join(macRoot, "attn.app"), filepath.Join(dataHome, "attn")),
			appExecutable: pick(darwin, filepath.Join(macRoot, "attn.app", "Contents", "MacOS", "app"), filepath.Join(dataHome, "attn", "bin", "attn-app")),
			appDaemon:     pick(darwin, filepath.Join(macRoot, "attn.app", "Contents", "MacOS", "attn"), filepath.Join(dataHome, "attn", "bin", "attn")),
		},
		{
			instance:      "lx",
			appPath:       pick(darwin, filepath.Join(macRoot, "attn-lx.app"), filepath.Join(dataHome, "attn-lx")),
			appExecutable: pick(darwin, filepath.Join(macRoot, "attn-lx.app", "Contents", "MacOS", "app"), filepath.Join(dataHome, "attn-lx", "bin", "attn-app")),
			appDaemon:     pick(darwin, filepath.Join(macRoot, "attn-lx.app", "Contents", "MacOS", "attn"), filepath.Join(dataHome, "attn-lx", "bin", "attn")),
		},
	}
	for _, tc := range tests {
		if got := AppPathForInstance(tc.instance); got != tc.appPath {
			t.Errorf("AppPathForInstance(%q) = %s, want %s", tc.instance, got, tc.appPath)
		}
		if got := AppExecutableForInstance(tc.instance); got != tc.appExecutable {
			t.Errorf("AppExecutableForInstance(%q) = %s, want %s", tc.instance, got, tc.appExecutable)
		}
		if got := AppDaemonBinaryForInstance(tc.instance); got != tc.appDaemon {
			t.Errorf("AppDaemonBinaryForInstance(%q) = %s, want %s", tc.instance, got, tc.appDaemon)
		}
	}
}

func TestAppPathHonorsXDGDataHomeOffDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin installs into ~/Applications, XDG plays no part")
	}
	t.Setenv("XDG_DATA_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	want := filepath.Join(home, ".local", "share", "attn-lx")
	if got := AppPathForInstance("lx"); got != want {
		t.Errorf("AppPathForInstance with no XDG_DATA_HOME = %s, want %s", got, want)
	}
}

func TestInstallResourcesDirFindsBothLayoutsAndNeitherElsewhere(t *testing.T) {
	root := t.TempDir()

	bundle := filepath.Join(root, "attn-lx.app")
	mkdirAll(t, filepath.Join(bundle, "Contents", "MacOS"))
	mkdirAll(t, filepath.Join(bundle, "Contents", "Resources"))

	tree := filepath.Join(root, "attn-lx")
	mkdirAll(t, filepath.Join(tree, "bin"))
	mkdirAll(t, filepath.Join(tree, "resources"))

	loose := filepath.Join(root, "local", "bin")
	mkdirAll(t, loose)

	tests := []struct {
		name       string
		executable string
		want       string
	}{
		{"bundle daemon", filepath.Join(bundle, "Contents", "MacOS", "attn"), filepath.Join(bundle, "Contents", "Resources")},
		{"bundle shell", filepath.Join(bundle, "Contents", "MacOS", "app"), filepath.Join(bundle, "Contents", "Resources")},
		{"tree daemon", filepath.Join(tree, "bin", "attn"), filepath.Join(tree, "resources")},
		{"tree shell", filepath.Join(tree, "bin", "attn-app"), filepath.Join(tree, "resources")},
		{"bin without a tree", filepath.Join(loose, "attn"), ""},
		{"loose binary", filepath.Join(root, "attn"), ""},
	}
	for _, tc := range tests {
		if got := InstallResourcesDir(tc.executable); got != tc.want {
			t.Errorf("%s: InstallResourcesDir(%s) = %q, want %q", tc.name, tc.executable, got, tc.want)
		}
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func pick(cond bool, whenTrue, whenFalse string) string {
	if cond {
		return whenTrue
	}
	return whenFalse
}
