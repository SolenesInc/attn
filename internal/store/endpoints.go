package store

import (
	"database/sql"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/config"
)

func endpointStamp() string { return time.Now().UTC().Format(sortableTimeFormat) }

type EndpointRecord struct {
	ID        string
	Name      string
	SSHTarget string
	Enabled   bool
	Instance  string
	CreatedAt string
	UpdatedAt string
}

type EndpointUpdate struct {
	Name      *string
	SSHTarget *string
	Enabled   *bool
	Instance  *string
}

func (s *Store) AddEndpoint(name, sshTarget, instance string) (*EndpointRecord, error) {
	canonicalInstance, err := config.NormalizeInstanceName(instance)
	if err != nil {
		return nil, err
	}
	instance = canonicalInstance

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, sql.ErrConnDone
	}

	now := endpointStamp()
	record := &EndpointRecord{
		ID:        uuid.NewString(),
		Name:      strings.TrimSpace(name),
		SSHTarget: strings.TrimSpace(sshTarget),
		Enabled:   true,
		Instance:  instance,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := s.db.Exec(`
		INSERT INTO endpoints (id, name, ssh_target, enabled, instance, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.Name,
		record.SSHTarget,
		boolToInt(record.Enabled),
		record.Instance,
		record.CreatedAt,
		record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) GetEndpoint(id string) *EndpointRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var record EndpointRecord
	var enabled int
	err := s.db.QueryRow(`
		SELECT id, name, ssh_target, enabled, instance, created_at, updated_at
		FROM endpoints
		WHERE id = ?`, id).Scan(
		&record.ID,
		&record.Name,
		&record.SSHTarget,
		&enabled,
		&record.Instance,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return nil
	}
	record.Enabled = enabled == 1
	return &record
}

func (s *Store) ListEndpoints() []EndpointRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT id, name, ssh_target, enabled, instance, created_at, updated_at
		FROM endpoints
		ORDER BY created_at ASC`)
	if err != nil {
		log.Printf("[store] ListEndpoints: query failed: %v", err)
		return nil
	}
	defer rows.Close()

	var records []EndpointRecord
	for rows.Next() {
		var record EndpointRecord
		var enabled int
		if err := rows.Scan(
			&record.ID,
			&record.Name,
			&record.SSHTarget,
			&enabled,
			&record.Instance,
			&record.CreatedAt,
			&record.UpdatedAt,
		); err != nil {
			log.Printf("[store] ListEndpoints: scan failed: %v", err)
			return records
		}
		record.Enabled = enabled == 1
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[store] ListEndpoints: rows failed: %v", err)
	}
	return records
}

func (s *Store) UpdateEndpoint(id string, update EndpointUpdate) (*EndpointRecord, error) {
	if update.Instance != nil {
		canonical, err := config.NormalizeInstanceName(*update.Instance)
		if err != nil {
			return nil, err
		}
		update.Instance = &canonical
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, sql.ErrConnDone
	}

	var record EndpointRecord
	var enabled int
	err := s.db.QueryRow(`
		SELECT id, name, ssh_target, enabled, instance, created_at, updated_at
		FROM endpoints
		WHERE id = ?`, id).Scan(
		&record.ID,
		&record.Name,
		&record.SSHTarget,
		&enabled,
		&record.Instance,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	record.Enabled = enabled == 1

	if update.Name != nil {
		record.Name = strings.TrimSpace(*update.Name)
	}
	if update.SSHTarget != nil {
		record.SSHTarget = strings.TrimSpace(*update.SSHTarget)
	}
	if update.Enabled != nil {
		record.Enabled = *update.Enabled
	}
	if update.Instance != nil {
		record.Instance = *update.Instance
	}
	record.UpdatedAt = endpointStamp()

	if _, err := s.db.Exec(`
		UPDATE endpoints
		SET name = ?, ssh_target = ?, enabled = ?, instance = ?, updated_at = ?
		WHERE id = ?`,
		record.Name,
		record.SSHTarget,
		boolToInt(record.Enabled),
		record.Instance,
		record.UpdatedAt,
		record.ID,
	); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) RemoveEndpoint(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return sql.ErrConnDone
	}
	_, err := s.db.Exec("DELETE FROM endpoints WHERE id = ?", id)
	return err
}
