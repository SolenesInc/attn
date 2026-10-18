package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Outpost struct {
	ID       string
	Name     string
	Address  string
	HostKey  string
	DaemonID string
	AddedAt  string
}

func (s *Store) AddOutpost(name, address string) (Outpost, error) {
	row := Outpost{ID: uuid.NewString(), Name: name, Address: address, AddedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	_, err := s.db.Exec(`INSERT INTO outposts (id,name,address,added_at) VALUES (?,?,?,?)`, row.ID, row.Name, row.Address, row.AddedAt)
	return row, err
}

func (s *Store) ListOutposts() ([]Outpost, error) {
	rows, err := s.db.Query(`SELECT id,name,address,COALESCE(host_key,''),COALESCE(daemon_id,''),added_at FROM outposts ORDER BY added_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Outpost{}
	for rows.Next() {
		var row Outpost
		if err := rows.Scan(&row.ID, &row.Name, &row.Address, &row.HostKey, &row.DaemonID, &row.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) PinOutpost(id, hostKey, daemonID string) error {
	result, err := s.db.Exec(`UPDATE outposts SET host_key=?,daemon_id=?,paired_at=? WHERE id=?`, hostKey, daemonID, time.Now().UTC().Format(time.RFC3339Nano), id)
	return outpostWriteResult(id, result, err)
}

func (s *Store) ClearOutpostPins(id string) error {
	result, err := s.db.Exec(`UPDATE outposts SET host_key=NULL,daemon_id=NULL,paired_at=NULL WHERE id=?`, id)
	return outpostWriteResult(id, result, err)
}

func (s *Store) DeleteOutpost(id string) error {
	result, err := s.db.Exec(`DELETE FROM outposts WHERE id=?`, id)
	return outpostWriteResult(id, result, err)
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
