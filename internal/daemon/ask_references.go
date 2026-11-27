package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/machinename"
	"github.com/victorarias/attn/internal/protocol"
)

// askReferences resolves the places markdown names against the ask's place, with every ~ path read on its machine.
// writtenIn is where the markdown was written; a relative link must name the same file from there as from the place.
func (d *Daemon) askReferences(markdown string, place ask.Place, writtenIn, askID string) ([]protocol.AskReference, error) {
	machines, err := d.machineNames()
	if err != nil {
		return nil, err
	}
	references, err := ask.References(markdown, machines, place)
	if err != nil {
		return nil, err
	}
	if writtenIn != place.BaseDir {
		there, err := ask.References(markdown, machines, ask.Place{Host: place.Host, BaseDir: writtenIn})
		if err != nil {
			return nil, err
		}
		if !slices.EqualFunc(references, there, sameReference) {
			return nil, protocol.Refuse(protocol.RefusalInvalidInput, "the user's view resolves the ask's links against %s, where %s was opened; name each file the amendment links by its absolute path", place.BaseDir, askID)
		}
	}
	homes := map[string]string{}
	for i, reference := range references {
		if !strings.HasPrefix(reference.Path, "~") {
			continue
		}
		host := protocol.Deref(reference.Host)
		if _, known := homes[host]; !known {
			if homes[host], err = d.machineHome(host); err != nil {
				return nil, protocol.Refuse(protocol.RefusalWrongState, "line %d names %s, and %s did not say where ~ is there: %v; name the file by its absolute path", reference.Line, reference.Text, d.outpostName(host), err)
			}
		}
		references[i].Path = filepath.Join(homes[host], strings.TrimPrefix(reference.Path, "~"))
	}
	return references, nil
}

func sameReference(a, b protocol.AskReference) bool {
	return a.Kind == b.Kind && a.Text == b.Text && protocol.Deref(a.Host) == protocol.Deref(b.Host) && a.Path == b.Path && a.Line == b.Line
}

var errNoHomeFolder = errors.New("it has no home folder")

func (d *Daemon) machineHome(host string) (string, error) {
	if host == d.store.MachineID() {
		return os.UserHomeDir()
	}
	shell, err := Call[protocol.TerminalShellResult](context.Background(), d.router(), protocol.TerminalShellMessage{Cmd: protocol.CmdTerminalShell, Host: host})
	if err != nil {
		return "", err
	}
	if shell.Home == "" {
		return "", errNoHomeFolder
	}
	return shell.Home, nil
}

// machineNamesByHost names each machine by the host the wire carries it as: "" for this daemon's own.
func (d *Daemon) machineNamesByHost() map[string]string {
	names := map[string]string{}
	machines, err := d.machineNames()
	if err != nil {
		d.logf("asks: reading machine names: %v", err)
	}
	for _, m := range machines {
		name := m.Name
		if name == "" {
			name = machinename.HomeName
		}
		names[protocol.Deref(d.store.WireHost(m.ID))] = name
	}
	return names
}

// wireReferences are stored references as the protocol carries them: no host for this daemon's own machine.
func (d *Daemon) wireReferences(stored []protocol.AskReference) []protocol.AskReference {
	out := make([]protocol.AskReference, len(stored))
	for i, reference := range stored {
		reference.Host = d.store.WireHost(protocol.Deref(reference.Host))
		out[i] = reference
	}
	return out
}
