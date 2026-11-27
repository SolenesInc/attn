package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
)

// Machine is one daemon as this database knows it: its own (transport self) or an outpost.
type Machine struct {
	ID          string
	Name        string
	Transport   string
	Destination string
	HostKey     string
	DaemonID    string
	Home        string
	AddedAt     string
}

// MachineID is this database's own machine row: on an enrolled outpost, the id its home knows it by (AdoptMachineID).
func (s *Store) MachineID() string { return *s.machineID.Load() }

// WireHost is a stored machine reference as the protocol carries it: absent for this daemon's own machine.
func (s *Store) WireHost(machine string) *string {
	if machine == "" || machine == s.MachineID() {
		return nil
	}
	return protocol.Ptr(machine)
}

// StoredHost is the machine a protocol host names, with absent meaning this daemon's own machine.
func (s *Store) StoredHost(host *string) string {
	if h := protocol.Deref(host); h != "" {
		return h
	}
	return s.MachineID()
}

func readSelfMachineID(db *sql.DB) (string, error) {
	var id string
	err := db.QueryRow(`SELECT id FROM machines WHERE transport = 'self'`).Scan(&id)
	if err != nil && !machinesTableExists(db) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("this database names no machine of its own: %w", err)
	}
	return id, nil
}

func machinesTableExists(db interface {
	QueryRow(string, ...any) *sql.Row
}) bool {
	var n int
	return db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'machines'`).Scan(&n) == nil && n == 1
}

const TransportReverse = "reverse"

func (m Machine) Reverse() bool { return m.Transport == TransportReverse }

func (s *Store) AddOutpost(name, destination string) (Machine, error) {
	row := Machine{ID: uuid.NewString(), Name: name, Transport: "ssh", Destination: destination, AddedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	_, err := s.db.Exec(`INSERT INTO machines (id,name,transport,destination,added_at) VALUES (?,?,'ssh',?,?)`, row.ID, row.Name, row.Destination, row.AddedAt)
	return row, err
}

// AddReverseMachine adds a machine that dials this home, pinned to the host key and daemon id it presented.
func (s *Store) AddReverseMachine(id, name, hostKey, daemonID string) (Machine, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := Machine{ID: id, Name: name, Transport: TransportReverse, HostKey: hostKey, DaemonID: daemonID, AddedAt: now}
	_, err := s.db.Exec(`INSERT INTO machines (id,name,transport,host_key,daemon_id,added_at,paired_at) VALUES (?,?,'reverse',?,?,?,?)`, id, name, hostKey, daemonID, now, now)
	return row, err
}

// SelfMachine is this database's own machine row.
func (s *Store) SelfMachine() (Machine, error) {
	var row Machine
	err := s.db.QueryRow(`SELECT id,name,transport,added_at FROM machines WHERE transport = 'self'`).Scan(&row.ID, &row.Name, &row.Transport, &row.AddedAt)
	return row, err
}

func (s *Store) ListOutposts() ([]Machine, error) {
	rows, err := s.db.Query(`SELECT id,name,transport,destination,COALESCE(host_key,''),COALESCE(daemon_id,''),COALESCE(home,''),added_at FROM machines WHERE transport != 'self' ORDER BY added_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Machine{}
	for rows.Next() {
		var row Machine
		if err := rows.Scan(&row.ID, &row.Name, &row.Transport, &row.Destination, &row.HostKey, &row.DaemonID, &row.Home, &row.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) PinOutpost(id, hostKey, daemonID string) error {
	result, err := s.db.Exec(`UPDATE machines SET host_key=?,daemon_id=?,paired_at=? WHERE id=? AND transport != 'self'`, hostKey, daemonID, time.Now().UTC().Format(time.RFC3339Nano), id)
	return outpostWriteResult(id, result, err)
}

func (s *Store) SetOutpostHome(id, home string) error {
	result, err := s.db.Exec(`UPDATE machines SET home=? WHERE id=? AND transport != 'self'`, home, id)
	return outpostWriteResult(id, result, err)
}

func (s *Store) ClearOutpostPins(id string) error {
	result, err := s.db.Exec(`UPDATE machines SET host_key=NULL,daemon_id=NULL,paired_at=NULL WHERE id=? AND transport != 'self'`, id)
	return outpostWriteResult(id, result, err)
}

func (s *Store) DeleteOutpost(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM machines WHERE id=? AND transport != 'self'`, id)
	if err := outpostWriteResult(id, result, err); err != nil {
		return err
	}
	for _, table := range []string{"outpost_outbox", "file_activity", "recent_locations", "markdown_annotation_drafts"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE host=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func outpostWriteResult(id string, result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("outpost %q does not exist", id)
	}
	return err
}
