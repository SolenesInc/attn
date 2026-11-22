package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/command"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type callerAsk struct {
	paths    []protocol.CallerPath
	evidence []string
	commit   string
}

func callerFile(arg, value string) protocol.CallerPath {
	return protocol.CallerPath{Arg: arg, Value: value}
}

func callerDestination(arg, value string) protocol.CallerPath {
	return protocol.CallerPath{Arg: arg, Value: value, Destination: protocol.Ptr(true)}
}

func (d *Daemon) callerFolder(c command.Caller) (folder string, sessionFolder bool) {
	if c.Directory != "" {
		return c.Directory, false
	}
	if row := d.store.Get(c.Session); row != nil {
		return row.Directory, true
	}
	return "", true
}

type callerFound struct{ protocol.CallerPaths }

func (d *Daemon) callerPath(ctx context.Context, c command.Caller, asked protocol.CallerPath) (string, callerFound, error) {
	if asked.Value == "" {
		return "", callerFound{}, nil
	}
	found, err := d.askCallersMachine(ctx, c, callerAsk{paths: []protocol.CallerPath{asked}})
	if err == nil {
		err = refuseCallerProblems(found.Problems)
	}
	if err != nil {
		return "", found, err
	}
	return found.Paths[0], found, nil
}

func (d *Daemon) askCallersMachine(ctx context.Context, c command.Caller, ask callerAsk) (callerFound, error) {
	found := callerFound{protocol.CallerPaths{Evidence: ask.evidence}}
	relative := false
	for _, path := range ask.paths {
		found.Paths = append(found.Paths, path.Value)
		relative = relative || !absoluteCallerPath(path.Value)
	}
	if !relative && len(ask.evidence) == 0 && ask.commit == "" {
		return found, nil
	}
	folder, sessionFolder := d.callerFolder(c)
	msg := protocol.InspectCallerPathsMessage{Cmd: protocol.CmdInspectCallerPaths, Folder: folder, SessionFolder: optionalTrue(sessionFolder), Paths: ask.paths, Evidence: ask.evidence, Commit: optionalText(ask.commit)}
	if ask.commit != "" {
		msg.GitEnvironment = c.GitEnvironment
	}
	if origin, remote := OriginOf(ctx); remote {
		msg.Host = protocol.Ptr(origin.Host)
	}
	answer, err := Call[protocol.CallerPaths](ctx, d.router(), msg)
	return callerFound{answer}, err
}

func refuseCallerProblems(problems []string) error {
	switch len(problems) {
	case 0:
		return nil
	case 1:
		return callerRefusal(problems[0])
	default:
		lines := make([]string, len(problems))
		for i, problem := range problems {
			lines[i] = "  - " + problem
		}
		return callerRefusal(fmt.Sprintf("%d problems; fix each and run the same command again:\n%s", len(problems), strings.Join(lines, "\n")))
	}
}

func callerRefusal(message string) *command.Refusal {
	return &command.Refusal{Code: command.CodeUsage, Message: message, Fix: "Fix what the message names and try again.", Arguments: true}
}

func (found callerFound) explain(result any, err error) (any, error) {
	if err == nil || len(found.Absent) == 0 {
		return result, err
	}
	explained := *command.AsRefusal(err)
	explained.Message = strings.Join(append([]string{explained.Message}, found.Absent...), "\n")
	return nil, &explained
}

func absoluteCallerPath(value string) bool {
	return filepath.IsAbs(value) || value == "~" || strings.HasPrefix(value, "~/")
}

type callerPathsHere struct {
	d     *Daemon
	ctx   context.Context
	msg   protocol.InspectCallerPathsMessage
	where string
	found protocol.CallerPaths
}

func (d *Daemon) inspectCallerPaths(ctx context.Context, msg protocol.InspectCallerPathsMessage) (protocol.CallerPaths, error) {
	w := &callerPathsHere{d: d, ctx: ctx, msg: msg, where: msg.Folder, found: protocol.CallerPaths{Paths: []string{}, Evidence: []string{}, Problems: []string{}, Absent: []string{}}}
	if protocol.Deref(msg.SessionFolder) {
		w.where += ", the folder the session started in"
	}
	for _, path := range msg.Paths {
		w.found.Paths = append(w.found.Paths, w.path(path))
	}
	if len(msg.Evidence) > 0 {
		w.evidence()
	}
	if rev := strings.TrimSpace(protocol.Deref(msg.Commit)); rev != "" {
		w.found.Commit = w.commit(rev)
	}
	return w.found, nil
}

func (w *callerPathsHere) problem(format string, values ...any) {
	w.found.Problems = append(w.found.Problems, fmt.Sprintf(format, values...))
}

func (w *callerPathsHere) path(asked protocol.CallerPath) string {
	if absoluteCallerPath(asked.Value) {
		return asked.Value
	}
	if w.msg.Folder == "" {
		w.problem("{%s} %q is relative, and this caller has no session folder to resolve it against; pass an absolute path", asked.Arg, asked.Value)
		return ""
	}
	resolved := filepath.Join(w.msg.Folder, asked.Value)
	if !protocol.Deref(w.msg.SessionFolder) {
		return resolved
	}
	notFound := fmt.Sprintf("{%s} %q is not found in %s; pass an absolute path", asked.Arg, asked.Value, w.where)
	if protocol.Deref(asked.Destination) {
		if _, err := os.Stat(filepath.Dir(resolved)); err != nil {
			w.found.Problems = append(w.found.Problems, notFound)
		}
		return resolved
	}
	if _, err := os.Stat(resolved); err != nil {
		w.found.Absent = append(w.found.Absent, notFound)
	}
	return resolved
}

func looksLikeURL(value string) bool {
	scheme, rest, ok := strings.Cut(value, "://")
	if !ok || !(strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")) {
		return false
	}
	host, _, _ := strings.Cut(rest, "/")
	return host != ""
}

func (w *callerPathsHere) evidence() {
	byName := map[string]string{}
	for _, item := range w.msg.Evidence {
		value := strings.TrimSpace(item)
		path, cited := w.evidenceFile(value)
		switch {
		case cited:
			w.found.Evidence = append(w.found.Evidence, value)
		case path == "":
		case byName[filepath.Base(path)] == "":
			byName[filepath.Base(path)] = path
			w.found.Evidence = append(w.found.Evidence, path)
		case byName[filepath.Base(path)] != path:
			w.problem("{evidence} %s and %s are both named %s, and a seed keeps one file per name; rename one", byName[filepath.Base(path)], path, filepath.Base(path))
		}
	}
}

func (w *callerPathsHere) evidenceFile(value string) (path string, cited bool) {
	if looksLikeURL(value) {
		return "", true
	}
	explicit := filepath.IsAbs(value) || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../")
	path = value
	if !filepath.IsAbs(value) {
		if w.msg.Folder == "" {
			if explicit {
				w.problem("{evidence} %s is relative, and this caller has no session folder to resolve it against; pass an absolute path", value)
			}
			return "", !explicit
		}
		path = filepath.Join(w.msg.Folder, value)
	}
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err) && explicit && (filepath.IsAbs(value) || !protocol.Deref(w.msg.SessionFolder)):
		w.problem("{evidence} %s: no file at that path", value)
		return "", false
	case os.IsNotExist(err) && explicit:
		w.problem("{evidence} %s is not found in %s; pass an absolute path", value, w.where)
		return "", false
	case os.IsNotExist(err):
		return "", true
	case err != nil:
		w.problem("{evidence} %s: %v", value, err)
		return "", false
	case !info.Mode().IsRegular():
		w.problem("{evidence} %s is not a regular file; cite a file, an artifact on the seed, or an http(s) URL", value)
		return "", false
	}
	return path, false
}

func (w *callerPathsHere) commit(rev string) *string {
	if w.msg.Folder == "" {
		w.problem("{commit} %q resolves in the session's repository, and this caller has no session folder", rev)
		return nil
	}
	out, err := gitValue(w.ctx, w.d.gitExecution(), gitTask{Kind: gitTaskSeedArtifact, Lane: gitInteractive}, func(ctx context.Context, client *attngit.Client) ([]byte, error) {
		return client.OutputInRepository(ctx, attngit.OpMetadata, w.msg.Folder, w.msg.GitEnvironment, "rev-parse", "--verify", rev+"^{commit}")
	})
	if err != nil {
		w.problem("{commit} %q is not a commit in the repository of %s (git rev-parse): %v", rev, w.where, err)
		return nil
	}
	return protocol.Ptr(strings.TrimSpace(string(out)))
}
