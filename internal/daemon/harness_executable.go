package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
)

func (d *Daemon) machineAgentExecutable(id harness.ID, configured string) (string, error) {
	return d.machineExecutableOf(d.factsOrNone(id), configured)
}

func (d *Daemon) machineExecutableOf(facts harness.Facts, configured string) (string, error) {
	name := strings.TrimSpace(configured)
	if name == "" && d.store != nil {
		name = strings.TrimSpace(d.setting(executableSetting(facts.ID)))
	}
	if name == "" && facts.Executable.Env != "" {
		env := pty.MergeEnvironment(os.Environ(), d.latestLoginEnv())
		name, _ = pty.LookupEnv(env, facts.Executable.Env)
		name = strings.TrimSpace(name)
	}
	if name == "" {
		name = facts.Executable.Default
	}
	return d.machineExecutable(name)
}

func (d *Daemon) machineExecutable(name string) (string, error) {
	name = strings.TrimSpace(name)
	if strings.ContainsRune(name, filepath.Separator) {
		path, err := exec.LookPath(name)
		if err != nil {
			return name, err
		}
		return path, nil
	}
	env := pty.MergeEnvironment(os.Environ(), d.latestLoginEnv())
	if path, ok := pty.ResolveCommandPath(name, env); ok {
		return path, nil
	}
	return name, &exec.Error{Name: name, Err: exec.ErrNotFound}
}
