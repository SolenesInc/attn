package profilemigration

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	PhasePlacementRequired = "placement_required"
	PhaseComplete          = "complete"
)

type Manifest struct {
	ProfileID         string             `json:"profile_id"`
	Groups            []Group            `json:"groups"`
	DroppedWorkspaces []DroppedWorkspace `json:"dropped_workspaces,omitempty"`
	DroppedPlacements []DroppedPlacement `json:"dropped_placements,omitempty"`
	DroppedPanes      []DroppedPane      `json:"dropped_panes,omitempty"`
	RenamedPanes      []RenamedPane      `json:"renamed_panes,omitempty"`
	UnplacedSessions  []string           `json:"unplaced_sessions,omitempty"`
}

type Group struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Directory    string   `json:"directory"`
	DesktopID    string   `json:"desktop_id"`
	ShortcutSlot int      `json:"shortcut_slot,omitempty"`
	LeafIDs      []string `json:"leaf_ids"`
}

type DroppedWorkspace struct {
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
	Reason      string `json:"reason"`
}

type DroppedPlacement struct {
	SessionID       string `json:"session_id"`
	WorkspaceID     string `json:"workspace_id"`
	PaneID          string `json:"pane_id"`
	KeptWorkspaceID string `json:"kept_workspace_id"`
	KeptPaneID      string `json:"kept_pane_id"`
}

type DroppedPane struct {
	WorkspaceID string `json:"workspace_id"`
	PaneID      string `json:"pane_id"`
	SessionID   string `json:"session_id,omitempty"`
	Reason      string `json:"reason"`
}

type RenamedPane struct {
	WorkspaceID string `json:"workspace_id"`
	From        string `json:"from"`
	To          string `json:"to"`
}

const (
	DropReasonDocumentOnly  = "document_only"
	DropReasonEmpty         = "empty"
	DropReasonNoPaneRow     = "no_pane_row"
	DropReasonNoSession     = "no_session"
	DropReasonSessionClosed = "session_closed"
)

func EncodeManifest(m Manifest) (string, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encoding the migration manifest: %w", err)
	}
	return string(data), nil
}

func DecodeManifest(raw string) (Manifest, error) {
	var m Manifest
	if strings.TrimSpace(raw) == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Manifest{}, fmt.Errorf("the stored migration manifest does not decode: %w", err)
	}
	return m, nil
}
