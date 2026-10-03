package harness

import "time"

// Turn is where a session's turn stands after a link's report.
type Turn uint8

const (
	// TurnUnknown is a link that cannot tell, such as one whose harness went quiet.
	TurnUnknown Turn = iota
	// TurnRunning is a turn that started, or went on after an approval or a question.
	TurnRunning
	// TurnApproval waits on the user's approval.
	TurnApproval
	// TurnQuestion waits on the user's answer, asked during the turn or as it ended.
	TurnQuestion
	// TurnEnded leaves the agent at its prompt.
	TurnEnded
)

// TurnEvent is one report on a session's turn. Epoch (one launch of the harness) and Seq order
// a link's reports that can arrive reordered: core drops one from another epoch or not after the last.
type TurnEvent struct {
	Turn  Turn
	Epoch string
	Seq   uint64
	// Restated repeats the harness's state after a reconnect; core takes it only while it cannot tell.
	Restated bool
}

// Events is core's side of a link: what the harness tells attn, addressed by session.
type Events interface {
	Turn(session string, at time.Time, e TurnEvent)
}
