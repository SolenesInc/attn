package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/machinename"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) machineNames() ([]machinename.Machine, error) {
	self, err := d.store.SelfMachine()
	if err != nil {
		return nil, err
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return nil, err
	}
	machines := []machinename.Machine{{ID: self.ID, Name: self.Name, Home: true}}
	for _, row := range rows {
		machines = append(machines, machinename.Machine{ID: row.ID, Name: row.Name})
	}
	return machines, nil
}

// readMachine is the machine that text, as a person or an agent wrote it, names.
func (d *Daemon) readMachine(text string) (machine, error) {
	machines, err := d.machineNames()
	if err != nil {
		return machine{}, err
	}
	named, err := machinename.Read(text, machines)
	if err != nil {
		return machine{}, refuseMachineName(err)
	}
	return d.machineFor(named.ID), nil
}

// readAddress splits text written as `<machine>:<path>`; ok is false when text names no machine.
func (d *Daemon) readAddress(text string) (place machine, rest string, ok bool, err error) {
	machines, err := d.machineNames()
	if err != nil {
		return machine{}, "", false, err
	}
	address, ok, err := machinename.ReadAddress(text, machines)
	if err != nil {
		return machine{}, "", true, refuseMachineName(err)
	}
	if !ok {
		return machine{}, "", false, nil
	}
	return d.machineFor(address.Machine.ID), address.Rest, true, nil
}

func refuseMachineName(err error) error {
	var unknown *machinename.Unknown
	if errors.As(err, &unknown) {
		return protocol.Refuse(protocol.RefusalNotFound, "%s", err.Error())
	}
	return protocol.Refuse(protocol.RefusalInvalidInput, "%s", err.Error())
}

// refuseMachineNameTaken refuses a name for a new machine that `home` or another machine already holds.
func (d *Daemon) refuseMachineNameTaken(name string) error {
	if strings.EqualFold(strings.TrimSpace(name), machinename.HomeName) {
		return protocol.Refuse(protocol.RefusalInvalidInput, "machine name %q is reserved for the home", name)
	}
	machines, err := d.machineNames()
	if err != nil {
		return err
	}
	for _, m := range machines {
		if strings.EqualFold(m.Name, strings.TrimSpace(name)) {
			return protocol.Refuse(protocol.RefusalInvalidInput, "machine %s already has the name %q", m.ID, m.Name)
		}
	}
	return nil
}

func (d *Daemon) machineByID(id string) (store.Machine, error) {
	rows, err := d.store.ListOutposts()
	if err != nil {
		return store.Machine{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return store.Machine{}, fmt.Errorf("outpost %q does not exist", id)
}
