package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/viewlang"
)

// maxViewDataBytes bounds the values of all of a view's sources together, a
// file's or a command's, so a view's document stays inside one 8 MiB message
// (protocol.MaxSocketFrameBytes) beside its 1 MiB file. Measured 2026-11-28:
// the transcript task's 200 clips are 71 KB of JSON Lines; GitHub's 100 newest
// runs of main, printed raw by `gh api`, are 1.3 MB, and shaped for a CI view
// 7.5 KB.
const maxViewDataBytes = 4 << 20

// viewRunTimeout stops a command that hangs. Measured 2026-11-28 on oma: one
// `gh api` call for main's runs takes 1.8 s (30 runs) to 3.0 s (100 runs), so
// a script of three such calls stays under 10 s.
const viewRunTimeout = 60 * time.Second

// viewRunWaitDelay bounds the wait for a command's output once its process
// group is killed, which closes the output at once unless a child left the
// group.
const viewRunWaitDelay = 5 * time.Second

const viewStderrTail = 2 << 10

// viewCommand is a command of a view that runs only once the user starts it:
// a run= source, which attn reruns every so often or runs once when every is
// zero, or one of the view's own commands, which a control runs.
type viewCommand struct {
	line      string
	every     time.Duration
	everyText string
	// own is set for a view's own command, whose line is its definition.
	own *protocol.ViewOwnCommand
}

// words is the program and the arguments that could name a file of the view's
// folder.
func (c viewCommand) words() []string {
	if c.own == nil {
		return strings.Fields(c.line)
	}
	words := []string{c.own.Program}
	for _, arg := range c.own.Args {
		if len(arg) == 1 && arg[0].Text != nil {
			words = append(words, *arg[0].Text)
		}
	}
	return words
}

// viewCommands lists the run= commands of a view's parts that have no problem,
// in the order its sources are listed, then its own commands.
func viewCommands(parts []protocol.ViewPart) []viewCommand {
	var commands []viewCommand
	listed := func(line string) bool {
		return slices.ContainsFunc(commands, func(c viewCommand) bool { return c.line == line })
	}
	eachViewPart(parts, func(part protocol.ViewPart) {
		data := part.Data
		if data == nil || data.Run == nil || part.Problem != nil || listed(*data.Run) {
			return
		}
		command := viewCommand{line: *data.Run}
		if data.Every != nil {
			command.everyText = *data.Every
			command.every, _ = time.ParseDuration(*data.Every)
		}
		commands = append(commands, command)
	})
	eachViewPart(parts, func(part protocol.ViewPart) {
		if part.Commands == nil {
			return
		}
		for i := range part.Commands.Commands {
			if own := &part.Commands.Commands[i]; !listed(own.Written) {
				commands = append(commands, viewCommand{line: own.Written, own: own})
			}
		}
	})
	return commands
}

// viewScriptFiles is the regular files of folder that the words name.
func viewScriptFiles(folder string, words []string) []string {
	var files []string
	for _, file := range viewLineFiles(folder, words) {
		if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
			files = append(files, file)
		}
	}
	return files
}

// viewLineFiles is the paths in folder that the words could name, whether or
// not a file is there yet.
func viewLineFiles(folder string, words []string) []string {
	var files []string
	for _, word := range words {
		if file := filepath.Join(folder, word); filepath.IsLocal(word) && !slices.Contains(files, file) {
			files = append(files, file)
		}
	}
	return files
}

// viewScript is the sha256 of the names and contents of files, which the user
// starts a command under.
func viewScript(folder string, files []string) string {
	hash := sha256.New()
	for _, file := range files {
		content, err := os.ReadFile(file)
		name, _ := filepath.Rel(folder, file)
		if err != nil {
			fmt.Fprintf(hash, "%s\x00unreadable\x00", name)
			continue
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(content))
		hash.Write(content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// viewCommandState is how a command stands against the commands the user
// started for its view, and, when it changed, what changed.
func viewCommandState(folder string, command viewCommand, files []string, script string, started []protocol.ViewStartedCommand, lines []string) (protocol.ViewRunState, *string) {
	for _, s := range started {
		if s.Line != command.line {
			continue
		}
		if s.ScriptSHA256 == script {
			return protocol.ViewRunStateStarted, nil
		}
		return protocol.ViewRunStateChanged, protocol.Ptr(viewScriptChange(folder, command, files))
	}
	if was, replaced := replacedViewLine(started, lines); replaced {
		return protocol.ViewRunStateChanged, protocol.Ptr(fmt.Sprintf("was %q when you started it", was))
	}
	return protocol.ViewRunStateWaiting, nil
}

// replacedViewLine is a line the user started that the view no longer holds,
// which a command not started took the place of.
func replacedViewLine(started []protocol.ViewStartedCommand, lines []string) (string, bool) {
	for _, s := range started {
		if !slices.Contains(lines, s.Line) {
			return s.Line, true
		}
	}
	return "", false
}

// viewScriptChange says what changed in the files a command runs, read after the command's line.
func viewScriptChange(folder string, command viewCommand, files []string) string {
	if len(files) != 1 {
		return "uses a file that changed since you started it"
	}
	name, _ := filepath.Rel(folder, files[0])
	if program := command.words()[0]; filepath.Clean(program) == name {
		return "changed since you started it"
	}
	return fmt.Sprintf("uses %s, which changed since you started it", name)
}

// viewRun is one command of a shown view: the loop that reruns it while it
// stays started, and its last values or failure, which outlive the loop.
type viewRun struct {
	command viewCommand
	script  string
	stop    context.CancelFunc
	has     bool
	values  any
	size    int
	digest  [sha256.Size]byte
	failure string
}

func (run *viewRun) halt() {
	if run.stop != nil {
		run.stop()
		run.stop = nil
	}
}

// viewRuns holds the commands of the shown views of this machine, each view's
// by its line, and the commands the user started.
type viewRuns struct {
	mu      sync.Mutex
	started map[string][]protocol.ViewStartedCommand
	views   map[string]map[string]*viewRun
}

func newViewRuns() viewRuns {
	return viewRuns{started: map[string][]protocol.ViewStartedCommand{}, views: map[string]map[string]*viewRun{}}
}

// setStarted takes the commands the user started, and reports the views whose
// started commands changed.
func (runs *viewRuns) setStarted(started []protocol.ViewStartedCommand) []string {
	runs.mu.Lock()
	defer runs.mu.Unlock()
	was := runs.started
	runs.started = map[string][]protocol.ViewStartedCommand{}
	for _, command := range started {
		runs.started[command.Path] = append(runs.started[command.Path], command)
	}
	var changed []string
	for path := range was {
		if runs.started[path] == nil {
			changed = append(changed, path)
		}
	}
	for path, commands := range runs.started {
		if !slices.Equal(was[path], commands) {
			changed = append(changed, path)
		}
	}
	return changed
}

type viewCommandRead struct {
	command viewCommand
	files   []string
	script  string
}

func (d *Daemon) readViewCommands(path string) []viewCommandRead {
	if !d.on(setting.ViewsEnabled) {
		return nil
	}
	content, err := readMarkdownFile(path)
	if err != nil || !viewlang.IsView(content) {
		return nil
	}
	folder := filepath.Dir(path)
	var read []viewCommandRead
	for _, command := range viewCommands(viewlang.Check(content).Parts) {
		files := viewScriptFiles(folder, command.words())
		read = append(read, viewCommandRead{command: command, files: files, script: viewScript(folder, files)})
	}
	return read
}

// followViewRuns runs each started command of the view at path while the home
// lists the view as shown, and stops each that is not, keeping its last
// values. It reports whether a command started or stopped.
func (d *Daemon) followViewRuns(path string) bool {
	read := d.readViewCommands(path)
	w := &d.tileDocuments
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.watched[path] == nil {
		read = nil
	}
	runs := &d.viewRuns
	runs.mu.Lock()
	defer runs.mu.Unlock()
	lines := make([]string, 0, len(read))
	for _, r := range read {
		lines = append(lines, r.command.line)
	}
	held, kept := runs.views[path], map[string]*viewRun{}
	changed := false
	for _, r := range read {
		if r.command.own != nil {
			continue
		}
		state, _ := viewCommandState(filepath.Dir(path), r.command, r.files, r.script, runs.started[path], lines)
		run := held[r.command.line]
		if run == nil {
			run = &viewRun{}
			if was, replaced := replacedViewLine(runs.started[path], lines); replaced && state == protocol.ViewRunStateChanged && held[was] != nil {
				run.has, run.values, run.size, run.digest = held[was].has, held[was].values, held[was].size, held[was].digest
			}
		}
		running := run.stop != nil && run.command == r.command && run.script == r.script
		switch {
		case state == protocol.ViewRunStateStarted && !running:
			run.halt()
			ctx, stop := context.WithCancel(d.life.Context())
			run.command, run.script, run.stop, run.failure = r.command, r.script, stop, ""
			command, script := r.command, r.script
			d.life.Go("viewRun", func() { d.rerunViewCommand(ctx, path, run, command, script) })
			changed = true
		case state != protocol.ViewRunStateStarted && run.stop != nil:
			run.halt()
			changed = true
		}
		run.command = r.command
		kept[r.command.line] = run
	}
	for line, run := range held {
		if kept[line] == nil {
			changed = changed || run.stop != nil
			run.halt()
		}
	}
	if len(kept) == 0 {
		delete(runs.views, path)
	} else {
		runs.views[path] = kept
	}
	return changed
}

// rerunViewCommand runs command in the view's folder, again at each tick of
// its every while it stays started under script. A run still going when a tick
// comes skips that tick.
func (d *Daemon) rerunViewCommand(ctx context.Context, path string, run *viewRun, command viewCommand, script string) {
	folder := filepath.Dir(path)
	for {
		began := time.Now()
		if viewScript(folder, viewScriptFiles(folder, command.words())) != script {
			if d.followViewRuns(path) {
				d.reportTileDocuments(d.tileDocuments.bump([]string{path}))
			}
			return
		}
		values, output, err := runViewCommand(ctx, folder, command.line)
		runs := &d.viewRuns
		runs.mu.Lock()
		if ctx.Err() != nil {
			runs.mu.Unlock()
			return
		}
		var fresh bool
		if err != nil {
			fresh = run.failure != err.Error()
			run.failure = err.Error()
		} else {
			digest := sha256.Sum256(output)
			fresh = !run.has || run.failure != "" || run.digest != digest
			run.has, run.values, run.size, run.digest, run.failure = true, values, len(output), digest, ""
		}
		runs.mu.Unlock()
		if fresh {
			d.reportTileDocuments(d.tileDocuments.bump([]string{path}))
		}
		if command.every <= 0 {
			<-ctx.Done()
			return
		}
		next := began.Add(command.every)
		for !next.After(time.Now()) {
			next = next.Add(command.every)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// runViewCommand runs line as a program and its arguments in folder, never
// through a shell, and reads the one JSON object it prints.
func runViewCommand(ctx context.Context, folder, line string) (any, []byte, error) {
	tooLarge := fmt.Errorf("%s printed more than %d bytes, past what a view's sources hold together", line, maxViewDataBytes)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stdout := &viewOutput{cancel: func() { cancel(tooLarge) }}
	if err := runViewProgram(ctx, folder, line, strings.Fields(line), stdout); err != nil {
		return nil, nil, err
	}
	var values any
	if err := json.Unmarshal(stdout.data, &values); err != nil {
		return nil, nil, fmt.Errorf("%s printed no JSON object: %v", line, err)
	}
	if _, isObject := values.(map[string]any); !isObject {
		return nil, nil, fmt.Errorf("%s printed JSON that is not one object", line)
	}
	return values, stdout.data, nil
}

// runViewProgram runs words as a program and its arguments in folder, never
// through a shell, in a process group of its own that a timeout or ctx ends.
// It says the command as line in what it reports.
func runViewProgram(ctx context.Context, folder, line string, words []string, stdout io.Writer) error {
	timedOut := fmt.Errorf("%s ran for more than %s, so attn stopped it", line, viewRunTimeout)
	ctx, stop := context.WithTimeoutCause(ctx, viewRunTimeout, timedOut)
	defer stop()
	stderr := &viewTail{}
	cmd := exec.CommandContext(ctx, words[0], words[1:]...)
	cmd.Dir = folder
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = viewRunWaitDelay
	err := cmd.Run()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
		said := fmt.Sprintf("%s exited with status %d", line, exit.ExitCode())
		if last := stderr.lastLine(); last != "" {
			said += ": " + last
		}
		return errors.New(said)
	}
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) && !strings.Contains(words[0], "/") {
			return fmt.Errorf("%s did not start: %v. Name a program in the view's folder from there, such as ./%s", line, err, words[0])
		}
		return fmt.Errorf("%s did not start: %v", line, err)
	}
	return nil
}

type viewOutput struct {
	data   []byte
	cancel func()
}

func (w *viewOutput) Write(p []byte) (int, error) {
	if available := maxViewDataBytes - len(w.data); len(p) > available {
		w.data = append(w.data, p[:max(available, 0)]...)
		w.cancel()
		return len(p), nil
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

type viewTail struct{ data []byte }

func (w *viewTail) Write(p []byte) (int, error) {
	w.data = append(w.data, p...)
	if excess := len(w.data) - viewStderrTail; excess > 0 {
		w.data = w.data[excess:]
	}
	return len(p), nil
}

func (w *viewTail) lastLine() string {
	lines := strings.Split(strings.TrimSpace(string(w.data)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// viewRunCommands is how each command of the view at path stands for the user.
func (d *Daemon) viewRunCommands(path string, commands []viewCommand) []protocol.ViewRunCommand {
	folder := filepath.Dir(path)
	lines := make([]string, 0, len(commands))
	for _, command := range commands {
		lines = append(lines, command.line)
	}
	d.viewRuns.mu.Lock()
	started := d.viewRuns.started[path]
	d.viewRuns.mu.Unlock()
	listed := []protocol.ViewRunCommand{}
	for _, command := range commands {
		files := viewScriptFiles(folder, command.words())
		script := viewScript(folder, files)
		state, change := viewCommandState(folder, command, files, script, started, lines)
		shown := protocol.ViewRunCommand{Line: command.line, ScriptSHA256: script, State: state, Change: change}
		if command.everyText != "" {
			shown.Every = protocol.Ptr(command.everyText)
		}
		if command.own != nil {
			shown.Name = protocol.Ptr(command.own.Name)
		}
		listed = append(listed, shown)
	}
	return listed
}

type viewRunRead struct {
	running bool
	has     bool
	values  any
	size    int
	failure string
}

func (d *Daemon) viewRunsOf(path string) map[string]viewRunRead {
	runs := &d.viewRuns
	runs.mu.Lock()
	defer runs.mu.Unlock()
	read := make(map[string]viewRunRead, len(runs.views[path]))
	for line, run := range runs.views[path] {
		read[line] = viewRunRead{running: run.stop != nil, has: run.has, values: run.values, size: run.size, failure: run.failure}
	}
	return read
}

// viewSources lists the sources the parts of the view at path name, in the
// order they first appear, with the values of its files and of its commands'
// last runs.
func (d *Daemon) viewSources(path string, parts []protocol.ViewPart, commands []viewCommand) ([]protocol.ViewSource, []protocol.ViewValues) {
	folder := filepath.Dir(path)
	sources := namedViewSources(parts)
	values := []protocol.ViewValues{}
	runs := d.viewRunsOf(path)
	budget := maxViewDataBytes
	for i := range sources {
		source := &sources[i]
		switch source.Kind {
		case protocol.ViewSourceKindFile:
			read, size, err := readViewDataFile(filepath.Join(folder, source.ID), budget)
			if err != nil {
				source.State, source.Notice = protocol.ViewSourceStateFailed, protocol.Ptr(fmt.Sprintf("%s: %v", source.ID, err))
				continue
			}
			if _, isObject := read.(map[string]any); source.Data && source.As == nil && !isObject {
				source.State, source.Notice = protocol.ViewSourceStateFailed, protocol.Ptr(fmt.Sprintf("%s holds no JSON object, so its values need a name: add as= to its :::data part", source.ID))
				continue
			}
			budget -= size
			source.State = protocol.ViewSourceStateOk
			values = append(values, protocol.ViewValues{Source: source.ID, Values: read})
		case protocol.ViewSourceKindRun:
			if !slices.ContainsFunc(commands, func(c viewCommand) bool { return c.line == source.ID }) {
				source.State, source.Notice = protocol.ViewSourceStateUnavailable, protocol.Ptr("attn runs it once its problem is fixed")
				continue
			}
			run := runs[source.ID]
			switch {
			case run.failure != "":
				source.State, source.Notice = protocol.ViewSourceStateFailed, protocol.Ptr(run.failure)
			case run.has && run.running:
				source.State = protocol.ViewSourceStateOk
			case run.has:
				source.State = protocol.ViewSourceStateOutOfDate
			case run.running:
				source.State = protocol.ViewSourceStateRunning
			default:
				source.State = protocol.ViewSourceStateWaiting
			}
			switch {
			case !run.has:
			case run.size > budget:
				source.State, source.Notice = protocol.ViewSourceStateFailed, protocol.Ptr(fmt.Sprintf("%s printed more than the %d bytes a view's sources hold together", source.ID, maxViewDataBytes))
			default:
				budget -= run.size
				values = append(values, protocol.ViewValues{Source: source.ID, Values: run.values})
			}
		default:
			source.State, source.Notice = protocol.ViewSourceStateUnavailable, protocol.Ptr("not available yet")
		}
	}
	return sources, values
}

// viewDataFiles is the files of a view's folder that its parts show or its
// commands run, so a change to one reaches the tile.
func viewDataFiles(folder string, parts []protocol.ViewPart) []string {
	var files []string
	for _, source := range namedViewSources(parts) {
		if source.Kind == protocol.ViewSourceKindFile {
			files = append(files, filepath.Join(folder, source.ID))
		}
	}
	for _, command := range viewCommands(parts) {
		files = append(files, viewLineFiles(folder, command.words())...)
	}
	return files
}

func namedViewSources(parts []protocol.ViewPart) []protocol.ViewSource {
	sources := []protocol.ViewSource{}
	at := map[string]int{}
	name := func(source protocol.ViewSource) {
		key := string(source.Kind) + ":" + source.ID
		if i, seen := at[key]; seen {
			sources[i].Data = sources[i].Data || source.Data
			if source.As != nil {
				sources[i].As = source.As
			}
			return
		}
		at[key] = len(sources)
		sources = append(sources, source)
	}
	eachViewPart(parts, func(part protocol.ViewPart) {
		if data := part.Data; data != nil {
			for kind, id := range map[protocol.ViewSourceKind]*string{
				protocol.ViewSourceKindFile:  data.File,
				protocol.ViewSourceKindRun:   data.Run,
				protocol.ViewSourceKindAttn:  data.Attn,
				protocol.ViewSourceKindStore: data.Store,
			} {
				if id != nil {
					name(protocol.ViewSource{ID: *id, Kind: kind, Data: true, As: data.As})
				}
			}
		}
		if items := viewItemsOf(part); items != nil && items.File != nil {
			name(protocol.ViewSource{ID: *items.File, Kind: protocol.ViewSourceKindFile})
		}
	})
	return sources
}

func eachViewPart(parts []protocol.ViewPart, visit func(protocol.ViewPart)) {
	for _, part := range parts {
		visit(part)
		switch {
		case part.Each != nil:
			eachViewPart(part.Each.Blocks, visit)
		case part.OneByOne != nil:
			eachViewPart(part.OneByOne.Blocks, visit)
		}
	}
}

func viewItemsOf(part protocol.ViewPart) *protocol.ViewItems {
	switch {
	case part.List != nil:
		return &part.List.Items
	case part.Table != nil:
		return &part.Table.Items
	case part.Chart != nil:
		return &part.Chart.Items
	case part.Each != nil:
		return &part.Each.Items
	case part.OneByOne != nil:
		return &part.OneByOne.Items
	}
	return nil
}

// readViewDataFile reads a .json file as one value and a .jsonl file as the
// list of its lines' values, refusing more than budget bytes.
func readViewDataFile(path string, budget int) (any, int, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, fmt.Errorf("file not found")
		}
		return nil, 0, err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("not a regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, int64(budget)+1))
	if err != nil {
		return nil, 0, err
	}
	if len(content) > budget {
		return nil, 0, fmt.Errorf("holds more than the %d bytes a view's sources hold together", maxViewDataBytes)
	}
	if filepath.Ext(path) == ".json" {
		var value any
		if err := json.Unmarshal(content, &value); err != nil {
			return nil, 0, fmt.Errorf("not JSON: %v", err)
		}
		return value, len(content), nil
	}
	items := []any{}
	lines := bufio.NewScanner(bytes.NewReader(content))
	lines.Buffer(nil, len(content)+1)
	for number := 1; lines.Scan(); number++ {
		line := bytes.TrimSpace(lines.Bytes())
		if len(line) == 0 {
			continue
		}
		var item any
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, 0, fmt.Errorf("line %d is not JSON: %v", number, err)
		}
		items = append(items, item)
	}
	return items, len(content), nil
}
