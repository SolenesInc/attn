package pty

import (
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

const (
	ProgramWorking           = "working"
	ProgramBlocked           = "blocked"
	ProgramBlockedPermission = "blocked_permission"
	ProgramBlockedQuestion   = "blocked_question"
	ProgramBlockedAuth       = "blocked_auth"
	ProgramDone              = "done"
	ProgramIdle              = "idle"
	ProgramError             = "error"
	ProgramClear             = "clear"
)

type programStatusObserver struct {
	rootReported bool
}

func newProgramStatusObserver(last *Observation) *programStatusObserver {
	return &programStatusObserver{
		rootReported: last != nil && last.Source == SourceProgramStatus && last.Claim != ProgramClear,
	}
}

func (o *programStatusObserver) Observe(reports []ghosttyvt.ProgramStatus, now time.Time) []Observation {
	var out []Observation
	for _, report := range reports {
		if report.ID != "" {
			continue
		}
		claim := programStatusClaim(report)
		o.rootReported = claim != ProgramClear
		out = append(out, newObservation(SourceProgramStatus, claim, report.Message, now))
	}
	return out
}

func (o *programStatusObserver) held() bool {
	return o.rootReported
}

func (o *programStatusObserver) release(now time.Time) Observation {
	o.rootReported = false
	return newObservation(SourceProgramStatus, ProgramClear, "", now)
}

func programStatusClaim(report ghosttyvt.ProgramStatus) string {
	switch report.State {
	case ghosttyvt.ProgramStatusWorking:
		return ProgramWorking
	case ghosttyvt.ProgramStatusBlocked:
		switch report.Kind {
		case ghosttyvt.ProgramStatusKindPermission:
			return ProgramBlockedPermission
		case ghosttyvt.ProgramStatusKindQuestion:
			return ProgramBlockedQuestion
		case ghosttyvt.ProgramStatusKindAuth:
			return ProgramBlockedAuth
		default:
			return ProgramBlocked
		}
	case ghosttyvt.ProgramStatusDone:
		return ProgramDone
	case ghosttyvt.ProgramStatusError:
		return ProgramError
	case ghosttyvt.ProgramStatusClear:
		return ProgramClear
	default:
		return ProgramIdle
	}
}
