package attention

import (
	"context"
	"time"
)

type Failure string

const (
	FailureHeadlessOff    Failure = "headless_tasks_off"
	FailureHarness        Failure = "harness"
	FailureUnusableAnswer Failure = "unusable_answer"
)

type Supervisor struct {
	Headless func(context.Context, Facts) (Verdict, error)
}

func (s Supervisor) Judge(ctx context.Context, f Facts) Verdict {
	if v := Rules(f); v.Kind != Unsure {
		return v
	}
	started := time.Now()
	v, err := s.Headless(ctx, f)
	if err != nil {
		v = Verdict{Kind: Quiet, Decider: DeciderNone, Reason: err.Error(), Failure: v.Failure}
	}
	v.Latency = time.Since(started)
	return v
}
