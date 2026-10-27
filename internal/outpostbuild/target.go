package outpostbuild

import (
	"fmt"
	"path/filepath"
)

func CheckInstallTarget(instance, executable, home string) error {
	defaultPath := filepath.Join(home, ".local", "bin", "attn")
	if instance != "default" && filepath.Clean(executable) == defaultPath {
		return fmt.Errorf("outpost instance %s refuses default install path %s; install this instance in its own binary path", instance, defaultPath)
	}
	return nil
}
