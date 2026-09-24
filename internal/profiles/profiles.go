package profiles

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/layouttree"
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
	ChiefSessionID   string
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
	Panes        []Pane
	Revision     int64
}

type Pane struct {
	PaneID    string
	DesktopID string
	Kind      PaneKind
	SessionID string
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
	CodeInvalid         Code = "invalid"
	CodeNotFound        Code = "not_found"
	CodeStaleRevision   Code = "stale_revision"
	CodeNameTaken       Code = "name_taken"
	CodeSlotTaken       Code = "slot_taken"
	CodeLastProfile     Code = "last_profile"
	CodeLastDesktop     Code = "last_desktop"
	CodeProfileDeleted  Code = "profile_deleted"
	CodeCrossProfile    Code = "cross_profile"
	CodeAlreadyPlaced   Code = "already_placed"
	CodeSessionClosed   Code = "session_closed"
	CodeDestinationSame Code = "destination_same"
	CodeUnavailable     Code = "unavailable"
	CodeInternal        Code = "internal"
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

func checkPaneRows(desktop Desktop, inTree map[string]struct{}) (map[string]struct{}, error) {
	sessions := make(map[string]string, len(desktop.Panes))
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
		if strings.TrimSpace(pane.SessionID) == "" {
			return nil, Errorf(CodeInvalid, "desktop %s: pane %s names no session", desktop.ID, pane.PaneID)
		}
		if other, dup := sessions[pane.SessionID]; dup {
			return nil, Errorf(CodeAlreadyPlaced, "desktop %s: session %s is placed in panes %s and %s", desktop.ID, pane.SessionID, other, pane.PaneID)
		}
		sessions[pane.SessionID] = pane.PaneID
		switch pane.Status {
		case PaneStatusSpawning, PaneStatusReady, PaneStatusFailed:
		default:
			return nil, Errorf(CodeInvalid, "desktop %s: pane %s has status %q", desktop.ID, pane.PaneID, pane.Status)
		}
	}
	return rows, nil
}

func checkActivePane(desktop Desktop, inTree map[string]struct{}) error {
	if len(inTree) == 0 {
		if desktop.ActivePaneID != "" {
			return Errorf(CodeInvalid, "desktop %s: active pane %s is set but the desktop has no panes", desktop.ID, desktop.ActivePaneID)
		}
		return nil
	}
	if _, ok := inTree[desktop.ActivePaneID]; !ok {
		return Errorf(CodeInvalid, "desktop %s: active pane %q does not belong to the desktop", desktop.ID, desktop.ActivePaneID)
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
	return checkActivePane(desktop, inTree)
}

func Settle(desktop Desktop) Desktop {
	desktop.Tree = layouttree.Rebalance(desktop.Tree)
	treePanes := layouttree.PaneIDs(desktop.Tree)
	stillThere := false
	for _, id := range treePanes {
		if id == desktop.ActivePaneID {
			stillThere = true
			break
		}
	}
	if !stillThere {
		desktop.ActivePaneID = ""
		if len(treePanes) > 0 {
			desktop.ActivePaneID = treePanes[0]
		}
	}
	return desktop
}
