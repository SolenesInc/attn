package daemon

import (
	"errors"
	"testing"

	attngit "github.com/victorarias/attn/internal/git"
)

func testGitExecutor(t *testing.T, config gitExecutorConfig) *coordinatedGitExecutor {
	t.Helper()
	executor, err := newGitExecutor(config, attngit.NewClient())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { executor.Close(errors.New("test complete")) })
	return executor
}
