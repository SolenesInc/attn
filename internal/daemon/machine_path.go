package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type machine struct {
	host  string
	known bool
}

var unknownMachine machine

func localMachine() machine {
	return machine{known: true}
}

func machineFor(host string) machine {
	return machine{host: host, known: true}
}

func (m machine) Known() bool {
	return m.known
}

func (m machine) IsLocal() bool {
	return m.known && m.host == ""
}

func (m machine) IsRemote() bool {
	return m.known && m.host != ""
}

func (m machine) Outpost() string {
	if !m.known {
		return ""
	}
	return m.host
}

func (m machine) SameAs(other machine) bool {
	return m.known && other.known && m.host == other.host
}

func (m machine) CouldBe(other machine) bool {
	return !m.known || !other.known || m.host == other.host
}

func (m machine) local(raw string) (string, bool) {
	if !m.IsLocal() {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	return raw, true
}

func (m machine) normalize(raw string) normalizedPath {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return normalizedPath{on: m}
	}
	return m.canonicalize(raw)
}

func (m machine) canonicalize(raw string) normalizedPath {
	if m.IsLocal() {
		return normalizedPath{on: m, path: attngit.CanonicalizePath(raw)}
	}
	return normalizedPath{on: m, path: filepath.Clean(raw)}
}

func (m machine) resolveSymlinks(raw string) (string, error) {
	if !m.IsLocal() {
		return "", fmt.Errorf("cannot resolve symlinks without a known local machine")
	}
	return filepath.EvalSymlinks(raw)
}

type normalizedPath struct {
	on   machine
	path string
}

func (p normalizedPath) Empty() bool {
	return p.path == ""
}

func (p normalizedPath) Equal(other normalizedPath) bool {
	if p.path == "" || other.path == "" || !p.on.CouldBe(other.on) {
		return false
	}
	return p.path == other.path
}

func (p normalizedPath) AtOrBelow(root normalizedPath) bool {
	if p.path == "" || root.path == "" || !p.on.CouldBe(root.on) {
		return false
	}
	rel, err := filepath.Rel(root.path, p.path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func originPlace(ctx context.Context) machine {
	if origin, remote := OriginOf(ctx); remote {
		return machineFor(origin.Host)
	}
	return localMachine()
}

func executionPlace(execution garden.Execution) machine {
	if execution.Machine != nil {
		return machineFor(*execution.Machine)
	}
	return unknownMachine
}

func (d *Daemon) sessionPlace(id protocol.SessionID) machine {
	if id == "" {
		return unknownMachine
	}
	host, copied, known := d.store.SessionHost(id)
	if !known {
		return unknownMachine
	}
	if copied {
		return localMachine()
	}
	return machineFor(host)
}
