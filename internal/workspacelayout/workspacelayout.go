package workspacelayout

import (
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/layouttree"
)

const DefaultPaneTitle = "Agent"

type PaneKind string

const (
	PaneKindAgent PaneKind = "agent"
)

type PaneStatus string

const (
	PaneStatusSpawning PaneStatus = "spawning"
	PaneStatusReady    PaneStatus = "ready"
	PaneStatusFailed   PaneStatus = "failed"
)

type Pane struct {
	PaneID    string
	RuntimeID string
	SessionID string
	Kind      PaneKind
	Title     string
	Status    PaneStatus
	Error     string
}

type WorkspaceLayout struct {
	WorkspaceID  string
	ActivePaneID string
	Layout       layouttree.Node
	Panes        []Pane
	UpdatedAt    string
}

func DefaultWorkspaceLayout(workspaceID, paneID, sessionID string) WorkspaceLayout {
	return WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: paneID,
		Layout:       layouttree.DefaultLayout(paneID),
		Panes: []Pane{
			{
				PaneID:    paneID,
				RuntimeID: sessionID,
				SessionID: sessionID,
				Kind:      PaneKindAgent,
				Title:     DefaultPaneTitle,
				Status:    PaneStatusReady,
			},
		},
	}
}

func NormalizeWorkspaceLayout(snapshot WorkspaceLayout) WorkspaceLayout {
	normalized := snapshot

	panesByID := make(map[string]Pane, len(normalized.Panes))
	for _, pane := range normalized.Panes {
		paneID := strings.TrimSpace(pane.PaneID)
		if paneID == "" {
			continue
		}
		runtimeID := strings.TrimSpace(pane.RuntimeID)
		if runtimeID == "" {
			continue
		}
		sessionID := strings.TrimSpace(pane.SessionID)
		if sessionID == "" {
			continue
		}
		title := strings.TrimSpace(pane.Title)
		if title == "" {
			title = paneID
		}
		status := pane.Status
		if status == "" {
			status = PaneStatusReady
		}
		if status != PaneStatusSpawning && status != PaneStatusReady && status != PaneStatusFailed {
			status = PaneStatusReady
		}
		panesByID[paneID] = Pane{
			PaneID:    paneID,
			RuntimeID: runtimeID,
			SessionID: sessionID,
			Kind:      PaneKindAgent,
			Title:     title,
			Status:    status,
			Error:     strings.TrimSpace(pane.Error),
		}
	}

	normalized.Layout = layouttree.NormalizeLayout(normalized.Layout, panesByID)
	paneIDs := layouttree.PaneIDs(normalized.Layout)
	normalized.Panes = make([]Pane, 0, len(paneIDs))
	for _, paneID := range paneIDs {
		if pane, ok := panesByID[paneID]; ok {
			normalized.Panes = append(normalized.Panes, pane)
		}
	}
	if !slices.Contains(paneIDs, normalized.ActivePaneID) {
		normalized.ActivePaneID = ""
		if len(paneIDs) > 0 {
			normalized.ActivePaneID = paneIDs[0]
		}
	}
	return normalized
}
