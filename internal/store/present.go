package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Presentation struct {
	ID                   string
	SessionID            string
	TicketID             *string
	Title                string
	Kind                 string
	RepoPath             string
	Status               string
	CreatedAt            string
	LatestRoundSeq       int
	LatestRoundSubmitted bool
}

type PresentationRound struct {
	ID             string
	PresentationID string
	Seq            int
	ManifestYAML   string
	BaseSHA        string
	HeadSHA        string
	CreatedAt      string
	SubmittedAt    *string
	Verdict        *string
}

type PresentationComment struct {
	ID        string
	RoundID   string
	Filepath  string
	LineStart int
	LineEnd   int
	Side      string
	Content   string
	Author    string
	CreatedAt string
}

func (s *Store) CreatePresentation(sessionID string, ticketID *string, title, kind, repoPath string, now time.Time) (*Presentation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	createdAt := now.UTC().Format(time.RFC3339)
	p := &Presentation{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		TicketID:  ticketID,
		Title:     title,
		Kind:      kind,
		RepoPath:  repoPath,
		Status:    "open",
		CreatedAt: createdAt,
	}

	_, err := s.db.Exec(`
		INSERT INTO presentations (id, session_id, ticket_id, title, kind, repo_path, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.SessionID, p.TicketID, p.Title, p.Kind, p.RepoPath, p.Status, p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create presentation: %w", err)
	}

	return p, nil
}

func (s *Store) CreatePresentationRound(presentationID, manifestYAML, baseSHA, headSHA string, now time.Time) (*PresentationRound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var maxSeq sql.NullInt64
	if err := tx.QueryRow(`
		SELECT MAX(seq) FROM presentation_rounds WHERE presentation_id = ?
	`, presentationID).Scan(&maxSeq); err != nil {
		return nil, fmt.Errorf("failed to compute next round seq: %w", err)
	}

	seq := 1
	if maxSeq.Valid {
		seq = int(maxSeq.Int64) + 1
	}

	round := &PresentationRound{
		ID:             uuid.New().String(),
		PresentationID: presentationID,
		Seq:            seq,
		ManifestYAML:   manifestYAML,
		BaseSHA:        baseSHA,
		HeadSHA:        headSHA,
		CreatedAt:      now.UTC().Format(time.RFC3339),
	}

	if _, err := tx.Exec(`
		INSERT INTO presentation_rounds (id, presentation_id, seq, manifest_yaml, base_sha, head_sha, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, round.ID, round.PresentationID, round.Seq, round.ManifestYAML, round.BaseSHA, round.HeadSHA, round.CreatedAt); err != nil {
		return nil, fmt.Errorf("failed to create presentation round: %w", err)
	}

	// A new round must reopen an approved or closed presentation, or its chip never surfaces again (App.tsx gates on status open).
	if _, err := tx.Exec(`
		UPDATE presentations SET status = 'open' WHERE id = ? AND status != 'open'
	`, presentationID); err != nil {
		return nil, fmt.Errorf("failed to reopen presentation for new round: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit presentation round: %w", err)
	}

	return round, nil
}

func (s *Store) GetPresentation(id string) (*Presentation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, err := s.getPresentationLocked(id)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) getPresentationLocked(id string) (*Presentation, error) {
	var p Presentation
	var ticketID sql.NullString

	err := s.db.QueryRow(`
		SELECT id, session_id, ticket_id, title, kind, repo_path, status, created_at
		FROM presentations WHERE id = ?
	`, id).Scan(&p.ID, &p.SessionID, &ticketID, &p.Title, &p.Kind, &p.RepoPath, &p.Status, &p.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get presentation: %w", err)
	}
	if ticketID.Valid {
		t := ticketID.String
		p.TicketID = &t
	}

	if err := s.enrichLatestRound(&p); err != nil {
		return nil, err
	}

	return &p, nil
}

func (s *Store) enrichLatestRound(p *Presentation) error {
	var seq sql.NullInt64
	var submittedAt sql.NullString

	err := s.db.QueryRow(`
		SELECT seq, submitted_at FROM presentation_rounds
		WHERE presentation_id = ?
		ORDER BY seq DESC LIMIT 1
	`, p.ID).Scan(&seq, &submittedAt)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to enrich latest round: %w", err)
	}
	if seq.Valid {
		p.LatestRoundSeq = int(seq.Int64)
		p.LatestRoundSubmitted = submittedAt.Valid && submittedAt.String != ""
	}
	return nil
}

func (s *Store) ListPresentations() ([]*Presentation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, session_id, ticket_id, title, kind, repo_path, status, created_at
		FROM presentations ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list presentations: %w", err)
	}
	defer rows.Close()

	var result []*Presentation
	for rows.Next() {
		var p Presentation
		var ticketID sql.NullString
		if err := rows.Scan(&p.ID, &p.SessionID, &ticketID, &p.Title, &p.Kind, &p.RepoPath, &p.Status, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan presentation: %w", err)
		}
		if ticketID.Valid {
			t := ticketID.String
			p.TicketID = &t
		}
		result = append(result, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, p := range result {
		if err := s.enrichLatestRound(p); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (s *Store) GetPresentationRound(presentationID string, seq int) (*PresentationRound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var row *sql.Row
	if seq <= 0 {
		row = s.db.QueryRow(`
			SELECT id, presentation_id, seq, manifest_yaml, base_sha, head_sha, created_at, submitted_at, verdict
			FROM presentation_rounds
			WHERE presentation_id = ?
			ORDER BY seq DESC LIMIT 1
		`, presentationID)
	} else {
		row = s.db.QueryRow(`
			SELECT id, presentation_id, seq, manifest_yaml, base_sha, head_sha, created_at, submitted_at, verdict
			FROM presentation_rounds
			WHERE presentation_id = ? AND seq = ?
		`, presentationID, seq)
	}

	var r PresentationRound
	var submittedAt, verdict sql.NullString
	err := row.Scan(&r.ID, &r.PresentationID, &r.Seq, &r.ManifestYAML, &r.BaseSHA, &r.HeadSHA, &r.CreatedAt, &submittedAt, &verdict)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get presentation round: %w", err)
	}
	if submittedAt.Valid {
		v := submittedAt.String
		r.SubmittedAt = &v
	}
	if verdict.Valid {
		v := verdict.String
		r.Verdict = &v
	}

	return &r, nil
}

func (s *Store) GetPresentationRoundByID(roundID string) (*PresentationRound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var r PresentationRound
	var submittedAt, verdict sql.NullString
	err := s.db.QueryRow(`
		SELECT id, presentation_id, seq, manifest_yaml, base_sha, head_sha, created_at, submitted_at, verdict
		FROM presentation_rounds WHERE id = ?
	`, roundID).Scan(&r.ID, &r.PresentationID, &r.Seq, &r.ManifestYAML, &r.BaseSHA, &r.HeadSHA, &r.CreatedAt, &submittedAt, &verdict)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get presentation round by id: %w", err)
	}
	if submittedAt.Valid {
		v := submittedAt.String
		r.SubmittedAt = &v
	}
	if verdict.Valid {
		v := verdict.String
		r.Verdict = &v
	}

	return &r, nil
}

var validPresentationVerdicts = map[string]bool{"approved": true, "feedback": true}

func (s *Store) SubmitPresentationRound(roundID string, verdict string, comments []PresentationComment, now time.Time) error {
	if !validPresentationVerdicts[verdict] {
		return fmt.Errorf("invalid verdict %q: must be \"approved\" or \"feedback\"", verdict)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var existingSubmittedAt sql.NullString
	var presentationID string
	err = tx.QueryRow(`SELECT submitted_at, presentation_id FROM presentation_rounds WHERE id = ?`, roundID).Scan(&existingSubmittedAt, &presentationID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("presentation round %s not found", roundID)
		}
		return fmt.Errorf("failed to look up presentation round: %w", err)
	}
	if existingSubmittedAt.Valid && existingSubmittedAt.String != "" {
		return fmt.Errorf("presentation round %s already submitted", roundID)
	}

	createdAt := now.UTC().Format(time.RFC3339)
	for _, c := range comments {
		id := c.ID
		if id == "" {
			id = uuid.New().String()
		}
		author := c.Author
		if author == "" {
			author = "user"
		}
		if _, err := tx.Exec(`
			INSERT INTO presentation_comments (id, round_id, filepath, line_start, line_end, side, content, author, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, roundID, c.Filepath, c.LineStart, c.LineEnd, c.Side, c.Content, author, createdAt); err != nil {
			return fmt.Errorf("failed to insert presentation comment: %w", err)
		}
	}

	if _, err := tx.Exec(`
		UPDATE presentation_rounds SET submitted_at = ?, verdict = ? WHERE id = ?
	`, createdAt, verdict, roundID); err != nil {
		return fmt.Errorf("failed to mark presentation round submitted: %w", err)
	}

	if verdict == "approved" {
		if _, err := tx.Exec(`
			UPDATE presentations SET status = 'approved' WHERE id = ?
		`, presentationID); err != nil {
			return fmt.Errorf("failed to approve presentation: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit presentation round submission: %w", err)
	}

	return nil
}

func (s *Store) ClosePresentation(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRow(`SELECT status FROM presentations WHERE id = ?`, id).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("presentation %s not found", id)
		}
		return fmt.Errorf("failed to look up presentation: %w", err)
	}
	if status != "open" {
		return fmt.Errorf("presentation %s is not open (status: %s)", id, status)
	}

	if _, err := tx.Exec(`UPDATE presentations SET status = 'closed' WHERE id = ?`, id); err != nil {
		return fmt.Errorf("failed to close presentation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit presentation close: %w", err)
	}

	return nil
}

func (s *Store) ListPresentationComments(roundID string) ([]*PresentationComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, round_id, filepath, line_start, line_end, side, content, author, created_at
		FROM presentation_comments
		WHERE round_id = ?
		ORDER BY filepath, line_start, created_at
	`, roundID)
	if err != nil {
		return nil, fmt.Errorf("failed to list presentation comments: %w", err)
	}
	defer rows.Close()

	var result []*PresentationComment
	for rows.Next() {
		var c PresentationComment
		if err := rows.Scan(&c.ID, &c.RoundID, &c.Filepath, &c.LineStart, &c.LineEnd, &c.Side, &c.Content, &c.Author, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan presentation comment: %w", err)
		}
		result = append(result, &c)
	}
	return result, rows.Err()
}
