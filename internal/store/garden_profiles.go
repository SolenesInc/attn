package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func (s *Store) GardenSessionProfileIDs(sessionIDs []protocol.SessionID) (map[protocol.SessionID]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, fmt.Errorf("garden recipient profiles need the SQLite store")
	}
	ids, err := json.Marshal(sessionIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, profile_id, 0 AS delegated FROM sessions_with_runtime WHERE id IN (SELECT value FROM json_each(?1))
		UNION ALL SELECT session_id, coalesce(json_extract(request_json, '$.profile_id'), ''), 1 FROM delegation_operations
		WHERE session_id IN (SELECT value FROM json_each(?1)) AND state IN (?2, ?3)
		ORDER BY delegated`, string(ids),
		string(protocol.DelegationOperationStateAccepted), string(protocol.DelegationOperationStatePreparing))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := make(map[protocol.SessionID]string, len(sessionIDs))
	for rows.Next() {
		var id protocol.SessionID
		var profileID string
		var delegated bool
		if err := rows.Scan(&id, &profileID, &delegated); err != nil {
			return nil, err
		}
		if _, known := profiles[id]; !known {
			profiles[id] = profileID
		}
	}
	return profiles, rows.Err()
}

func migrateGardenProfiles(tx *sql.Tx) error {
	view, err := loadProfileMigration(tx)
	if err != nil {
		return err
	}
	for _, collection := range []string{garden.CollectionSeeds, garden.CollectionReviewRuns} {
		schema, table, found, err := readCollectionTx(tx, garden.Namespace, collection)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		fields := schema.Fields
		hasProfile := false
		for _, field := range fields {
			hasProfile = hasProfile || field.Name == "profile_id"
		}
		if !hasProfile {
			fields = append(fields, docstore.FieldSpec{Name: "profile_id", Type: docstore.FieldString})
		}
		if err := alterCollectionTable(tx, table, schema.Fields, fields); err != nil {
			return err
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE document_collections SET fields_json = ? WHERE namespace = ? AND collection = ?`, string(encoded), garden.Namespace, collection); err != nil {
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET body = json_set(body, '$.profile_id', ?), rev = rev + 1`, table), view.Manifest.ProfileID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE delegation_operations SET request_json = json_set(request_json, '$.profile_id', coalesce(nullif(profile_id, ''), ?))
		WHERE coalesce(json_extract(request_json, '$.profile_id'), '') = ''`, view.Manifest.ProfileID)
	if err != nil {
		return err
	}
	return nil
}

func countOpenProfileSeeds(tx *sql.Tx, profileID string) (int, error) {
	_, table, found, err := readCollectionTx(tx, garden.Namespace, garden.CollectionSeeds)
	if err != nil || !found {
		return 0, err
	}
	var count int
	err = tx.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s WHERE json_extract(body, '$.profile_id') = ? AND json_extract(body, '$.status') IN ('planted', 'growing', 'parked')`, table), profileID).Scan(&count)
	return count, err
}

func countRunningProfileReviews(tx *sql.Tx, profileID string) (int, error) {
	_, table, found, err := readCollectionTx(tx, garden.Namespace, garden.CollectionReviewRuns)
	if err != nil || !found {
		return 0, err
	}
	var count int
	err = tx.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s WHERE json_extract(body, '$.profile_id') = ? AND json_extract(body, '$.status') = 'running'`, table), profileID).Scan(&count)
	return count, err
}

func countPendingProfileDelegations(tx *sql.Tx, profileID string) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT count(*) FROM delegation_operations
		WHERE json_extract(request_json, '$.profile_id') = ? AND state IN (?, ?)`, profileID,
		string(protocol.DelegationOperationStateAccepted), string(protocol.DelegationOperationStatePreparing)).Scan(&count)
	return count, err
}

func checkSeedProfileWrite(q rowQuerier, schema docstore.CollectionSchema, table, id string, body []byte) error {
	if schema.Namespace != garden.Namespace || schema.Collection != garden.CollectionSeeds {
		return nil
	}
	seed, err := garden.Decode(body)
	if err != nil {
		return err
	}
	if seed.ProfileID == "" {
		return fmt.Errorf("seed %s needs profile_id: every seed belongs to one profile", id)
	}
	owner, err := loadLiveProfile(q, seed.ProfileID)
	if err != nil {
		return err
	}
	var previous, previousTender string
	err = q.QueryRow(`SELECT json_extract(body, '$.profile_id'), coalesce(json_extract(body, '$.tender'), '') FROM `+table+` WHERE id = ?`, id).Scan(&previous, &previousTender)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && previous != seed.ProfileID {
		return fmt.Errorf("seed %s belongs to profile %s for life; cannot move it to profile %q", id, previous, owner.Name)
	}
	if sessionID, ok := seed.Tender.Session(); ok {
		var profileID string
		err := q.QueryRow(`SELECT profile_id FROM sessions WHERE id = ?`, sessionID).Scan(&profileID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && profileID != seed.ProfileID {
			caller, err := loadProfile(q, profileID)
			if err != nil {
				return err
			}
			return fmt.Errorf("seed %s belongs to profile %q; tender %s belongs to profile %q", id, owner.Name, sessionID, caller.Name)
		}
	}
	if key, ok := seed.Tender.Member(); ok && seed.Tender.String() != previousTender {
		var profileID string
		err := q.QueryRow(`SELECT profile_id FROM crew_members WHERE member_key = ?`, key.String()).Scan(&profileID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && profileID != seed.ProfileID {
			caller, err := loadProfile(q, profileID)
			if err != nil {
				return err
			}
			return fmt.Errorf("seed %s belongs to profile %q; crew member %s belongs to profile %q", id, owner.Name, key, caller.Name)
		}
	}
	for _, edge := range seed.Edges {
		var profileID string
		if err := q.QueryRow(`SELECT json_extract(body, '$.profile_id') FROM `+table+` WHERE id = ?`, edge.To).Scan(&profileID); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return err
		}
		if profileID != seed.ProfileID {
			target, err := loadProfile(q, profileID)
			if err != nil {
				return err
			}
			return fmt.Errorf("seed %s belongs to profile %q; edge target %s belongs to profile %q", id, owner.Name, edge.To, target.Name)
		}
	}
	return nil
}
