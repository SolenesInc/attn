package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/rankkey"
	"github.com/victorarias/attn/internal/setupmigration"
	"github.com/victorarias/attn/internal/setups"
)

const SetupConversionSchemaVersion = 153

const DefaultSetupName = "Default"

var setupStampedTables = []string{
	"delegation_operations",
	"chief_of_staff_dispatches",
	"automation_definitions",
	"automation_runs",
	"automation_continuity_bindings",
	"workflow_runs",
}

type legacyWorkspace struct {
	ID        string
	Title     string
	Directory string
}

type legacyLayout struct {
	ActivePaneID string
	LayoutJSON   string
}

type legacyPane struct {
	PaneID    string
	SessionID string
	Title     string
	Status    string
	Error     string
	UpdatedAt string
}

type legacySession struct {
	Label    string
	ClosedAt string
}

type legacyInput struct {
	Workspaces []legacyWorkspace
	Layouts    map[string]legacyLayout
	Panes      map[string]map[string]legacyPane
	Sessions   map[string]legacySession
}

type convertedWorkspaces struct {
	Manifest setupmigration.Manifest
	Desktops []setups.Desktop
}

type placementCandidate struct {
	rank        int
	workspaceID string
	paneID      string
	sessionID   string
	updatedAt   string
}

type workspaceConversion struct {
	workspace legacyWorkspace
	rank      int
	tree      layouttree.Node
	active    string
}

func applySetupConversion(tx *sql.Tx) error {
	var one int
	converted, err := rowFound(tx.QueryRow(`SELECT 1 FROM setup_migration WHERE id = 1`), &one)
	if err != nil || converted {
		return err
	}
	var existing int
	if err := tx.QueryRow(`SELECT count(*) FROM setups`).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return fmt.Errorf("the workspace conversion expects no setups, but %d already exist without a recorded conversion", existing)
	}
	if err := addSetupStampColumns(tx); err != nil {
		return err
	}
	input, err := readLegacyWorkspaces(tx)
	if err != nil {
		return err
	}
	result, err := convertLegacyWorkspaces(input, newSetupEntityID)
	if err != nil {
		return err
	}
	return writeSetupConversion(tx, result)
}

func existingTables(tx *sql.Tx, names ...string) ([]string, error) {
	var present []string
	for _, name := range names {
		exists, err := tableExists(tx, name)
		if err != nil {
			return nil, err
		}
		if exists {
			present = append(present, name)
		}
	}
	return present, nil
}

func addSetupStampColumns(tx *sql.Tx) error {
	tables, err := existingTables(tx, setupStampedTables...)
	if err != nil {
		return err
	}
	for _, table := range tables {
		has, err := columnExists(tx, table, "setup_id")
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN setup_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("adding setup_id to %s: %w", table, err)
		}
	}
	return nil
}

func readLegacyWorkspaces(tx *sql.Tx) (legacyInput, error) {
	input := legacyInput{
		Layouts:  make(map[string]legacyLayout),
		Panes:    make(map[string]map[string]legacyPane),
		Sessions: make(map[string]legacySession),
	}
	present, err := existingTables(tx, "workspaces", "workspace_layouts", "workspace_layout_panes")
	if err != nil {
		return input, err
	}
	if len(present) != 3 {
		return input, readLegacySessions(tx, &input)
	}
	rows, err := tx.Query(`SELECT id, title, directory FROM workspaces ORDER BY rank, created_at, id`)
	if err != nil {
		return input, fmt.Errorf("reading legacy workspaces: %w", err)
	}
	for rows.Next() {
		var ws legacyWorkspace
		if err := rows.Scan(&ws.ID, &ws.Title, &ws.Directory); err != nil {
			rows.Close()
			return input, err
		}
		input.Workspaces = append(input.Workspaces, ws)
	}
	if err := rows.Close(); err != nil {
		return input, err
	}
	if rows, err = tx.Query(`SELECT workspace_id, active_pane_id, layout_json FROM workspace_layouts`); err != nil {
		return input, fmt.Errorf("reading legacy workspace layouts: %w", err)
	}
	for rows.Next() {
		var id string
		var layout legacyLayout
		if err := rows.Scan(&id, &layout.ActivePaneID, &layout.LayoutJSON); err != nil {
			rows.Close()
			return input, err
		}
		input.Layouts[id] = layout
	}
	if err := rows.Close(); err != nil {
		return input, err
	}
	if rows, err = tx.Query(`SELECT workspace_id, pane_id, COALESCE(session_id, ''), title, status, error, updated_at FROM workspace_layout_panes`); err != nil {
		return input, fmt.Errorf("reading legacy workspace panes: %w", err)
	}
	for rows.Next() {
		var workspaceID string
		var pane legacyPane
		if err := rows.Scan(&workspaceID, &pane.PaneID, &pane.SessionID, &pane.Title, &pane.Status, &pane.Error, &pane.UpdatedAt); err != nil {
			rows.Close()
			return input, err
		}
		if input.Panes[workspaceID] == nil {
			input.Panes[workspaceID] = make(map[string]legacyPane)
		}
		input.Panes[workspaceID][pane.PaneID] = pane
	}
	if err := rows.Close(); err != nil {
		return input, err
	}
	return input, readLegacySessions(tx, &input)
}

func readLegacySessions(tx *sql.Tx, input *legacyInput) error {
	rows, err := tx.Query(`SELECT id, label, closed_at FROM sessions`)
	if err != nil {
		return fmt.Errorf("reading sessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var session legacySession
		if err := rows.Scan(&id, &session.Label, &session.ClosedAt); err != nil {
			return err
		}
		input.Sessions[id] = session
	}
	return rows.Err()
}

func remintSplitIDs(node layouttree.Node, newID func(string) string) layouttree.Node {
	if node.Type != "split" {
		return node
	}
	node.SplitID = newID("split")
	children := make([]layouttree.Node, len(node.Children))
	for i, child := range node.Children {
		children[i] = remintSplitIDs(child, newID)
	}
	node.Children = children
	return node
}

func renamePane(node layouttree.Node, from, to string) layouttree.Node {
	if node.Type == "pane" && node.PaneID == from {
		node.PaneID = to
		return node
	}
	if len(node.Children) == 0 {
		return node
	}
	children := make([]layouttree.Node, len(node.Children))
	for i, child := range node.Children {
		children[i] = renamePane(child, from, to)
	}
	node.Children = children
	return node
}

func decodeLegacyTree(ws legacyWorkspace, layout legacyLayout, newID func(string) string) (layouttree.Node, error) {
	tree, err := layouttree.DecodeLayout(layout.LayoutJSON)
	if err != nil {
		return tree, fmt.Errorf("legacy workspace %s (%q): its layout does not decode: %w", ws.ID, ws.Title, err)
	}
	tree = remintSplitIDs(tree, newID)
	if err := layouttree.Validate(tree); err != nil {
		return tree, fmt.Errorf("legacy workspace %s (%q): %w", ws.ID, ws.Title, err)
	}
	return tree, nil
}

func (c *convertedWorkspaces) dropPane(workspaceID, paneID, sessionID, reason string) {
	c.Manifest.DroppedPanes = append(c.Manifest.DroppedPanes, setupmigration.DroppedPane{WorkspaceID: workspaceID, PaneID: paneID, SessionID: sessionID, Reason: reason})
}

func (c *convertedWorkspaces) retainedAgents(input legacyInput, ws legacyWorkspace, rank int, tree layouttree.Node) (layouttree.Node, []placementCandidate) {
	var candidates []placementCandidate
	for _, paneID := range layouttree.PaneIDs(tree) {
		pane, hasRow := input.Panes[ws.ID][paneID]
		session, hasSession := input.Sessions[pane.SessionID]
		reason := ""
		switch {
		case !hasRow:
			reason = setupmigration.DropReasonNoPaneRow
		case pane.SessionID == "" || !hasSession:
			reason = setupmigration.DropReasonNoSession
		case session.ClosedAt != "":
			reason = setupmigration.DropReasonSessionClosed
		}
		if reason != "" {
			c.dropPane(ws.ID, paneID, pane.SessionID, reason)
			tree, _ = layouttree.Remove(tree, paneID)
			continue
		}
		candidates = append(candidates, placementCandidate{rank: rank, workspaceID: ws.ID, paneID: paneID, sessionID: pane.SessionID, updatedAt: pane.UpdatedAt})
	}
	return tree, candidates
}

func newestPlacements(candidates []placementCandidate) (map[string]placementCandidate, []placementCandidate) {
	kept := make(map[string]placementCandidate)
	for _, candidate := range candidates {
		current, seen := kept[candidate.sessionID]
		if !seen || candidate.updatedAt > current.updatedAt {
			kept[candidate.sessionID] = candidate
		}
	}
	var dropped []placementCandidate
	for _, candidate := range candidates {
		if kept[candidate.sessionID] != candidate {
			dropped = append(dropped, candidate)
		}
	}
	return kept, dropped
}

func convertLegacyWorkspaces(input legacyInput, newID func(string) string) (convertedWorkspaces, error) {
	var result convertedWorkspaces
	var retained []workspaceConversion
	var candidates []placementCandidate
	for rank, ws := range input.Workspaces {
		layout, ok := input.Layouts[ws.ID]
		if !ok {
			result.Manifest.DroppedWorkspaces = append(result.Manifest.DroppedWorkspaces, setupmigration.DroppedWorkspace{WorkspaceID: ws.ID, Title: ws.Title, Reason: setupmigration.DropReasonEmpty})
			continue
		}
		tree, err := decodeLegacyTree(ws, layout, newID)
		if err != nil {
			return result, err
		}
		tree, found := result.retainedAgents(input, ws, rank, tree)
		candidates = append(candidates, found...)
		retained = append(retained, workspaceConversion{workspace: ws, rank: rank, tree: tree, active: layout.ActivePaneID})
	}
	kept, dropped := newestPlacements(candidates)
	for _, candidate := range dropped {
		winner := kept[candidate.sessionID]
		result.Manifest.DroppedPlacements = append(result.Manifest.DroppedPlacements, setupmigration.DroppedPlacement{
			SessionID: candidate.sessionID, WorkspaceID: candidate.workspaceID, PaneID: candidate.paneID,
			KeptWorkspaceID: winner.workspaceID, KeptPaneID: winner.paneID,
		})
		for i := range retained {
			if retained[i].workspace.ID == candidate.workspaceID {
				retained[i].tree, _ = layouttree.Remove(retained[i].tree, candidate.paneID)
			}
		}
	}
	return result.buildDesktops(input, retained, newID)
}

func (c convertedWorkspaces) buildDesktops(input legacyInput, retained []workspaceConversion, newID func(string) string) (convertedWorkspaces, error) {
	usedPaneIDs := make(map[string]bool)
	placed := make(map[string]bool)
	for _, conversion := range retained {
		ws := conversion.workspace
		if len(layouttree.PaneIDs(conversion.tree)) == 0 {
			reason := setupmigration.DropReasonEmpty
			if len(layouttree.TileIDs(conversion.tree)) > 0 {
				reason = setupmigration.DropReasonDocumentOnly
			}
			c.Manifest.DroppedWorkspaces = append(c.Manifest.DroppedWorkspaces, setupmigration.DroppedWorkspace{WorkspaceID: ws.ID, Title: ws.Title, Reason: reason})
			continue
		}
		slot := 0
		if len(c.Desktops) < setups.LastShortcutSlot {
			slot = len(c.Desktops) + 1
		}
		desktop := setups.Desktop{ID: newID("desktop"), ShortcutSlot: slot, Tree: conversion.tree, ActivePaneID: conversion.active, Revision: 1}
		for _, legacyID := range layouttree.PaneIDs(conversion.tree) {
			pane := input.Panes[ws.ID][legacyID]
			paneID := legacyID
			if usedPaneIDs[paneID] {
				paneID = newID("pane")
				desktop.Tree = renamePane(desktop.Tree, legacyID, paneID)
				if desktop.ActivePaneID == legacyID {
					desktop.ActivePaneID = paneID
				}
				c.Manifest.RenamedPanes = append(c.Manifest.RenamedPanes, setupmigration.RenamedPane{WorkspaceID: ws.ID, From: legacyID, To: paneID})
			}
			usedPaneIDs[paneID] = true
			placed[pane.SessionID] = true
			desktop.Panes = append(desktop.Panes, legacyDesktopPane(desktop.ID, paneID, pane, input.Sessions[pane.SessionID]))
		}
		desktop = setups.Settle(desktop)
		if err := setups.CheckDesktop(desktop); err != nil {
			return c, fmt.Errorf("legacy workspace %s (%q) does not convert to a valid desktop: %w", ws.ID, ws.Title, err)
		}
		c.Desktops = append(c.Desktops, desktop)
		c.Manifest.Groups = append(c.Manifest.Groups, setupmigration.Group{
			ID: ws.ID, Title: ws.Title, Directory: ws.Directory, DesktopID: desktop.ID, ShortcutSlot: slot,
			LeafIDs: append(layouttree.PaneIDs(desktop.Tree), layouttree.TileIDs(desktop.Tree)...),
		})
	}
	for id, session := range input.Sessions {
		if session.ClosedAt == "" && !placed[id] {
			c.Manifest.UnplacedSessions = append(c.Manifest.UnplacedSessions, id)
		}
	}
	sort.Strings(c.Manifest.UnplacedSessions)
	if len(c.Desktops) == 0 {
		c.Desktops = append(c.Desktops, setups.Desktop{ID: newID("desktop"), ShortcutSlot: setups.FirstShortcutSlot, Revision: 1})
	}
	return c, nil
}

func legacyDesktopPane(desktopID, paneID string, pane legacyPane, session legacySession) setups.Pane {
	title := strings.TrimSpace(session.Label)
	if title == "" {
		title = strings.TrimSpace(pane.Title)
	}
	status := setups.PaneStatus(pane.Status)
	switch status {
	case setups.PaneStatusSpawning, setups.PaneStatusReady, setups.PaneStatusFailed:
	default:
		status = setups.PaneStatusReady
	}
	return setups.Pane{PaneID: paneID, DesktopID: desktopID, Kind: setups.PaneKindAgent, SessionID: pane.SessionID, Title: title, Status: status, Error: pane.Error}
}

func writeSetupConversion(tx *sql.Tx, result convertedWorkspaces) error {
	now := time.Now().UTC().Format(sortableTimeFormat)
	setupID := newSetupEntityID("setup")
	result.Manifest.SetupID = setupID
	if _, err := tx.Exec(`
		INSERT INTO setups (id, name, current_desktop_id, last_used_at, revision, created_at, deleted_at)
		VALUES (?, ?, ?, '', 1, ?, '')`, setupID, DefaultSetupName, result.Desktops[0].ID, now); err != nil {
		return fmt.Errorf("creating the Default setup: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET setup_id = ?`, setupID); err != nil {
		return fmt.Errorf("stamping sessions with the Default setup: %w", err)
	}
	tables, err := existingTables(tx, setupStampedTables...)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if _, err := tx.Exec(`UPDATE `+table+` SET setup_id = ?`, setupID); err != nil {
			return fmt.Errorf("stamping %s with the Default setup: %w", table, err)
		}
	}
	orderKeys := rankkey.Seed(len(result.Desktops))
	for i := range result.Desktops {
		desktop := &result.Desktops[i]
		desktop.SetupID = setupID
		desktop.OrderKey = orderKeys[i]
		if _, err := tx.Exec(`
			INSERT INTO desktops (id, setup_id, name, shortcut_slot, order_key, tree_json, active_pane_id, revision, created_at, updated_at)
			VALUES (?, ?, '', ?, ?, '', '', 1, ?, ?)`,
			desktop.ID, setupID, slotValue(desktop.ShortcutSlot), desktop.OrderKey, now, now); err != nil {
			return fmt.Errorf("creating desktop for slot %d: %w", desktop.ShortcutSlot, err)
		}
		desktop.Revision = 0
		if err := writeDesktopArrangement(tx, now, desktop); err != nil {
			return fmt.Errorf("writing the converted desktop %s: %w", desktop.ID, err)
		}
	}
	return recordSetupConversion(tx, result.Manifest)
}

func recordSetupConversion(tx *sql.Tx, manifest setupmigration.Manifest) error {
	phase, draft := setupmigration.PhaseComplete, ""
	if len(manifest.Groups) > 0 {
		phase = setupmigration.PhasePlacementRequired
		encoded, err := setupmigration.EncodePlan(setupmigration.InitialPlan(manifest))
		if err != nil {
			return err
		}
		draft = encoded
	}
	encodedManifest, err := setupmigration.EncodeManifest(manifest)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO setup_migration (id, schema_version, phase, revision, imported_groups, draft)
		VALUES (1, ?, ?, 1, ?, ?)`, SetupConversionSchemaVersion, phase, encodedManifest, draft)
	return err
}
