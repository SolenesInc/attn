package tendril

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/victorarias/attn/internal/automation"
)

type Verdict struct {
	Launch bool
	Skip   string
	Effort string
	Prompt string
}

var errRateLimited = errors.New("github rate limited")

func reviewGateEnv(t testing.TB, files map[int][]string) *Env {
	attn := NewPackage("attn").
		Type("PullRequestInput", automation.PullRequestInput{}).
		Type("Verdict", Verdict{}).
		Func("ChangedFiles", func(ctx context.Context, repo string, number int) ([]string, error) {
			if number == 13 {
				return nil, errRateLimited
			}
			return files[number], nil
		})
	return mustEnv(t, attn)
}

func TestReviewGateExample(t *testing.T) {
	src, err := os.ReadFile("testdata/examples/review_gate.go")
	if err != nil {
		t.Fatal(err)
	}
	env := reviewGateEnv(t, map[int][]string{
		7: {"README.md", "internal/pty/wirefeed.go"},
		8: {"docs/glossary.md"},
	})
	prog, err := env.Compile("review_gate.go", string(src))
	if err != nil {
		t.Fatal(err)
	}
	pr := automation.PullRequestInput{Owner: "victorarias", Repository: "attn", URL: "https://github.com/victorarias/attn/pull/7", Number: 7}

	out, stats, err := prog.Call(context.Background(), DefaultLimits, "Gate", pr)
	if err != nil {
		t.Fatal(err)
	}
	v := out[0].(Verdict)
	if !v.Launch || v.Prompt != "Review https://github.com/victorarias/attn/pull/7. Take extra care with internal/pty/wirefeed.go." {
		t.Fatalf("verdict: %+v", v)
	}
	t.Logf("verdict %+v (fuel %d, memory %d)", v, stats.Fuel, stats.Memory)

	pr.Number = 8
	out, _, _ = prog.Call(context.Background(), DefaultLimits, "Gate", pr)
	if v := out[0].(Verdict); v.Launch || v.Skip != "nothing risky" {
		t.Fatalf("verdict: %+v", v)
	}

	pr.Number = 13
	_, _, err = prog.Call(context.Background(), DefaultLimits, "Gate", pr)
	if !errors.Is(err, errRateLimited) {
		t.Fatalf("host error identity lost: %v", err)
	}
	t.Logf("error: %v", err)
}
