package gate

import (
	"attn"
	"fmt"
	"path"
	"strings"
)

var riskyPaths = []string{"internal/pty/*", "internal/store/*", "internal/protocol/schema/*.tsp"}

func Gate(pr attn.PullRequestInput) (attn.Verdict, error) {
	if pr.Draft || strings.HasPrefix(pr.Title, "WIP") {
		return attn.Verdict{Skip: "draft"}, nil
	}
	files, err := attn.ChangedFiles(pr.Owner+"/"+pr.Repository, pr.Number)
	if err != nil {
		return attn.Verdict{}, fmt.Errorf("listing files of %s: %w", pr.URL, err)
	}
	var risky []string
	for _, f := range files {
		for _, pattern := range riskyPaths {
			if ok, _ := path.Match(pattern, f); ok {
				risky = append(risky, f)
			}
		}
	}
	if len(risky) == 0 {
		return attn.Verdict{Skip: "nothing risky"}, nil
	}
	return attn.Verdict{
		Launch: true,
		Effort: "high",
		Prompt: fmt.Sprintf("Review %s. Take extra care with %s.", pr.URL, strings.Join(risky, ", ")),
	}, nil
}
