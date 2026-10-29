package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/pty"
)

func (d *Daemon) machineAgentExecutable(driver agentdriver.Driver, configured string) (string, error) {
	name := strings.TrimSpace(configured)
	if name == "" && d.store != nil {
		name = strings.TrimSpace(d.machineSetting(MachineSetting(executableSettingKey(driver.Name()))))
	}
	if name == "" {
		env := pty.MergeEnvironment(os.Environ(), d.loginShellEnvironment())
		name, _ = pty.LookupEnv(env, driver.ExecutableEnvVar())
		name = strings.TrimSpace(name)
	}
	if name == "" {
		name = driver.DefaultExecutable()
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
	env := pty.MergeEnvironment(os.Environ(), d.loginShellEnvironment())
	if path, ok := pty.ResolveCommandPath(name, env); ok {
		return path, nil
	}
	return name, &exec.Error{Name: name, Err: exec.ErrNotFound}
}
