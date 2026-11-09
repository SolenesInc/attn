package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultWSPort        = "9849"
	devWSPort            = "29849"
	derivedPortBase      = 20000
	derivedPortSpan      = 9849
	instancePortFileName = "ws-port"
)

func WSPortForInstance(instance string) string {
	return instancePorts(DataDirForInstance("")).port(instance)
}

func ClaimWSPortForInstance(instance string) (string, error) {
	return instancePorts(DataDirForInstance("")).claim(instance)
}

func KeepWSPortForInstance(instance, port string) (bool, error) {
	name, fixed := fixedOrNamedInstance(instance)
	if fixed != "" {
		return false, nil
	}
	return true, instancePorts(DataDirForInstance("")).store(name, port)
}

func InstancesHoldingWSPort(port, except string) []string {
	return instancePorts(DataDirForInstance("")).holders(port, except)
}

type instancePorts string

func (base instancePorts) port(instance string) string {
	name, fixed := fixedOrNamedInstance(instance)
	if fixed != "" {
		return fixed
	}
	if stored, ok := base.stored(name); ok {
		return stored
	}
	return derivedInstancePort(name)
}

func (base instancePorts) claim(instance string) (string, error) {
	name, fixed := fixedOrNamedInstance(instance)
	if fixed != "" {
		return fixed, nil
	}
	if stored, ok := base.stored(name); ok {
		return stored, nil
	}
	held := map[int]bool{}
	for _, other := range base.others(name) {
		if port, err := strconv.Atoi(base.port(other)); err == nil {
			held[port] = true
		}
	}
	derived, _ := strconv.Atoi(derivedInstancePort(name))
	for offset := range derivedPortSpan {
		port := derivedPortBase + (derived-derivedPortBase+offset)%derivedPortSpan
		if held[port] {
			continue
		}
		claimed := strconv.Itoa(port)
		if err := base.store(name, claimed); err != nil {
			return "", err
		}
		return claimed, nil
	}
	return "", fmt.Errorf("no free WebSocket port for instance %q: other instances hold all %d ports in %d-%d", name, derivedPortSpan, derivedPortBase, derivedPortBase+derivedPortSpan-1)
}

func (base instancePorts) holders(port, except string) []string {
	var holders []string
	for _, name := range append([]string{""}, base.others("")...) {
		if name == except || base.port(name) != port {
			continue
		}
		if name == "" {
			name = "default"
		}
		holders = append(holders, name)
	}
	return holders
}

func fixedOrNamedInstance(instance string) (name, fixedPort string) {
	p := strings.ToLower(strings.TrimSpace(instance))
	switch {
	case p == "" || p == "default" || !instanceNamePattern().MatchString(p):
		return "", defaultWSPort
	case p == "dev":
		return p, devWSPort
	default:
		return p, ""
	}
}

func derivedInstancePort(instance string) string {
	return strconv.Itoa(derivedPortBase + int(instanceFNV(instance)%derivedPortSpan))
}

func (base instancePorts) dir(name string) string {
	return string(base) + "-" + name
}

func (base instancePorts) stored(name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(base.dir(name), instancePortFileName))
	if err != nil {
		return "", false
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port < 1 || port > 65535 {
		return "", false
	}
	return strconv.Itoa(port), true
}

func (base instancePorts) store(name, port string) error {
	dir := base.dir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("store WebSocket port for instance %q: %w", name, err)
	}
	tmp, err := os.CreateTemp(dir, instancePortFileName+".*")
	if err != nil {
		return fmt.Errorf("store WebSocket port for instance %q: %w", name, err)
	}
	_, writeErr := tmp.WriteString(port + "\n")
	err = errors.Join(writeErr, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, instancePortFileName))
	}
	if err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("store WebSocket port for instance %q: %w", name, err)
	}
	return nil
}

func (base instancePorts) others(self string) []string {
	entries, err := os.ReadDir(filepath.Dir(string(base)))
	if err != nil {
		return nil
	}
	prefix := filepath.Base(string(base)) + "-"
	var names []string
	for _, entry := range entries {
		name, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok || !entry.IsDir() || name == self || !instanceNamePattern().MatchString(name) {
			continue
		}
		names = append(names, name)
	}
	return names
}
