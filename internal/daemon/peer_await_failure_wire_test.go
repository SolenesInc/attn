package daemon_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	failInsideAwaitEnv     = "ATTN_TEST_FAIL_INSIDE_PEER_AWAIT"
	failureInsideAwait     = "the deliberate failure inside the await"
	screenBeforeTheFailure = "the screen before the failure"
)

func failInsidePeerAwait(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	run.Reply(screenBeforeTheFailure + " <!-- attn:state=idle -->")
	app.AwaitScreen(session, screenBeforeTheFailure)
	testworld.AwaitSession(app, session, func(protocol.Session) bool {
		t.Fatal(failureInsideAwait)
		return false
	})
}

func TestAFailureInsideAPeerAwaitEndsTheTestAtOnceWithItsScreensAndDaemonLog(t *testing.T) {
	if os.Getenv(failInsideAwaitEnv) != "" {
		failInsidePeerAwait(t)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	failing := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$")
	failing.Env = append(os.Environ(), failInsideAwaitEnv+"=1")
	output, err := failing.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the failing test was still running after %s:\n%s", fakeagent.HangGuard, output)
	}
	if err == nil {
		t.Fatalf("the failing test passed:\n%s", output)
	}
	for _, want := range []string{failureInsideAwait, "output this peer received for terminal", screenBeforeTheFailure, "daemon.log tail:"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("the failing test's output is missing %q:\n%s", want, output)
		}
	}
}
