package fakeagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

type commandHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookGroup struct {
	Matcher string        `json:"matcher"`
	Hooks   []commandHook `json:"hooks"`
}

type hookSet struct {
	groups map[string][]hookGroup
	env    []string
	cwd    string
}

func (s hookSet) run(event, target string, payload any) error {
	input, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var failures []error
	for _, group := range s.groups[event] {
		if !matcherMatches(group.Matcher, target) {
			continue
		}
		for _, hook := range group.Hooks {
			if hook.Type != "command" {
				continue
			}
			if err := s.exec(hook.Command, input); err != nil {
				failures = append(failures, fmt.Errorf("%s hook %q: %w", event, hook.Command, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (s hookSet) exec(command string, input []byte) error {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = s.cwd
	cmd.Env = s.env
	cmd.Stdin = bytes.NewReader(input)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func matcherMatches(matcher, target string) bool {
	matcher = strings.TrimSpace(matcher)
	if matcher == "" || matcher == "*" || target == "" {
		return true
	}
	pattern, err := regexp.Compile("^(?:" + matcher + ")$")
	return err == nil && pattern.MatchString(target)
}

type claudeSettings struct {
	Hooks map[string][]hookGroup `json:"hooks"`
	Env   map[string]string      `json:"env"`
}

func claudeHooks(settingsPath, cwd string) (hookSet, error) {
	if strings.TrimSpace(settingsPath) == "" {
		return hookSet{}, errors.New("claude launched without --settings")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return hookSet{}, fmt.Errorf("read --settings: %w", err)
	}
	var settings claudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return hookSet{}, fmt.Errorf("decode --settings: %w", err)
	}
	env := os.Environ()
	for key, value := range settings.Env {
		env = withEnv(env, key, value)
	}
	return hookSet{groups: settings.Hooks, env: env, cwd: cwd}, nil
}

var codexHookEnvFromPolicyOnly = []string{"ATTN_SESSION_ID", "ATTN_SOCKET_PATH"}

type codexConfig struct {
	Features struct {
		Hooks bool `json:"hooks"`
	} `json:"features"`
	ShellEnvironmentPolicy struct {
		Set map[string]string `json:"set"`
	} `json:"shell_environment_policy"`
	Hooks map[string]json.RawMessage `json:"hooks"`
}

func codexHooks(overrides []string, cwd string) (hookSet, error) {
	merged := map[string]any{}
	for _, override := range overrides {
		key, value, ok := strings.Cut(override, "=")
		if !ok {
			return hookSet{}, fmt.Errorf("-c %q is not key=value", override)
		}
		var doc map[string]any
		if _, err := toml.Decode(key+" = "+value, &doc); err != nil {
			doc = map[string]any{}
			if _, err := toml.Decode(key+" = "+tomlString(value), &doc); err != nil {
				return hookSet{}, fmt.Errorf("-c %q: %w", override, err)
			}
		}
		mergeTables(merged, doc)
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return hookSet{}, err
	}
	var cfg codexConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return hookSet{}, fmt.Errorf("decode -c overrides: %w", err)
	}
	env := os.Environ()
	for _, key := range codexHookEnvFromPolicyOnly {
		env = withoutEnv(env, key)
	}
	for key, value := range cfg.ShellEnvironmentPolicy.Set {
		env = withEnv(env, key, value)
	}
	set := hookSet{groups: map[string][]hookGroup{}, env: env, cwd: cwd}
	if !cfg.Features.Hooks {
		return set, nil
	}
	for event, groups := range cfg.Hooks {
		var decoded []hookGroup
		if json.Unmarshal(groups, &decoded) == nil {
			set.groups[event] = decoded
		}
	}
	return set, nil
}

func tomlString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func mergeTables(dst, src map[string]any) {
	for key, value := range src {
		if table, ok := value.(map[string]any); ok {
			if existing, ok := dst[key].(map[string]any); ok {
				mergeTables(existing, table)
				continue
			}
		}
		dst[key] = value
	}
}

func withEnv(env []string, key, value string) []string {
	return append(withoutEnv(env, key), key+"="+value)
}

func withoutEnv(env []string, key string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(entry string) bool { return strings.HasPrefix(entry, key+"=") })
}
