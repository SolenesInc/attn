package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// See docs/plans/2026-06-26-work-tracker.md.

type TicketStatus string

const (
	TicketStatusTodo     TicketStatus = "todo"
	TicketStatusWorking  TicketStatus = "working"
	TicketStatusBlocked  TicketStatus = "blocked"
	TicketStatusInReview TicketStatus = "in_review"
	TicketStatusDone     TicketStatus = "done"
	TicketStatusFailed   TicketStatus = "failed"
	TicketStatusCrashed  TicketStatus = "crashed"
)

const TicketAuthorAttn = "attn"

const TicketAuthorYou = "you"

func (st TicketStatus) IsValid() bool {
	switch st {
	case TicketStatusTodo, TicketStatusWorking, TicketStatusBlocked,
		TicketStatusInReview, TicketStatusDone, TicketStatusFailed, TicketStatusCrashed:
		return true
	}
	return false
}

func (st TicketStatus) IsTerminal() bool {
	switch st {
	case TicketStatusDone, TicketStatusFailed, TicketStatusCrashed:
		return true
	}
	return false
}

type TicketActivityKind string

const (
	TicketActivityStatusChange TicketActivityKind = "status_change"
	TicketActivityComment      TicketActivityKind = "comment"
	TicketActivityAttach       TicketActivityKind = "attach"
)

type Ticket struct {
	ID              string
	Title           string
	Description     string
	Status          TicketStatus
	Assignee        string
	Cwd             string
	LastAgentID     string
	ProjectID       string
	AutomationRunID string
	ResumeSessionID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ClosedAt        *time.Time
	ArchivedAt      *time.Time
	ReconciledAt    *time.Time
	LatestEventSeq  int64

	Activity    []TicketActivity
	Attachments []TicketAttachment
}

type TicketActivity struct {
	ID         int64
	TicketID   string
	Kind       TicketActivityKind
	Author     string
	FromStatus TicketStatus
	ToStatus   TicketStatus
	Comment    string
	CreatedAt  time.Time
}

type TicketAttachment struct {
	ID        int64
	TicketID  string
	Filename  string
	Path      string
	Note      string
	CreatedAt time.Time
}

type TicketAttachResult struct {
	EventSeq     int64
	Status       TicketStatus
	Deduplicated bool
}

type TicketListFilter struct {
	Status          TicketStatus
	IncludeArchived bool
}

var (
	ErrTicketIDTaken                 = errors.New("ticket id already in use")
	ErrTicketNotFound                = errors.New("ticket not found")
	ErrInvalidTicketID               = errors.New("invalid ticket id")
	ErrInvalidTicketStatus           = errors.New("invalid ticket status")
	ErrTicketTitleRequired           = errors.New("ticket title required")
	ErrTicketNotClosed               = errors.New("ticket is not closed")
	ErrTicketAdoptionConfirmRequired = errors.New("ticket has a non-orphan assignee")
)

var ticketIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type ticketScanner interface {
	Scan(dest ...any) error
}

func ValidateTicketID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is empty", ErrInvalidTicketID)
	}
	if !ticketIDPattern.MatchString(id) {
		return fmt.Errorf("%w: %q must be lowercase letters, digits, and hyphens (e.g. store-migration)", ErrInvalidTicketID, id)
	}
	return nil
}

func (s *Store) CreateTicket(t Ticket, author string, now time.Time) (*Ticket, error) {
	return s.createTicket(t, author, "", nil, now)
}

func (s *Store) CreateRoleOwnedTicket(t Ticket, author, ownerRole string, now time.Time) (*Ticket, error) {
	return s.createTicket(t, author, strings.TrimSpace(ownerRole), nil, now)
}

func (s *Store) CreateTicketWithSubscribers(t Ticket, author, ownerRole string, subscribers []string, now time.Time) (*Ticket, error) {
	return s.createTicket(t, author, strings.TrimSpace(ownerRole), subscribers, now)
}

func (s *Store) EnsureAutomationTicket(t Ticket, author, ownerRole string, now time.Time) (*Ticket, error) {
	if t.AutomationRunID == "" {
		return nil, errors.New("automation run id required")
	}
	if existing, err := s.GetTicketByAutomationRunID(t.AutomationRunID); err != nil || existing != nil {
		return existing, err
	}
	return s.createTicket(t, author, strings.TrimSpace(ownerRole), nil, now)
}

func (s *Store) createTicket(t Ticket, author, ownerRole string, subscribers []string, now time.Time) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, nil
	}

	t.ID = strings.TrimSpace(t.ID)
	if err := ValidateTicketID(t.ID); err != nil {
		return nil, err
	}
	t.Title = strings.TrimSpace(t.Title)
	if t.Title == "" {
		return nil, ErrTicketTitleRequired
	}
	if t.Status == "" {
		t.Status = TicketStatusTodo
	}
	if !t.Status.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTicketStatus, t.Status)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var exists int
	switch err := tx.QueryRow(`SELECT 1 FROM tickets WHERE id = ?`, t.ID).Scan(&exists); err {
	case nil:
		return nil, fmt.Errorf("%w: %q is already taken — pick a new name, or append a number (e.g. %q)", ErrTicketIDTaken, t.ID, t.ID+"-2")
	case sql.ErrNoRows:
	default:
		return nil, err
	}

	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Status.IsTerminal() {
		closed := now
		t.ClosedAt = &closed
	} else {
		t.ClosedAt = nil
	}
	t.ArchivedAt = nil
	t.ReconciledAt = nil

	if _, err := tx.Exec(`
		INSERT INTO tickets (
			id, title, description, status, assignee, cwd, last_agent_id,
			project_id, automation_run_id, created_at, updated_at, closed_at, archived_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		t.ID, t.Title, t.Description, string(t.Status), t.Assignee, t.Cwd, t.LastAgentID,
		t.ProjectID, nullIfEmpty(t.AutomationRunID), formatTicketTime(now), formatTicketTime(now),
		formatTicketTimePtr(t.ClosedAt), formatTicketTimePtr(t.ArchivedAt),
	); err != nil {
		return nil, err
	}
	createdSeq, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID:   t.ID,
		Kind:       TicketEventCreated,
		Author:     author,
		AuthorRole: ownerRole,
		ToStatus:   t.Status,
	}, now)
	if err != nil {
		return nil, err
	}
	if ownerRole != "" {
		if _, err := tx.Exec(`
			INSERT INTO ticket_role_owners (role, ticket_id, created_at)
			VALUES (?, ?, ?)
		`, ownerRole, t.ID, formatTicketTime(now)); err != nil {
			return nil, err
		}
	}
	// Assign-at-birth is delegation only, and its brief already went out in the spawn
	// prompt; a pre-assigned ticket needing its brief from the inbox would lose it.
	if t.Assignee != "" {
		if err := setTicketCursorTx(tx, t.Assignee, t.ID, createdSeq, now); err != nil {
			return nil, err
		}
	}
	for _, identity := range subscribers {
		identity = strings.TrimSpace(identity)
		if identity == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO ticket_subscriptions (identity, ticket_id, created_at)
			VALUES (?, ?, ?)
			ON CONFLICT(identity, ticket_id) DO NOTHING
		`, identity, t.ID, formatTicketTime(now)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) GetTicket(id string) (*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	ticket, err := scanTicket(s.db.QueryRow(ticketSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if ticket.Activity, err = s.ticketActivity(id); err != nil {
		return nil, err
	}
	if ticket.Attachments, err = s.ticketAttachments(id); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM ticket_events WHERE ticket_id = ?`, id).Scan(&ticket.LatestEventSeq); err != nil {
		return nil, err
	}
	return ticket, nil
}

func (s *Store) ListTickets(filter TicketListFilter) ([]*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	where := []string{}
	args := []any{}
	if !filter.IncludeArchived {
		where = append(where, `archived_at = ''`)
	}
	if filter.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, string(filter.Status))
	}
	query := ticketSelect
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY created_at DESC, id DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickets []*Ticket
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, ticket)
	}
	return tickets, rows.Err()
}

func (s *Store) ActiveTicketForSession(sessionID string) (*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil || sessionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ticketSelect+` WHERE assignee = ? ORDER BY created_at DESC, id DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		if !ticket.Status.IsTerminal() {
			return ticket, nil
		}
	}
	return nil, rows.Err()
}

func (s *Store) ActiveTicketsForSession(sessionID string) ([]*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil || sessionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ticketSelect+` WHERE assignee = ? ORDER BY created_at DESC, id DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickets []*Ticket
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		if !ticket.Status.IsTerminal() {
			tickets = append(tickets, ticket)
		}
	}
	return tickets, rows.Err()
}

func (s *Store) CrashedTicketsForAssignee(assignee string) ([]*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		ticketSelect+` WHERE assignee = ? AND status = ? AND archived_at = '' ORDER BY created_at DESC, id DESC`,
		assignee, string(TicketStatusCrashed),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickets []*Ticket
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, ticket)
	}
	return tickets, rows.Err()
}

func (s *Store) ClaimTicketReconciliation(id string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	res, err := s.db.Exec(
		`UPDATE tickets SET reconciled_at = ? WHERE id = ? AND reconciled_at = ''`,
		formatTicketTime(now), id,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *Store) ClearTicketReconciliationForAssignee(assignee string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE tickets SET reconciled_at = '' WHERE assignee = ? AND reconciled_at != ''`,
		assignee,
	)
	return err
}

func (s *Store) SetTicketStatus(id string, to TicketStatus, author, comment string, now time.Time) (*Ticket, error) {
	updated, _, err := s.SetTicketStatusWithOptions(id, to, author, comment, TicketMutationOptions{}, now)
	return updated, err
}

func (s *Store) SetTicketStatusWithOptions(
	id string,
	to TicketStatus,
	author, comment string,
	options TicketMutationOptions,
	now time.Time,
) (*Ticket, TicketMutationOutcome, error) {
	if !to.IsValid() {
		return nil, TicketMutationOutcome{}, fmt.Errorf("%w: %q", ErrInvalidTicketStatus, to)
	}
	var updated *Ticket
	outcome, err := s.withTicketMutation(id, options, now, func(tx *sql.Tx) error {
		var mutationErr error
		updated, mutationErr = setTicketStatusTx(tx, id, to, author, "", comment, now)
		return mutationErr
	})
	return updated, outcome, err
}

func setTicketStatusTx(tx *sql.Tx, id string, to TicketStatus, author, authorRole, comment string, now time.Time) (*Ticket, error) {
	current, err := scanTicket(tx.QueryRow(ticketSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %q", ErrTicketNotFound, id)
	}
	if err != nil {
		return nil, err
	}

	from := current.Status
	closedAt := current.ClosedAt
	archivedAt := current.ArchivedAt
	switch {
	case to.IsTerminal() && !from.IsTerminal():
		closed := now
		closedAt = &closed
	case !to.IsTerminal():
		closedAt = nil
		archivedAt = nil
	}

	if _, err := tx.Exec(`
		UPDATE tickets SET status = ?, updated_at = ?, closed_at = ?, archived_at = ? WHERE id = ?
	`, string(to), formatTicketTime(now), formatTicketTimePtr(closedAt), formatTicketTimePtr(archivedAt), id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO ticket_activity (ticket_id, kind, author, from_status, to_status, comment, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, string(TicketActivityStatusChange), author, string(from), string(to), comment, formatTicketTime(now)); err != nil {
		return nil, err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID:   id,
		Kind:       TicketEventStatusChanged,
		Author:     author,
		AuthorRole: authorRole,
		FromStatus: from,
		ToStatus:   to,
		Comment:    comment,
	}, now); err != nil {
		return nil, err
	}

	current.Status = to
	current.ClosedAt = closedAt
	current.ArchivedAt = archivedAt
	current.UpdatedAt = now
	return current, nil
}

func (s *Store) AddTicketComment(id, author, comment string, now time.Time) (*TicketActivity, error) {
	activity, _, err := s.AddTicketCommentWithOptions(id, author, comment, TicketMutationOptions{}, now)
	return activity, err
}

func addTicketCommentTx(tx *sql.Tx, id, author, comment string, now time.Time) (*TicketActivity, error) {
	if err := touchTicketTx(tx, id, now); err != nil {
		return nil, err
	}
	res, err := tx.Exec(`
		INSERT INTO ticket_activity (ticket_id, kind, author, comment, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, string(TicketActivityComment), author, comment, formatTicketTime(now))
	if err != nil {
		return nil, err
	}
	activityID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: id,
		Kind:     TicketEventCommented,
		Author:   author,
		Comment:  comment,
	}, now); err != nil {
		return nil, err
	}
	return &TicketActivity{
		ID:        activityID,
		TicketID:  id,
		Kind:      TicketActivityComment,
		Author:    author,
		Comment:   comment,
		CreatedAt: now,
	}, nil
}

func (s *Store) AddTicketCommentWithOptions(
	id, author, comment string,
	options TicketMutationOptions,
	now time.Time,
) (*TicketActivity, TicketMutationOutcome, error) {
	var activity *TicketActivity
	outcome, err := s.withTicketMutation(id, options, now, func(tx *sql.Tx) error {
		var mutationErr error
		activity, mutationErr = addTicketCommentTx(tx, id, author, comment, now)
		return mutationErr
	})
	return activity, outcome, err
}

// The new brief goes in Detail: without it a second consecutive edit looks
// identical to the first and is silently deduped away.
func (s *Store) EditTicketDescription(id, description, author string, now time.Time) error {
	_, err := s.EditTicketDescriptionWithOptions(id, description, author, TicketMutationOptions{}, now)
	return err
}

func (s *Store) EditTicketDescriptionWithOptions(
	id, description, author string,
	options TicketMutationOptions,
	now time.Time,
) (TicketMutationOutcome, error) {
	evt := TicketEvent{
		TicketID: id,
		Kind:     TicketEventDescriptionEdited,
		Author:   author,
		Detail:   description,
	}
	return s.withTicketMutation(id, options, now, func(tx *sql.Tx) error {
		return updateTicketFieldWithEventTx(tx, id, "description", description, evt, now)
	})
}

func (s *Store) AssignTicket(id, assignee, author string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE tickets SET assignee = ?, reconciled_at = '', updated_at = ? WHERE id = ?`,
		assignee, formatTicketTime(now), id,
	)
	if err != nil {
		return err
	}
	if err := ticketUpdateResult(res, id); err != nil {
		return err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: id,
		Kind:     TicketEventAssigned,
		Author:   author,
		Detail:   assignee,
	}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func ValidateTicketDelegationAdoption(ticket *Ticket, sessionID string, confirm bool) error {
	if ticket == nil {
		return ErrTicketNotFound
	}
	if strings.TrimSpace(ticket.Description) == "" {
		return errors.New("ticket description is empty; add a description before delegating it")
	}
	if ticket.Assignee != "" && ticket.Assignee != sessionID && ticket.ReconciledAt == nil && !confirm {
		return fmt.Errorf("%w: %s; pass --confirm to take it over", ErrTicketAdoptionConfirmRequired, ticket.Assignee)
	}
	return nil
}

func (s *Store) AdoptTicketForDelegation(id, sessionID, cwd, lastAgentID, author, ownerRole string, subscribers []string, confirm bool, now time.Time) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	current, err := scanTicket(tx.QueryRow(ticketSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %q", ErrTicketNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	if err := ValidateTicketDelegationAdoption(current, sessionID, confirm); err != nil {
		return nil, err
	}

	previousAssignee := current.Assignee
	if _, err := tx.Exec(`
		UPDATE tickets SET assignee = ?, cwd = ?, last_agent_id = ?, reconciled_at = '', updated_at = ? WHERE id = ?
	`, sessionID, cwd, lastAgentID, formatTicketTime(now), id); err != nil {
		return nil, err
	}
	if previousAssignee != sessionID {
		if _, _, err := appendTicketEventTx(tx, TicketEvent{
			TicketID: id, Kind: TicketEventAssigned, Author: author, AuthorRole: ownerRole, Detail: sessionID,
		}, now); err != nil {
			return nil, err
		}
	}
	if current.Status != TicketStatusWorking {
		if _, err := setTicketStatusTx(tx, id, TicketStatusWorking, author, ownerRole, "", now); err != nil {
			return nil, err
		}
	}

	if ownerRole != "" {
		if _, err := tx.Exec(`
			INSERT INTO ticket_role_owners (role, ticket_id, created_at)
			VALUES (?, ?, ?) ON CONFLICT(role, ticket_id) DO NOTHING
		`, ownerRole, id, formatTicketTime(now)); err != nil {
			return nil, err
		}
	}
	if previousAssignee != "" && previousAssignee != sessionID {
		subscribers = append(subscribers, previousAssignee)
	}
	for _, identity := range subscribers {
		identity = strings.TrimSpace(identity)
		if identity == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO ticket_subscriptions (identity, ticket_id, created_at)
			VALUES (?, ?, ?) ON CONFLICT(identity, ticket_id) DO NOTHING
		`, identity, id, formatTicketTime(now)); err != nil {
			return nil, err
		}
	}
	var latestSeq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM ticket_events WHERE ticket_id = ?`, id).Scan(&latestSeq); err != nil {
		return nil, err
	}
	if err := setTicketCursorTx(tx, sessionID, id, latestSeq, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	current.Assignee = sessionID
	current.Cwd = cwd
	current.LastAgentID = lastAgentID
	current.Status = TicketStatusWorking
	current.ClosedAt = nil
	current.ArchivedAt = nil
	current.ReconciledAt = nil
	current.UpdatedAt = now
	current.LatestEventSeq = latestSeq
	return current, nil
}

func (s *Store) SetTicketSession(id, cwd, lastAgentID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	res, err := s.db.Exec(`
		UPDATE tickets SET cwd = ?, last_agent_id = ?, updated_at = ? WHERE id = ?
	`, cwd, lastAgentID, formatTicketTime(now), id)
	if err != nil {
		return err
	}
	return ticketUpdateResult(res, id)
}

func (s *Store) SetTicketResumeSessionID(assignee, resumeSessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE tickets SET resume_session_id = ? WHERE assignee = ?`,
		strings.TrimSpace(resumeSessionID), assignee,
	)
	return err
}

func (s *Store) GetTicketResumeSessionID(assignee string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return ""
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return ""
	}
	var resumeSessionID string
	err := s.db.QueryRow(
		`SELECT resume_session_id FROM tickets
		   WHERE assignee = ? AND resume_session_id != ''
		   ORDER BY updated_at DESC, id DESC LIMIT 1`,
		assignee,
	).Scan(&resumeSessionID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(resumeSessionID)
}

func (s *Store) AddTicketAttachment(att TicketAttachment, author string, now time.Time) (*TicketAttachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, nil
	}
	att.Filename = strings.TrimSpace(att.Filename)
	if att.Filename == "" {
		return nil, errors.New("attachment filename required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := touchTicketTx(tx, att.TicketID, now); err != nil {
		return nil, err
	}
	res, err := tx.Exec(`
		INSERT INTO ticket_attachments (ticket_id, filename, path, note, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, att.TicketID, att.Filename, att.Path, att.Note, formatTicketTime(now))
	if err != nil {
		return nil, err
	}
	attachmentID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: att.TicketID,
		Kind:     TicketEventAttachmentAdded,
		Author:   author,
		Detail:   att.Filename,
	}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	att.ID = attachmentID
	att.CreatedAt = now
	return &att, nil
}

func (s *Store) SubmitTicketAttach(
	ticketID, author, fingerprint, detail, activityComment string,
	status *TicketStatus,
	now time.Time,
) (*TicketAttachResult, error) {
	result, _, err := s.SubmitTicketAttachWithOptions(
		ticketID, author, fingerprint, detail, activityComment, status,
		TicketMutationOptions{}, now,
	)
	return result, err
}

func (s *Store) SubmitTicketAttachWithOptions(
	ticketID, author, fingerprint, detail, activityComment string,
	status *TicketStatus,
	options TicketMutationOptions,
	now time.Time,
) (*TicketAttachResult, TicketMutationOutcome, error) {
	if strings.TrimSpace(fingerprint) == "" {
		return nil, TicketMutationOutcome{}, errors.New("attach fingerprint required")
	}
	if status != nil && !status.IsValid() {
		return nil, TicketMutationOutcome{}, fmt.Errorf("%w: %q", ErrInvalidTicketStatus, *status)
	}
	var result *TicketAttachResult
	outcome, err := s.withTicketMutation(ticketID, options, now, func(tx *sql.Tx) error {
		var mutationErr error
		result, mutationErr = submitTicketAttachTx(tx, ticketID, author, fingerprint, detail, activityComment, status, now)
		return mutationErr
	})
	return result, outcome, err
}

func submitTicketAttachTx(
	tx *sql.Tx,
	ticketID, author, fingerprint, detail, activityComment string,
	status *TicketStatus,
	now time.Time,
) (*TicketAttachResult, error) {
	var existingSeq int64
	var existingStatus string
	err := tx.QueryRow(`
		SELECT seq, to_status FROM ticket_events
		WHERE ticket_id = ? AND kind = ? AND detail LIKE ?
		ORDER BY seq DESC LIMIT 1
	`, ticketID, string(TicketEventAttachSubmitted), fingerprint+"\n%").Scan(&existingSeq, &existingStatus)
	if err == nil {
		return &TicketAttachResult{EventSeq: existingSeq, Status: TicketStatus(existingStatus), Deduplicated: true}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	current, err := scanTicket(tx.QueryRow(ticketSelect+` WHERE id = ?`, ticketID))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %q", ErrTicketNotFound, ticketID)
	}
	if err != nil {
		return nil, err
	}
	if err := touchTicketTx(tx, ticketID, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO ticket_activity (ticket_id, kind, author, comment, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, ticketID, string(TicketActivityAttach), author, activityComment, formatTicketTime(now)); err != nil {
		return nil, err
	}
	resultStatus := current.Status
	if status != nil {
		resultStatus = *status
	}
	eventSeq, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: ticketID,
		Kind:     TicketEventAttachSubmitted,
		Author:   author,
		Comment:  activityComment,
		Detail:   detail,
		ToStatus: resultStatus,
	}, now)
	if err != nil {
		return nil, err
	}
	if status != nil {
		updated, updateErr := setTicketStatusTx(tx, ticketID, *status, author, "", "", now)
		if updateErr != nil {
			return nil, updateErr
		}
		resultStatus = updated.Status
	}
	return &TicketAttachResult{EventSeq: eventSeq, Status: resultStatus}, nil
}

func (s *Store) ArchiveTicket(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	current, err := scanTicket(tx.QueryRow(ticketSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %q", ErrTicketNotFound, id)
	}
	if err != nil {
		return err
	}
	if !current.Status.IsTerminal() {
		return fmt.Errorf("%w: %q is %s", ErrTicketNotClosed, id, current.Status)
	}

	if _, err := tx.Exec(`
		UPDATE tickets SET archived_at = ?, updated_at = ? WHERE id = ?
	`, formatTicketTime(now), formatTicketTime(now), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SweepExpiredAutomationTickets(now time.Time, ttl time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}

	cutoff := formatTicketTime(now.Add(-ttl))
	// closed_at is a fixed-width RFC3339 UTC string, so a lexical compare is a
	// chronological compare.
	const expired = `status IN ('done','failed','crashed') AND closed_at != '' AND closed_at < ? AND (
		(automation_run_id IS NOT NULL AND automation_run_id != '') OR
		EXISTS (SELECT 1 FROM automation_runs WHERE automation_runs.ticket_id = tickets.id) OR
		EXISTS (SELECT 1 FROM automation_ticket_occurrence_events WHERE automation_ticket_occurrence_events.ticket_id = tickets.id) OR
		EXISTS (SELECT 1 FROM automation_continuity_bindings WHERE automation_continuity_bindings.ticket_id = tickets.id)
	)`

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DROP TABLE IF EXISTS temp.expired_automation_tickets`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`CREATE TEMP TABLE expired_automation_tickets(id TEXT PRIMARY KEY) WITHOUT ROWID`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO expired_automation_tickets(id) SELECT id FROM tickets WHERE `+expired, cutoff); err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM expired_automation_tickets`).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		if _, err := tx.Exec(`DROP TABLE expired_automation_tickets`); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}

	if _, err := tx.Exec(`DELETE FROM ticket_activity WHERE ticket_id IN (SELECT id FROM expired_automation_tickets)`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM ticket_attachments WHERE ticket_id IN (SELECT id FROM expired_automation_tickets)`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM ticket_events WHERE ticket_id IN (SELECT id FROM expired_automation_tickets)`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM ticket_event_cursors WHERE ticket_id IN (SELECT id FROM expired_automation_tickets)`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM ticket_subscriptions WHERE ticket_id IN (SELECT id FROM expired_automation_tickets)`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM automation_ticket_occurrence_events WHERE ticket_id IN (SELECT id FROM expired_automation_tickets)`); err != nil {
		return 0, err
	}
	// Bindings are append-only, so this releases and never deletes;
	// already-released rows keep their own reason.
	if _, err := tx.Exec(
		`UPDATE automation_continuity_bindings SET status=?,released_reason=?,released_at=?,updated_at=? WHERE status=? AND ticket_id IN (SELECT id FROM expired_automation_tickets)`,
		AutomationBindingStatusReleased, AutomationBindingReleasedTicketSwept, formatTicketTime(now), formatTicketTime(now), AutomationBindingStatusActive,
	); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM tickets WHERE id IN (SELECT id FROM expired_automation_tickets)`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if int(n) != count {
		return 0, fmt.Errorf("expired automation ticket count changed: selected %d, deleted %d", count, n)
	}
	if _, err := tx.Exec(`DROP TABLE expired_automation_tickets`); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

const ticketSelect = `
	SELECT id, title, description, status, assignee, cwd, last_agent_id,
		project_id, automation_run_id, resume_session_id, created_at, updated_at, closed_at, archived_at, reconciled_at
	FROM tickets`

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// column is a trusted internal literal, never caller input.
func updateTicketFieldWithEventTx(tx *sql.Tx, id, column, value string, evt TicketEvent, now time.Time) error {
	res, err := tx.Exec(
		`UPDATE tickets SET `+column+` = ?, updated_at = ? WHERE id = ?`,
		value, formatTicketTime(now), id,
	)
	if err != nil {
		return err
	}
	if err := ticketUpdateResult(res, id); err != nil {
		return err
	}
	if _, _, err := appendTicketEventTx(tx, evt, now); err != nil {
		return err
	}
	return nil
}

func touchTicketTx(tx *sql.Tx, id string, now time.Time) error {
	res, err := tx.Exec(`UPDATE tickets SET updated_at = ? WHERE id = ?`, formatTicketTime(now), id)
	if err != nil {
		return err
	}
	return ticketUpdateResult(res, id)
}

func ticketUpdateResult(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %q", ErrTicketNotFound, id)
	}
	return nil
}

func (s *Store) ticketActivity(ticketID string) ([]TicketActivity, error) {
	rows, err := s.db.Query(`
		SELECT id, ticket_id, kind, author, from_status, to_status, comment, created_at
		FROM ticket_activity WHERE ticket_id = ? ORDER BY id ASC
	`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activity []TicketActivity
	for rows.Next() {
		var (
			a         TicketActivity
			kind      string
			from, to  string
			createdAt string
		)
		if err := rows.Scan(&a.ID, &a.TicketID, &kind, &a.Author, &from, &to, &a.Comment, &createdAt); err != nil {
			return nil, err
		}
		a.Kind = TicketActivityKind(kind)
		a.FromStatus = TicketStatus(from)
		a.ToStatus = TicketStatus(to)
		a.CreatedAt = parseTicketTime(createdAt)
		activity = append(activity, a)
	}
	return activity, rows.Err()
}

func (s *Store) ticketAttachments(ticketID string) ([]TicketAttachment, error) {
	rows, err := s.db.Query(`
		SELECT id, ticket_id, filename, path, note, created_at
		FROM ticket_attachments WHERE ticket_id = ? ORDER BY id ASC
	`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attachments []TicketAttachment
	for rows.Next() {
		var (
			att       TicketAttachment
			createdAt string
		)
		if err := rows.Scan(&att.ID, &att.TicketID, &att.Filename, &att.Path, &att.Note, &createdAt); err != nil {
			return nil, err
		}
		att.CreatedAt = parseTicketTime(createdAt)
		attachments = append(attachments, att)
	}
	return attachments, rows.Err()
}

func scanTicket(scanner ticketScanner) (*Ticket, error) {
	var (
		t               Ticket
		status          string
		createdAt       string
		updatedAt       string
		closedAt        string
		archivedAt      string
		reconciledAt    string
		automationRunID sql.NullString
	)
	if err := scanner.Scan(
		&t.ID, &t.Title, &t.Description, &status, &t.Assignee, &t.Cwd, &t.LastAgentID,
		&t.ProjectID, &automationRunID, &t.ResumeSessionID, &createdAt, &updatedAt, &closedAt, &archivedAt, &reconciledAt,
	); err != nil {
		return nil, err
	}
	t.Status = TicketStatus(status)
	t.AutomationRunID = automationRunID.String
	t.CreatedAt = parseTicketTime(createdAt)
	t.UpdatedAt = parseTicketTime(updatedAt)
	if closedAt != "" {
		ts := parseTicketTime(closedAt)
		t.ClosedAt = &ts
	}
	if archivedAt != "" {
		ts := parseTicketTime(archivedAt)
		t.ArchivedAt = &ts
	}
	if reconciledAt != "" {
		ts := parseTicketTime(reconciledAt)
		t.ReconciledAt = &ts
	}
	return &t, nil
}

func (s *Store) GetTicketByAutomationRunID(runID string) (*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	t, err := scanTicket(s.db.QueryRow(ticketSelect+` WHERE automation_run_id = ?`, runID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// formatTicketTime is fixed-width RFC3339 UTC, so stored timestamps sort lexically
// — which the TTL sweep relies on.
func formatTicketTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func formatTicketTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTicketTime(*t)
}

func parseTicketTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *Store) StrandedTickets() ([]*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ticketSelect+`
		WHERE archived_at = '' AND automation_run_id IS NULL AND status IN (?, ?)
		ORDER BY created_at DESC, id DESC`,
		string(TicketStatusCrashed), string(TicketStatusFailed),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickets []*Ticket
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, ticket)
	}
	return tickets, rows.Err()
}
