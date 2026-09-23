package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var routingOverrideEnv = []string{
	"ATTN_DATA_DIR",
	"ATTN_HARNESS_DATA_DIR",
	"ATTN_HARNESS_NOTEBOOK_ROOT",
	"ATTN_SOCKET_PATH",
	"ATTN_DB_PATH",
	"ATTN_CONFIG_PATH",
	"ATTN_PLUGIN_DIR",
	"ATTN_WS_PORT",
}

func RoutingOverrideEnv() []string {
	return append([]string(nil), routingOverrideEnv...)
}

func ValidateInstanceRouting() error {
	if err := validateHarnessRouting(); err != nil {
		return err
	}
	instance := Instance()
	if instance == "" {
		return nil
	}
	instanceDir := DataDirForInstance(instance)
	instancePort := WSPortForInstance(instance)

	checks := []struct {
		env       string
		configKey string
		resolved  string
		expected  string
		isPath    bool
	}{
		{"ATTN_DATA_DIR", "", DataDir(), instanceDir, true},
		{"ATTN_SOCKET_PATH", "socket_path", SocketPath(), filepath.Join(instanceDir, "attn.sock"), true},
		{"ATTN_DB_PATH", "db_path", DBPath(), filepath.Join(instanceDir, "attn.db"), true},
		{"ATTN_CONFIG_PATH", "", ConfigPath(), filepath.Join(instanceDir, "config.json"), true},
		{"ATTN_PLUGIN_DIR", "", PluginDir(), filepath.Join(instanceDir, "plugins"), true},
		{"ATTN_WS_PORT", "", WSPort(), instancePort, false},
	}

	var (
		conflicts       []routingConflict
		dataDirConflict bool
	)
	for _, check := range checks {
		agree, err := routingValuesAgree(check.resolved, check.expected, check.isPath)
		if err != nil {
			return fmt.Errorf("resolve %s for instance %s: %w", check.env, instance, err)
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
	return formatRoutingConflict(instance, instanceDir, instancePort, conflicts)
}

func validateHarnessRouting() error {
	rawRoot := strings.TrimSpace(os.Getenv("ATTN_HARNESS_DATA_DIR"))
	if rawRoot == "" {
		return nil
	}
	if Instance() != "" {
		return fmt.Errorf("refusing ATTN_HARNESS_DATA_DIR with named instance %s", Instance())
	}
	if !filepath.IsAbs(rawRoot) {
		return fmt.Errorf("ATTN_HARNESS_DATA_DIR must be absolute")
	}
	info, err := os.Lstat(rawRoot)
	if err != nil {
		return fmt.Errorf("inspect ATTN_HARNESS_DATA_DIR: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("ATTN_HARNESS_DATA_DIR must be a direct owner-only directory")
	}

	root, err := CanonicalRuntimePath(rawRoot)
	if err != nil {
		return fmt.Errorf("resolve ATTN_HARNESS_DATA_DIR: %w", err)
	}
	production, err := CanonicalRuntimePath(DataDirForInstance(""))
	if err != nil {
		return fmt.Errorf("resolve production data directory: %w", err)
	}
	if runtimePathWithin(root, production) || runtimePathWithin(production, root) {
		return fmt.Errorf("refusing harness root %q because it overlaps production %q", root, production)
	}

	checks := []struct {
		label string
		path  string
	}{
		{"ATTN_DATA_DIR", DataDir()},
		{"ATTN_SOCKET_PATH", SocketPath()},
		{"ATTN_DB_PATH", DBPath()},
		{"ATTN_CONFIG_PATH", ConfigPath()},
		{"ATTN_PLUGIN_DIR", PluginDir()},
		{"ATTN_HARNESS_NOTEBOOK_ROOT", HarnessNotebookRoot()},
	}
	for _, check := range checks {
		resolved, resolveErr := CanonicalRuntimePath(check.path)
		if resolveErr != nil {
			return fmt.Errorf("resolve %s for default-instance harness: %w", check.label, resolveErr)
		}
		if !runtimePathWithin(resolved, root) {
			return fmt.Errorf("refusing %s=%q outside default-instance harness root %q", check.label, resolved, root)
		}
	}
	if port := strings.TrimSpace(WSPort()); port == "9849" || port == "29849" {
		return fmt.Errorf("refusing default-instance harness websocket port %s", port)
	}
	return nil
}

func HarnessNotebookRoot() string {
	if strings.TrimSpace(os.Getenv("ATTN_HARNESS_DATA_DIR")) == "" {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(os.Getenv("ATTN_HARNESS_NOTEBOOK_ROOT")))
}

func runtimePathWithin(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}

type routingConflict struct {
	label      string
	value      string
	env        string
	configKey  string
	configFile string
}

func formatRoutingConflict(instance, instanceDir, instancePort string, conflicts []routingConflict) error {
	var b strings.Builder
	fmt.Fprintf(&b, "ATTN_INSTANCE=%s disagrees with the routing this process resolved.\n", instance)
	fmt.Fprintf(&b, "  instance %s is %s (port %s), but:\n", instance, instanceDir, instancePort)

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
	fmt.Fprintf(&b, "  An explicit override outranks ATTN_INSTANCE, so this process would act as instance %s"+
		" against another instance's data. Refusing before anything opens it.\n", instance)

	if len(envNames) > 0 {
		fmt.Fprintf(&b, "  Fix: env%s ATTN_INSTANCE=%s <command>\n", scrubFlags(envNames), instance)
		fmt.Fprintf(&b, "  Or clear them in your shell: eval \"$(attn instance-env %s)\"\n", instance)
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "  No environment change fixes %s: edit it, or start the instance over with `attn instance clean %s`\n",
			files[0], instance)
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
