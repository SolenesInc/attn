package harness

// TerminalEvent is what happened to a terminal, told to every harness; each acts only on its own terminals.
type TerminalEvent interface{ terminalEvent() }

// Started: a session's launch began in a server's view, and Prompted says whether it carries an initial prompt.
type Started struct {
	Session  SessionID
	Prompted bool
}

type Exited struct {
	Code              int
	Signal, Lifecycle string
}

type Dropped struct{}

// Handed: the terminal moved on to a successor session in Profile.
type Handed struct{ Profile string }

// Working: the terminal's program says it works for Session.
type Working struct{ Session SessionID }

// Restarting: the terminal's runtime is replaced in place.
type Restarting struct{}

func (Started) terminalEvent()    {}
func (Exited) terminalEvent()     {}
func (Dropped) terminalEvent()    {}
func (Handed) terminalEvent()     {}
func (Working) terminalEvent()    {}
func (Restarting) terminalEvent() {}
