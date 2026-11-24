package daemon

import "github.com/victorarias/attn/internal/protocol"

const (
	cmdSeedPlant    = "seed_plant"
	cmdSeedPlot     = "seed_plot"
	cmdSeedList     = "seed_list"
	cmdSeedSearch   = "seed_search"
	cmdSeedShow     = "seed_show"
	cmdSeedEdit     = "seed_edit"
	cmdSeedRollback = "seed_rollback"
	cmdSeedReminder = "seed_reminder"
	cmdSeedStandby  = "seed_standby"
	cmdSeedNotes    = "seed_notes"
	cmdSeedWatch    = "seed_watch"
	cmdSeedLink     = "seed_link"
	cmdSeedReady    = "seed_ready"
	cmdSeedBed      = "seed_bed"
	cmdSeedPulse    = "seed_pulse"
)

type seedBedScopeRequest struct {
	Cmd             string              `json:"cmd"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SeedID          string              `json:"seed_id,omitempty"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
}

type seedPlantRequest struct {
	Bed             *string                        `json:"bed,omitempty"`
	Body            *string                        `json:"body,omitempty"`
	CallerDirectory *string                        `json:"caller_directory,omitempty"`
	Checks          []protocol.SeedCheckDefinition `json:"checks,omitempty"`
	Cmd             string                         `json:"cmd"`
	DiscoveredFrom  *string                        `json:"discovered_from,omitempty"`
	PartOf          *string                        `json:"part_of,omitempty"`
	ProfileID       *string                        `json:"profile_id,omitempty"`
	SourceSessionID *protocol.SessionID            `json:"source_session_id,omitempty"`
	Title           string                         `json:"title"`
}

type seedPlotRequest struct {
	Bed             *string                        `json:"bed,omitempty"`
	Body            *string                        `json:"body,omitempty"`
	CallerDirectory *string                        `json:"caller_directory,omitempty"`
	Checks          []protocol.SeedCheckDefinition `json:"checks,omitempty"`
	Children        []seedPlotChild                `json:"children"`
	Cmd             string                         `json:"cmd"`
	DiscoveredFrom  *string                        `json:"discovered_from,omitempty"`
	PartOf          *string                        `json:"part_of,omitempty"`
	ProfileID       *string                        `json:"profile_id,omitempty"`
	SourceSessionID *protocol.SessionID            `json:"source_session_id,omitempty"`
	Title           string                         `json:"title"`
}

type seedListRequest struct {
	Bed                *string             `json:"bed,omitempty"`
	Cmd                string              `json:"cmd"`
	Plot               *string             `json:"plot,omitempty"`
	ProfileID          *string             `json:"profile_id,omitempty"`
	SourceSessionID    *protocol.SessionID `json:"source_session_id,omitempty"`
	Stale              *bool               `json:"stale,omitempty"`
	StaleWindowSeconds *int                `json:"stale_window_seconds,omitempty"`
	Standby            *bool               `json:"standby,omitempty"`
}

type seedSearchRequest struct {
	Cmd             string              `json:"cmd"`
	Limit           *int                `json:"limit,omitempty"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	Query           string              `json:"query"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
}

type seedShowRequest struct {
	Cmd             string              `json:"cmd"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SeedID          string              `json:"seed_id"`
	Since           *int                `json:"since,omitempty"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
}

type seedEditRequest struct {
	BaseBodySHA256  string              `json:"base_body_sha256"`
	Body            string              `json:"body"`
	Cmd             string              `json:"cmd"`
	Note            *string             `json:"note,omitempty"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SeedID          string              `json:"seed_id"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
}

type seedRollbackRequest struct {
	Cmd             string              `json:"cmd"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SeedID          string              `json:"seed_id"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
	Version         *int                `json:"version,omitempty"`
}

type seedReminderRequest struct {
	At              *string             `json:"at,omitempty"`
	Clear           *bool               `json:"clear,omitempty"`
	Cmd             string              `json:"cmd"`
	In              *string             `json:"in,omitempty"`
	Message         *string             `json:"message,omitempty"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SeedID          string              `json:"seed_id"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
}

type seedStandbyRequest struct {
	At              *string             `json:"at,omitempty"`
	Clear           *bool               `json:"clear,omitempty"`
	Cmd             string              `json:"cmd"`
	In              *string             `json:"in,omitempty"`
	Note            *string             `json:"note,omitempty"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SeedID          string              `json:"seed_id"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
	Stay            *bool               `json:"stay,omitempty"`
}

type seedNotesRequest struct {
	Cmd             string              `json:"cmd"`
	Kind            *string             `json:"kind,omitempty"`
	Limit           *int                `json:"limit,omitempty"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SeedID          string              `json:"seed_id"`
	Since           *int                `json:"since,omitempty"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
}

type seedWatchRequest struct {
	Cmd             string             `json:"cmd"`
	ProfileID       *string            `json:"profile_id,omitempty"`
	SeedID          string             `json:"seed_id"`
	SourceSessionID protocol.SessionID `json:"source_session_id"`
	Unwatch         *bool              `json:"unwatch,omitempty"`
}

type seedLinkRequest struct {
	Cmd             string              `json:"cmd"`
	Kind            string              `json:"kind"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SeedID          string              `json:"seed_id"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
	ToSeedID        string              `json:"to_seed_id"`
	Unlink          *bool               `json:"unlink,omitempty"`
}

type seedReadyRequest struct {
	All             *bool               `json:"all,omitempty"`
	Cmd             string              `json:"cmd"`
	Plot            *string             `json:"plot,omitempty"`
	ProfileID       *string             `json:"profile_id,omitempty"`
	SourceSessionID *protocol.SessionID `json:"source_session_id,omitempty"`
}

type seedPlotChild struct {
	After  []string                       `json:"after,omitempty"`
	Body   *string                        `json:"body,omitempty"`
	Checks []protocol.SeedCheckDefinition `json:"checks,omitempty"`
	Title  string                         `json:"title"`
}
