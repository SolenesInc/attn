package harness

// As returns h as the optional part T, when h has it.
func As[T any](h Harness) (T, bool) {
	part, ok := h.(T)
	return part, ok
}

// Runs is the optional part of a harness that tracks each launch as a run of its own, apart from the terminal.
type Runs interface {
	// RunEnded tells the harness its run ended. It returns at once; a harness that cannot be told is skipped.
	RunEnded(e RunEnd)
	// Route is the input route of a run recorded with none, once the harness can say.
	Route(s SessionFacts) (route string, ok bool)
}

// RunEnd is one run that ended, addressed by the terminal it ran in.
type RunEnd struct {
	Session    SessionID
	Terminal   TerminalID
	Owner, Run string
	Reason     RunEndReason
	ExitCode   *int
	Signal     string
}

type RunEndReason string

const (
	RunKilled       RunEndReason = "killed"
	RunExited       RunEndReason = "exited"
	RunReloaded     RunEndReason = "reloaded"
	RunLaunchFailed RunEndReason = "launch_failed"
	RunReloadFailed RunEndReason = "reload_failed"
)

// Ending is a session whose close committed. Released is the conversation no open session holds any more, or "".
type Ending struct {
	Session  SessionID
	Profile  string
	Released string
}
