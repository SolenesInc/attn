package workflow

import "fmt"

type ErrDeterminismBan struct {
	API        string
	Substitute string
}

func (e *ErrDeterminismBan) Error() string {
	return fmt.Sprintf("%s is banned in workflows (non-deterministic). %s", e.API, e.Substitute)
}

type ErrAgentCap struct {
	Cap int
}

func (e *ErrAgentCap) Error() string {
	return fmt.Sprintf("workflow exceeded the %d-agent lifetime cap", e.Cap)
}

type ErrTooManyItems struct {
	Construct string
	Count     int
	Max       int
}

func (e *ErrTooManyItems) Error() string {
	return fmt.Sprintf("%s received %d items, exceeding the per-call cap of %d", e.Construct, e.Count, e.Max)
}

type ErrInterrupted struct {
	Reason string
}

func (e *ErrInterrupted) Error() string {
	return e.Reason
}

type ErrWorkflowNotImpl struct{}

func (e *ErrWorkflowNotImpl) Error() string {
	return "workflow() nesting is not implemented in E1"
}

type ErrMeta struct {
	Reason string
}

func (e *ErrMeta) Error() string {
	return fmt.Sprintf("invalid workflow meta: %s", e.Reason)
}
