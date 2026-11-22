package harness

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
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
	SpawnRoutingEnv() []string
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
	RunInShellTile(ctx context.Context, profile, desktop, label, command string) (ran bool, err error)
	Records() ServerRecords
	Clock() Clock
	// AgentSockets: attn serves the sockets its agents reach it on. Only tests turn them off.
	AgentSockets() bool
	// Executable resolves the harness's executable on this machine: configured, else its setting, else its login-environment variable, else its default.
	Executable(facts Facts, configured string) (string, error)
	RunBinary(ctx context.Context, binary string, args ...string) ([]byte, error)
	RegisterCron(kind string, interval time.Duration, runOnStart bool, fn func(context.Context) error) (enqueueNow func())
	Notifications() HostNotifications
	Profiles() HostProfiles
	HeadlessRefused(task string) bool
	RegisterClientCommand(cmd string, blocksDuringRecovery bool, fn func(profile string, msg any) any)
	RegisterSocketCommand(cmd string, fn func(ctx context.Context, msg any) protocol.Response)
	RegisterCatalogCommand(name string, handler command.Handler)
	RegisterProfileSnapshot(s ProfileSnapshot) (changed func(profiles ...string))
	RepositoryOrigin(repository string) string
	DataDir() string
	DaemonSocket() string
}

type Life interface {
	// Go runs fn until attn stops, or reports false when attn is stopping.
	Go(name string, fn func()) bool
	// Do runs fn on the calling goroutine unless attn is stopping, and reports whether it ran.
	Do(name string, fn func()) bool
	// Hold admits work running on a goroutine attn did not start; false means attn is stopping.
	Hold(name string) (release func(), ok bool)
	Context() context.Context
}

// Clock makes timers a test can step by name.
type Clock interface {
	NewTimer(name stepclock.Name, d time.Duration) stepclock.Timer
	AfterFunc(name stepclock.Name, d time.Duration, f func()) stepclock.Timer
}

type AgentTools struct {
	URL, Credential                 string
	Name, Description, Instructions string
}

type HostSessions interface {
	Get(id SessionID) *protocol.Session
	List() []*protocol.Session
	Holding(conversation string) []SessionID
	Holder(profile, conversation string) SessionID
	StoredConversation(id SessionID) (conversation, path string)
	ConversationOwner(session SessionID, conversation string) SessionID
	LedgerProfile(id SessionID) (string, bool)
	LaunchedAt(id SessionID) time.Time
	Lifetime(id SessionID) (ID, Lifetime)
	Conversation(id SessionID) string
	Hidden(id SessionID) bool
	MirrorLabel(id SessionID)
	StartedDirectories(profile string) []string
	LaunchInfo(id SessionID, agent ID) SessionLaunchInfo
	Instructions(id SessionID, agentTools bool) string
	Reload(id SessionID, cols, rows int) error
	LaunchUnconfirmed(id SessionID, reason string)
	EnsureTranscriptWatcher(id SessionID, path string)
	FinishUsage(id SessionID)
	// Remote: the session runs on another machine, so its conversation is not this machine's to run.
	Remote(id SessionID) bool
	PrimaryTerminal(id SessionID) TerminalID
	// Showing is the session terminal t shows.
	Showing(t TerminalID) (SessionID, bool)
	// ChannelUp asks the machine that runs the session whether its harness's channel reaches it.
	ChannelUp(id SessionID) bool
	// Evidence is what the session's monitor holds, and false when no monitor watches the session.
	Evidence(id SessionID) (SessionEvidence, bool)
	// TraceClaim records the status a server claimed for a session; quiet claims come from the server's silence, not a report.
	TraceClaim(id SessionID, quiet bool, status string, at time.Time)
	TraceServerStatus(id SessionID, turn Turn, at time.Time)
	// BeginLaunch holds the reports of run until its launch is committed, so a report that arrives while the harness starts is applied after it.
	BeginLaunch(id SessionID, owner, run, route string)
}

type NoShellMarksError struct {
	Shell string
}

func (e NoShellMarksError) Error() string {
	return "shell " + e.Shell + " gives attn no command marks"
}

type ShellExitError struct {
	Code int
}

func (e ShellExitError) Error() string {
	return fmt.Sprintf("command exited %d", e.Code)
}

type SessionLaunchInfo struct {
	ContextCap    int
	ApprovalRoute launchcontract.ApprovalRoute
	CrewBound     bool
	AwarenessDirs []string
}

type SessionEvidence struct {
	TurnOpen, ToolOpen, Compacting bool
	// TurnEnded: the session's prompt is ready for input.
	TurnEnded bool
	// ObservedCompletion is the last of the server's finished runs the session saw.
	ObservedCompletion    int64
	TerminalClaimsWorking bool
	FollowsServer         bool
}

// HostTerminals are the adapter's own terminals. attn records each one as the adapter's, so restarts and prunes keep it.
type HostTerminals interface {
	Live(ctx context.Context) map[TerminalID]struct{}
	LocalLive(ctx context.Context) map[TerminalID]struct{}
	// Start runs spec in a terminal of the adapter's, with the login-shell environment and without the caller's identity.
	Start(ctx context.Context, spec TerminalSpec) error
	// Adopt records a running terminal as the adapter's.
	Adopt(ctx context.Context, t TerminalID)
	Owned(t TerminalID) bool
	Info(ctx context.Context, t TerminalID) (TerminalInfo, bool, error)
	Kill(ctx context.Context, t TerminalID, sig syscall.Signal) error
	// Remove ends the terminal and forgets it before returning.
	Remove(ctx context.Context, t TerminalID) error
}

type TerminalInfo struct {
	Running    bool
	PID        int
	Cols       uint16
	Rows       uint16
	Executable string
}

type TerminalSpec struct {
	ID         TerminalID
	Agent      string
	Label      string
	Dir        string
	Executable string
	Argv       []string
	Env        []string
}

type HostNotifications interface {
	List() ([]Notification, error)
	Ensure(n Notification) error
	Resolve(id string) error
}

type Notification struct {
	ID         string
	Kind       string
	Severity   string
	Title      string
	Body       string
	Trigger    string
	Impact     string
	Actions    []NotificationAction
	SourceKind string
	SourceID   string
}

type NotificationAction struct {
	Kind  string
	Label string
}

type HostProfiles interface {
	List() ([]string, error)
	Resolve(caller SessionID, requested string) (id, name string, err error)
}

type ProfileSnapshot struct {
	Fact     string
	Snapshot string
	Initial  func(profile string, msg *protocol.InitialStateMessage)
	Changed  func(profile string) any
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
	// Path is the transcript file of ref's session, or "" when attn has none to read.
	Path(ref ConversationRef) string
	// Check says which of refs can resume: resumable reads the harness's own storage, and attn's kept copies count too.
	Check(refs []ConversationRef, resumable func(id string) bool) map[ConversationRef]Availability
	// Ready restores attn's kept copy of ref when the harness lost it, and reports whether ref can resume.
	Ready(ref ConversationRef, resumable func(id string) bool) bool
}
