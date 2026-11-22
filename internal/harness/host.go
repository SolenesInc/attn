package harness

import (
	"context"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/transcript"
)

// Host is what attn gives an adapter that runs its own processes: a lifetime, the session reads and writes it needs, and terminals of its own.
type Host interface {
	Life() Life
	Logf(format string, args ...any)
	Events() Events
	Setting(s *setting.Setting) string
	// LoginEnv is the latest login-shell environment attn read, without waiting for a fresh one.
	LoginEnv() []string
	// ServerTools are attn's tools for the agents of a server holding subject, and whether attn offers them.
	ServerTools(subject string) (AgentTools, bool)
	// TerminalTools are attn's tools for the agent in terminal t, and whether attn offers them.
	TerminalTools(t TerminalID) (AgentTools, bool)
	// ExecutableHealth reports whether the harness's executable is installed on this machine, without waiting for the login environment.
	ExecutableHealth(facts Facts, offered bool) Health
	Sessions() HostSessions
	// Files reads the conversations a harness writes as transcript files.
	Files() FileConversations
	Terminals() HostTerminals
	Records() ServerRecords
}

type Life interface {
	// Go runs fn until attn stops, or reports false when attn is stopping.
	Go(name string, fn func()) bool
	Context() context.Context
}

type AgentTools struct {
	URL, Credential                 string
	Name, Description, Instructions string
}

type HostSessions interface {
	Get(id SessionID) *protocol.Session
	List() []*protocol.Session
	Holding(conversation string) []SessionID
	LaunchedAt(id SessionID) time.Time
	Lifetime(id SessionID) (ID, Lifetime)
	Conversation(id SessionID) string
	// Remote: the session runs on another machine, so its conversation is not this machine's to run.
	Remote(id SessionID) bool
	PrimaryTerminal(id SessionID) TerminalID
	// Evidence is what the session's monitor holds, and false when no monitor watches the session.
	Evidence(id SessionID) (SessionEvidence, bool)
	// TraceClaim records the status a server claimed for a session; quiet claims come from the server's silence, not a report.
	TraceClaim(id SessionID, quiet bool, status string, at time.Time)
	// BeginLaunch holds the reports of run until its launch is committed, so a report that arrives while the harness starts is applied after it.
	BeginLaunch(id SessionID, owner, run, route string)
}

type SessionEvidence struct {
	TurnOpen, ToolOpen, Compacting bool
	// TurnEnded: the session's prompt is ready for input.
	TurnEnded bool
	// ObservedCompletion is the last of the server's finished runs the session saw.
	ObservedCompletion int64
}

// HostTerminals are the adapter's own terminals. attn records each one as the adapter's, so restarts and prunes keep it.
type HostTerminals interface {
	Live(ctx context.Context) map[TerminalID]struct{}
	// Start runs spec in a terminal of the adapter's, with the login-shell environment and without the caller's identity.
	Start(ctx context.Context, spec TerminalSpec) error
	// Adopt records a running terminal as the adapter's.
	Adopt(ctx context.Context, t TerminalID)
	Owned(t TerminalID) bool
	// Remove ends the terminal and forgets it before returning.
	Remove(ctx context.Context, t TerminalID) error
}

type TerminalSpec struct {
	ID         TerminalID
	Label      string
	Dir        string
	Executable string
	Argv       []string
	Env        []string
}

type ServerRecords interface {
	TerminalViews(kind string) ([]TerminalView, error)
	SaveTerminalView(v TerminalView) error
	DeleteTerminalView(t TerminalID) error
	Feed(id SessionID) FeedRecord
	SaveFeedEntry(id SessionID, entry int64)
	SaveUsageBaseline(id SessionID, baseline string)
	ConversationSeen(conversation string) bool
}

type TerminalView struct {
	Terminal TerminalID
	Kind     string
	Profile  string
}

type FeedRecord struct {
	Entry         int64
	UsageBaseline string
	Completion    int64
}

// FileConversations reads conversations from the transcript files attn located for their sessions.
type FileConversations interface {
	Page(ctx context.Context, ref ConversationRef, cursor string, limit int) (transcript.EventPage, error)
	LastReply(ctx context.Context, ref ConversationRef, max int) (string, error)
	Window(ctx context.Context, ref ConversationRef) (AssistantWindow, bool)
	Activity(ctx context.Context, ref ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error)
}
