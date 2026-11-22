package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
	"time"

	"github.com/victorarias/attn/internal/modeltiers"
)

// ID names a harness: a built-in's name, or the agent a plugin registered.
type ID = string

// Harness is one adapter: everything attn knows about a harness and asks of it.
type Harness interface {
	Facts() Facts
	Health(ctx context.Context) Health
	Models(ctx context.Context) (Models, error)
	// Plan decides, at the home and before anything starts, where the launch's conversation runs.
	Plan(req LaunchRequest) (Planned, error)
	Launch(ctx context.Context, req LaunchRequest) (Command, error)
	Conversation(s SessionFacts) Conversation
	// End runs once after a close commits, for a session whose conversation lives in the harness's server.
	End(ctx context.Context, e Ending)
	Terminal(t TerminalID, e TerminalEvent)
	Start(ctx context.Context) error
	Recover(ctx context.Context, point RecoverPoint)
	SettingChanged(key string)
	// Released: no session of profile uses the harness's server any more, unless one still does.
	Released(profile string)
	Stop()
	Conversations() ConversationStore
}

// LaunchRequest is one launch of a session's harness.
type LaunchRequest struct {
	Reason   Reason
	Session  SessionID
	Terminal TerminalID
	// Lifetime, for Plan, is the one stored for an open session the launch brings back, or "" for a session that starts. For Launch it is what Plan decided.
	Lifetime      Lifetime
	Profile       string
	Dir           string
	Label         string
	Cols, Rows    uint16
	Conversation  string
	Picker        bool
	InitialPrompt string
	Options       LaunchOptions
	Executable    string
	Unattended    launchcontract.UnattendedLaunchSpec
	Guidance      prompts.Launch
	AwarenessDirs []string
	Trust         *RepositoryTrust
	Note          json.RawMessage
	Direct        bool
}

// Reason is why a terminal launches the harness.
type Reason string

const (
	Start  Reason = "start"
	Reload Reason = "reload"
)

// LaunchOptions are the options a launch asks of the harness.
type LaunchOptions struct {
	Yolo        bool
	AutoApprove bool
	Model       string
	Effort      string
	Target      string
	ContextCap  int
}

// RepositoryTrust names the repositories whose folders an automation may trust for the harness.
type RepositoryTrust struct {
	KnownOrigins []string
}

// Command is what the terminal runs. Dir is the folder the harness asks for, or "" for the session's.
type Command struct {
	Argv, Env    []string
	Dir          string
	LoginShell   bool
	Executable   string
	TitleSignals TitleKind
}

// FixableError is a launch refusal the user can fix in the named folder, as an automation shows it.
type FixableError struct {
	Message string
	Fix     Fix
}

type Fix struct {
	Harness ID
	Dir     string
}

func (e *FixableError) Error() string { return e.Message }

// Keeper is the optional part of a harness whose conversations attn keeps copies of.
type Keeper interface {
	// ConversationFiles maps each conversation to its transcript first, then the files that travel with it.
	ConversationFiles(ids []string) map[string][]string
	// ClassifyFile says what a file, named relative to the harness home, is to a conversation's copy.
	ClassifyFile(id, name string) KeptFile
}

// KeptFile is what a file is to a kept conversation.
type KeptFile uint8

const (
	KeptFileNone KeptFile = iota
	KeptFileMain
	KeptFileAuxiliary
)

// Trust is the optional part of a harness that can trust a folder ahead of a launch there.
type Trust interface {
	TrustFolder(dir string) error
}

// Planned is what Plan decided for a launch.
type Planned struct {
	Lifetime Lifetime
}

// RecoverPoint is a step of the daemon's start at which a harness brings back what it ran before.
type RecoverPoint uint8

const (
	// RecoverProcesses: the terminals are back, and sessions are not reconciled yet.
	RecoverProcesses RecoverPoint = iota
	// RecoverViews: sessions are reconciled, and their monitors are not started yet.
	RecoverViews
	// RecoverMonitors: startup recovery is over.
	RecoverMonitors
)

// ConversationStore is where a harness keeps its conversations.
type ConversationStore interface {
	// Check answers for each conversation whether a resume could pick it up.
	Check(ctx context.Context, refs []ConversationRef) map[ConversationRef]Availability
	// Prepare readies a conversation for a resume; ready means a resume can read it now.
	Prepare(ctx context.Context, ref ConversationRef) (ready bool, err error)
	// Admit refuses a conversation that another profile or harness holds open.
	Admit(ref ConversationRef) error
	// Page reads one page of a conversation's events from cursor.
	Page(ctx context.Context, ref ConversationRef, cursor string, limit int) (transcript.EventPage, error)
	// LastReply is the conversation's last assistant message, cut to its last max characters.
	LastReply(ctx context.Context, ref ConversationRef, max int) (string, error)
	// Window is the conversation's recent assistant messages, and whether it has any to read.
	Window(ctx context.Context, ref ConversationRef) (AssistantWindow, bool)
	// Activity answers the activity line's reads: kind is head, modified or events.
	Activity(ctx context.Context, ref ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error)
}

// AssistantWindow is a conversation's recent assistant messages.
type AssistantWindow struct {
	Status   protocol.SessionMessageWindowStatus
	Messages []transcript.AssistantMessage
	Report   transcript.AssistantWindowReport
	Detail   string
}

// ConversationRef names one conversation of a session.
type ConversationRef struct {
	Harness    ID
	Session    SessionID
	Profile    string
	Dir        string
	DirMissing bool
	ID         string
	Lifetime   Lifetime
}

// Availability is whether a resume could pick a conversation up, and why not.
type Availability struct {
	Resumable   bool
	Reason      string
	CheckFailed bool
	OutpostLost bool
}

// Facts are what a harness is, constant for a built-in and fixed by the registration for a plugin.
type Facts struct {
	ID          ID
	DisplayName string
	Options     Options
	Executable  Executable
	SkillDirs   func(lookupEnv func(string) string, homeDir string) ([]string, error)
	CacheTTL    time.Duration
	Tiers       map[modeltiers.Tier]ModelChoice
	Advisor     ModelChoice
	CrewModel   string
	Signals     Signals
	Recovery    Recovery
	// ModelDiscovery is whether the harness can list the models it offers.
	ModelDiscovery bool
	// Activity is whether attn can read the harness's conversations to write activity lines.
	Activity bool
	// ServerTerminal is the terminal a session's monitor is anchored on while its conversation runs in the harness's server. Nil for a harness with no server.
	ServerTerminal func(profile string) TerminalID
}

// Lifetime is where a session's conversation runs: in its terminal's process, or in a server that outlives the terminal.
type Lifetime string

const (
	InTerminal Lifetime = "terminal"
	InServer   Lifetime = "server"
)

// Recovery is who keeps a session whose terminal is lost: attn, from the terminal's last claim, or the harness's server.
type Recovery uint8

const (
	ByTerminal Recovery = iota
	ByServer
)

// Signals are what a harness tells attn about its turns, and how long its terminal's signals stay fresh.
type Signals struct {
	TurnStarts     TurnStarts
	StateAuthority StateAuthority
	// Heartbeat is how long the terminal's last sign of work counts as working.
	Heartbeat time.Duration
	// ReviewerDwell holds a settled state while a reviewer in the loop may still answer.
	ReviewerDwell time.Duration
	// ReportsTools: the harness reports its tool use, so the agent is not asked to report its pull requests.
	ReportsTools bool
	// ConfirmsFirstTurn: an automation waits for the harness's first report before it counts as delivered.
	ConfirmsFirstTurn bool
	// Titles is which of attn's title parsers reads the terminal's titles.
	Titles TitleKind
}

// TitleKind names one of attn's terminal-title parsers; TitlesNone reads no titles.
type TitleKind string

const (
	TitlesNone   TitleKind = ""
	TitlesClaude TitleKind = "claude-titles"
	TitlesCodex  TitleKind = "codex-titles"
)

// TurnStarts says whether the harness announces a turn's start or attn infers it.
type TurnStarts uint8

const (
	TurnStartsInferred TurnStarts = iota
	TurnStartsReported
)

// StateAuthority says who sets a session's state while its harness runs.
type StateAuthority uint8

const (
	StateByResolver StateAuthority = iota
	StateByHarness
)

// ModelChoice is a model and the effort it runs at.
type ModelChoice struct {
	Model  string
	Effort string
}

// Executable is how attn finds a harness's program when no setting names one: an env variable, then a default name. Empty for a harness that resolves its own.
type Executable struct {
	Default string
	Env     string
}

// Options are the launch options a harness takes.
type Options struct {
	Resume             bool
	Yolo               bool
	AutoApprove        bool
	InitialPrompt      bool
	LaunchInstructions bool
	ModelPin           bool
	EffortPin          bool
	ContextCap         bool
	Picker             bool
	Provider           bool
}

// Request is the launch options a caller asks of a harness.
type Request struct {
	InitialPrompt bool
	Model         bool
	Effort        bool
	Yolo          bool
	AutoApprove   bool
	ContextCap    bool
}

// Accept refuses the first option in r that the harness does not take.
func (f Facts) Accept(r Request) error {
	for _, option := range []struct {
		asked, taken bool
		refusal      string
	}{
		{r.InitialPrompt, f.Options.InitialPrompt, "does not support initial prompts"},
		{r.Model, f.Options.ModelPin, "does not support model pins"},
		{r.Effort, f.Options.EffortPin, "does not support effort pins"},
		{r.Yolo, f.Options.Yolo, "does not support yolo launches"},
		{r.AutoApprove, f.Options.AutoApprove, "does not support automation automatic approval"},
		{r.ContextCap, f.Options.ContextCap, "takes no context-window cap"},
	} {
		if option.asked && !option.taken {
			return fmt.Errorf("agent %q %s", f.ID, option.refusal)
		}
	}
	return nil
}

// Models are the models a harness offers now, with a sentence on where the list comes from.
type Models struct {
	List   []protocol.HarnessModel
	Detail string
}

// Installation says whether the harness can run on this machine now.
type Installation uint8

const (
	// Unknown: attn cannot tell yet, as while the login environment is captured.
	Unknown Installation = iota
	Installed
	Missing
)

// Health is whether the harness can start a session now, and whether attn offers it for new ones.
type Health struct {
	Installation Installation
	Offered      bool
	Executable   string
	Reason       string
}
