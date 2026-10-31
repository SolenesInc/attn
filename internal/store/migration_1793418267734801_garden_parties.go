package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/shortid"
	"github.com/victorarias/attn/internal/who"
)

func init() {
	registerMigration(migration{
		version: 1793418267734801,
		desc:    "garden parties",
		apply:   applyMigration1793418267734801,
	})
}

func applyMigration1793418267734801(tx *sql.Tx) error {
	names, err := readGardenPartyNames(tx)
	if err != nil {
		return err
	}
	tables, err := gardenPartyTables(tx)
	if err != nil {
		return err
	}
	seedProfiles := map[string]string{}
	if table := tables["seeds"]; table != "" {
		if err := migrateGardenPartySeeds(tx, table, tables["notes"], names, seedProfiles); err != nil {
			return err
		}
	}
	if table := tables["notes"]; table != "" {
		err := rewriteGardenPartyDocuments(tx, table, func(body map[string]any) bool {
			if _, authored := body["author"]; authored {
				return false
			}
			profile := seedProfiles[gardenPartyText(body, "seed")]
			body["author"] = names.actor(profile, gardenPartyText(body, "author_member"), gardenPartyText(body, "author_session"))
			delete(body, "author_member")
			delete(body, "author_session")
			return true
		})
		if err != nil {
			return err
		}
	}
	if table := tables["dispatches"]; table != "" {
		err := rewriteGardenPartyDocuments(tx, table, func(body map[string]any) bool {
			_, hasMember := body["dispatcher_member"]
			_, hasSession := body["dispatcher_session"]
			if !hasMember && !hasSession {
				return false
			}
			member, session := gardenPartyText(body, "dispatcher_member"), gardenPartyText(body, "dispatcher_session")
			delete(body, "dispatcher_member")
			delete(body, "dispatcher_session")
			if member == "" && session == "" {
				return true
			}
			profile := seedProfiles[gardenPartyText(body, "crown")]
			if profile == "" {
				profile = names.sessionProfiles[session]
			}
			body["dispatcher"] = names.actor(profile, member, session)
			return true
		})
		if err != nil {
			return err
		}
	}
	return migrateGardenPartyHandovers(tx, names)
}

type gardenPartyNames struct {
	members         map[[2]string]string
	memberOfSession map[string]string
	sessionProfiles map[string]string
}

func readGardenPartyNames(tx *sql.Tx) (gardenPartyNames, error) {
	names := gardenPartyNames{members: map[[2]string]string{}, memberOfSession: map[string]string{}, sessionProfiles: map[string]string{}}
	rows, err := tx.Query(`SELECT member_key, profile_id, name FROM crew_members`)
	if err != nil {
		return names, err
	}
	for rows.Next() {
		var key, profile, name string
		if err := rows.Scan(&key, &profile, &name); err != nil {
			rows.Close()
			return names, err
		}
		if _, err := who.ParseMemberKey(key); err != nil {
			continue
		}
		names.members[[2]string{profile, key}] = key
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			names.members[[2]string{profile, name}] = key
		}
	}
	if err := rows.Close(); err != nil {
		return names, err
	}
	rows, err = tx.Query(`SELECT id, profile_id, member_key FROM sessions`)
	if err != nil {
		return names, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, profile, key string
		if err := rows.Scan(&id, &profile, &key); err != nil {
			return names, err
		}
		names.sessionProfiles[id] = profile
		if _, err := who.ParseMemberKey(key); err == nil {
			names.memberOfSession[id] = key
		}
	}
	return names, rows.Err()
}

func (n gardenPartyNames) member(profile, text string) (string, bool) {
	key, ok := n.members[[2]string{profile, strings.ToLower(strings.TrimSpace(text))}]
	return key, ok
}

func (n gardenPartyNames) sessionParty(id string) string {
	if key := n.memberOfSession[id]; key != "" {
		return "member:" + key
	}
	return "session:" + id
}

func (n gardenPartyNames) actor(profile, member, session string) string {
	if key, ok := n.member(profile, member); ok {
		return "member:" + key
	}
	if strings.TrimSpace(member) == "attn" {
		return "attn"
	}
	if session = strings.TrimSpace(session); session != "" {
		return n.sessionParty(session)
	}
	return "user"
}

func (n gardenPartyNames) tender(profile, member, session string) (party string, named bool) {
	if key, ok := n.member(profile, member); ok {
		return "member:" + key, false
	}
	if strings.TrimSpace(member) != "" {
		return "", true
	}
	if session = strings.TrimSpace(session); session != "" {
		return n.sessionParty(session), false
	}
	return "", false
}

func gardenPartyTables(tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.Query(`SELECT collection, id FROM document_collections WHERE namespace = 'core/garden'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tables := map[string]string{}
	for rows.Next() {
		var collection string
		var id int64
		if err := rows.Scan(&collection, &id); err != nil {
			return nil, err
		}
		tables[collection] = docstore.TableName(id)
	}
	return tables, rows.Err()
}

func gardenPartyText(body map[string]any, field string) string {
	text, _ := body[field].(string)
	return text
}

func rewriteGardenPartyDocuments(tx *sql.Tx, table string, rewrite func(map[string]any) bool) error {
	rows, err := tx.Query(`SELECT id, body FROM ` + table)
	if err != nil {
		return err
	}
	type document struct {
		id   string
		body []byte
	}
	var documents []document
	for rows.Next() {
		var doc document
		if err := rows.Scan(&doc.id, &doc.body); err != nil {
			rows.Close()
			return err
		}
		documents = append(documents, doc)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, doc := range documents {
		var body map[string]any
		if err := json.Unmarshal(doc.body, &body); err != nil {
			return fmt.Errorf("%s %s: %w", table, doc.id, err)
		}
		if !rewrite(body) {
			continue
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE `+table+` SET body = ? WHERE id = ?`, encoded, doc.id); err != nil {
			return err
		}
	}
	return nil
}

func migrateGardenPartySeeds(tx *sql.Tx, table, notes string, names gardenPartyNames, profiles map[string]string) error {
	automated := map[string]bool{}
	rows, err := tx.Query(`SELECT DISTINCT seed_id FROM automation_runs WHERE seed_id != ''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		automated[id] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	type dropped struct{ seed, name string }
	var claims []dropped
	err = rewriteGardenPartyDocuments(tx, table, func(body map[string]any) bool {
		id, profile := gardenPartyText(body, "id"), gardenPartyText(body, "profile_id")
		profiles[id] = profile
		planterMember, planterSession := gardenPartyText(body, "planter_member"), gardenPartyText(body, "planter_session")
		body["planter"] = names.actor(profile, planterMember, planterSession)
		if planterMember == "" && planterSession == "" && automated[id] {
			body["planter"] = "attn"
		}
		member := gardenPartyText(body, "tender_member")
		if tender, named := names.tender(profile, member, gardenPartyText(body, "tender_session")); tender != "" {
			body["tender"] = tender
		} else if named {
			claims = append(claims, dropped{seed: id, name: member})
		}
		for _, field := range []string{"planter_member", "planter_session", "tender_member", "tender_session"} {
			delete(body, field)
		}
		if condition, ok := body["harvest_when"].(map[string]any); ok {
			condition["set_by"] = names.actor(profile, gardenPartyText(condition, "set_by_member"), gardenPartyText(condition, "set_by_session"))
			delete(condition, "set_by_member")
			delete(condition, "set_by_session")
		}
		results, _ := body["check_results"].(map[string]any)
		for _, value := range results {
			result, _ := value.(map[string]any)
			for _, kind := range []string{"report", "verdict"} {
				if proof, ok := result[kind].(map[string]any); ok {
					proof["by"] = names.actor(profile, gardenPartyText(proof, "by_member"), gardenPartyText(proof, "by_session"))
					delete(proof, "by_member")
					delete(proof, "by_session")
				}
			}
		}
		return true
	})
	if err != nil || len(claims) == 0 {
		return err
	}
	if notes == "" {
		return fmt.Errorf("drop the named claim on %s: the garden has no notes collection", claims[0].seed)
	}
	stamp := time.Now().UTC().Format(docstore.TimeFormat)
	for _, claim := range claims {
		id, err := shortid.New("n-")
		if err != nil {
			return err
		}
		note, err := json.Marshal(map[string]any{
			"id": id, "seed": claim.seed, "kind": "note", "author": "attn",
			"body": fmt.Sprintf("attn removed the claim by %q, which names no crew member; named claims are gone. Assign this seed to a crew member with `attn seed tend %s --for <name>`.", claim.name, claim.seed),
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO `+notes+` (id, body, rev, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`, id, note, stamp, stamp); err != nil {
			return err
		}
	}
	return nil
}

func migrateGardenPartyHandovers(tx *sql.Tx, names gardenPartyNames) error {
	rows, err := tx.Query(`SELECT request_id, profile_id, session_id, handover_tender_member, handover_tender_session FROM delegation_operations
		WHERE handover_tender_member != '' OR handover_tender_session != ''`)
	if err != nil {
		return err
	}
	type handover struct{ request, tender string }
	var handovers []handover
	for rows.Next() {
		var request, profile, session, member, tenderSession string
		if err := rows.Scan(&request, &profile, &session, &member, &tenderSession); err != nil {
			rows.Close()
			return err
		}
		if profile == "" {
			profile = names.sessionProfiles[session]
		}
		tender, _ := names.tender(profile, member, tenderSession)
		handovers = append(handovers, handover{request: request, tender: tender})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, h := range handovers {
		if _, err := tx.Exec(`UPDATE delegation_operations SET handover_tender = ? WHERE request_id = ?`, h.tender, h.request); err != nil {
			return err
		}
	}
	return nil
}
