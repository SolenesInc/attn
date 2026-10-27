package store

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

const SessionNameRunes = 48

type SessionLabels struct {
	UserLabel    string
	HarnessTitle string
}

func (labels SessionLabels) shown(id protocol.SessionID, directory string, agent protocol.SessionAgent) string {
	if labels.UserLabel != "" {
		return labels.UserLabel
	}
	if labels.HarnessTitle != "" {
		return labels.HarnessTitle
	}
	if agent == protocol.SessionAgentShell {
		return ""
	}
	return DefaultSessionLabel(directory, id)
}

func DefaultSessionLabel(directory string, id protocol.SessionID) string {
	label := filepath.Base(directory)
	if label == "" || label == "." || label == string(filepath.Separator) {
		return string(id)
	}
	return label
}

var idShapedSessionLabel = regexp.MustCompile(`^(?:s-)?[a-z0-9]{6}$|^[0-9a-f]{6,}$|^[A-Z][A-Z0-9]*-[0-9]+$|^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f-]{4,}$`)
var sixLetterSessionWord = regexp.MustCompile(`^(?:s-)?[a-z]{6}$`)

func SessionLabelIsPlaceholder(label, directory string, id protocol.SessionID) bool {
	label = strings.TrimSpace(label)
	def := DefaultSessionLabel(directory, id)
	truncated := def
	if runes := []rune(def); len(runes) > SessionNameRunes {
		truncated = strings.TrimRight(string(runes[:SessionNameRunes]), "-_. \t")
	}
	return label == def || label == truncated || (idShapedSessionLabel.MatchString(label) && !sixLetterSessionWord.MatchString(label))
}

func initialSessionLabels(session *protocol.Session) SessionLabels {
	if session.Label == "" {
		return SessionLabels{}
	}
	if session.Agent != protocol.SessionAgentShell && SessionLabelIsPlaceholder(session.Label, session.Directory, session.ID) {
		return SessionLabels{HarnessTitle: session.Label}
	}
	return SessionLabels{UserLabel: session.Label}
}

func (s *Store) SessionLabels(id protocol.SessionID) SessionLabels {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return s.sessionLabels[id]
	}
	var labels SessionLabels
	if err := s.db.QueryRow(`SELECT user_label,harness_title FROM sessions_with_runtime WHERE id=?`, id).Scan(&labels.UserLabel, &labels.HarnessTitle); err != nil {
		return SessionLabels{}
	}
	return labels
}

func sessionLabelTx(tx *sql.Tx, id protocol.SessionID) (string, error) {
	var labels SessionLabels
	var directory string
	var agent protocol.SessionAgent
	if err := tx.QueryRow(`SELECT user_label,harness_title,directory,agent FROM sessions_with_runtime WHERE id=?`, id).Scan(&labels.UserLabel, &labels.HarnessTitle, &directory, &agent); err != nil {
		return "", err
	}
	return labels.shown(id, directory, agent), nil
}
