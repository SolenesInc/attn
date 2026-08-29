package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

type TicketSeedNote struct {
	ID   string
	Body []byte
	Fact BusEvent
}

type TicketSeedHandover struct {
	TicketID             string
	SeedID               string
	SeedBody             []byte
	SeedFact             BusEvent
	SeedTitle            string
	SeedDescription      string
	SeedSchema           docstore.CollectionSchema
	NoteSchema           docstore.CollectionSchema
	DispatchSchema       docstore.CollectionSchema
	Notes                []TicketSeedNote
	SessionIDs           []string
	HandoverKind         string
	EvidenceFingerprint  string
	OriginalTicketStatus TicketStatus
	CreatedAt            time.Time
}

type TicketSeedHandoverResult struct {
	SeedID string
	Result string
	Seqs   []int64
}

type TicketSeedLink struct {
	TicketID             string
	SeedID               string
	HandoverKind         string
	EvidenceFingerprint  string
	OriginalTicketStatus TicketStatus
	CreatedAt            time.Time
}

func (s *Store) EnsureTicketSeedHandover(handover TicketSeedHandover) (TicketSeedHandoverResult, error) {
	if s == nil || s.db == nil {
		return TicketSeedHandoverResult{}, fmt.Errorf("store: no database")
	}
	seedTable, err := s.documentTable(handover.SeedSchema)
	if err != nil {
		return TicketSeedHandoverResult{}, err
	}
	noteTable, err := s.documentTable(handover.NoteSchema)
	if err != nil {
		return TicketSeedHandoverResult{}, err
	}
	dispatchTable, err := s.documentTable(handover.DispatchSchema)
	if err != nil {
		return TicketSeedHandoverResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return TicketSeedHandoverResult{}, err
	}
	defer tx.Rollback()

	var existingSeedID string
	if err := tx.QueryRow(`SELECT seed_id FROM legacy_ticket_seed_links WHERE ticket_id=?`, handover.TicketID).Scan(&existingSeedID); err == nil {
		return TicketSeedHandoverResult{SeedID: existingSeedID, Result: "adopted_link"}, nil
	} else if err != sql.ErrNoRows {
		return TicketSeedHandoverResult{}, err
	}

	lineage, err := ticketSeedLineage(tx, seedTable, noteTable, dispatchTable, handover)
	if err != nil {
		return TicketSeedHandoverResult{}, err
	}
	if len(lineage) > 1 {
		return TicketSeedHandoverResult{Result: "ambiguous_lineage"}, nil
	}
	if len(lineage) == 1 {
		if err := insertTicketSeedLink(tx, handover, lineage[0]); err != nil {
			return TicketSeedHandoverResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return TicketSeedHandoverResult{}, err
		}
		return TicketSeedHandoverResult{SeedID: lineage[0], Result: "adopted_lineage"}, nil
	}

	exact, err := exactSeedContentMatches(tx, seedTable, handover.SeedTitle, handover.SeedDescription)
	if err != nil {
		return TicketSeedHandoverResult{}, err
	}
	if len(exact) > 0 {
		return TicketSeedHandoverResult{Result: "ambiguous_content"}, nil
	}

	expected := int64(docstore.ExpectAbsent)
	commits := []DocumentCommit{{
		Write: DocumentWrite{Schema: handover.SeedSchema, ID: handover.SeedID, Body: handover.SeedBody, Expected: &expected},
		Fact:  handover.SeedFact,
	}}
	tables := []string{seedTable}
	for _, note := range handover.Notes {
		commits = append(commits, DocumentCommit{
			Write: DocumentWrite{Schema: handover.NoteSchema, ID: note.ID, Body: note.Body, Expected: &expected},
			Fact:  note.Fact,
		})
		tables = append(tables, noteTable)
	}
	results, _, err := commitDocumentWritesWith(tx, commits, tables, handover.CreatedAt)
	if err != nil {
		return TicketSeedHandoverResult{}, err
	}
	if err := insertTicketSeedLink(tx, handover, handover.SeedID); err != nil {
		return TicketSeedHandoverResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketSeedHandoverResult{}, err
	}
	seqs := make([]int64, len(results))
	for i := range results {
		seqs[i] = results[i].Seq
	}
	return TicketSeedHandoverResult{SeedID: handover.SeedID, Result: "created", Seqs: seqs}, nil
}

func (s *Store) TicketSeedLink(ticketID string) (*TicketSeedLink, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	var link TicketSeedLink
	var status, createdAt string
	err := s.db.QueryRow(`SELECT ticket_id,seed_id,source_kind,evidence_fingerprint,original_terminal_state,created_at
		FROM legacy_ticket_seed_links WHERE ticket_id=?`, ticketID).Scan(
		&link.TicketID, &link.SeedID, &link.HandoverKind, &link.EvidenceFingerprint, &status, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	link.OriginalTicketStatus = TicketStatus(status)
	link.CreatedAt = parseStoreTime(createdAt)
	return &link, nil
}

func insertTicketSeedLink(tx *sql.Tx, handover TicketSeedHandover, seedID string) error {
	_, err := tx.Exec(`INSERT INTO legacy_ticket_seed_links
		(ticket_id,seed_id,source_kind,evidence_fingerprint,original_terminal_state,created_at)
		VALUES (?,?,?,?,?,?)`, handover.TicketID, seedID, handover.HandoverKind, handover.EvidenceFingerprint,
		string(handover.OriginalTicketStatus), handover.CreatedAt.UTC().Format(sortableTimeFormat))
	return err
}

func ticketSeedLineage(tx *sql.Tx, seedTable, noteTable, dispatchTable string, handover TicketSeedHandover) ([]string, error) {
	query := `SELECT DISTINCT candidate.seed_id FROM (
		SELECT json_extract(body,'$.seed') AS seed_id FROM ` + noteTable + `
		WHERE json_valid(body) AND (instr(json_extract(body,'$.body'),?) > 0 OR instr(json_extract(body,'$.body'),?) > 0)`
	args := []any{
		fmt.Sprintf("converted from backlog ticket `%s` at the garden cutover", handover.TicketID),
		fmt.Sprintf("replanted from ticket `%s`,", handover.TicketID),
	}
	sessions := make(map[string]struct{})
	for _, id := range handover.SessionIDs {
		if id = strings.TrimSpace(id); id != "" {
			sessions[id] = struct{}{}
		}
	}
	if len(sessions) > 0 {
		ids := make([]string, 0, len(sessions))
		for id := range sessions {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		query += ` UNION SELECT json_extract(body,'$.crown') FROM ` + dispatchTable +
			` WHERE json_valid(body) AND json_extract(body,'$.session_id') IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	query += `) AS candidate JOIN ` + seedTable + ` AS seed ON seed.id=candidate.seed_id
		WHERE candidate.seed_id != '' ORDER BY candidate.seed_id`

	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func exactSeedContentMatches(tx *sql.Tx, seedTable, title, body string) ([]string, error) {
	rows, err := tx.Query(`SELECT id,body FROM ` + seedTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var seed struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if json.Unmarshal([]byte(raw), &seed) == nil && strings.TrimSpace(seed.Title) == strings.TrimSpace(title) && strings.TrimSpace(seed.Body) == strings.TrimSpace(body) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}
