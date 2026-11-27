package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

var (
	ErrAskNotOpen    = errors.New("the ask is not open")
	ErrAskGone       = errors.New("the ask is gone")
	ErrAskerGone     = errors.New("the asking session is not live")
	ErrAskSeedClosed = errors.New("the seed the ask is about is no longer open")
)

type StaleAskDraftError struct {
	Generation int
}

func (e *StaleAskDraftError) Error() string {
	return fmt.Sprintf("the ask's draft is already at generation %d", e.Generation)
}

type Ask struct {
	ID         string
	SessionID  protocol.SessionID
	To         who.Address
	Subject    string
	Markdown   string
	BaseDir    string
	Host       string
	Cwd        string
	CreatedAt  string
	Re         *protocol.AskThread
	Amendments []AskAmendment
	// References are the ask's own, with each host a machine id; an amendment keeps its own.
	References []protocol.AskReference
	Reply      *protocol.AskReply
	Draft      *protocol.AskDraft
}

type AskAmendment struct {
	Markdown   string
	AmendedAt  time.Time
	References []protocol.AskReference
}

const (
	askedColumns = "id, session_id, address, subject, markdown, base_dir, host, cwd, created_at, re_ask_id, re_subject, re_answer, re_answered_at"
	askColumns   = askedColumns + ", answered_at, answers, note, COALESCE((SELECT draft FROM ask_drafts WHERE ask_id = asks.id), '')"
)

const openAsk = "answered_at = ''"

func (s *Store) AddAsk(a Ask, defaultDueAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("asks need the SQLite store")
	}
	dueAt := ""
	if !defaultDueAt.IsZero() {
		dueAt = defaultDueAt.UTC().Format(sortableTimeFormat)
	}
	var re protocol.AskThread
	if a.Re != nil {
		re = *a.Re
	}
	query := "INSERT INTO asks (" + askedColumns + ", default_due_at) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM sessions WHERE id = ? AND closed_at = '')"
	args := []any{a.ID, a.SessionID, a.To.String(), a.Subject, a.Markdown, a.BaseDir, a.Host, a.Cwd, a.CreatedAt,
		re.AskID, protocol.Deref(re.Subject), protocol.Deref(re.Answer), protocol.Deref(re.AnsweredAt), dueAt, a.SessionID}
	if seedID, ok := a.To.TenderOf(); ok {
		_, seeds, found, err := readCollection(s.db, garden.Namespace, garden.CollectionSeeds)
		if err != nil {
			return err
		}
		if !found {
			return ErrAskSeedClosed
		}
		query += " AND EXISTS (SELECT 1 FROM " + seeds + " WHERE id = ? AND json_extract(body, '$.status') IN (?, ?, ?))"
		args = append(args, seedID, garden.StatusPlanted, garden.StatusGrowing, garden.StatusParked)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		if err := addAskReferences(tx, a.ID, 0, a.References); err != nil {
			return err
		}
		return tx.Commit()
	}
	var live int
	if err := tx.QueryRow("SELECT count(*) FROM sessions WHERE id = ? AND closed_at = ''", a.SessionID).Scan(&live); err != nil {
		return err
	}
	if live == 0 {
		return ErrAskerGone
	}
	return ErrAskSeedClosed
}

func (s *Store) Asks() ([]Ask, error) {
	return s.queryAsks("SELECT " + askColumns + " FROM asks ORDER BY answered_at != '', created_at, id")
}

func (s *Store) HasAsks() (bool, error) {
	return s.askExists("SELECT EXISTS(SELECT 1 FROM asks)")
}

func (s *Store) HasAsksTo(to who.Address) (bool, error) {
	return s.askExists("SELECT EXISTS(SELECT 1 FROM asks WHERE address = ?)", to.String())
}

func (s *Store) askExists(query string, args ...any) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false, nil
	}
	var exists bool
	err := s.db.QueryRow(query, args...).Scan(&exists)
	return exists, err
}

func (s *Store) OpenAsksTo(to who.Address) ([]Ask, error) {
	return s.queryAsks("SELECT "+askColumns+" FROM asks WHERE address = ? AND "+openAsk+" ORDER BY created_at, id", to.String())
}

func (s *Store) OpenAsksFrom(session protocol.SessionID) ([]Ask, error) {
	return s.queryAsks("SELECT "+askColumns+" FROM asks WHERE session_id = ? AND "+openAsk+" ORDER BY created_at, id", session)
}

func (s *Store) queryAsks(query string, args ...any) ([]Ask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var asks []Ask
	for rows.Next() {
		a, err := scanAsk(rows)
		if err != nil {
			return nil, err
		}
		asks = append(asks, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range asks {
		if err := readAskAfterward(s.db, &asks[i]); err != nil {
			return nil, err
		}
	}
	return asks, nil
}

// readAskAfterward reads what an ask holds beside its row: its references and its amendments with theirs.
func readAskAfterward(db rowsQuerier, a *Ask) error {
	references, err := referencesOf(db, a.ID)
	if err != nil {
		return err
	}
	a.References = references[0]
	a.Amendments, err = amendmentsOf(db, a.ID, references)
	return err
}

func addAskReferences(tx *sql.Tx, askID string, amendment int, references []protocol.AskReference) error {
	for i, r := range references {
		if _, err := tx.Exec("INSERT INTO ask_references (ask_id, amendment, seq, kind, text, host, path, line) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			askID, amendment, i, string(r.Kind), r.Text, protocol.Deref(r.Host), r.Path, r.Line); err != nil {
			return fmt.Errorf("ask %s reference %q: %w", askID, r.Text, err)
		}
	}
	return nil
}

// referencesOf are the ask's references by the amendment that holds them, 0 for the ask's own.
func referencesOf(db rowsQuerier, askID string) (map[int][]protocol.AskReference, error) {
	rows, err := db.Query("SELECT amendment, kind, text, host, path, line FROM ask_references WHERE ask_id = ? ORDER BY amendment, seq", askID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	references := map[int][]protocol.AskReference{}
	for rows.Next() {
		var amendment int
		var r protocol.AskReference
		var kind, host string
		if err := rows.Scan(&amendment, &kind, &r.Text, &host, &r.Path, &r.Line); err != nil {
			return nil, err
		}
		r.Kind, r.Host = protocol.AskReferenceKind(kind), protocol.Ptr(host)
		references[amendment] = append(references[amendment], r)
	}
	return references, rows.Err()
}

func amendmentsOf(db rowsQuerier, askID string, references map[int][]protocol.AskReference) ([]AskAmendment, error) {
	rows, err := db.Query("SELECT seq, markdown, amended_at FROM ask_amendments WHERE ask_id = ? ORDER BY seq", askID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var amendments []AskAmendment
	for rows.Next() {
		var amendment AskAmendment
		var seq int
		var at string
		if err := rows.Scan(&seq, &amendment.Markdown, &at); err != nil {
			return nil, err
		}
		amendment.References = references[seq]
		if amendment.AmendedAt, err = time.Parse(sortableTimeFormat, at); err != nil {
			return nil, fmt.Errorf("ask %s amendment time: %w", askID, err)
		}
		amendments = append(amendments, amendment)
	}
	return amendments, rows.Err()
}

func (s *Store) AmendAsk(id string, amendment AskAmendment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrAskNotOpen
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var seq int
	err = tx.QueryRow("INSERT INTO ask_amendments (ask_id, seq, markdown, amended_at) SELECT id, (SELECT count(*) FROM ask_amendments WHERE ask_id = asks.id) + 1, ?, ? FROM asks WHERE id = ? AND "+openAsk+" RETURNING seq",
		amendment.Markdown, amendment.AmendedAt.UTC().Format(sortableTimeFormat), id).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAskNotOpen
	}
	if err != nil {
		return err
	}
	if err := addAskReferences(tx, id, seq, amendment.References); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveAskDraft(id string, draft protocol.AskDraft, at time.Time) error {
	encoded, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrAskNotOpen
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var open int
	if err := tx.QueryRow("SELECT count(*) FROM asks WHERE id = ? AND "+openAsk, id).Scan(&open); err != nil {
		return err
	}
	if open == 0 {
		return ErrAskNotOpen
	}
	var stored int
	err = tx.QueryRow("SELECT generation FROM ask_drafts WHERE ask_id = ?", id).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && draft.Generation <= stored {
		return &StaleAskDraftError{Generation: stored}
	}
	if _, err := tx.Exec("INSERT INTO ask_drafts (ask_id, generation, draft, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(ask_id) DO UPDATE SET generation = excluded.generation, draft = excluded.draft, updated_at = excluded.updated_at",
		id, draft.Generation, string(encoded), at.UTC().Format(sortableTimeFormat)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetOpenAsk(id string) (Ask, error) {
	a, err := s.getAsk(id, " AND "+openAsk)
	if errors.Is(err, ErrAskGone) {
		return Ask{}, ErrAskNotOpen
	}
	return a, err
}

func (s *Store) GetAsk(id string) (Ask, error) {
	return s.getAsk(id, "")
}

func (s *Store) getAsk(id, condition string) (Ask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return Ask{}, ErrAskGone
	}
	a, err := scanAsk(s.db.QueryRow("SELECT "+askColumns+" FROM asks WHERE id = ?"+condition, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ask{}, ErrAskGone
	}
	if err != nil {
		return Ask{}, err
	}
	return a, readAskAfterward(s.db, &a)
}

func (s *Store) CloseAsk(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrAskNotOpen
	}
	return expectOpenAsk(s.db.Exec("DELETE FROM asks WHERE id = ? AND "+openAsk, id))
}

func (s *Store) AnswerAsk(profileID, id string, reply protocol.AskReply, answer inbox.Item, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrAskNotOpen
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replyToAsk(tx, profileID, id, reply, at); err != nil {
		return err
	}
	if err := putInbox(tx, answer, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AnswerAskWithNote(profileID, id string, reply protocol.AskReply, answer inbox.Item, note DocumentWrite, fact BusEvent, events []BusEvent, now time.Time) (DocumentWriteResult, []int64, error) {
	results, eventSeqs, err := s.commitDocumentWrites([]DocumentCommit{{Write: note, Fact: fact}}, events, now, true, func(tx *sql.Tx) (bool, error) {
		if err := replyToAsk(tx, profileID, id, reply, now); err != nil {
			return true, err
		}
		return true, putInbox(tx, answer, now)
	})
	if err != nil {
		return DocumentWriteResult{}, nil, err
	}
	return results[0], eventSeqs, nil
}

func replyToAsk(tx *sql.Tx, profileID, id string, reply protocol.AskReply, at time.Time) error {
	answers, err := json.Marshal(reply.Answers)
	if err != nil {
		return err
	}
	if err := expectOpenAsk(tx.Exec("UPDATE asks SET answered_at = ?, answers = ?, note = ? WHERE id = ? AND "+openAsk,
		at.UTC().Format(sortableTimeFormat), string(answers), protocol.Deref(reply.Note), id)); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM ask_drafts WHERE ask_id = ?", id); err != nil {
		return err
	}
	ids := make([]string, len(reply.Attachments))
	for i, a := range reply.Attachments {
		ids[i] = a.ID
	}
	return commitAttachments(tx, profileID, protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindAsk, ID: id}, ids)
}

func expectOpenAsk(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return ErrAskNotOpen
	}
	return nil
}

func (s *Store) AsksWithDefaultDue(now time.Time) ([]Ask, error) {
	return s.queryAsks("SELECT "+askColumns+" FROM asks WHERE default_due_at != '' AND default_due_at <= ? AND "+openAsk+" ORDER BY default_due_at, id",
		now.UTC().Format(sortableTimeFormat))
}

func (s *Store) FireAskDefault(id string, notice inbox.Item, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE asks SET default_due_at = '' WHERE id = ? AND default_due_at != '' AND default_due_at <= ? AND "+openAsk,
		id, now.UTC().Format(sortableTimeFormat))
	if err != nil {
		return false, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if err := putInbox(tx, notice, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) ForgetRepliesBefore(cutoff time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, nil
	}
	return deleteAsks(s.db, "DELETE FROM asks WHERE answered_at != '' AND answered_at < ? RETURNING id", cutoff.UTC().Format(sortableTimeFormat))
}

func (s *Store) CloseAsksTo(to who.Address) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, nil
	}
	return closeAsksTo(s.db, to)
}

func closeAsksTo(db rowsQuerier, to who.Address) ([]string, error) {
	return deleteAsks(db, "DELETE FROM asks WHERE address = ? RETURNING id", to.String())
}

func closeProfileMailboxAsks(db rowsQuerier, profileID string) ([]string, error) {
	return deleteAsks(db, "DELETE FROM asks WHERE address IN (SELECT 'member:' || member_key FROM crew_members WHERE profile_id = ?) RETURNING id", profileID)
}

func deleteAsks(db rowsQuerier, query string, args ...any) ([]string, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func scanAsk(row interface{ Scan(...any) error }) (Ask, error) {
	var a Ask
	var address, reAskID, reSubject, reAnswer, reAnsweredAt, answeredAt, answers, note, draft string
	if err := row.Scan(&a.ID, &a.SessionID, &address, &a.Subject, &a.Markdown, &a.BaseDir, &a.Host, &a.Cwd, &a.CreatedAt,
		&reAskID, &reSubject, &reAnswer, &reAnsweredAt, &answeredAt, &answers, &note, &draft); err != nil {
		return Ask{}, err
	}
	if draft != "" {
		a.Draft = &protocol.AskDraft{}
		if err := json.Unmarshal([]byte(draft), a.Draft); err != nil {
			log.Printf("[store] ask %s: its draft does not read, so it shows without one: %v", a.ID, err)
			a.Draft = nil
		}
	}
	if reAskID != "" {
		a.Re = &protocol.AskThread{AskID: reAskID, Subject: nonEmptyPtr(reSubject), Answer: nonEmptyPtr(reAnswer), AnsweredAt: nonEmptyPtr(reAnsweredAt)}
	}
	to, err := who.ParseAddress(address)
	if err != nil {
		return Ask{}, err
	}
	a.To = to
	if answeredAt == "" {
		return a, nil
	}
	a.Reply = &protocol.AskReply{AnsweredAt: answeredAt}
	if err := json.Unmarshal([]byte(answers), &a.Reply.Answers); err != nil {
		return Ask{}, fmt.Errorf("ask %s reply: %w", a.ID, err)
	}
	if note != "" {
		a.Reply.Note = &note
	}
	return a, nil
}
