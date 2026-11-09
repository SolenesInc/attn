package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/config"
)

type instanceCase struct {
	Name          string            `json:"name"`
	Env           map[string]string `json:"env"`
	Dirs          []string          `json:"dirs,omitempty"`
	OwnerOnlyDirs []string          `json:"owner_only_dirs,omitempty"`
	Files         map[string]string `json:"files,omitempty"`
	Want          instanceWant      `json:"want"`
}

type instanceWant struct {
	Instance         string  `json:"instance"`
	Label            string  `json:"label"`
	DataDir          string  `json:"data_dir"`
	SocketPath       string  `json:"socket_path"`
	ConfigPath       string  `json:"config_path"`
	Port             string  `json:"port"`
	Banner           *string `json:"banner"`
	ValidateInstance *string `json:"validate_instance"`
	ValidateRouting  *string `json:"validate_routing"`
}

const corpusInstance = "corpus-x9"

func instanceCaseTable() []instanceCase {
	isolated := map[string]string{"ATTN_CONFIG_PATH": "$ROOT/empty.json"}
	with := func(base map[string]string, more ...string) map[string]string {
		env := map[string]string{}
		for key, value := range base {
			env[key] = value
		}
		for i := 0; i < len(more); i += 2 {
			env[more[i]] = more[i+1]
		}
		return env
	}
	named := with(nil, "ATTN_INSTANCE", corpusInstance)
	rootEnv := with(nil, "ATTN_DATA_DIR", "$ROOT/data")
	harness := with(nil,
		"ATTN_HARNESS_DATA_DIR", "$ROOT/harness",
		"ATTN_DATA_DIR", "$ROOT/harness",
		"ATTN_HARNESS_NOTEBOOK_ROOT", "$ROOT/harness/notebook",
		"ATTN_SOCKET_PATH", "$ROOT/harness/attn.sock",
		"ATTN_DB_PATH", "$ROOT/harness/attn.db",
		"ATTN_CONFIG_PATH", "$ROOT/harness/config.json",
		"ATTN_PLUGIN_DIR", "$ROOT/harness/plugins",
		"ATTN_WS_PORT", "29150",
	)
	harnessDirs := []string{"$ROOT/harness"}
	return []instanceCase{
		{Name: "no instance reads the default home", Env: isolated},
		{Name: "default spelled out", Env: with(isolated, "ATTN_INSTANCE", "default")},
		{Name: "blank instance", Env: with(isolated, "ATTN_INSTANCE", "   ")},
		{Name: "uppercase instance normalizes", Env: with(isolated, "ATTN_INSTANCE", "  CORPUS-X9 ", "ATTN_DATA_DIR", "$ROOT/data")},
		{Name: "dev port", Env: with(isolated, "ATTN_INSTANCE", "dev")},
		{Name: "invalid instance name", Env: with(isolated, "ATTN_INSTANCE", "Bad_Name!")},
		{Name: "instance name too long", Env: with(isolated, "ATTN_INSTANCE", "aaaaaaaaaaaaaaaaa")},
		{Name: "invalid name with a quote", Env: with(isolated, "ATTN_INSTANCE", `a"b`)},
		{Name: "named instance agrees", Env: named},
		{Name: "named instance, explicit own paths agree", Env: with(named, "ATTN_DATA_DIR", "$HOME/.attn-"+corpusInstance+"/", "ATTN_SOCKET_PATH", "$HOME/.attn-"+corpusInstance+"/attn.sock")},
		{Name: "data dir override with no instance is legal", Env: rootEnv},
		{Name: "data dir trims and cleans", Env: with(nil, "ATTN_DATA_DIR", "  $ROOT/a/../data/  ")},
		{Name: "socket override with no instance", Env: with(rootEnv, "ATTN_SOCKET_PATH", "$ROOT/other.sock")},
		{Name: "named instance with a foreign data dir", Env: with(named, "ATTN_DATA_DIR", "$ROOT/data")},
		{Name: "named instance with a foreign socket", Env: with(named, "ATTN_SOCKET_PATH", "$ROOT/other.sock")},
		{Name: "named instance with a foreign db", Env: with(named, "ATTN_DB_PATH", "$ROOT/other.db")},
		{Name: "named instance with a foreign port", Env: with(named, "ATTN_WS_PORT", "1234")},
		{Name: "every override disagrees", Env: with(named,
			"ATTN_DATA_DIR", "$ROOT/d", "ATTN_SOCKET_PATH", "$ROOT/s", "ATTN_DB_PATH", "$ROOT/b",
			"ATTN_CONFIG_PATH", "$ROOT/c", "ATTN_PLUGIN_DIR", "$ROOT/p", "ATTN_WS_PORT", "99")},
		{Name: "config file sets the socket with no instance", Env: with(nil, "ATTN_CONFIG_PATH", "$ROOT/custom.json", "ATTN_DATA_DIR", "$ROOT/data"),
			Files: map[string]string{"$ROOT/custom.json": `{"socket_path":"$ROOT/from-config.sock","db_path":"$ROOT/from-config.db"}`}},
		{Name: "instance whose config file diverts the socket", Env: with(named, "ATTN_CONFIG_PATH", "$ROOT/custom.json"),
			Files: map[string]string{"$ROOT/custom.json": `{"socket_path":"$ROOT/elsewhere.sock","db_path":"$ROOT/elsewhere.db"}`}},
		{Name: "malformed config file is ignored", Env: with(rootEnv, "ATTN_CONFIG_PATH", "$ROOT/bad.json"),
			Files: map[string]string{"$ROOT/bad.json": `{"socket_path":`}},
		{Name: "config with a wrong field type keeps the other field", Env: with(rootEnv, "ATTN_CONFIG_PATH", "$ROOT/mixed.json"),
			Files: map[string]string{"$ROOT/mixed.json": `{"db_path":7,"socket_path":"$ROOT/kept.sock"}`}},
		{Name: "named instance reads its stored port", Env: with(named, "HOME", "$ROOT/home"),
			Dirs: []string{"$ROOT/home/.attn-" + corpusInstance}, Files: map[string]string{"$ROOT/home/.attn-" + corpusInstance + "/ws-port": "23456\n"}},
		{Name: "named instance with a stored port refuses its derived port", Env: with(named, "HOME", "$ROOT/home", "ATTN_WS_PORT", "29291"),
			Dirs: []string{"$ROOT/home/.attn-" + corpusInstance}, Files: map[string]string{"$ROOT/home/.attn-" + corpusInstance + "/ws-port": "23456\n"}},
		{Name: "unreadable stored port falls back to the derived port", Env: with(named, "HOME", "$ROOT/home"),
			Dirs: []string{"$ROOT/home/.attn-" + corpusInstance}, Files: map[string]string{"$ROOT/home/.attn-" + corpusInstance + "/ws-port": "70000\n"}},
		{Name: "harness relative root", Env: with(harness, "ATTN_HARNESS_DATA_DIR", "harness")},
		{Name: "harness root missing", Env: with(harness, "ATTN_HARNESS_DATA_DIR", "$ROOT/missing")},
		{Name: "harness root world readable", Env: harness, Dirs: harnessDirs},
		{Name: "harness keeps every route inside", Env: harness, OwnerOnlyDirs: harnessDirs},
		{Name: "harness refuses an outside socket", Env: with(harness, "ATTN_SOCKET_PATH", "$ROOT/outside.sock"), OwnerOnlyDirs: harnessDirs},
		{Name: "harness refuses an outside notebook", Env: with(harness, "ATTN_HARNESS_NOTEBOOK_ROOT", "$ROOT/notebook"), OwnerOnlyDirs: harnessDirs},
		{Name: "harness refuses the default port", Env: with(harness, "ATTN_WS_PORT", "9849"), OwnerOnlyDirs: harnessDirs},
		{Name: "harness refuses the dev port", Env: with(harness, "ATTN_WS_PORT", "29849"), OwnerOnlyDirs: harnessDirs},
	}
}

func instanceCases() ([]any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var cases []any
	for _, c := range instanceCaseTable() {
		want, err := runInstanceCase(c, home)
		if err != nil {
			return nil, fmt.Errorf("instance corpus %q: %w", c.Name, err)
		}
		c.Want = want
		cases = append(cases, c)
	}
	return cases, nil
}

func runInstanceCase(c instanceCase, home string) (instanceWant, error) {
	saved := os.Environ()
	defer func() {
		os.Clearenv()
		for _, entry := range saved {
			name, value, _ := strings.Cut(entry, "=")
			os.Setenv(name, value)
		}
	}()

	root, err := os.MkdirTemp("", "cligen-instance-")
	if err != nil {
		return instanceWant{}, err
	}
	defer os.RemoveAll(root)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return instanceWant{}, err
	}

	concrete := strings.NewReplacer("$ROOT", root, "$HOME", home)
	for _, dir := range c.Dirs {
		if err := os.MkdirAll(concrete.Replace(dir), 0o755); err != nil {
			return instanceWant{}, err
		}
	}
	for _, dir := range c.OwnerOnlyDirs {
		if err := os.MkdirAll(concrete.Replace(dir), 0o700); err != nil {
			return instanceWant{}, err
		}
	}
	for path, content := range c.Files {
		if err := os.WriteFile(concrete.Replace(path), []byte(concrete.Replace(content)), 0o644); err != nil {
			return instanceWant{}, err
		}
	}

	os.Clearenv()
	os.Setenv("HOME", home)
	for name, value := range c.Env {
		os.Setenv(name, concrete.Replace(value))
	}
	config.ReloadForTesting()

	abstract := strings.NewReplacer(root, "$ROOT", home, "$HOME")
	optional := func(err error) *string {
		if err == nil {
			return nil
		}
		text := abstract.Replace(err.Error())
		return &text
	}
	want := instanceWant{
		Instance:         config.Instance(),
		Label:            config.InstanceLabel(),
		DataDir:          abstract.Replace(config.DataDir()),
		SocketPath:       abstract.Replace(config.SocketPath()),
		ConfigPath:       abstract.Replace(config.ConfigPath()),
		Port:             config.WSPort(),
		ValidateInstance: optional(config.ValidateInstance()),
		ValidateRouting:  optional(config.ValidateInstanceRouting()),
	}
	if config.Instance() != "" {
		var banner strings.Builder
		config.PrintInstanceBanner(&banner)
		text := abstract.Replace(strings.TrimSuffix(banner.String(), "\n"))
		want.Banner = &text
	}
	return want, nil
}
