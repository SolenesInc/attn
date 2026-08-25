package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var routingOverrideEnv = []string{
	"ATTN_DATA_DIR",
	"ATTN_SOCKET_PATH",
	"ATTN_DB_PATH",
	"ATTN_CONFIG_PATH",
	"ATTN_PLUGIN_DIR",
	"ATTN_WS_PORT",
}

func RoutingOverrideEnv() []string {
	return append([]string(nil), routingOverrideEnv...)
}

// Receipt: on 2026-08-17 an inherited path override let `make install PROFILE=<name>` take
// the production PID lock and migrate the production database. Call this before any of that.
func ValidateProfileRouting() error {
	profile := Profile()
	if profile == "" {
		return nil
	}
	profileDir := DataDirForProfile(profile)
	profilePort := WSPortForProfile(profile)

	// ATTN_DATA_DIR comes first: every other default derives from it.
	checks := []struct {
		env       string
		configKey string
		resolved  string
		expected  string
		isPath    bool
	}{
		{"ATTN_DATA_DIR", "", DataDir(), profileDir, true},
		{"ATTN_SOCKET_PATH", "socket_path", SocketPath(), filepath.Join(profileDir, "attn.sock"), true},
		{"ATTN_DB_PATH", "db_path", DBPath(), filepath.Join(profileDir, "attn.db"), true},
		{"ATTN_CONFIG_PATH", "", ConfigPath(), filepath.Join(profileDir, "config.json"), true},
		{"ATTN_PLUGIN_DIR", "", PluginDir(), filepath.Join(profileDir, "plugins"), true},
		{"ATTN_WS_PORT", "", WSPort(), profilePort, false},
	}

	var (
		conflicts       []routingConflict
		dataDirConflict bool
	)
	for _, check := range checks {
		agree, err := routingValuesAgree(check.resolved, check.expected, check.isPath)
		if err != nil {
			return fmt.Errorf("resolve %s for profile %s: %w", check.env, profile, err)
		}
		if agree {
			continue
		}
		if check.env == "ATTN_DATA_DIR" {
			dataDirConflict = true
		}
		if envValue, ok := lookupRoutingOverride(check.env); ok {
			conflicts = append(conflicts, routingConflict{label: check.env, value: envValue, env: check.env})
			continue
		}
		if dataDirConflict || check.configKey == "" {
			continue
		}
		conflicts = append(conflicts, routingConflict{
			label:      check.env,
			value:      check.resolved,
			configKey:  check.configKey,
			configFile: ConfigPath(),
		})
	}
	if len(conflicts) == 0 {
		return nil
	}
	return formatRoutingConflict(profile, profileDir, profilePort, conflicts)
}

type routingConflict struct {
	label      string
	value      string
	env        string
	configKey  string
	configFile string
}

func formatRoutingConflict(profile, profileDir, profilePort string, conflicts []routingConflict) error {
	var b strings.Builder
	fmt.Fprintf(&b, "ATTN_PROFILE=%s disagrees with the routing this process resolved.\n", profile)
	fmt.Fprintf(&b, "  profile %s is %s (port %s), but:\n", profile, profileDir, profilePort)

	var envNames []string
	var files []string
	for _, conflict := range conflicts {
		if conflict.env != "" {
			fmt.Fprintf(&b, "    %-16s = %s\n", conflict.label, conflict.value)
			envNames = append(envNames, conflict.env)
			continue
		}
		fmt.Fprintf(&b, "    %-16s = %s (%s in %s)\n", conflict.label, conflict.value, conflict.configKey, conflict.configFile)
		files = append(files, conflict.configFile)
	}
	fmt.Fprintf(&b, "  An explicit override outranks ATTN_PROFILE, so this process would act as profile %s"+
		" against another profile's data. Refusing before anything opens it.\n", profile)

	if len(envNames) > 0 {
		fmt.Fprintf(&b, "  Fix: env%s ATTN_PROFILE=%s <command>\n", scrubFlags(envNames), profile)
		fmt.Fprintf(&b, "  Or clear them in your shell: eval \"$(attn profile-env %s)\"\n", profile)
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "  No environment change fixes %s: edit it, or start the profile over with `attn profile clean %s`\n",
			files[0], profile)
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

func scrubFlags(names []string) string {
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, " -u %s", name)
	}
	return b.String()
}

func lookupRoutingOverride(name string) (string, bool) {
	value := strings.TrimSpace(os.Getenv(name))
	return value, value != ""
}

func routingValuesAgree(resolved, expected string, isPath bool) (bool, error) {
	if !isPath {
		return strings.TrimSpace(resolved) == strings.TrimSpace(expected), nil
	}
	resolvedPath, err := CanonicalRuntimePath(resolved)
	if err != nil {
		return false, err
	}
	expectedPath, err := CanonicalRuntimePath(expected)
	if err != nil {
		return false, err
	}
	return resolvedPath == expectedPath, nil
}
