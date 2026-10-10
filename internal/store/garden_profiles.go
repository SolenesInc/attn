package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (s *Store) GardenSessionProfileID(sessionID protocol.SessionID) (string, error) {
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
	tender, claimed := seed.Claim.Tender()
	if claimed && tender.String() != previousTender {
		profileID, err := partyProfile(q, tender)
		if err != nil {
			return err
		}
		if profileID != seed.ProfileID {
			return fmt.Errorf("seed %s belongs to profile %q; tender %s belongs to profile %s", id, owner.Name, tender, profileID)
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

func (s *Store) PartyProfile(p who.Party) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return partyProfile(s.db, p)
}
func partyProfile(q rowQuerier, p who.Party) (string, error) {
	return who.SwitchParty(p, func(id protocol.SessionID) (string, error) {
		var profile string
		err := q.QueryRow(`SELECT profile_id FROM sessions WHERE id=? UNION ALL SELECT coalesce(json_extract(request_json,'$.profile_id'),'') FROM delegation_operations WHERE session_id=? AND state IN (?,?) LIMIT 1`, id, id, string(protocol.DelegationOperationStateAccepted), string(protocol.DelegationOperationStatePreparing)).Scan(&profile)
		if err != nil {
			return "", fmt.Errorf("read profile for %s: %w", p, err)
		}
		return profile, nil
	}, func(key who.MemberKey) (string, error) {
		var profile string
		err := q.QueryRow(`SELECT profile_id FROM crew_members WHERE member_key=?`, key).Scan(&profile)
		if err != nil {
			return "", fmt.Errorf("read profile for %s: %w", p, err)
		}
		return profile, nil
	})
}
