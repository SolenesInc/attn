package harness

import (
	"context"
	"time"

	"github.com/victorarias/attn/internal/sessioncost"
)

// Turn is where a session's turn stands after a report.
type Turn uint8

const (
	// TurnUnknown is a report that cannot tell, such as one whose harness went quiet.
	TurnUnknown Turn = iota
	// TurnRunning is a turn that started, or went on after an approval or a question.
	TurnRunning
	// TurnApproval waits on the user's approval.
	TurnApproval
	// TurnQuestion waits on the user's answer, asked during the turn or as it ended.
	TurnQuestion
	// TurnEnded leaves the agent at its prompt.
	TurnEnded
	// TurnPromptIdle: the harness says its prompt has waited a while for the user.
	TurnPromptIdle
	// TurnAborted: the turn stopped before it finished.
	TurnAborted
	// TurnFailed: the turn ended in an error.
	TurnFailed
)

// TurnEvent is one report on a session's turn.
type TurnEvent struct {
	Turn Turn
	// Epoch (one launch of the harness) and Seq order the reports of a harness that sets state itself, which can
	// arrive reordered: core drops one from another epoch or not after the last. A report with an epoch sets the state.
	Epoch string
	Seq   uint64
	// Restated repeats the harness's state after a reconnect; core takes it only while it cannot tell.
	Restated bool
	// Inferred: attn read the report from what the harness did not say, so it does not show that a launch started.
	Inferred bool
	// Status: a server's program status, which holds until its next one.
	Status bool
	// Detail is the harness's words for an approval, an aborted turn or a failure.
	Detail string
	// Stop is what the harness said as its turn stopped.
	Stop *Stop
	// Completion counts the server's finished runs, so a run that ended while attn was away is seen once.
	Completion int64
	// Said is the answer the turn ended with, classified for the state it leaves. An answer can have no words, and
	// nil is no answer.
	Said *string
}

// Stop is a turn's end as the harness reported it.
type Stop struct {
	// Left is what the harness's stop hook says it left running.
	Left *Left
	// Yielded: work goes on in the background, so the stop ends no turn.
	Yielded    bool
	Classifies bool
}

// Left is the work a stopped turn left behind.
type Left struct {
	BackgroundWork bool
	PendingCron    bool
}

// Reporter is where an event came from: a session, or a hook's terminal, which can show a conversation that is not
// yet its session's.
type Reporter struct {
	session  SessionID
	terminal TerminalID
}

func FromSession(s SessionID) Reporter { return Reporter{session: s} }

func FromTerminal(t TerminalID) Reporter { return Reporter{terminal: t} }

func (r Reporter) Session() SessionID { return r.session }

func (r Reporter) Terminal() TerminalID { return r.terminal }

// Prompt is a prompt the harness shows in its conversation.
type Prompt struct {
	Text string
	// Live: the prompt reached the harness now, so it counts as the user's submission.
	Live bool
	// History: the prompt is from before this launch, so it can only title the session.
	History bool
	// Titles: attn may title the session from this prompt.
	Titles bool
}

// ToolUse is what the harness's tools did: the markdown files they edited and the pull requests they opened.
type ToolUse struct {
	EditedFiles  []string
	PullRequests []string
}

// Move is a conversation the harness now runs for its reporter.
type Move struct {
	Conversation string
	Path         string
	Run          string
}

// Events is core's side of a harness: everything the harness tells attn arrives here.
type Events interface {
	Turn(r Reporter, at time.Time, e TurnEvent)
	Compacting(r Reporter, active bool, trigger string)
	Prompt(r Reporter, at time.Time, p Prompt)
	Tool(session SessionID, use ToolUse) error
	Usage(session SessionID, source string, observations []sessioncost.Observation)
	Replied(session SessionID)
	Named(session SessionID, title string)
	// Moved returns what to wait on until the move is recorded, or nil when nothing is to wait on.
	Moved(r Reporter, m Move) (wait func(context.Context) error)
}
