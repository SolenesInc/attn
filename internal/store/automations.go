package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/docstore"
)

func parseOptionalAutomationTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed := parseTicketTime(value)
	return &parsed
}

const (
	AutomationRunStatePending   = "pending"
	AutomationRunStateDelivered = "delivered"
	AutomationRunStateFailed    = "failed"
	AutomationRunStateCancelled = "cancelled"
)

const (
	AutomationCancelReasonReviewWithdrawn    = "review_withdrawn"
	AutomationCancelReasonDefinitionDisabled = "definition_disabled"
	AutomationCancelReasonDefinitionDeleted  = "definition_deleted"
)

const (
	AutomationBindingStatusActive   = "active"
	AutomationBindingStatusReleased = "released"
)
const (
	AutomationBindingReleasedContractRotated   = "contract_rotated"
	AutomationBindingReleasedTicketSwept       = "ticket_swept"
	AutomationBindingReleasedDefinitionDeleted = "definition_deleted"
)

type AutomationDefinition struct {
	ID, Name, SpecJSON   string
	Enabled              bool
	Revision             int
	CreatedAt, UpdatedAt time.Time
	DeletedAt            *time.Time
}

type AutomationRun struct {
	ID, DefinitionID, OccurrenceID           string
	DefinitionRevision                       int
	SnapshotJSON, State, CancelReason        string
	Attempts                                 int
	LastError                                string
	TicketID, SessionID, WorkspaceID, PaneID string
	ResolvedLocationJSON                     string
	CreatedAt, UpdatedAt                     time.Time
	DeliveredAt                              *time.Time
}

type AutomationContinuityBinding struct {
	ID, DefinitionID, ContinuityKey          string
	TicketID, SessionID, WorkspaceID, PaneID string
	Status, ReleasedReason                   string
	ReleasedAt                               *time.Time
	CreatedAt, UpdatedAt                     time.Time
}

type AutomationOccurrence struct {
	ID, DefinitionID, Provider, OccurrenceKey, SubjectKey, PayloadJSON string
	ObservedAt, CreatedAt                                              time.Time
}

type AutomationProvenanceRecord struct {
	RunID, DefinitionID, DefinitionName, DefinitionSpecJSON string
	SessionID, TicketID                                     string
	Provider, SubjectKey, PayloadJSON                       string
	CreatedAt                                               time.Time
}

type AutomationRunReservation struct {
	RunID, OccurrenceID, TicketID, SessionID, WorkspaceID, PaneID string
}

type AutomationReviewRequestCandidate struct {
	SubjectKey string
	HeadSHA    string
	Cycle      int
}

type AutomationReviewRequestObservation struct {
	SubjectKey string
	HeadSHA    string
}

func (s *Store) AutomationReviewRequestNeedsClaim(definitionID, subjectKey string, cycle int) (bool, error) {
	return s.AutomationReviewRequestHeadNeedsClaim(definitionID, subjectKey, cycle, "")
}

func (s *Store) AutomationReviewRequestHeadNeedsClaim(definitionID, subjectKey string, cycle int, headSHA string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false, errors.New("automation persistence unavailable")
	}
	var active, currentCycle, baselineCycle int
	err := s.db.QueryRow(`SELECT active,cycle,baseline_cycle FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, definitionID, subjectKey).Scan(&active, &currentCycle, &baselineCycle)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if active != 1 || currentCycle != cycle || cycle <= baselineCycle {
		return false, nil
	}
	hasOccurrence, hasPending, matchesHead, err := githubReviewCycleStatus(s.db, definitionID, subjectKey, cycle, headSHA)
	return hasPending || !hasOccurrence || !matchesHead, err
}

type automationReviewQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func githubReviewOccurrenceBase(subjectKey string, cycle int) string {
	return fmt.Sprintf("review_requested:%s:%d", subjectKey, cycle)
}

func githubReviewOccurrenceKey(subjectKey string, cycle int, headSHA string) string {
	base := githubReviewOccurrenceBase(subjectKey, cycle)
	headSHA = strings.ToLower(strings.TrimSpace(headSHA))
	if headSHA == "" {
		return base
	}
	return base + ":" + headSHA
}

func githubReviewPayloadHead(payloadJSON string) string {
	var payload struct {
		HeadSHA string `json:"head_sha"`
	}
	if json.Unmarshal([]byte(payloadJSON), &payload) != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(payload.HeadSHA))
}

// githubReviewCycleStatus treats a legacy cycle-only occurrence with no readable head as
// covering the cycle: old data must not replay because the head cannot be recovered.
func githubReviewCycleStatus(q automationReviewQueryer, definitionID, subjectKey string, cycle int, headSHA string) (hasOccurrence, hasPending, matchesHead bool, err error) {
	base := githubReviewOccurrenceBase(subjectKey, cycle)
	prefix := base + ":"
	headSHA = strings.ToLower(strings.TrimSpace(headSHA))
	rows, err := q.Query(`
		SELECT o.occurrence_key,o.payload_json,r.state
		FROM automation_occurrences o
		JOIN automation_runs r ON r.occurrence_id=o.id
		WHERE o.definition_id=? AND o.provider='github' AND o.subject_key=?
		  AND (o.occurrence_key=? OR instr(o.occurrence_key,?)=1)
	`, definitionID, subjectKey, base, prefix)
	if err != nil {
		return false, false, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var occurrenceKey, payloadJSON, state string
		if err := rows.Scan(&occurrenceKey, &payloadJSON, &state); err != nil {
			return false, false, false, err
		}
		hasOccurrence = true
		hasPending = hasPending || state == AutomationRunStatePending
		if headSHA == "" || occurrenceKey == prefix+headSHA {
			matchesHead = true
			continue
		}
		if occurrenceKey == base {
			legacyHead := githubReviewPayloadHead(payloadJSON)
			matchesHead = matchesHead || legacyHead == "" || legacyHead == headSHA
		}
	}
	return hasOccurrence, hasPending, matchesHead, rows.Err()
}

func (s *Store) UpsertAutomationDefinition(id, name, specJSON string, now time.Time) (*AutomationDefinition, error) {
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var revision, oldEnabled int
	var oldSpec, deletedAt string
	// Deliberately not filtered by deleted_at='': a soft-deleted row must be found here too,
	// so applying the same id resurrects it instead of colliding with the PRIMARY KEY.
	err = tx.QueryRow(`SELECT revision, spec_json, enabled, deleted_at FROM automation_definitions WHERE id=?`, id).Scan(&revision, &oldSpec, &oldEnabled, &deletedAt)
	enabled := true
	activation := err == sql.ErrNoRows
	switch err {
	case sql.ErrNoRows:
		revision = 1
		_, err = tx.Exec(`INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at) VALUES(?,?,?,?,?,?,?,'')`, id, name, enabled, revision, specJSON, formatTicketTime(now), formatTicketTime(now))
	case nil:
		wasDeleted := deletedAt != ""
		if wasDeleted {
			revision++
		} else {
			enabled = oldEnabled != 0
			if oldSpec != specJSON {
				revision++
			}
		}
		_, err = tx.Exec(`UPDATE automation_definitions SET name=?, enabled=?, revision=?, spec_json=?, updated_at=?, deleted_at='' WHERE id=?`, name, enabled, revision, specJSON, formatTicketTime(now), id)
		activation = wasDeleted
	}
	if err != nil {
		return nil, err
	}
	if enabled && activation {
		if err := activateAutomationReviewRequestsTx(tx, id, now); err != nil {
			return nil, err
		}
	} else if enabled {
		if err := fenceAutomationProviderCursorsTx(tx, id, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.mu.Unlock()
	locked = false
	return s.GetAutomationDefinition(id)
}

func activateAutomationReviewRequestsTx(tx *sql.Tx, definitionID string, now time.Time) error {
	if _, err := tx.Exec(`UPDATE automation_review_request_edges SET active=0,updated_at=? WHERE definition_id=?`, formatTicketTime(now), definitionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM automation_provider_cursors WHERE definition_id=? AND provider='github_review_requested' AND scope<>'*'`, definitionID); err != nil {
		return err
	}
	return fenceAutomationProviderCursorsTx(tx, definitionID, now)
}

func fenceAutomationProviderCursorsTx(tx *sql.Tx, definitionID string, now time.Time) error {
	fence := now.UTC().Format(sortableTimeFormat)
	_, err := tx.Exec(`INSERT INTO automation_provider_cursors(definition_id,provider,scope,observed_at) VALUES(?,'github_review_requested','*',?) ON CONFLICT(definition_id,provider,scope) DO UPDATE SET observed_at=excluded.observed_at`, definitionID, fence)
	return err
}

func (s *Store) SetAutomationEnabled(id string, enabled bool, now time.Time) (*AutomationDefinition, bool, error) {
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	if s.db == nil {
		return nil, false, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var oldEnabled int
	err = tx.QueryRow(`SELECT enabled FROM automation_definitions WHERE id=? AND deleted_at=''`, id).Scan(&oldEnabled)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	wasEnabled := oldEnabled != 0
	if wasEnabled == enabled {
		tx.Rollback()
		s.mu.Unlock()
		locked = false
		def, getErr := s.GetAutomationDefinition(id)
		return def, false, getErr
	}
	if _, err := tx.Exec(`UPDATE automation_definitions SET enabled=?, updated_at=? WHERE id=?`, enabled, formatTicketTime(now), id); err != nil {
		return nil, false, err
	}
	if enabled {
		if err := activateAutomationReviewRequestsTx(tx, id, now); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	s.mu.Unlock()
	locked = false
	def, getErr := s.GetAutomationDefinition(id)
	return def, true, getErr
}

func scanAutomationDefinition(scanner interface{ Scan(...any) error }) (*AutomationDefinition, error) {
	var d AutomationDefinition
	var enabled int
	var created, updated, deleted string
	if err := scanner.Scan(&d.ID, &d.Name, &enabled, &d.Revision, &d.SpecJSON, &created, &updated, &deleted); err != nil {
		return nil, err
	}
	d.Enabled = enabled != 0
	d.CreatedAt = parseTicketTime(created)
	d.UpdatedAt = parseTicketTime(updated)
	d.DeletedAt = parseOptionalAutomationTime(deleted)
	return &d, nil
}

func (s *Store) GetAutomationDefinition(id string) (*AutomationDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	d, err := scanAutomationDefinition(s.db.QueryRow(`SELECT id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at FROM automation_definitions WHERE id=? AND deleted_at=''`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func (s *Store) GetAutomationDefinitionIncludingDeleted(id string) (*AutomationDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	d, err := scanAutomationDefinition(s.db.QueryRow(`SELECT id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at FROM automation_definitions WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

const automationContinuityBindingColumns = `id,definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,status,released_reason,released_at,created_at,updated_at`

func scanAutomationContinuityBinding(scanner interface{ Scan(...any) error }) (*AutomationContinuityBinding, error) {
	var b AutomationContinuityBinding
	var releasedAt, created, updated string
	if err := scanner.Scan(&b.ID, &b.DefinitionID, &b.ContinuityKey, &b.TicketID, &b.SessionID, &b.WorkspaceID, &b.PaneID, &b.Status, &b.ReleasedReason, &releasedAt, &created, &updated); err != nil {
		return nil, err
	}
	b.ReleasedAt = parseOptionalAutomationTime(releasedAt)
	b.CreatedAt = parseTicketTime(created)
	b.UpdatedAt = parseTicketTime(updated)
	return &b, nil
}

func (s *Store) GetActiveAutomationContinuityBinding(definitionID, continuityKey string) (*AutomationContinuityBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	b, err := scanAutomationContinuityBinding(s.db.QueryRow(`SELECT `+automationContinuityBindingColumns+` FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=? AND status=?`, definitionID, continuityKey, AutomationBindingStatusActive))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

func (s *Store) ReleaseAutomationContinuityBinding(definitionID, continuityKey, reason string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(
		`UPDATE automation_continuity_bindings SET status=?,released_reason=?,released_at=?,updated_at=? WHERE definition_id=? AND continuity_key=? AND status=?`,
		AutomationBindingStatusReleased, reason, formatTicketTime(now), formatTicketTime(now), definitionID, continuityKey, AutomationBindingStatusActive,
	)
	return err
}

func (s *Store) ReleaseAutomationContinuityBindings(definitionID, reason string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(
		`UPDATE automation_continuity_bindings SET status=?,released_reason=?,released_at=?,updated_at=? WHERE definition_id=? AND status=?`,
		AutomationBindingStatusReleased, reason, formatTicketTime(now), formatTicketTime(now), definitionID, AutomationBindingStatusActive,
	)
	return err
}

func getOrCreateActiveAutomationContinuityBindingTx(tx *sql.Tx, definitionID, continuityKey string, ids *AutomationRunReservation, now time.Time) error {
	var createdAt, updatedAt string
	err := tx.QueryRow(
		`SELECT ticket_id,session_id,workspace_id,pane_id,created_at,updated_at FROM automation_continuity_bindings WHERE definition_id=? AND continuity_key=? AND status=?`,
		definitionID, continuityKey, AutomationBindingStatusActive,
	).Scan(&ids.TicketID, &ids.SessionID, &ids.WorkspaceID, &ids.PaneID, &createdAt, &updatedAt)
	switch err {
	case sql.ErrNoRows:
		nowRaw := formatTicketTime(now)
		_, err = tx.Exec(
			`INSERT INTO automation_continuity_bindings(id,definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			uuid.NewString(), definitionID, continuityKey, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, AutomationBindingStatusActive, nowRaw, nowRaw,
		)
		return err
	case nil:
		return nil
	default:
		return err
	}
}

// AutomationSessionHasContinuityBinding checks ACTIVE bindings across all definitions: a
// bound thread's shared worktree is keyed on session id alone.
func (s *Store) AutomationSessionHasContinuityBinding(sessionID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sessionID == "" {
		return false, nil
	}
	if s.db == nil {
		return false, errors.New("automation persistence unavailable")
	}
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM automation_continuity_bindings WHERE session_id=? AND status=?)`, sessionID, AutomationBindingStatusActive).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists != 0, nil
}

func (s *Store) DeactivateAutomationReviewRequestEdges(definitionID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(`UPDATE automation_review_request_edges SET active=0,updated_at=? WHERE definition_id=?`, formatTicketTime(now), definitionID)
	return err
}

func (s *Store) FenceAutomationProviderCursors(definitionID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fenceAutomationProviderCursorsTx(tx, definitionID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteAutomationDefinition(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	res, err := s.db.Exec(`UPDATE automation_definitions SET deleted_at=?, updated_at=? WHERE id=? AND deleted_at=''`, formatTicketTime(now), formatTicketTime(now), id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("automation %q not found or already deleted", id)
	}
	return nil
}

func (s *Store) ListAutomationDefinitions() ([]AutomationDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id,name,enabled,revision,spec_json,created_at,updated_at,deleted_at FROM automation_definitions WHERE deleted_at='' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationDefinition
	for rows.Next() {
		d, err := scanAutomationDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) ListAutomationDefinitionIDsIncludingDeleted() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id FROM automation_definitions ORDER BY id`)
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

func (s *Store) ClaimManualAutomationRun(definitionID, requestID, subjectKey, payloadJSON string, expectedRevision int, snapshotJSON string, observedAt time.Time, ids AutomationRunReservation) (*AutomationRun, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	key := "manual:" + requestID
	var existingID string
	err = tx.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='manual' AND o.occurrence_key=?`, definitionID, key).Scan(&existingID)
	if err == nil {
		tx.Rollback()
		run, e := s.getAutomationRunUnlocked(existingID)
		return run, false, e
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}
	var revision int
	var enabled int
	if err := tx.QueryRow(`SELECT revision,enabled FROM automation_definitions WHERE id=? AND deleted_at=''`, definitionID).Scan(&revision, &enabled); err != nil {
		return nil, false, err
	}
	if revision != expectedRevision {
		return nil, false, fmt.Errorf("automation definition changed while starting run")
	}
	if enabled == 0 {
		return nil, false, fmt.Errorf("automation %q is disabled", definitionID)
	}
	now := formatTicketTime(observedAt)
	if _, err = tx.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,?, 'manual',?,?,?,?,?)`, ids.OccurrenceID, definitionID, key, subjectKey, now, payloadJSON, now); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, ids.RunID, definitionID, ids.OccurrenceID, revision, snapshotJSON, AutomationRunStatePending, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	run, e := s.getAutomationRunUnlocked(ids.RunID)
	return run, true, e
}

func (s *Store) ClaimScheduledAutomationRun(definitionID, occurrenceKey, continuityKey string, expectedRevision int, payloadJSON, snapshotJSON string, observedAt time.Time, reservation AutomationRunReservation) (*AutomationRun, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var existingID string
	err = tx.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='schedule' AND o.occurrence_key=?`, definitionID, occurrenceKey).Scan(&existingID)
	if err == nil {
		tx.Rollback()
		run, e := s.getAutomationRunUnlocked(existingID)
		return run, false, e
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}
	var revision, enabled int
	if err := tx.QueryRow(`SELECT revision,enabled FROM automation_definitions WHERE id=? AND deleted_at=''`, definitionID).Scan(&revision, &enabled); err != nil {
		return nil, false, err
	}
	if revision != expectedRevision {
		return nil, false, errors.New("automation definition changed while accepting observation")
	}
	if enabled == 0 {
		return nil, false, fmt.Errorf("automation %q is disabled", definitionID)
	}
	ids := reservation
	if continuityKey != "" {
		// A later occurrence must not overtake an earlier one whose ticket does not exist yet:
		// delivery would mistake the not-yet-created ticket for one already swept.
		var undeliveredPredecessor int
		if err := tx.QueryRow(`
			SELECT EXISTS(
				SELECT 1
				FROM automation_runs r
				JOIN automation_occurrences o ON o.id=r.occurrence_id
				WHERE r.definition_id=? AND o.provider='schedule' AND r.state=?
				  AND NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id=r.ticket_id)
			)
		`, definitionID, AutomationRunStatePending).Scan(&undeliveredPredecessor); err != nil {
			return nil, false, err
		}
		if undeliveredPredecessor != 0 {
			return nil, false, errors.New("an earlier scheduled automation run for this definition has not created its ticket yet")
		}
		if err := getOrCreateActiveAutomationContinuityBindingTx(tx, definitionID, continuityKey, &ids, observedAt); err != nil {
			return nil, false, err
		}
	}
	now := formatTicketTime(observedAt)
	// subject_key is recorded as continuityKey (not always ""): binding
	// lookups elsewhere key off of it for a scheduled singleton's history.
	if _, err = tx.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,?, 'schedule',?,?,?,?,?)`, ids.OccurrenceID, definitionID, occurrenceKey, continuityKey, now, payloadJSON, now); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, ids.RunID, definitionID, ids.OccurrenceID, revision, snapshotJSON, AutomationRunStatePending, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	run, e := s.getAutomationRunUnlocked(ids.RunID)
	return run, true, e
}

func (s *Store) GetAutomationScheduleCursor(definitionID string) (time.Time, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return time.Time{}, false, errors.New("automation persistence unavailable")
	}
	var raw string
	err := s.db.QueryRow(`SELECT observed_at FROM automation_provider_cursors WHERE definition_id=? AND provider='schedule' AND scope='*'`, definitionID).Scan(&raw)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	at, err := docstore.ParseTime(raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse automation schedule cursor: %w", err)
	}
	return at, true, nil
}

func (s *Store) SetAutomationScheduleCursor(definitionID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(`INSERT INTO automation_provider_cursors(definition_id,provider,scope,observed_at) VALUES(?,'schedule','*',?) ON CONFLICT(definition_id,provider,scope) DO UPDATE SET observed_at=excluded.observed_at`, definitionID, at.UTC().Format(sortableTimeFormat))
	return err
}

func (s *Store) ReconcileAutomationReviewRequests(definitionID, host string, subjectKeys []string, observedAt time.Time) ([]AutomationReviewRequestCandidate, error) {
	observations := make([]AutomationReviewRequestObservation, 0, len(subjectKeys))
	for _, subjectKey := range subjectKeys {
		observations = append(observations, AutomationReviewRequestObservation{SubjectKey: subjectKey})
	}
	return s.ReconcileAutomationReviewRequestHeads(definitionID, host, observations, observedAt)
}

func (s *Store) ReconcileAutomationReviewRequestHeads(definitionID, host string, observations []AutomationReviewRequestObservation, observedAt time.Time) ([]AutomationReviewRequestCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	observedRaw := observedAt.UTC().Format(sortableTimeFormat)
	updatedRaw := formatTicketTime(observedAt)
	var enableFenceRaw string
	err = tx.QueryRow(`SELECT observed_at FROM automation_provider_cursors WHERE definition_id=? AND provider='github_review_requested' AND scope='*'`, definitionID).Scan(&enableFenceRaw)
	if err == nil {
		enableFence, parseErr := docstore.ParseTime(enableFenceRaw)
		if parseErr != nil {
			return nil, fmt.Errorf("parse automation enable fence: %w", parseErr)
		}
		if observedAt.Before(enableFence) {
			return nil, nil
		}
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	var cursorRaw string
	baselining := false
	err = tx.QueryRow(`SELECT observed_at FROM automation_provider_cursors WHERE definition_id=? AND provider='github_review_requested' AND scope=?`, definitionID, host).Scan(&cursorRaw)
	if err == nil {
		cursorAt, parseErr := docstore.ParseTime(cursorRaw)
		if parseErr != nil {
			return nil, fmt.Errorf("parse automation provider cursor: %w", parseErr)
		}
		if observedAt.Before(cursorAt) {
			return nil, nil
		}
	}
	if err == sql.ErrNoRows {
		baselining = true
	} else if err != nil {
		return nil, err
	}
	current := make(map[string]string, len(observations))
	for _, observation := range observations {
		subjectKey := observation.SubjectKey
		if _, exists := current[subjectKey]; subjectKey == "" || exists {
			continue
		}
		current[subjectKey] = strings.ToLower(strings.TrimSpace(observation.HeadSHA))
		var active, cycle int
		err := tx.QueryRow(`SELECT active,cycle FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, definitionID, subjectKey).Scan(&active, &cycle)
		switch err {
		case sql.ErrNoRows:
			_, err = tx.Exec(`INSERT INTO automation_review_request_edges(definition_id,subject_key,host,active,cycle,last_observed_at,updated_at) VALUES(?,?,?,1,1,?,?)`, definitionID, subjectKey, host, observedRaw, updatedRaw)
		case nil:
			if active == 0 {
				cycle++
			}
			_, err = tx.Exec(`UPDATE automation_review_request_edges SET host=?,active=1,cycle=?,last_observed_at=?,updated_at=? WHERE definition_id=? AND subject_key=?`, host, cycle, observedRaw, updatedRaw, definitionID, subjectKey)
		}
		if err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(`SELECT subject_key FROM automation_review_request_edges WHERE definition_id=? AND host=? AND active=1`, definitionID, host)
	if err != nil {
		return nil, err
	}
	var deactivate []string
	for rows.Next() {
		var subjectKey string
		if err := rows.Scan(&subjectKey); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := current[subjectKey]; !ok {
			deactivate = append(deactivate, subjectKey)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, subjectKey := range deactivate {
		if _, err := tx.Exec(`UPDATE automation_review_request_edges SET active=0,updated_at=? WHERE definition_id=? AND subject_key=?`, updatedRaw, definitionID, subjectKey); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`
			UPDATE automation_continuity_bindings
			SET status=?,released_reason=?,released_at=?,updated_at=?
			WHERE definition_id=? AND continuity_key=? AND status=?
			  AND NOT EXISTS (
				SELECT 1 FROM tickets
				WHERE tickets.id=automation_continuity_bindings.ticket_id
			  )
		`, AutomationBindingStatusReleased, AutomationBindingReleasedTicketSwept, updatedRaw, updatedRaw, definitionID, subjectKey, AutomationBindingStatusActive); err != nil {
			return nil, err
		}
	}
	if baselining {
		if _, err := tx.Exec(`UPDATE automation_review_request_edges SET baseline_cycle=cycle WHERE definition_id=? AND host=? AND active=1`, definitionID, host); err != nil {
			return nil, err
		}
	}
	rows, err = tx.Query(`SELECT subject_key,cycle,baseline_cycle FROM automation_review_request_edges WHERE definition_id=? AND host=? AND active=1 ORDER BY subject_key`, definitionID, host)
	if err != nil {
		return nil, err
	}
	type activeReviewEdge struct {
		subjectKey string
		cycle      int
		baseline   int
	}
	var activeEdges []activeReviewEdge
	for rows.Next() {
		var edge activeReviewEdge
		if err := rows.Scan(&edge.subjectKey, &edge.cycle, &edge.baseline); err != nil {
			rows.Close()
			return nil, err
		}
		activeEdges = append(activeEdges, edge)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var candidates []AutomationReviewRequestCandidate
	for _, edge := range activeEdges {
		if edge.cycle <= edge.baseline {
			continue
		}
		headSHA := current[edge.subjectKey]
		hasOccurrence, hasPending, matchesHead, err := githubReviewCycleStatus(tx, definitionID, edge.subjectKey, edge.cycle, headSHA)
		if err != nil {
			return nil, err
		}
		if hasPending || !hasOccurrence || !matchesHead {
			candidates = append(candidates, AutomationReviewRequestCandidate{SubjectKey: edge.subjectKey, HeadSHA: headSHA, Cycle: edge.cycle})
		}
	}
	if _, err := tx.Exec(`INSERT INTO automation_provider_cursors(definition_id,provider,scope,observed_at) VALUES(?,'github_review_requested',?,?) ON CONFLICT(definition_id,provider,scope) DO UPDATE SET observed_at=excluded.observed_at WHERE excluded.observed_at >= automation_provider_cursors.observed_at`, definitionID, host, observedRaw); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func (s *Store) GitHubReviewAutomationRunStillRequested(runID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false, errors.New("automation persistence unavailable")
	}
	var occurrenceKey, subjectKey string
	var active, cycle int
	err := s.db.QueryRow(`
		SELECT o.occurrence_key,o.subject_key,e.active,e.cycle
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_review_request_edges e
		  ON e.definition_id=r.definition_id AND e.subject_key=o.subject_key
		WHERE r.id=? AND o.provider='github'
	`, runID).Scan(&occurrenceKey, &subjectKey, &active, &cycle)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	base := githubReviewOccurrenceBase(subjectKey, cycle)
	return active == 1 && (occurrenceKey == base || strings.HasPrefix(occurrenceKey, base+":")), nil
}

func (s *Store) ListWithdrawnGitHubReviewUndeliveredRuns(definitionID, host string) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	rows, err := s.db.Query(`
		SELECT `+automationRunColumnsQualified+`
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_review_request_edges e
		  ON e.definition_id=r.definition_id AND e.subject_key=o.subject_key
		WHERE r.definition_id=? AND e.host=? AND e.active=0
		  AND (r.state=? OR (r.state=? AND r.cancel_reason=?))
		  AND o.provider='github'
		  AND (
			o.occurrence_key=('review_requested:' || e.subject_key || ':' || CAST(e.cycle AS TEXT))
			OR instr(o.occurrence_key,('review_requested:' || e.subject_key || ':' || CAST(e.cycle AS TEXT) || ':'))=1
		  )
		ORDER BY r.created_at
	`, definitionID, host, AutomationRunStatePending, AutomationRunStateCancelled, AutomationCancelReasonReviewWithdrawn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		run, err := scanAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

func (s *Store) ClaimGitHubReviewAutomationRun(definitionID, subjectKey string, cycle, expectedRevision int, payloadJSON, snapshotJSON string, observedAt time.Time, reserved AutomationRunReservation) (*AutomationRun, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var active, currentCycle, baselineCycle int
	if err := tx.QueryRow(`SELECT active,cycle,baseline_cycle FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, definitionID, subjectKey).Scan(&active, &currentCycle, &baselineCycle); err != nil {
		return nil, false, err
	}
	if active == 0 || currentCycle != cycle || cycle <= baselineCycle {
		return nil, false, errors.New("review-request edge changed before occurrence claim")
	}
	baseKey := githubReviewOccurrenceBase(subjectKey, cycle)
	headSHA := githubReviewPayloadHead(payloadJSON)
	occurrenceKey := githubReviewOccurrenceKey(subjectKey, cycle, headSHA)
	var existingID string
	err = tx.QueryRow(`
		SELECT r.id
		FROM automation_occurrences o
		JOIN automation_runs r ON r.occurrence_id=o.id
		WHERE o.definition_id=? AND o.provider='github' AND o.subject_key=? AND r.state=?
		  AND (o.occurrence_key=? OR instr(o.occurrence_key,?)=1)
		ORDER BY r.created_at
		LIMIT 1
	`, definitionID, subjectKey, AutomationRunStatePending, baseKey, baseKey+":").Scan(&existingID)
	if err == nil {
		tx.Rollback()
		run, e := s.getAutomationRunUnlocked(existingID)
		return run, false, e
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}
	err = tx.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='github' AND o.occurrence_key=?`, definitionID, occurrenceKey).Scan(&existingID)
	if err == nil {
		tx.Rollback()
		run, e := s.getAutomationRunUnlocked(existingID)
		return run, false, e
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}
	if headSHA != "" {
		var legacyID, legacyPayload string
		err = tx.QueryRow(`
			SELECT r.id,o.payload_json
			FROM automation_occurrences o
			JOIN automation_runs r ON r.occurrence_id=o.id
			WHERE o.definition_id=? AND o.provider='github' AND o.occurrence_key=?
		`, definitionID, baseKey).Scan(&legacyID, &legacyPayload)
		if err == nil {
			legacyHead := githubReviewPayloadHead(legacyPayload)
			if legacyHead == "" || legacyHead == headSHA {
				tx.Rollback()
				run, e := s.getAutomationRunUnlocked(legacyID)
				return run, false, e
			}
		} else if err != sql.ErrNoRows {
			return nil, false, err
		}
	}
	var revision, enabled int
	if err := tx.QueryRow(`SELECT revision,enabled FROM automation_definitions WHERE id=? AND deleted_at=''`, definitionID).Scan(&revision, &enabled); err != nil {
		return nil, false, err
	}
	if revision != expectedRevision {
		return nil, false, errors.New("automation definition changed while accepting observation")
	}
	if enabled == 0 {
		return nil, false, fmt.Errorf("automation %q is disabled", definitionID)
	}
	// A later request cycle must not overtake the initial delivery for this subject: the
	// binding can exist before its ticket, and delivery would mistake it for a swept one.
	var undeliveredPredecessor int
	if err := tx.QueryRow(`
		SELECT EXISTS(
			SELECT 1
			FROM automation_runs r
			JOIN automation_occurrences o ON o.id=r.occurrence_id
			WHERE r.definition_id=? AND o.subject_key=? AND r.state=?
			  AND NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id=r.ticket_id)
		)
	`, definitionID, subjectKey, AutomationRunStatePending).Scan(&undeliveredPredecessor); err != nil {
		return nil, false, err
	}
	if undeliveredPredecessor != 0 {
		return nil, false, errors.New("an earlier automation run for this subject has not created its ticket yet")
	}
	ids := reserved
	if err := getOrCreateActiveAutomationContinuityBindingTx(tx, definitionID, subjectKey, &ids, observedAt); err != nil {
		return nil, false, err
	}
	now := formatTicketTime(observedAt)
	if _, err = tx.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,?, 'github',?,?,?,?,?)`, ids.OccurrenceID, definitionID, occurrenceKey, subjectKey, now, payloadJSON, now); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,workspace_id,pane_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, ids.RunID, definitionID, ids.OccurrenceID, revision, snapshotJSON, AutomationRunStatePending, ids.TicketID, ids.SessionID, ids.WorkspaceID, ids.PaneID, now, now); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	run, err := s.getAutomationRunUnlocked(ids.RunID)
	return run, true, err
}

func (s *Store) EnsureAutomationContinuationTicket(ticketID, sessionID, runID, occurrencePath, author string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var assignee, originRunID string
	if err := tx.QueryRow(`SELECT assignee,COALESCE(automation_run_id,'') FROM tickets WHERE id=?`, ticketID).Scan(&assignee, &originRunID); err != nil {
		return err
	}
	if assignee != sessionID || originRunID == "" {
		return errors.New("continuity ticket does not match its automation binding")
	}
	result, err := tx.Exec(`INSERT OR IGNORE INTO automation_ticket_occurrence_events(run_id,ticket_id,created_at) VALUES(?,?,?)`, runID, ticketID, formatTicketTime(now))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 {
		comment := "Accepted automation occurrence " + runID + " for the existing reviewer. Structured occurrence input: " + occurrencePath
		if _, err := addTicketCommentTx(tx, ticketID, author, comment, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanAutomationRun(scanner interface{ Scan(...any) error }) (*AutomationRun, error) {
	var r AutomationRun
	var created, updated, delivered string
	err := scanner.Scan(&r.ID, &r.DefinitionID, &r.OccurrenceID, &r.DefinitionRevision, &r.SnapshotJSON, &r.State, &r.CancelReason, &r.Attempts, &r.LastError, &r.TicketID, &r.SessionID, &r.WorkspaceID, &r.PaneID, &r.ResolvedLocationJSON, &created, &updated, &delivered)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = parseTicketTime(created)
	r.UpdatedAt = parseTicketTime(updated)
	r.DeliveredAt = parseOptionalAutomationTime(delivered)
	return &r, nil
}

const automationRunColumns = `id,definition_id,occurrence_id,definition_revision,snapshot_json,state,cancel_reason,attempts,last_error,ticket_id,session_id,workspace_id,pane_id,resolved_location_json,created_at,updated_at,delivered_at`

const automationRunColumnsQualified = `r.id,r.definition_id,r.occurrence_id,r.definition_revision,r.snapshot_json,r.state,r.cancel_reason,r.attempts,r.last_error,r.ticket_id,r.session_id,r.workspace_id,r.pane_id,r.resolved_location_json,r.created_at,r.updated_at,r.delivered_at`

func (s *Store) getAutomationRunUnlocked(id string) (*AutomationRun, error) {
	r, e := scanAutomationRun(s.db.QueryRow(`SELECT `+automationRunColumns+` FROM automation_runs WHERE id=?`, id))
	if e == sql.ErrNoRows {
		return nil, nil
	}
	return r, e
}
func (s *Store) GetAutomationRun(id string) (*AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	return s.getAutomationRunUnlocked(id)
}
func (s *Store) GetManualAutomationRun(definitionID, requestID string) (*AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	var runID string
	err := s.db.QueryRow(`SELECT r.id FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE o.definition_id=? AND o.provider='manual' AND o.occurrence_key=?`, definitionID, "manual:"+requestID).Scan(&runID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.getAutomationRunUnlocked(runID)
}
func (s *Store) ListAutomationRuns(definitionID string) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, e := s.db.Query(`SELECT `+automationRunColumns+` FROM automation_runs WHERE definition_id=? ORDER BY created_at DESC`, definitionID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, e := scanAutomationRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

type AutomationRunWithOccurrenceKey struct {
	AutomationRun
	OccurrenceKey string
	Provenance    AutomationProvenanceRecord
}

func (s *Store) ListAutomationRunsWithOccurrenceKeys(definitionID string, limit int) ([]AutomationRunWithOccurrenceKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT `+automationRunColumnsQualified+`,o.occurrence_key,
			d.name,d.spec_json,o.provider,o.subject_key,o.payload_json
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_definitions d ON d.id=r.definition_id
		WHERE r.definition_id=?
		ORDER BY r.created_at DESC
		LIMIT ?
	`, definitionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRunWithOccurrenceKey
	for rows.Next() {
		var r AutomationRun
		var created, updated, delivered, occurrenceKey string
		var provenance AutomationProvenanceRecord
		if err := rows.Scan(&r.ID, &r.DefinitionID, &r.OccurrenceID, &r.DefinitionRevision, &r.SnapshotJSON, &r.State, &r.CancelReason, &r.Attempts, &r.LastError, &r.TicketID, &r.SessionID, &r.WorkspaceID, &r.PaneID, &r.ResolvedLocationJSON, &created, &updated, &delivered, &occurrenceKey, &provenance.DefinitionName, &provenance.DefinitionSpecJSON, &provenance.Provider, &provenance.SubjectKey, &provenance.PayloadJSON); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTicketTime(created)
		r.UpdatedAt = parseTicketTime(updated)
		r.DeliveredAt = parseOptionalAutomationTime(delivered)
		provenance.RunID = r.ID
		provenance.DefinitionID = r.DefinitionID
		provenance.SessionID = r.SessionID
		provenance.TicketID = r.TicketID
		provenance.CreatedAt = r.CreatedAt
		out = append(out, AutomationRunWithOccurrenceKey{AutomationRun: r, OccurrenceKey: occurrenceKey, Provenance: provenance})
	}
	return out, rows.Err()
}

func (s *Store) LatestAutomationRunPerDefinition() (map[string]AutomationRunWithOccurrenceKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT ` + automationRunColumnsQualified + `,o.occurrence_key,
			d.name,d.spec_json,o.provider,o.subject_key,o.payload_json
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_definitions d ON d.id=r.definition_id
		WHERE r.id IN (
			SELECT id FROM (
				SELECT id, definition_id,
					ROW_NUMBER() OVER (PARTITION BY definition_id ORDER BY created_at DESC, id DESC) AS rn
				FROM automation_runs
			) WHERE rn = 1
		)
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]AutomationRunWithOccurrenceKey)
	for rows.Next() {
		var r AutomationRun
		var created, updated, delivered, occurrenceKey string
		var provenance AutomationProvenanceRecord
		if err := rows.Scan(&r.ID, &r.DefinitionID, &r.OccurrenceID, &r.DefinitionRevision, &r.SnapshotJSON, &r.State, &r.CancelReason, &r.Attempts, &r.LastError, &r.TicketID, &r.SessionID, &r.WorkspaceID, &r.PaneID, &r.ResolvedLocationJSON, &created, &updated, &delivered, &occurrenceKey, &provenance.DefinitionName, &provenance.DefinitionSpecJSON, &provenance.Provider, &provenance.SubjectKey, &provenance.PayloadJSON); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTicketTime(created)
		r.UpdatedAt = parseTicketTime(updated)
		r.DeliveredAt = parseOptionalAutomationTime(delivered)
		provenance.RunID = r.ID
		provenance.DefinitionID = r.DefinitionID
		provenance.SessionID = r.SessionID
		provenance.TicketID = r.TicketID
		provenance.CreatedAt = r.CreatedAt
		out[r.DefinitionID] = AutomationRunWithOccurrenceKey{AutomationRun: r, OccurrenceKey: occurrenceKey, Provenance: provenance}
	}
	return out, rows.Err()
}

func (s *Store) ListLatestAutomationProvenanceRecords() ([]AutomationProvenanceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT r.id,r.definition_id,d.name,d.spec_json,r.session_id,r.ticket_id,
			o.provider,o.subject_key,o.payload_json,r.created_at
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_definitions d ON d.id=r.definition_id
		WHERE r.id IN (
			SELECT id FROM (
				SELECT id,
					ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at DESC,id DESC) AS session_rank,
					ROW_NUMBER() OVER (PARTITION BY ticket_id ORDER BY created_at DESC,id DESC) AS ticket_rank
				FROM automation_runs
			) WHERE session_rank=1 OR ticket_rank=1
		)
		ORDER BY r.created_at DESC,r.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationProvenanceRecord
	for rows.Next() {
		var record AutomationProvenanceRecord
		var created string
		if err := rows.Scan(&record.RunID, &record.DefinitionID, &record.DefinitionName, &record.DefinitionSpecJSON, &record.SessionID, &record.TicketID, &record.Provider, &record.SubjectKey, &record.PayloadJSON, &created); err != nil {
			return nil, err
		}
		record.CreatedAt = parseTicketTime(created)
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) GetAutomationProvenanceRecord(runID string) (*AutomationProvenanceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	var record AutomationProvenanceRecord
	var created string
	err := s.db.QueryRow(`
		SELECT r.id,r.definition_id,d.name,d.spec_json,r.session_id,r.ticket_id,
			o.provider,o.subject_key,o.payload_json,r.created_at
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_definitions d ON d.id=r.definition_id
		WHERE r.id=?
	`, runID).Scan(&record.RunID, &record.DefinitionID, &record.DefinitionName, &record.DefinitionSpecJSON, &record.SessionID, &record.TicketID, &record.Provider, &record.SubjectKey, &record.PayloadJSON, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record.CreatedAt = parseTicketTime(created)
	return &record, nil
}

func (s *Store) GetLatestAutomationProvenanceRecordForSession(sessionID string) (*AutomationProvenanceRecord, error) {
	return s.getLatestAutomationProvenanceRecord(`r.session_id=?`, sessionID)
}

func (s *Store) GetLatestAutomationProvenanceRecordForTicket(ticketID string) (*AutomationProvenanceRecord, error) {
	return s.getLatestAutomationProvenanceRecord(`r.ticket_id=?`, ticketID)
}

func (s *Store) getLatestAutomationProvenanceRecord(where, id string) (*AutomationProvenanceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || id == "" {
		return nil, nil
	}
	var record AutomationProvenanceRecord
	var created string
	err := s.db.QueryRow(`
		SELECT r.id,r.definition_id,d.name,d.spec_json,r.session_id,r.ticket_id,
			o.provider,o.subject_key,o.payload_json,r.created_at
		FROM automation_runs r
		JOIN automation_occurrences o ON o.id=r.occurrence_id
		JOIN automation_definitions d ON d.id=r.definition_id
		WHERE `+where+`
		ORDER BY r.created_at DESC,r.id DESC
		LIMIT 1
	`, id).Scan(&record.RunID, &record.DefinitionID, &record.DefinitionName, &record.DefinitionSpecJSON, &record.SessionID, &record.TicketID, &record.Provider, &record.SubjectKey, &record.PayloadJSON, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record.CreatedAt = parseTicketTime(created)
	return &record, nil
}

func (s *Store) ListPendingAutomationRuns() ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, e := s.db.Query(`SELECT `+automationRunColumns+` FROM automation_runs WHERE state=? ORDER BY created_at`, AutomationRunStatePending)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, e := scanAutomationRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
func (s *Store) MarkAutomationRunDelivered(id, resolved string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`UPDATE automation_runs SET state=?,last_error='',resolved_location_json=?,updated_at=?,delivered_at=? WHERE id=?`, AutomationRunStateDelivered, resolved, formatTicketTime(now), formatTicketTime(now), id)
	return e
}
func (s *Store) MarkAutomationRunFailed(id, message string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`UPDATE automation_runs SET state=?,last_error=?,updated_at=? WHERE id=?`, AutomationRunStateFailed, message, formatTicketTime(now), id)
	return e
}

func (s *Store) MarkAutomationRunCancelled(id, reason string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(`UPDATE automation_runs SET state=?,cancel_reason=?,updated_at=? WHERE id=?`, AutomationRunStateCancelled, reason, formatTicketTime(now), id)
	return err
}

// ListPrunableAutomationRuns excludes the origin run of a still-bound continuity thread:
// tickets.automation_run_id is set once, so pruning it breaks every later occurrence.
func (s *Store) ListPrunableAutomationRuns(definitionID string, keep int, olderThan time.Time) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT `+automationRunColumns+`
		FROM automation_runs
		WHERE definition_id=? AND state IN (?,?,?) AND created_at<?
		  AND id NOT IN (
			SELECT id FROM automation_runs WHERE definition_id=? ORDER BY created_at DESC LIMIT ?
		  )
		  -- A still-bound continuity thread's origin run is never prunable: see
		  -- this function's doc comment.
		  AND id NOT IN (
			SELECT t.automation_run_id FROM tickets t
			JOIN automation_continuity_bindings b ON b.ticket_id = t.id
			WHERE t.automation_run_id IS NOT NULL AND t.automation_run_id <> ''
		  )
		ORDER BY created_at
	`, definitionID, AutomationRunStateDelivered, AutomationRunStateFailed, AutomationRunStateCancelled, formatTicketTime(olderThan), definitionID, keep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, err := scanAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) ListTerminalAutomationRuns(definitionID string) ([]AutomationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT `+automationRunColumns+`
		FROM automation_runs
		WHERE definition_id=? AND state IN (?,?,?)
		ORDER BY created_at
	`, definitionID, AutomationRunStateDelivered, AutomationRunStateFailed, AutomationRunStateCancelled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, err := scanAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAutomationRun(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var occurrenceID string
	err = tx.QueryRow(`SELECT occurrence_id FROM automation_runs WHERE id=?`, runID).Scan(&occurrenceID)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM automation_runs WHERE id=?`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM automation_occurrences WHERE id=?`, occurrenceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AutomationOccurrencePayload(id string, out *string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	return s.db.QueryRow(`SELECT payload_json FROM automation_occurrences WHERE id=?`, id).Scan(out)
}

func (s *Store) GetAutomationOccurrence(id string) (*AutomationOccurrence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	var occurrence AutomationOccurrence
	var observedAt, createdAt string
	err := s.db.QueryRow(`SELECT id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at FROM automation_occurrences WHERE id=?`, id).Scan(
		&occurrence.ID, &occurrence.DefinitionID, &occurrence.Provider, &occurrence.OccurrenceKey,
		&occurrence.SubjectKey, &observedAt, &occurrence.PayloadJSON, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	occurrence.ObservedAt = parseTicketTime(observedAt)
	occurrence.CreatedAt = parseTicketTime(createdAt)
	return &occurrence, nil
}
