package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/command"
)

const codeLocalWork = "needs_the_cli"

var fullCommitPattern = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`) })

func (d *Daemon) localWork(def agentToolDefinition, args map[string]any, c command.Caller) *command.Refusal {
	refuse := func(format string, values ...any) *command.Refusal {
		cli := "attn " + def.Command
		return &command.Refusal{Code: codeLocalWork, Message: fmt.Sprintf(format, values...), Fix: "Run `" + cli + "` from a shell in your working directory; the CLI does this part on your machine.", Arguments: true}
	}
	text := func(name string) string { value, _ := args[name].(string); return value }
	for _, name := range def.Stdin {
		if text(name) == "-" {
			return refuse("%s is -, which reads stdin only from the CLI; pass the text itself", name)
		}
	}
	onHome := d.enrollmentStatus().IsHome()
	localPath := func(name string) *command.Refusal {
		path := text(name)
		switch {
		case path == "":
			return nil
		case !filepath.IsAbs(path):
			return refuse("%s %q is relative, and a tool call has no working directory; pass an absolute path", name, path)
		case !onHome:
			return refuse("%s %q is a file on this machine, and %s runs on the home", name, path, def.Name)
		}
		return nil
	}
	switch def.Command {
	case "seed attach":
		if text("mode") != "" && text("repo") == "" && text("notebook") == "" && text("url") == "" {
			return localPath("path")
		}
	case "seed detach":
		return localPath("to")
	case "seed export":
		if text("out") != "" {
			return refuse("out writes a file only from the CLI; the result's text holds the stamped body")
		}
	case "seed check report":
		if commit := text("commit"); commit != "" && !fullCommitPattern().MatchString(commit) {
			return refuse("commit %q is not a full commit SHA; resolve it with git rev-parse first", commit)
		}
		evidence, _ := args["evidence"].([]any)
		for _, item := range evidence {
			if value, _ := item.(string); d.namesLocalFile(value, c) {
				return refuse("evidence %q names a local file, which only the CLI attaches; attach it with seed_attach or cite an artifact or URL", value)
			}
		}
	}
	return nil
}

func (d *Daemon) namesLocalFile(value string, c command.Caller) bool {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return false
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
		return true
	}
	if row := d.store.Get(c.Session); row != nil && row.Directory != "" {
		if info, err := os.Stat(filepath.Join(row.Directory, value)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
