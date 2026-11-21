package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/catalog"
	"github.com/victorarias/attn/internal/command"
	attngit "github.com/victorarias/attn/internal/git"
)

type agentToolLocal struct {
	Arg  string        `json:"arg"`
	Kind catalog.Local `json:"kind"`
	When string        `json:"when"`
}

type callerSide struct {
	d           *Daemon
	ctx         context.Context
	dir         string
	problems    []string
	absentPaths []string
}

func (d *Daemon) doCallerSideWork(ctx context.Context, def agentToolDefinition, args map[string]any, c command.Caller) (*callerSide, *command.Refusal) {
	w := &callerSide{d: d, ctx: ctx}
	if len(def.Local) == 0 {
		return w, nil
	}
	if row := d.store.Get(c.Session); row != nil {
		w.dir = row.Directory
	}
	for _, local := range def.Local {
		value, given := args[local.Arg]
		if !given || (local.When != "" && args[local.When] == nil) {
			continue
		}
		switch local.Kind {
		case catalog.LocalPath, catalog.LocalDestination:
			if resolved, ok := w.path(local.Arg, value.(string), local.Kind); ok {
				args[local.Arg] = resolved
			}
		case catalog.LocalEvidence:
			args[local.Arg] = w.evidence(local.Arg, value.([]any))
		case catalog.LocalCommit:
			if full, ok := w.commit(local.Arg, strings.TrimSpace(value.(string))); ok {
				args[local.Arg] = full
			}
		}
	}
	return w, w.refuseProblems(w.problems)
}

func (w *callerSide) explainRefusal(refusal *command.Refusal) *command.Refusal {
	if refusal == nil || len(w.absentPaths) == 0 {
		return refusal
	}
	explained := *refusal
	explained.Message = strings.Join(append([]string{refusal.Message}, w.absentPaths...), "\n")
	return &explained
}

func (w *callerSide) refuseProblems(problems []string) *command.Refusal {
	switch len(problems) {
	case 0:
		return nil
	case 1:
		return w.refusal(problems[0])
	default:
		lines := make([]string, len(problems))
		for i, problem := range problems {
			lines[i] = "  - " + problem
		}
		return w.refusal(fmt.Sprintf("%d problems; fix each and call again:\n%s", len(problems), strings.Join(lines, "\n")))
	}
}

func (w *callerSide) refusal(message string) *command.Refusal {
	return &command.Refusal{Code: command.CodeUsage, Message: message, Fix: "Fix what the message names and call again.", Arguments: true}
}

func (w *callerSide) problem(format string, values ...any) {
	w.problems = append(w.problems, fmt.Sprintf(format, values...))
}

func (w *callerSide) path(name, value string, kind catalog.Local) (string, bool) {
	if filepath.IsAbs(value) {
		return value, true
	}
	if w.dir == "" {
		w.problem("{%s} %q is relative, and this caller has no session folder to resolve it against; pass an absolute path", name, value)
		return "", false
	}
	resolved := filepath.Join(w.dir, value)
	notFound := fmt.Sprintf("{%s} %q is not found in %s, the folder the session started in; pass an absolute path", name, value, w.dir)
	switch kind {
	case catalog.LocalDestination:
		if _, err := os.Stat(filepath.Dir(resolved)); err != nil {
			w.problems = append(w.problems, notFound)
			return "", false
		}
	default:
		if _, err := os.Stat(resolved); err != nil {
			w.absentPaths = append(w.absentPaths, notFound)
		}
	}
	return resolved, true
}

func looksLikeURL(value string) bool {
	scheme, rest, ok := strings.Cut(value, "://")
	if !ok || !(strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")) {
		return false
	}
	host, _, _ := strings.Cut(rest, "/")
	return host != ""
}

func (w *callerSide) evidence(name string, values []any) []any {
	out := make([]any, 0, len(values))
	byName := map[string]string{}
	for _, item := range values {
		value := strings.TrimSpace(item.(string))
		path, cited := w.evidenceFile(name, value)
		switch {
		case cited:
			out = append(out, value)
		case path == "":
		case byName[filepath.Base(path)] == "":
			byName[filepath.Base(path)] = path
			out = append(out, path)
		case byName[filepath.Base(path)] != path:
			w.problem("{%s} %s and %s are both named %s, and a seed keeps one file per name; rename one", name, byName[filepath.Base(path)], path, filepath.Base(path))
		}
	}
	return out
}

func (w *callerSide) evidenceFile(name, value string) (path string, cited bool) {
	if looksLikeURL(value) {
		return "", true
	}
	explicit := filepath.IsAbs(value) || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../")
	path = value
	if !filepath.IsAbs(value) {
		if w.dir == "" {
			if explicit {
				w.problem("{%s} %s is relative, and this caller has no session folder to resolve it against; pass an absolute path", name, value)
			}
			return "", !explicit
		}
		path = filepath.Join(w.dir, value)
	}
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err) && explicit && filepath.IsAbs(value):
		w.problem("{%s} %s: no file at that path", name, value)
		return "", false
	case os.IsNotExist(err) && explicit:
		w.problem("{%s} %s is not found in %s, the folder the session started in; pass an absolute path", name, value, w.dir)
		return "", false
	case os.IsNotExist(err):
		return "", true
	case err != nil:
		w.problem("{%s} %s: %v", name, value, err)
		return "", false
	case !info.Mode().IsRegular():
		w.problem("{%s} %s is not a regular file; cite a file, an artifact on the seed, or an http(s) URL", name, value)
		return "", false
	}
	return path, false
}

func (w *callerSide) commit(name, rev string) (string, bool) {
	if w.dir == "" {
		w.problem("{%s} %q resolves in the session's repository, and this caller has no session folder", name, rev)
		return "", false
	}
	out, err := w.d.gitOutput(w.ctx, gitTask{Kind: gitTaskSeedArtifact, Lane: gitInteractive}, attngit.OpMetadata, w.dir, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		w.problem("{%s} %q is not a commit in the repository of %s, the folder the session started in (git rev-parse): %v", name, rev, w.dir, err)
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
