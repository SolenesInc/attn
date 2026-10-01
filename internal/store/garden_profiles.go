package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func (s *Store) GardenSessionProfileID(sessionID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return "", fmt.Errorf("garden recipient profiles need the SQLite store")
	}
	var profileID string
	err := s.db.QueryRow(`SELECT profile_id FROM sessions WHERE id = ?
		UNION ALL SELECT coalesce(json_extract(request_json, '$.profile_id'), '') FROM delegation_operations
		WHERE session_id = ? AND state IN (?, ?) LIMIT 1`, sessionID, sessionID,
		string(protocol.DelegationOperationStateAccepted), string(protocol.DelegationOperationStatePreparing)).Scan(&profileID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return profileID, err
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
	err = tx.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s WHERE json_extract(body, '$.profile_id') = ? AND json_extract(body, '$.status') IN ('planted', 'growing', 'dormant')`, table), profileID).Scan(&count)
	return count, err
}

func countLiveProfileDispatches(tx *sql.Tx, profileID string) (int, error) {
	_, table, found, err := readCollectionTx(tx, garden.Namespace, garden.CollectionDispatches)
	if err != nil || !found {
		return 0, err
	}
	var count int
	err = tx.QueryRow(`SELECT count(*) FROM sessions s JOIN `+table+` d ON d.id = s.id
		WHERE s.profile_id = ? AND s.closed_at = '' AND coalesce(json_extract(d.body, '$.crown'), '') != ''`, profileID).Scan(&count)
	return count, err
}

func countPendingProfileDelegations(tx *sql.Tx, profileID string) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT count(*) FROM delegation_operations
		WHERE json_extract(request_json, '$.profile_id') = ? AND state IN (?, ?)`, profileID,
		string(protocol.DelegationOperationStateAccepted), string(protocol.DelegationOperationStatePreparing)).Scan(&count)
	return count, err
}

func cancelProfileGardenReviews(tx *sql.Tx, profileID, now string) ([]string, []string, error) {
	_, runsTable, found, err := readCollectionTx(tx, garden.Namespace, garden.CollectionReviewRuns)
	if err != nil || !found {
		return nil, nil, err
	}
	running := `SELECT id FROM ` + runsTable + ` WHERE json_extract(body, '$.profile_id') = ? AND json_extract(body, '$.status') = ?`
	runs, err := queryColumn[string](tx, running, profileID, garden.ReviewRunStatusRunning)
	if err != nil || len(runs) == 0 {
		return runs, nil, err
	}
	var items []string
	_, itemsTable, found, err := readCollectionTx(tx, garden.Namespace, garden.CollectionReviewItems)
	if err != nil {
		return nil, nil, err
	}
	if found {
		items, err = queryColumn[string](tx, `SELECT id FROM `+itemsTable+` WHERE json_extract(body, '$.run_id') IN (`+running+`)`, profileID, garden.ReviewRunStatusRunning)
		if err != nil {
			return nil, nil, err
		}
	}
	_, err = tx.Exec(`UPDATE `+runsTable+` SET body = json_set(body, '$.status', ?, '$.completed_at', ?), rev = rev + 1, updated_at = ? WHERE json_extract(body, '$.profile_id') = ? AND json_extract(body, '$.status') = ?`, garden.ReviewRunStatusCanceled, now, now, profileID, garden.ReviewRunStatusRunning)
	return runs, items, err
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
	var previous, previousMember string
	err = q.QueryRow(`SELECT json_extract(body, '$.profile_id'), coalesce(json_extract(body, '$.tender_member'), '') FROM `+table+` WHERE id = ?`, id).Scan(&previous, &previousMember)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && previous != seed.ProfileID {
		return fmt.Errorf("seed %s belongs to profile %s for life; cannot move it to profile %q", id, previous, owner.Name)
	}
	for _, sessionID := range []string{seed.TenderSession} {
		if sessionID == "" {
			continue
		}
		var profileID string
		err := q.QueryRow(`SELECT profile_id FROM sessions WHERE id = ?`, sessionID).Scan(&profileID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if profileID != seed.ProfileID {
			caller, err := loadProfile(q, profileID)
			if err != nil {
				return err
			}
			return fmt.Errorf("seed %s belongs to profile %q; tender %s belongs to profile %q", id, owner.Name, sessionID, caller.Name)
		}
	}
	if seed.TenderMember != "" && seed.TenderMember != previousMember {
		var profileID string
		err := q.QueryRow(`SELECT profile_id FROM crew_profiles WHERE member_id = ?`, seed.TenderMember).Scan(&profileID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && profileID != seed.ProfileID {
			caller, err := loadProfile(q, profileID)
			if err != nil {
				return err
			}
			return fmt.Errorf("seed %s belongs to profile %q; crew member %s belongs to profile %q", id, owner.Name, seed.TenderMember, caller.Name)
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

func refuseMovingGardenWork(tx *sql.Tx, sessionID, memberID, from, to string) error {
	owner, err := loadProfile(tx, from)
	if err != nil {
		return err
	}
	target, err := loadProfile(tx, to)
	if err != nil {
		return err
	}
	_, dispatchTable, dispatchFound, err := readCollectionTx(tx, garden.Namespace, garden.CollectionDispatches)
	if err != nil {
		return err
	}
	if dispatchFound {
		var crown string
		err := tx.QueryRow(`SELECT json_extract(body, '$.crown') FROM `+dispatchTable+` WHERE id = ? AND coalesce(json_extract(body, '$.crown'), '') != ''`, sessionID).Scan(&crown)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			return fmt.Errorf("session %s was dispatched for seed %s in profile %q; cannot move it to profile %q: close it and delegate afresh in the destination profile", sessionID, crown, owner.Name, target.Name)
		}
	}
	_, table, found, err := readCollectionTx(tx, garden.Namespace, garden.CollectionSeeds)
	if err != nil || !found {
		return err
	}
	var seedID string
	query := `SELECT id FROM ` + table + ` WHERE json_extract(body, '$.status') IN ('planted', 'growing', 'dormant') AND (json_extract(body, '$.tender_session') = ? OR (json_extract(body, '$.profile_id') = ? AND ? != '' AND json_extract(body, '$.tender_member') = ?)`
	query += `) ORDER BY id LIMIT 1`
	if err := tx.QueryRow(query, sessionID, from, memberID, memberID).Scan(&seedID); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("session %s tends open seed %s in profile %q; cannot move it to profile %q: park or finish the work first", sessionID, seedID, owner.Name, target.Name)
}
