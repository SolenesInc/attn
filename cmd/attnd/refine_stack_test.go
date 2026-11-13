package main_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPairedSenderRefinesDictationThroughTheCLI(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_HEADLESS_TASKS=on")
	s.Start()
	token := pairSender(t, s, "Lens")
	profile := s.TrustedApp().SelectedProfile()

	refining := s.Launch(testworld.Invocation{Args: []string{"refine", "--token-stdin", "--duration-ms", "7616", "--json"}, Stdin: token + "\num so deploy\nto staging"})
	task := s.HeadlessTask()
	if !strings.Contains(task.Prompt, `{"transcript":"um so deploy\nto staging"}`) || !strings.Contains(task.Prompt, "plain text field") {
		t.Fatalf("attn refine sent the prompt %q, want the transcript after the token line, for plain text", task.Prompt)
	}
	task.Answer("Deploy to staging.")
	refined := refining.Wait()
	var keys jsonKeys
	refined.JSON(t, &keys)
	if refined.Code != 0 || !reflect.DeepEqual(keys.sorted(), []string{"profile_id", "refined_text", "success"}) ||
		string(keys["refined_text"]) != `"Deploy to staging."` || string(keys["profile_id"]) != `"`+profile+`"` {
		t.Fatalf("attn refine --json = %+v, want the refined text for profile %s", refined, profile)
	}

	plain := s.Launch(testworld.Invocation{Args: []string{"refine", "--token-stdin", "--format", "markdown"}, Stdin: token + "\nfirst point second point"})
	task = s.HeadlessTask()
	if !strings.Contains(task.Prompt, "Markdown document") {
		t.Fatalf("attn refine --format markdown sent the prompt %q", task.Prompt)
	}
	task.Answer("- First point\n- Second point")
	if got := plain.Wait(); got.Code != 0 || got.Stdout != "- First point\n- Second point\n" {
		t.Fatalf("attn refine = %+v, want the refined text alone on stdout", got)
	}

	expiring := s.Launch(testworld.Invocation{Args: []string{"refine", "--token-stdin", "--duration-ms", "40000", "--json"}, Stdin: token + "\ndeploy to staging"})
	s.AwaitTimer(daemon.DictationRefineDeadlineTimer)
	slow := s.HeadlessTask()
	s.Advance(25 * time.Second)
	expired := expiring.Wait()
	expired.JSON(t, &keys)
	if expired.Code != 1 || string(keys["limit_ms"]) != "25000" || !strings.Contains(string(keys["error"]), "within 25 s for this 40 s recording") {
		t.Fatalf("attn refine past the limit = %+v, want exit 1 with the error and limit_ms 25000", expired)
	}
	slow.AwaitExited()
	slow.Answer("Deploy to staging.")

	killed := s.Launch(testworld.Invocation{Args: []string{"refine", "--token-stdin"}, Stdin: token + "\nnever mind"})
	abandoned := s.HeadlessTask()
	killed.Kill()
	abandoned.AwaitExited()
	abandoned.Answer("Never mind.")

	for _, stranger := range []string{"not-a-paired-token", ""} {
		sent := s.Run(testworld.Invocation{Args: []string{"send", "sessions", "--token-stdin"}, Stdin: stranger + "\n"})
		refused := s.Run(testworld.Invocation{Args: []string{"refine", "--token-stdin", "--json"}, Stdin: stranger + "\nhello"})
		sendError := strings.TrimPrefix(lastLine(sent.Stderr), "send sessions: ")
		if refused.Code != sent.Code || refused.Stdout != "" || lastLine(refused.Stderr) != "refine: "+sendError {
			t.Errorf("attn refine with token %q = %+v; attn send refuses it with %+v", stranger, refused, sent)
		}
	}
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return lines[len(lines)-1]
}
