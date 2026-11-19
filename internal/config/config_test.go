package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
			"9849", "29849", "19849",
		}
		for _, other := range taken {
			if MockGitHubPortForInstance(instance) == other {
				t.Errorf("mock GitHub port for %q = %s collides with %s", instance, MockGitHubPortForInstance(instance), other)
			}
		}
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
