package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASampleMeasuresTheProcessThatHoldsATerminal(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	setAnalyticsEnabled(app, "true")
	s.Spawn(app, fakeagent.Harness("shell"), s.Path("notes"))

	s.Advance(sampleInterval + time.Second)
	s.AwaitJobsSettled()
	sample := newestProcessSample(t, app)

	if sample.Workload.Terminals != 1 {
		t.Errorf("sample workload = %+v, want 1 terminal", sample.Workload)
	}
	if sample.roleCount("daemon") != 1 || sample.roleCount("app") != 1 || sample.roleCount("pty_host") != 1 {
		t.Errorf("sample processes = %+v, want the daemon, the app and the host of the terminal", sample.Processes)
	}
	for _, process := range sample.Processes {
		if process.ResidentBytes == 0 {
			t.Errorf("sample of role %s = %+v, want it measured", process.Role, process)
		}
	}
}
