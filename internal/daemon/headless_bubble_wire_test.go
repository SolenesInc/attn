package daemon_test

import (
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/fakeagent"
)

func inBubbleAnsweringHeadlessTasks(t *testing.T, agents []fakeagent.Harness, script func(t *testing.T, w *world)) {
	t.Helper()
	prepared := prepareWorld(t, agents...)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true}
		w.start()
		script(t, w)
	})
}
