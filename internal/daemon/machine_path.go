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
	id    string
	here  bool
	known bool
}

var unknownMachine machine

func localMachine() machine {
	return machine{here: true, known: true}
}

func (d *Daemon) here() machine {
	return machine{id: d.store.MachineID(), here: true, known: true}
}

func (d *Daemon) machineFor(id string) machine {
	self := d.store.MachineID()
	if id == "" || id == self {
		return machine{id: self, here: true, known: true}
	}
	return machine{id: id, known: true}
}

func (d *Daemon) machineOnWire(host *string) machine {
	return d.machineFor(protocol.Deref(host))
}

func (m machine) Known() bool {
	return m.known
}

func (m machine) IsLocal() bool {
	return m.known && m.here
}

func (m machine) IsRemote() bool {
	return m.known && !m.here
}

func (m machine) ID() string {
	return m.id
}

func (m machine) Outpost() string {
	if !m.IsRemote() {
		return ""
	}
	return m.id
}

func (m machine) SameAs(other machine) bool {
	return m.known && other.known && (m.here && other.here || m.id == other.id)
}

func (m machine) CouldBe(other machine) bool {
	return !m.known || !other.known || m.SameAs(other)
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

func (d *Daemon) originPlace(ctx context.Context) machine {
	if origin, remote := OriginOf(ctx); remote {
		return d.machineFor(origin.Host)
	}
	return d.here()
}

func (d *Daemon) executionPlace(execution garden.Execution) machine {
	if execution.Machine != nil {
		return d.machineFor(*execution.Machine)
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
		return d.here()
	}
	return d.machineFor(host)
}
