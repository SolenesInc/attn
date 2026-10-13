package tendril

import (
	"context"
	"errors"
	"testing"
)

func TestResumeReplaysFinishedHostCalls(t *testing.T) {
	var live []string
	crashOn := "verify"
	errCrash := errors.New("daemon restarted")
	agents := NewPackage("agents").Journaled().
		Func("Run", func(ctx context.Context, role, prompt string) (string, error) {
			if role == crashOn {
				return "", errCrash
			}
			live = append(live, role)
			return role + " done: " + prompt, nil
		})
	env := mustEnv(t, agents)
	src := `
import (
	"agents"
	"fmt"
)

func Plan(task string) (string, error) {
	plan, err := agents.Run("planner", task)
	if err != nil {
		return "", err
	}
	impl, err := agents.Run("implementer", plan)
	if err != nil {
		return "", err
	}
	verdict, err := agents.Run("verify", impl)
	if err != nil {
		return "", fmt.Errorf("verifying: %w", err)
	}
	return verdict, nil
}`
	p := mustCompile(t, env, src)
	j := &Journal{}
	if _, _, err := p.Resume(context.Background(), DefaultLimits, j, "Plan", "fix bug"); !errors.Is(err, errCrash) {
		t.Fatalf("first run: %v", err)
	}
	j.Entries = j.Entries[:len(j.Entries)-1]

	crashOn = ""
	out, _, err := p.Resume(context.Background(), DefaultLimits, j, "Plan", "fix bug")
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0].(string); got != "verify done: implementer done: planner done: fix bug" {
		t.Fatalf("result: %q", got)
	}
	if len(live) != 3 {
		t.Fatalf("planner and implementer ran again on resume: %v", live)
	}

	edited := mustCompile(t, env, `
import "agents"

func Plan(task string) (string, error) {
	return agents.Run("reviewer", task)
}`)
	if _, _, err := edited.Resume(context.Background(), DefaultLimits, j, "Plan", "fix bug"); !errors.Is(err, ErrDiverged) {
		t.Fatalf("edited script replayed a stale journal: %v", err)
	}
}
