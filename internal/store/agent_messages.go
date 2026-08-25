package store

import (
	"fmt"
	"time"
)

type AgentMessage struct {
	ID              string
	SenderSessionID string
	TargetSessionID string
	Content         string
	CreatedAt       string
	DeliveredAt     string
}

type AgentMessageGuardCounts struct {
	DuplicateFromSender  bool
	FromSenderInWindow   int
	UndeliveredForTarget int
}

func (s *Store) EnqueueAgentMessage(msg AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO agent_messages (id, sender_session_id, target_session_id, content, created_at, delivered_at)
		VALUES (?, ?, ?, ?, ?, '')
	`, msg.ID, msg.SenderSessionID, msg.TargetSessionID, msg.Content, msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to enqueue agent message: %w", err)
	}
	return nil
}

// Rolls back a delivery whose target could not be launched. Only an undelivered row:
// once a target took the message, a late cleanup must not erase its receipt.
func (s *Store) DeleteQueuedAgentMessage(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		DELETE FROM agent_messages WHERE id = ? AND delivered_at = ''
	`, id)
	if err != nil {
		return fmt.Errorf("failed to delete queued agent message: %w", err)
	}
	return nil
}

func (s *Store) UndeliveredAgentMessages(targetSessionID string) ([]AgentMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT id, sender_session_id, target_session_id, content, created_at
		FROM agent_messages
		WHERE target_session_id = ? AND delivered_at = ''
		ORDER BY created_at, id
	`, targetSessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list queued agent messages: %w", err)
	}
	defer rows.Close()

	messages := []AgentMessage{}
	for rows.Next() {
		var msg AgentMessage
		if err := rows.Scan(&msg.ID, &msg.SenderSessionID, &msg.TargetSessionID, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan queued agent message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func (s *Store) AgentMessageQueued(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var queued bool
	if err := s.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM agent_messages WHERE id = ? AND delivered_at = ''
		)
	`, id).Scan(&queued); err != nil {
		return false, fmt.Errorf("failed to check queued agent message: %w", err)
	}
	return queued, nil
}

func (s *Store) TargetsWithQueuedAgentMessages() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT DISTINCT target_session_id FROM agent_messages WHERE delivered_at = ''
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list agent message targets: %w", err)
	}
	defer rows.Close()

	targets := []string{}
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, fmt.Errorf("failed to scan agent message target: %w", err)
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// MarkAgentMessageDelivered stamps a delivery. Stamping an already-delivered
// row does nothing, so a racing drain cannot double-count one message.
func (s *Store) MarkAgentMessageDelivered(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE agent_messages SET delivered_at = ? WHERE id = ? AND delivered_at = ''
	`, at.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("failed to mark agent message delivered: %w", err)
	}
	return nil
}

func (s *Store) AgentMessageGuardCounts(sender, target, content string, dedupeSince, rateSince time.Time) (AgentMessageGuardCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var counts AgentMessageGuardCounts
	err := s.db.QueryRow(`
		SELECT
			EXISTS (
				SELECT 1 FROM agent_messages
				WHERE sender_session_id = ? AND target_session_id = ? AND content = ? AND created_at >= ?
			),
			(
				SELECT COUNT(*) FROM agent_messages
				WHERE sender_session_id = ? AND target_session_id = ? AND created_at >= ?
			),
			(
				SELECT COUNT(*) FROM agent_messages
				WHERE target_session_id = ? AND delivered_at = ''
			)
	`,
		sender, target, content, dedupeSince.UTC().Format(time.RFC3339),
		sender, target, rateSince.UTC().Format(time.RFC3339),
		target,
	).Scan(&counts.DuplicateFromSender, &counts.FromSenderInWindow, &counts.UndeliveredForTarget)
	if err != nil {
		return AgentMessageGuardCounts{}, fmt.Errorf("failed to read agent message guard counts: %w", err)
	}
	return counts, nil
}
