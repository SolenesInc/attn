package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDBPath_DefaultsToAttnDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")
	os.Unsetenv("ATTN_INSTANCE")

	path := DBPath()

	expected := filepath.Join(dataDir, "attn.db")
	if path != expected {
		t.Errorf("DBPath() = %q, want %q", path, expected)
	}
}

func TestDBPath_EnvVarOverridesDefault(t *testing.T) {
	os.Setenv("ATTN_DB_PATH", "/custom/path/test.db")
	defer os.Unsetenv("ATTN_DB_PATH")

	path := DBPath()

	if path != "/custom/path/test.db" {
		t.Errorf("DBPath() = %q, want %q", path, "/custom/path/test.db")
	}
}

func TestSocketPath_DefaultsToAttnDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")
	os.Unsetenv("ATTN_INSTANCE")

	path := SocketPath()

	expected := filepath.Join(dataDir, "attn.sock")
	if path != expected {
		t.Errorf("SocketPath() = %q, want %q", path, expected)
	}
}

func TestSocketPath_EnvVarOverridesDefault(t *testing.T) {
	os.Setenv("ATTN_SOCKET_PATH", "/tmp/custom.sock")
	defer os.Unsetenv("ATTN_SOCKET_PATH")

	path := SocketPath()

	if path != "/tmp/custom.sock" {
		t.Errorf("SocketPath() = %q, want %q", path, "/tmp/custom.sock")
	}
}

func TestValidateDaemonIsolation_RejectsForeignSocketRootWithInstanceDB(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(t.TempDir(), "attn.sock"))
	t.Setenv("ATTN_DB_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", "")
	reloadConfig()

	err := ValidateDaemonIsolation(SocketPath())
	if err == nil {
		t.Fatal("ValidateDaemonIsolation() accepted an alternate socket root with the default instance DB")
	}
	if !strings.Contains(err.Error(), "refusing to start daemon") {
		t.Fatalf("ValidateDaemonIsolation() error = %q, want refusal message", err)
	}
}

func TestValidateDaemonIsolation_AllowsForeignSocketRootWithIsolatedDB(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ATTN_INSTANCE", "")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(tmpDir, "attn.sock"))
	t.Setenv("ATTN_DB_PATH", filepath.Join(tmpDir, "attn.db"))
	t.Setenv("ATTN_CONFIG_PATH", "")
	reloadConfig()

	if err := ValidateDaemonIsolation(SocketPath()); err != nil {
		t.Fatalf("ValidateDaemonIsolation() returned unexpected error: %v", err)
	}
}

func TestValidateDaemonIsolation_RejectsRelativeDBPathInInstanceDir(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	t.Setenv("ATTN_INSTANCE", "")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(t.TempDir(), "attn.sock"))
	t.Setenv("ATTN_DB_PATH", "attn.db")
	t.Setenv("ATTN_CONFIG_PATH", "")
	reloadConfig()

	instanceDataDir := DataDir()
	if err := os.MkdirAll(instanceDataDir, 0o755); err != nil {
		t.Fatalf("mkdir instance data dir: %v", err)
	}
	t.Chdir(instanceDataDir)

	err := ValidateDaemonIsolation(SocketPath())
	if err == nil {
		t.Fatal("ValidateDaemonIsolation() accepted a relative DB path that resolves to the default instance DB")
	}
	if !strings.Contains(err.Error(), "refusing to start daemon") {
		t.Fatalf("ValidateDaemonIsolation() error = %q, want refusal message", err)
	}
}

func TestValidateDaemonIsolation_AllowsSocketOverrideInsideInstanceDataDir(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "dev")
	t.Setenv("ATTN_DB_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", "")
	reloadConfig()
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(DataDir(), "custom.sock"))

	if err := ValidateDaemonIsolation(SocketPath()); err != nil {
		t.Fatalf("ValidateDaemonIsolation() returned unexpected error: %v", err)
	}
}

func TestPluginDir_DefaultsToAttnDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	os.Unsetenv("ATTN_PLUGIN_DIR")
	os.Unsetenv("ATTN_INSTANCE")

	want := filepath.Join(dataDir, "plugins")
	if got := PluginDir(); got != want {
		t.Errorf("PluginDir() = %q, want %q", got, want)
	}
}

func TestPluginDir_EnvVarOverridesDefault(t *testing.T) {
	t.Setenv("ATTN_PLUGIN_DIR", "/tmp/attn-test-plugins")
	if got := PluginDir(); got != "/tmp/attn-test-plugins" {
		t.Errorf("PluginDir() = %q, want %q", got, "/tmp/attn-test-plugins")
	}
}

func TestDBPath_ConfigFileOverridesDefault(t *testing.T) {
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_INSTANCE")

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configContent := `{"db_path": "/from/config/file.db"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("ATTN_CONFIG_PATH", configPath)
	defer os.Unsetenv("ATTN_CONFIG_PATH")

	reloadConfig()

	path := DBPath()

	if path != "/from/config/file.db" {
		t.Errorf("DBPath() = %q, want %q", path, "/from/config/file.db")
	}
}

func TestDBPath_EnvVarOverridesConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configContent := `{"db_path": "/from/config/file.db"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("ATTN_CONFIG_PATH", configPath)
	os.Setenv("ATTN_DB_PATH", "/from/env/var.db")
	defer os.Unsetenv("ATTN_CONFIG_PATH")
	defer os.Unsetenv("ATTN_DB_PATH")

	reloadConfig()

	path := DBPath()

	if path != "/from/env/var.db" {
		t.Errorf("DBPath() = %q, want %q (env var should override config file)", path, "/from/env/var.db")
	}
}

func TestSocketPath_ConfigFileOverridesDefault(t *testing.T) {
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_INSTANCE")

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configContent := `{"socket_path": "/from/config/file.sock"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("ATTN_CONFIG_PATH", configPath)
	defer os.Unsetenv("ATTN_CONFIG_PATH")

	reloadConfig()

	path := SocketPath()

	if path != "/from/config/file.sock" {
		t.Errorf("SocketPath() = %q, want %q", path, "/from/config/file.sock")
	}
}

func TestInstance_EmptyWhenUnset(t *testing.T) {
	os.Unsetenv("ATTN_INSTANCE")
	if got := Instance(); got != "" {
		t.Errorf("Instance() = %q, want empty", got)
	}
	if got := InstanceLabel(); got != "default" {
		t.Errorf("InstanceLabel() = %q, want %q", got, "default")
	}
}

func TestInstance_NormalizesValidName(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "  Dev  ")
	if got := Instance(); got != "dev" {
		t.Errorf("Instance() = %q, want %q", got, "dev")
	}
	if got := InstanceLabel(); got != "dev" {
		t.Errorf("InstanceLabel() = %q, want %q", got, "dev")
	}
	if err := ValidateInstance(); err != nil {
		t.Errorf("ValidateInstance() returned unexpected error: %v", err)
	}
}

func TestValidateInstance_RejectsBadNames(t *testing.T) {
	cases := []string{
		"has space",
		"has/slash",
		"with.dot",
		"-leadingdash",
		"UPPER_CASE_UNDERSCORE",
		strings.Repeat("a", 17),
	}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("ATTN_INSTANCE", bad)
			if err := ValidateInstance(); err == nil {
				t.Errorf("ValidateInstance() accepted %q, expected error", bad)
			}
			if got := Instance(); got != "" {
				t.Errorf("Instance() = %q for invalid input %q, want empty", got, bad)
			}
		})
	}
}

func TestDefaultAttnDir_SplitsByInstance(t *testing.T) {
	home, _ := os.UserHomeDir()

	if got, want := defaultAttnDir(""), filepath.Join(home, ".attn"); got != want {
		t.Errorf("defaultAttnDir(\"\") = %q, want %q", got, want)
	}
	if got, want := defaultAttnDir("dev"), filepath.Join(home, ".attn-dev"); got != want {
		t.Errorf("defaultAttnDir(\"dev\") = %q, want %q", got, want)
	}
}

func TestAttnDir_DerivedPathsAllInheritDataDir(t *testing.T) {
	wantDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", wantDir)
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")

	t.Setenv("ATTN_INSTANCE", "dev")
	reloadConfig()

	if got := DataDir(); got != wantDir {
		t.Errorf("DataDir() = %q, want %q", got, wantDir)
	}
	if got := SocketPath(); got != filepath.Join(wantDir, "attn.sock") {
		t.Errorf("SocketPath() = %q", got)
	}
	if got := DBPath(); got != filepath.Join(wantDir, "attn.db") {
		t.Errorf("DBPath() = %q", got)
	}
	if got := LogPath(); got != filepath.Join(wantDir, "daemon.log") {
		t.Errorf("LogPath() = %q", got)
	}
}

func TestWSPort_InstanceDefaults(t *testing.T) {
	os.Unsetenv("ATTN_WS_PORT")

	cases := map[string]string{
		"":      "9849",
		"dev":   "29849",
		"alpha": "",
	}
	for instance, want := range cases {
		t.Run("instance="+instance, func(t *testing.T) {
			if instance == "" {
				os.Unsetenv("ATTN_INSTANCE")
			} else {
				t.Setenv("ATTN_INSTANCE", instance)
			}
			got := WSPort()
			if want != "" && got != want {
				t.Errorf("WSPort() = %q, want %q", got, want)
			}
			if instance == "alpha" {
				if got == "9849" || got == "29849" {
					t.Errorf("hashed port for %q collided: %q", instance, got)
				}
			}
		})
	}
}

func TestWSPort_EnvOverridesInstanceDefault(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "dev")
	t.Setenv("ATTN_WS_PORT", "44444")
	if got := WSPort(); got != "44444" {
		t.Errorf("WSPort() = %q, want %q", got, "44444")
	}
}

func TestLegacyStatePath_SuffixedByInstance(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("ATTN_INSTANCE", "dev")
	SetBinaryName("attn")
	want := filepath.Join(home, ".attn-state-dev.json")
	if got := StatePath(); got != want {
		t.Errorf("StatePath() = %q, want %q", got, want)
	}
}

func TestDeepLinkScheme(t *testing.T) {
	t.Run("default → attn", func(t *testing.T) {
		os.Unsetenv("ATTN_INSTANCE")
		if got := DeepLinkScheme(); got != "attn" {
			t.Errorf("DeepLinkScheme() = %q, want %q", got, "attn")
		}
	})
	t.Run("dev → attn-dev", func(t *testing.T) {
		t.Setenv("ATTN_INSTANCE", "dev")
		if got := DeepLinkScheme(); got != "attn-dev" {
			t.Errorf("DeepLinkScheme() = %q, want %q", got, "attn-dev")
		}
	})
	t.Run("named instance → attn-<name> (its own bundle's scheme)", func(t *testing.T) {
		t.Setenv("ATTN_INSTANCE", "staging")
		if got := DeepLinkScheme(); got != "attn-staging" {
			t.Errorf("DeepLinkScheme() = %q, want %q", got, "attn-staging")
		}
	})
}

func TestValidateInstanceName_PureFunction(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "has space")
	if err := ValidateInstanceName("dev"); err != nil {
		t.Errorf("ValidateInstanceName(dev) unexpectedly errored: %v", err)
	}
	if err := ValidateInstanceName(""); err != nil {
		t.Errorf("ValidateInstanceName(\"\") unexpectedly errored: %v", err)
	}
	if err := ValidateInstanceName("bad name"); err == nil {
		t.Error("ValidateInstanceName(\"bad name\") should have errored")
	}
}

func TestPprofAddr(t *testing.T) {
	orig, had := os.LookupEnv("ATTN_PPROF")
	t.Cleanup(func() {
		if had {
			os.Setenv("ATTN_PPROF", orig)
		} else {
			os.Unsetenv("ATTN_PPROF")
		}
	})

	cases := []struct {
		name        string
		set         bool
		val         string
		wantAddr    string
		wantEnabled bool
	}{
		{name: "unset", set: false},
		{name: "empty", set: true, val: ""},
		{name: "off", set: true, val: "off"},
		{name: "zero", set: true, val: "0"},
		{name: "false", set: true, val: "false"},
		{name: "one_default_port", set: true, val: "1", wantAddr: "127.0.0.1:6060", wantEnabled: true},
		{name: "on", set: true, val: "on", wantAddr: "127.0.0.1:6060", wantEnabled: true},
		{name: "true", set: true, val: "true", wantAddr: "127.0.0.1:6060", wantEnabled: true},
		{name: "bare_port", set: true, val: "6061", wantAddr: "127.0.0.1:6061", wantEnabled: true},
		{name: "colon_port", set: true, val: ":7070", wantAddr: "127.0.0.1:7070", wantEnabled: true},
		{name: "host_port_forced_loopback", set: true, val: "0.0.0.0:8080", wantAddr: "127.0.0.1:8080", wantEnabled: true},
		{name: "garbage", set: true, val: "banana"},
		{name: "out_of_range", set: true, val: "70000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				os.Setenv("ATTN_PPROF", tc.val)
			} else {
				os.Unsetenv("ATTN_PPROF")
			}
			addr, enabled := PprofAddr()
			if enabled != tc.wantEnabled || addr != tc.wantAddr {
				t.Fatalf("PprofAddr() = (%q, %v), want (%q, %v)", addr, enabled, tc.wantAddr, tc.wantEnabled)
			}
		})
	}
}

func TestInstanceDerivation_DefaultAndDev(t *testing.T) {
	cases := []struct {
		instance                  string
		bundleID, appName, scheme string
	}{
		{"", "com.attn.manager", "attn", "attn"},
		{"default", "com.attn.manager", "attn", "attn"},
		{"dev", "com.attn.manager.dev", "attn-dev", "attn-dev"},
		{"agent7", "com.attn.manager.agent7", "attn-agent7", "attn-agent7"},
	}
	for _, tc := range cases {
		t.Run("instance="+tc.instance, func(t *testing.T) {
			if got := BundleIdentifierForInstance(tc.instance); got != tc.bundleID {
				t.Errorf("BundleIdentifierForInstance(%q) = %q, want %q", tc.instance, got, tc.bundleID)
			}
			if got := AppNameForInstance(tc.instance); got != tc.appName {
				t.Errorf("AppNameForInstance(%q) = %q, want %q", tc.instance, got, tc.appName)
			}
			if got := DeepLinkSchemeForInstance(tc.instance); got != tc.scheme {
				t.Errorf("DeepLinkSchemeForInstance(%q) = %q, want %q", tc.instance, got, tc.scheme)
			}
		})
	}
}

func TestE2EPorts_BandsAreDisjoint(t *testing.T) {
	if got := E2EDaemonPortForInstance(""); got != "19849" {
		t.Errorf("E2EDaemonPortForInstance(\"\") = %q, want 19849", got)
	}
	if got := E2EVitePortForInstance(""); got != "1421" {
		t.Errorf("E2EVitePortForInstance(\"\") = %q, want 1421", got)
	}
	for _, instance := range []string{"agent7", "alpha", "ci-2", "z"} {
		dPort, err := strconv.Atoi(E2EDaemonPortForInstance(instance))
		if err != nil {
			t.Fatalf("E2EDaemonPortForInstance(%q) not numeric: %v", instance, err)
		}
		vPort, err := strconv.Atoi(E2EVitePortForInstance(instance))
		if err != nil {
			t.Fatalf("E2EVitePortForInstance(%q) not numeric: %v", instance, err)
		}
		if dPort < 30000 || dPort > 30999 {
			t.Errorf("e2e daemon port for %q = %d, want [30000,30999]", instance, dPort)
		}
		if vPort < 31000 || vPort > 31999 {
			t.Errorf("e2e vite port for %q = %d, want [31000,31999]", instance, vPort)
		}
		realPort, _ := strconv.Atoi(WSPortForInstance(instance))
		for _, reserved := range []int{9849, 29849, 1420, 1421, 19849, realPort} {
			if dPort == reserved || vPort == reserved {
				t.Errorf("e2e port for %q collided with reserved %d (daemon=%d vite=%d)", instance, reserved, dPort, vPort)
			}
		}
	}
}

func TestE2EPorts_NeverCollideWithRealDaemon(t *testing.T) {
	instances := []string{"", "dev", "agent7", "agent8", "ci-1", "alpha"}
	realPorts := map[string]string{}
	for _, p := range instances {
		realPorts[WSPortForInstance(p)] = p
	}
	for _, p := range instances {
		for _, e2ePort := range []string{E2EDaemonPortForInstance(p), E2EVitePortForInstance(p)} {
			if owner, taken := realPorts[e2ePort]; taken {
				t.Errorf("e2e port %q for instance %q collides with the real daemon port of instance %q", e2ePort, p, owner)
			}
		}
	}
}

func TestMockGitHubPort_HasItsOwnBand(t *testing.T) {
	if got := MockGitHubPortForInstance(""); got != "19850" {
		t.Errorf("MockGitHubPortForInstance(\"\") = %q, want 19850", got)
	}
	for _, instance := range []string{"", "dev", "agent7", "alpha", "ci-2", "z"} {
		port, err := strconv.Atoi(MockGitHubPortForInstance(instance))
		if err != nil {
			t.Fatalf("MockGitHubPortForInstance(%q) not numeric: %v", instance, err)
		}
		if instance != "" && (port < 32000 || port > 32999) {
			t.Errorf("mock GitHub port for %q = %d, want [32000,32999]", instance, port)
		}
		taken := []string{
			WSPortForInstance(instance),
			E2EDaemonPortForInstance(instance),
			E2EVitePortForInstance(instance),
			"9849", "29849", "1420", "1421", "19849",
		}
		for _, other := range taken {
			if MockGitHubPortForInstance(instance) == other {
				t.Errorf("mock GitHub port for %q = %s collides with %s", instance, MockGitHubPortForInstance(instance), other)
			}
		}
	}
}

func TestAppLocalDataDirForInstance_FollowsThePlatformLayout(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	macRoot := filepath.Join(home, "Library", "Application Support")
	cases := []struct {
		instance string
		bundleID string
	}{
		{"", "com.attn.manager"},
		{"default", "com.attn.manager"},
		{"dev", "com.attn.manager.dev"},
		{"agent7", "com.attn.manager.agent7"},
	}
	for _, c := range cases {
		want := filepath.Join(dataHome, c.bundleID)
		if runtime.GOOS == "darwin" {
			want = filepath.Join(macRoot, c.bundleID)
		}
		if got := AppLocalDataDirForInstance(c.instance); got != want {
			t.Errorf("AppLocalDataDirForInstance(%q) = %q, want %q", c.instance, got, want)
		}
	}
}

func TestAppLocalDataDirFallsBackToTheXDGDefaultOffDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin resolves under ~/Library/Application Support, XDG plays no part")
	}
	t.Setenv("XDG_DATA_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	want := filepath.Join(home, ".local", "share", "com.attn.manager.agent7")
	if got := AppLocalDataDirForInstance("agent7"); got != want {
		t.Errorf("AppLocalDataDirForInstance with no XDG_DATA_HOME = %q, want %q", got, want)
	}
}

func TestAppLocalDataDir_UsesActiveInstance(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "dev")
	got := AppLocalDataDir()
	want := AppLocalDataDirForInstance("dev")
	if got != want {
		t.Errorf("AppLocalDataDir() = %q, want %q", got, want)
	}
}

func TestAppLockPathSitsOutsideEveryTreeCleanRemoves(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	for _, instance := range []string{"", "dev", "agent7"} {
		lock := AppLockPathForInstance(instance)
		label := instance
		if label == "" {
			label = "default"
		}
		if want := filepath.Join(home, ".attn.locks", "app-"+label+".lock"); lock != want {
			t.Errorf("AppLockPathForInstance(%q) = %q, want %q", instance, lock, want)
		}
		for _, tree := range []string{DataDirForInstance(instance), AppPathForInstance(instance), AppLocalDataDirForInstance(instance)} {
			if strings.HasPrefix(lock, tree+string(filepath.Separator)) {
				t.Errorf("AppLockPathForInstance(%q) = %q, which clean removes with %q", instance, lock, tree)
			}
		}
	}
}
