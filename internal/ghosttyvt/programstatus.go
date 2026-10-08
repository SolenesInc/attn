package ghosttyvt

type ProgramStatusState int

const (
	ProgramStatusIdle ProgramStatusState = iota
	ProgramStatusWorking
	ProgramStatusDone
	ProgramStatusBlocked
	ProgramStatusError
	ProgramStatusClear
)

type ProgramStatusKind int

const (
	ProgramStatusKindNone ProgramStatusKind = iota
	ProgramStatusKindPermission
	ProgramStatusKindQuestion
	ProgramStatusKindAuth
)

type ProgramStatus struct {
	State   ProgramStatusState
	Kind    ProgramStatusKind
	ID      string
	Message string
}
