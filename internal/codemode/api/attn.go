//go:build ignore

// Package attn is attn's API for code mode: one Go program that calls attn's
// tools directly, instead of one attn command per step. Every function acts
// as the session that ran the program, with that session's garden and
// permissions.
//
// A program is a main package. It may import this package, fmt, strings,
// strconv, errors, regexp, path, math, slices, sync, time, log, os (Exit
// only) and golang.org/x/sync/errgroup. Output printed with fmt goes to the
// caller; log and println go to its stderr. Calls from separate goroutines
// run in parallel.
package attn

// A Seed is a ticket in the garden.
type Seed struct {
	ID    string
	Title string
	Body  string
	// State is planted, growing, parked, harvested or withered.
	State string
	// Tender is the session or crew member that claims the seed, or "" when
	// nobody does.
	Tender string
	// Plot is the id of the plot the seed belongs to, or "" for a seed at the
	// top of the garden.
	Plot string
	// Ready reports that no open seed blocks this one.
	Ready bool
}

// A Note is one entry in a seed's log.
type Note struct {
	// Author is the session or crew member that wrote the note.
	Author string
	Body   string
	// At is when the note was written, in RFC 3339.
	At string
}

// PlantOptions holds the optional fields of a new seed.
type PlantOptions struct {
	// Body is the seed's markdown description.
	Body string
	// Plot plants the seed as a child of this plot.
	Plot string
}

// An Agent is a live session.
type Agent struct {
	ID   string
	Name string
	// Harness is the agent CLI: claude, codex, copilot or pi.
	Harness string
	// State is launching, working, waiting, needs_approval, idle or unknown.
	State     string
	Directory string
}

// Me returns the id of the session running the program.
func Me() string

// Plant creates a seed in the garden and returns it.
func Plant(title string, opts PlantOptions) (Seed, error)

// Show returns a seed and the latest notes in its log, oldest first.
func Show(id string) (Seed, []Note, error)

// Seeds returns the open seeds: planted, growing and parked.
func Seeds() ([]Seed, error)

// Search returns at most limit seeds whose title, body or notes match query.
func Search(query string, limit int) ([]Seed, error)

// AddNote appends a note to a seed's log.
func AddNote(id, body string) error

// Tend claims a seed for the calling session and makes it growing.
func Tend(id string) (Seed, error)

// Park puts a seed on hold and releases its claim. The reason goes in its log.
func Park(id, reason string) (Seed, error)

// Harvest closes a seed as done. The summary goes in its log.
func Harvest(id, summary string) (Seed, error)

// Agents returns the live agents in the caller's profile.
func Agents() ([]Agent, error)

// Message sends body to an agent's inbox. to is a session id or an agent
// name.
func Message(to, body string) error
