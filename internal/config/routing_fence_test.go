package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scopeRouting(t *testing.T, instance string, overrides map[string]string) {
	t.Helper()
	t.Setenv("ATTN_INSTANCE", instance)
	for _, name := range routingOverrideEnv {
		t.Setenv(name, overrides[name])
		if overrides[name] == "" {
			os.Unsetenv(name)
		}
	}
	if overrides["ATTN_DATA_DIR"] == "" {
		t.Setenv("ATTN_DATA_DIR", t.TempDir())
	}
	ReloadForTesting()
}

func TestValidateInstanceRouting_NoInstanceIsAlwaysLegal(t *testing.T) {
	scopeRouting(t, "", nil)
	if err := ValidateInstanceRouting(); err != nil {
		t.Fatalf("ATTN_DATA_DIR without ATTN_INSTANCE must stay legal, got: %v", err)
	}
}

func TestValidateInstanceRouting_DefaultInstanceHarnessKeepsEveryRouteInsideItsRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	scopeRouting(t, "", map[string]string{
		"ATTN_DATA_DIR":              root,
		"ATTN_HARNESS_DATA_DIR":      root,
		"ATTN_HARNESS_NOTEBOOK_ROOT": filepath.Join(root, "notebook"),
		"ATTN_SOCKET_PATH":           filepath.Join(root, "attn.sock"),
		"ATTN_DB_PATH":               filepath.Join(root, "attn.db"),
		"ATTN_CONFIG_PATH":           filepath.Join(root, "config.json"),
		"ATTN_PLUGIN_DIR":            filepath.Join(root, "plugins"),
		"ATTN_WS_PORT":               "29150",
	})
	if err := ValidateInstanceRouting(); err != nil {
		t.Fatalf("isolated default-instance harness routing was refused: %v", err)
	}
}

func TestValidateInstanceRouting_DefaultInstanceHarnessRefusesAnInheritedProductionPath(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	productionDB := filepath.Join(DataDirForInstance(""), "attn.db")
	scopeRouting(t, "", map[string]string{
		"ATTN_DATA_DIR":              root,
		"ATTN_HARNESS_DATA_DIR":      root,
		"ATTN_HARNESS_NOTEBOOK_ROOT": filepath.Join(root, "notebook"),
		"ATTN_DB_PATH":               productionDB,
		"ATTN_WS_PORT":               "29150",
	})

	err := ValidateInstanceRouting()
	if err == nil {
		t.Fatal("default-instance harness accepted a production database override")
	}
	for _, want := range []string{"ATTN_DB_PATH", productionDB, root} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q; got:\n%s", want, err)
		}
	}
}

func TestValidateInstanceRouting_LeakedDataDirIsRefused(t *testing.T) {
	prod := DataDirForInstance("")
	scopeRouting(t, "fb2lists", map[string]string{
		"ATTN_DATA_DIR":    prod,
		"ATTN_SOCKET_PATH": filepath.Join(prod, "attn.sock"),
		"ATTN_DB_PATH":     filepath.Join(prod, "attn.db"),
		"ATTN_CONFIG_PATH": filepath.Join(prod, "config.json"),
		"ATTN_PLUGIN_DIR":  filepath.Join(prod, "plugins"),
		"ATTN_WS_PORT":     "9849",
	})

	err := ValidateInstanceRouting()
	if err == nil {
		t.Fatal("an instance pointed at another instance's data dir must be refused")
	}
	message := err.Error()
	for _, want := range []string{
		"ATTN_INSTANCE=fb2lists",
		DataDirForInstance("fb2lists"),
		WSPortForInstance("fb2lists"),
		prod,
		"attn instance-env fb2lists",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("error must name %q; got:\n%s", want, message)
		}
	}
	for _, name := range routingOverrideEnv {
		if strings.HasPrefix(name, "ATTN_HARNESS_") {
			continue
		}
		if !strings.Contains(message, name) {
			t.Errorf("error must name the disagreeing variable %s; got:\n%s", name, message)
		}
		if !strings.Contains(message, "-u "+name) {
			t.Errorf("printed fix must clear %s; got:\n%s", name, message)
		}
	}
}

func TestValidateInstanceRouting_InstanceOwnPathsAgree(t *testing.T) {
	dir := DataDirForInstance("agent7")
	scopeRouting(t, "agent7", map[string]string{
		"ATTN_DATA_DIR":    dir,
		"ATTN_SOCKET_PATH": filepath.Join(dir, "attn.sock"),
		"ATTN_DB_PATH":     filepath.Join(dir, "attn.db"),
		"ATTN_CONFIG_PATH": filepath.Join(dir, "config.json"),
		"ATTN_PLUGIN_DIR":  filepath.Join(dir, "plugins"),
		"ATTN_WS_PORT":     WSPortForInstance("agent7"),
	})

	if err := ValidateInstanceRouting(); err != nil {
		t.Fatalf("overrides that match the instance must pass, got: %v", err)
	}
}

func TestValidateInstanceRouting_SingleOverrideNamesOnlyItself(t *testing.T) {
	dir := DataDirForInstance("agent7")
	scopeRouting(t, "agent7", map[string]string{
		"ATTN_DATA_DIR": dir,
		"ATTN_DB_PATH":  filepath.Join(DataDirForInstance(""), "attn.db"),
	})

	err := ValidateInstanceRouting()
	if err == nil {
		t.Fatal("a database from another instance must be refused")
	}
	message := err.Error()
	if !strings.Contains(message, "ATTN_DB_PATH") {
		t.Fatalf("error must name ATTN_DB_PATH; got:\n%s", message)
	}
	if strings.Contains(message, "ATTN_SOCKET_PATH") {
		t.Fatalf("error must not name variables that agree; got:\n%s", message)
	}
}

func TestValidateInstanceRouting_WSPortDisagreement(t *testing.T) {
	dir := DataDirForInstance("agent7")
	scopeRouting(t, "agent7", map[string]string{
		"ATTN_DATA_DIR": dir,
		"ATTN_WS_PORT":  "9849",
	})

	err := ValidateInstanceRouting()
	if err == nil {
		t.Fatal("a port belonging to another instance must be refused")
	}
	if !strings.Contains(err.Error(), "ATTN_WS_PORT") {
		t.Fatalf("error must name ATTN_WS_PORT; got:\n%v", err)
	}
}

func TestFormatRoutingConflict_ConfigSourcedValueNamesTheFile(t *testing.T) {
	configFile := "/Users/x/.attn-agent7/config.json"
	err := formatRoutingConflict("agent7", "/Users/x/.attn-agent7", "22944", []routingConflict{
		{label: "ATTN_DB_PATH", value: "/elsewhere/attn.db", configKey: "db_path", configFile: configFile},
	})

	message := err.Error()
	for _, want := range []string{"db_path", configFile, "attn instance clean agent7"} {
		if !strings.Contains(message, want) {
			t.Errorf("error must name %q; got:\n%s", want, message)
		}
	}
	if strings.Contains(message, "env -u") {
		t.Errorf("scrubbing the environment cannot fix a config file, so do not offer it; got:\n%s", message)
	}
}

func TestValidateInstanceRouting_SymlinkedDataDirAgrees(t *testing.T) {
	real := filepath.Join(t.TempDir(), "world")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	scopeRouting(t, "agent7", map[string]string{"ATTN_DATA_DIR": link})
	agree, err := routingValuesAgree(link, real, true)
	if err != nil {
		t.Fatal(err)
	}
	if !agree {
		t.Fatalf("a symlinked path to the same directory must compare equal")
	}
}
