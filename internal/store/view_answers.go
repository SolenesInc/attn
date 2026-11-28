package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

// ViewAnswers is what the user answered the view at view, a file on the
// machine whose id is Host, and when they last sent it.
func (s *Store) ViewAnswers(view HostPath) (protocol.ViewAnswers, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	answers := protocol.ViewAnswers{Drafts: []protocol.ViewAnswer{}}
	rows, err := s.db.Query("SELECT answer_json FROM view_drafts WHERE host = ? AND path = ? ORDER BY row, part", view.Host, view.Path)
	if err != nil {
		return answers, fmt.Errorf("read the view's answers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return answers, err
		}
		var answer protocol.ViewAnswer
		if err := json.Unmarshal([]byte(encoded), &answer); err != nil {
			return answers, fmt.Errorf("a stored answer of %s is not readable: %w", view.Path, err)
		}
		answers.Drafts = append(answers.Drafts, answer)
	}
	if err := rows.Err(); err != nil {
		return answers, err
	}
	var sent protocol.ViewSent
	err = s.db.QueryRow("SELECT sent_at, responses, told FROM view_sends WHERE host = ? AND path = ?", view.Host, view.Path).Scan(&sent.At, &sent.Responses, &sent.Told)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return answers, fmt.Errorf("read when the view was sent: %w", err)
	default:
		answers.Sent = &sent
	}
	return answers, nil
}

// SaveViewAnswer keeps answer as the user's answer to its question, or as
// their step on its row, in place of the one before. An answer that holds no
// value takes the earlier one back.
func (s *Store) SaveViewAnswer(view HostPath, answer protocol.ViewAnswer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, part := protocol.Deref(answer.Row), protocol.Deref(answer.Part)
	if answer.Picked == nil && answer.Text == nil && answer.Attachments == nil && answer.Step == nil {
		_, err := s.db.Exec("DELETE FROM view_drafts WHERE host = ? AND path = ? AND row = ? AND part = ?", view.Host, view.Path, row, part)
		return err
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO view_drafts (host, path, row, part, answer_json) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (host, path, row, part) DO UPDATE SET answer_json = excluded.answer_json`,
		view.Host, view.Path, row, part, string(encoded))
	return err
}

// CommitViewFiles marks the files a send gives the agent as sent, so a later
// answer that replaces them no longer removes them.
func (s *Store) CommitViewFiles(owner protocol.AttachmentOwner, files []protocol.Attachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range files {
		if _, err := s.db.Exec("UPDATE attachments SET state = 'committed' WHERE attachment_id = ? AND owner_kind = ? AND owner_id = ? AND state = 'ready'", file.ID, owner.Kind, owner.ID); err != nil {
			return err
		}
	}
	return nil
}

// RecordViewSent keeps when the user last sent the view's answers.
func (s *Store) RecordViewSent(view HostPath, sent protocol.ViewSent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO view_sends (host, path, sent_at, responses, told) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (host, path) DO UPDATE SET sent_at = excluded.sent_at, responses = excluded.responses, told = excluded.told`,
		view.Host, view.Path, sent.At, sent.Responses, sent.Told)
	return err
}
