package profiles

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

const (
	FirstShortcutSlot = 1
	LastShortcutSlot  = 9
)

type PaneKind string

const PaneKindAgent PaneKind = "agent"

type PaneStatus string

const (
	PaneStatusSpawning PaneStatus = "spawning"
	PaneStatusReady    PaneStatus = "ready"
	PaneStatusFailed   PaneStatus = "failed"
)

type Profile struct {
	ID               string
	Name             string
	CurrentDesktopID string
	LastUsedAt       string
	Revision         int64
	DeletedAt        string
	Chief            who.MemberKey
}

func (s Profile) Deleted() bool { return s.DeletedAt != "" }

type Desktop struct {
	ID           string
	ProfileID    string
	Name         string
	ShortcutSlot int
	OrderKey     string
	Tree         layouttree.Node
	ActivePaneID string
	FocusHistory []string
	Panes        []Pane
	Revision     int64
}

type Pane struct {
	PaneID    string
	DesktopID string
	Kind      PaneKind
	SessionID protocol.SessionID
	// RuntimeID names the terminal the pane holds; it outlives the session shown in it.
	RuntimeID protocol.TerminalID
	Title     string
	Status    PaneStatus
	Error     string
}

type MigrationState struct {
	SchemaVersion  int
	Phase          string
	Revision       int64
	ImportedGroups string
	Draft          string
}

type Placement struct {
	ProfileID string
	DesktopID string
	PaneID    string
}

type Code string

const (
	CodeInvalid        Code = "invalid"
	CodeLastDesktop    Code = "last_desktop"
	CodeNotFound       Code = "not_found"
	CodeStaleRevision  Code = "stale_revision"
	CodeNameTaken      Code = "name_taken"
	CodeSlotTaken      Code = "slot_taken"
	CodeLastProfile    Code = "last_profile"
	CodeProfileDeleted Code = "profile_deleted"
	CodeCrossProfile   Code = "cross_profile"
	CodeAlreadyPlaced  Code = "already_placed"
	CodeSessionClosed  Code = "session_closed"
	CodeUnavailable    Code = "unavailable"
	CodeInternal       Code = "internal"
)

type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string { return e.Message }

func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func Stale(entity, id string, expected, current int64) *Error {
	return Errorf(CodeStaleRevision, "%s %s is at revision %d, the edit was made against revision %d; re-read and retry", entity, id, current, expected)
}

func ValidateName(entity, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", Errorf(CodeInvalid, "%s name is empty", entity)
	}
	return trimmed, nil
}

func ValidateShortcutSlot(slot int) error {
	if slot == 0 || (slot >= FirstShortcutSlot && slot <= LastShortcutSlot) {
		return nil
	}
	return Errorf(CodeInvalid, "shortcut slot %d is outside %d-%d", slot, FirstShortcutSlot, LastShortcutSlot)
}

const numberedDesktopPrefix = "desktop_"

// NumberedDesktopID is the stable id of a profile's desktop on ⌘slot; its number never changes.
func NumberedDesktopID(profileID string, slot int) string {
	return fmt.Sprintf("%s/%s%d", profileID, numberedDesktopPrefix, slot)
}

// DesktopSlot is the ⌘ number a desktop id carries, 0 for an unnumbered desktop.
func DesktopSlot(id string) int {
	suffix := id[strings.LastIndex(id, "/")+1:]
	if !strings.HasPrefix(suffix, numberedDesktopPrefix) {
		return 0
	}
	slot, err := strconv.Atoi(strings.TrimPrefix(suffix, numberedDesktopPrefix))
	if err != nil {
		return 0
	}
	return slot
}

func IsNumberedDesktopID(profileID, id string) bool {
	slot := DesktopSlot(id)
	return slot != 0 && id == NumberedDesktopID(profileID, slot)
}

func checkPaneRows(desktop Desktop, inTree map[string]struct{}) (map[string]struct{}, error) {
	rows := make(map[string]struct{}, len(desktop.Panes))
	for _, pane := range desktop.Panes {
		if _, ok := inTree[pane.PaneID]; !ok {
			return nil, Errorf(CodeInvalid, "desktop %s: pane %s has a row but no leaf in the tree", desktop.ID, pane.PaneID)
		}
		if _, dup := rows[pane.PaneID]; dup {
			return nil, Errorf(CodeInvalid, "desktop %s: pane %s has two rows", desktop.ID, pane.PaneID)
		}
		rows[pane.PaneID] = struct{}{}
		if pane.Kind != PaneKindAgent {
			return nil, Errorf(CodeInvalid, "desktop %s: pane %s has kind %q, want %q", desktop.ID, pane.PaneID, pane.Kind, PaneKindAgent)
		}
		if protocol.TrimID(pane.SessionID) == "" {
			return nil, Errorf(CodeInvalid, "desktop %s: pane %s names no session", desktop.ID, pane.PaneID)
		}
		switch pane.Status {
		case PaneStatusSpawning, PaneStatusReady, PaneStatusFailed:
		default:
			return nil, Errorf(CodeInvalid, "desktop %s: pane %s has status %q", desktop.ID, pane.PaneID, pane.Status)
		}
	}
	return rows, nil
}

func checkActiveLeaf(desktop Desktop) error {
	if layouttree.LayoutEmpty(desktop.Tree) {
		if desktop.ActivePaneID != "" {
			return Errorf(CodeInvalid, "desktop %s: active leaf %s is set but the desktop has no leaves", desktop.ID, desktop.ActivePaneID)
		}
		return nil
	}
	if !layouttree.HasLeaf(desktop.Tree, desktop.ActivePaneID) {
		return Errorf(CodeInvalid, "desktop %s: active leaf %q does not belong to the desktop", desktop.ID, desktop.ActivePaneID)
	}
	return nil
}

func CheckDesktop(desktop Desktop) error {
	if err := layouttree.Validate(desktop.Tree); err != nil {
		return Errorf(CodeInvalid, "desktop %s: %v", desktop.ID, err)
	}
	if err := ValidateShortcutSlot(desktop.ShortcutSlot); err != nil {
		return err
	}
	treePanes := layouttree.PaneIDs(desktop.Tree)
	inTree := make(map[string]struct{}, len(treePanes))
	for _, id := range treePanes {
		inTree[id] = struct{}{}
	}
	rows, err := checkPaneRows(desktop, inTree)
	if err != nil {
		return err
	}
	for _, id := range treePanes {
		if _, ok := rows[id]; !ok {
			return Errorf(CodeInvalid, "desktop %s: leaf %s is in the tree but has no pane row", desktop.ID, id)
		}
	}
	return checkActiveLeaf(desktop)
}

func Settle(desktop Desktop) Desktop {
	return SettleAfter(desktop, layouttree.Node{})
}

func SettleAfter(desktop Desktop, previousTree layouttree.Node) Desktop {
	desktop.Tree = layouttree.Rebalance(desktop.Tree)
	leaves := layouttree.LeafIDs(desktop.Tree)
	live := make(map[string]bool, len(leaves))
	for _, id := range leaves {
		live[id] = true
	}
	history := make([]string, 0, len(desktop.FocusHistory)+1)
	for _, id := range desktop.FocusHistory {
		if live[id] {
			history = append(history, id)
		}
	}
	if !live[desktop.ActivePaneID] {
		if len(history) > 0 {
			desktop.ActivePaneID = history[0]
		} else {
			desktop.ActivePaneID = layouttree.LeftNeighbour(previousTree, desktop.ActivePaneID, live)
			if desktop.ActivePaneID == "" && len(leaves) > 0 {
				desktop.ActivePaneID = leaves[0]
			}
		}
	}
	desktop.FocusHistory = history
	return Focus(desktop, desktop.ActivePaneID)
}

func Focus(desktop Desktop, leafID string) Desktop {
	history := make([]string, 0, len(desktop.FocusHistory)+1)
	if leafID != "" {
		history = append(history, leafID)
	}
	for _, id := range desktop.FocusHistory {
		if id != leafID {
			history = append(history, id)
		}
	}
	desktop.ActivePaneID, desktop.FocusHistory = leafID, history
	return desktop
}
