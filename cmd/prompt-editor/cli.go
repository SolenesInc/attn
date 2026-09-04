package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/prompts"
)

type assignments map[string]string

func (a assignments) String() string { return "name=value" }
func (a assignments) Set(value string) error {
	name, text, ok := strings.Cut(value, "=")
	if !ok || name == "" {
		return fmt.Errorf("expected name=value")
	}
	a[name] = text
	return nil
}

type cliOptions struct {
	repo, base string
	port       int
}

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("prompt-editor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "Repository checkout")
	asJSON := flags.Bool("json", false, "Structured output")
	port := flags.Int("port", 0, "Loopback port for serve")
	q := operationRequest{Values: prompts.Values{}, Inputs: map[string]string{}}
	flags.StringVar(&q.DraftID, "draft", "", "Shared draft id")
	flags.StringVar(&q.ReviewID, "review", "", "Immutable review id")
	flags.StringVar(&q.Scenario, "scenario", "", "Saved scenario id")
	flags.StringVar(&q.Event, "event", "", "recipient/event")
	flags.StringVar(&q.Path, "source", "", "Markdown source path")
	flags.StringVar(&q.Title, "title", "", "Draft, review, or scenario title")
	flags.StringVar(&q.Text, "message", "", "Review feedback")
	flags.StringVar(&q.Author, "author", "agent", "Change author")
	flags.StringVar(&q.Expect, "expect", "", "Expected source or scenario revision")
	flags.Int64Var(&q.Revision, "revision", 0, "Expected draft revision")
	flags.StringVar(&q.Base, "base", "", "Comparison branch, tag, or commit")
	flags.StringVar(&q.Mode, "mode", "merge-base", "merge-base or tip")
	flags.StringVar(&q.Target, "target", "", "Feedback target: source or prompt")
	flags.StringVar(&q.Selection, "selection", "", "Quoted source or prompt selection")
	file := flags.String("file", "", "Read source text from a file; - reads stdin")
	valuesFile := flags.String("values", "", "Read scenario values as JSON; - reads stdin")
	open := flags.Bool("open", false, "Open the review link in a browser")
	after := flags.Int64("after", 0, "Watch after this draft revision or feedback count")
	timeout := flags.Duration("timeout", 30*time.Second, "Watch timeout")
	flags.Var(assignments(q.Values), "set", "Scenario input name=value; repeatable")
	flags.Var(assignments(q.Inputs), "input", "Producer binding field=scenario; repeatable")
	flags.Usage = func() { fmt.Fprint(stderr, cliHelp) }
	var options, words []string
	bools := map[string]bool{"--json": true, "--open": true, "--help": true, "-h": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			options = append(options, arg)
			if !bools[arg] && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
				options = append(options, args[i])
			}
		} else {
			words = append(words, arg)
		}
	}
	if err := flags.Parse(options); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(words) == 0 || words[0] == "serve" {
		if err := serve(ctx, cliOptions{*repo, q.Base, *port}, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if words[0] == "help" {
		fmt.Fprint(stdout, cliHelp)
		return 0
	}
	fail := func(err error) int {
		var detail *operationError
		if !errors.As(err, &detail) {
			detail = &operationError{Code: "validation_failed", Message: err.Error()}
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(detail)
		} else {
			fmt.Fprintf(stderr, "%s: %s\n", detail.Code, detail.Message)
		}
		if detail.Code == "revision_conflict" {
			return 3
		}
		return 2
	}
	read := func(name string) ([]byte, error) {
		if name == "-" {
			return io.ReadAll(stdin)
		}
		return os.ReadFile(name)
	}
	if *file != "" {
		data, err := read(*file)
		if err != nil {
			return fail(err)
		}
		q.Text = string(data)
	}
	if *valuesFile != "" {
		data, err := read(*valuesFile)
		if err != nil {
			return fail(err)
		}
		var values prompts.Values
		if err := decodeJSON(data, &values); err != nil {
			return fail(err)
		}
		for k, v := range values {
			if _, ok := q.Values[k]; !ok {
				q.Values[k] = v
			}
		}
	}
	if len(q.Values) == 0 && *valuesFile == "" {
		q.Values = nil
	}
	e, err := openEditor(*repo)
	if err != nil {
		return fail(err)
	}
	defer e.root.Close()
	q.Op = words[0]
	position := 1
	if q.Op == "draft" || q.Op == "review" || q.Op == "scenario" {
		if len(words) < 2 {
			return fail(fmt.Errorf("%s needs a subcommand", q.Op))
		}
		q.Op += "-" + words[1]
		position = 2
	}
	if position < len(words) {
		q.ID = words[position]
		position++
	}
	if position < len(words) {
		q.Path = words[position]
		position++
	}
	if position < len(words) {
		return fail(fmt.Errorf("unexpected arguments"))
	}
	if q.Op == "inspect" && q.Event == "" {
		q.Event = q.ID
	}
	if q.Op == "uses" {
		q.Target = q.ID
	}
	if q.Op == "draft-put" && *file == "" {
		return fail(fmt.Errorf("draft put requires --file"))
	}
	if q.Op == "draft-focus" || q.Op == "show" {
		if q.Op == "draft-focus" && q.DraftID == "" {
			q.DraftID = q.ID
		}
		snapshot, selected, err := e.dataset(q.DraftID, q.ReviewID)
		if err != nil {
			return fail(err)
		}
		if selected == nil {
			selected = &focus{Values: prompts.Values{}}
		}
		if q.Event != "" {
			if selected.Event != q.Event {
				selected.Scenario = ""
				selected.Values = prompts.Values{}
				selected.Path = ""
			}
			selected.Event = q.Event
		}
		if q.Path != "" {
			selected.Path = q.Path
		}
		if q.Values != nil {
			selected.Values = q.Values
			selected.Scenario = ""
		}
		if q.Scenario != "" {
			all, err := e.scenarios()
			if err != nil {
				return fail(err)
			}
			s, ok := all[q.Scenario]
			if !ok {
				return fail(fmt.Errorf("unknown scenario"))
			}
			catalog, err := snapshot.load(nil)
			if err != nil {
				return fail(err)
			}
			selected.Values, err = scenarioValues(catalog, all, q.Scenario, map[string]bool{})
			if err != nil {
				return fail(err)
			}
			selected.Event = s.Recipient + "/" + s.Event
			selected.Scenario = s.ID
		}
		if q.Base != "" {
			base, err := e.selectBase(ctx, q.Base, q.Mode)
			if err != nil {
				return fail(err)
			}
			selected.BaseCommit = base.Commit
		}
		q.Focus = selected
	}
	var result any
	if q.Op == "show" {
		result, err = e.show(ctx, q, *open)
	} else if q.Op == "watch" {
		result, err = e.watch(ctx, q, *after, *timeout)
	} else {
		result, err = e.operation(ctx, q)
	}
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(result)
	} else {
		printResult(stdout, q, result)
	}
	if err != nil {
		return fail(err)
	}
	if result, ok := result.(map[string]any); ok {
		if valid, ok := result["valid"].(bool); ok && !valid {
			return 1
		}
	}
	return 0
}

func printResult(w io.Writer, q operationRequest, result any) {
	switch value := result.(type) {
	case sharedDraft:
		fmt.Fprintf(w, "%s  %s  revision %d  %d changed files\n", value.ID, value.Title, value.Revision, len(value.Files))
		for path, file := range value.Files {
			fmt.Fprintf(w, "  %s  %s  by %s\n", path, file.Revision, file.Author)
		}
	case review:
		fmt.Fprintf(w, "%s  %s  draft %s revision %d\n", value.ID, value.Title, value.DraftID, value.DraftRevision)
		fmt.Fprintf(w, "  %s  scenario %s  %d comments\n", value.Focus.Event, value.Focus.Scenario, len(value.Feedback))
		for _, comment := range value.Feedback {
			fmt.Fprintf(w, "  %s: %s\n", comment.Author, comment.Message)
		}
	case map[string]any:
		if q.Op == "inspect" {
			fmt.Fprintf(w, "%s\n", value["event"])
			if fields, ok := value["fields"].([]prompts.Field); ok {
				for _, f := range fields {
					fmt.Fprintf(w, "  input %s (%s) %s\n", f.Name, f.Kind, f.Description)
				}
			}
			if sources, ok := value["sources"].(map[string]source); ok {
				names := []string{}
				for name := range sources {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					fmt.Fprintf(w, "  %s  revision %s\n", name, sources[name].Revision)
				}
			}
			if rendered, ok := value["result"].(prompts.Result); ok {
				fmt.Fprintf(w, "\n%s\n", rendered.Text)
			}
			return
		}
		if checks, ok := value["scenarios"].([]scenarioCheck); ok {
			fmt.Fprintf(w, "Catalog valid: %v; %d scenarios\n", value["valid"], len(checks))
			for _, c := range checks {
				status := "ok"
				if c.Changed {
					status = "changed"
				}
				if c.Error != "" {
					status = c.Error
				}
				fmt.Fprintf(w, "  %s (%s): %s\n", c.ID, c.Event, status)
			}
			if unavailable, ok := value["unavailable"].(string); ok && unavailable != "" {
				fmt.Fprintln(w, unavailable)
			}
			return
		}
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
	default:
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
	}
}

func (e *editor) show(ctx context.Context, q operationRequest, open bool) (any, error) {
	if q.DraftID == "" && q.ReviewID == "" {
		return nil, fmt.Errorf("show needs --draft or --review")
	}
	if q.DraftID != "" {
		q.Op = "draft-focus"
		q.ID = q.DraftID
		if _, err := e.operation(ctx, q); err != nil {
			return nil, err
		}
	}
	var address string
	_, err := e.withState(func(root *os.Root) (any, error) {
		var server struct {
			URL  string `json:"url"`
			Repo string `json:"repo"`
		}
		if err := readJSON(root, "server.json", &server); err != nil {
			return nil, fmt.Errorf("start prompt-editor serve in this checkout before show: %w", err)
		}
		if server.Repo != e.repo {
			return nil, fmt.Errorf("editor belongs to another checkout")
		}
		address = server.URL
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" {
		return nil, fmt.Errorf("invalid local editor address; restart prompt-editor serve")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", address+"/api/identity", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("editor is no longer running; start prompt-editor serve: %w", err)
	}
	defer response.Body.Close()
	var identity struct {
		Repo string `json:"repo"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&identity) != nil || identity.Repo != e.repo {
		return nil, fmt.Errorf("running editor does not match this checkout; restart prompt-editor serve")
	}
	kind, id := "draft", q.DraftID
	if q.ReviewID != "" {
		kind, id = "review", q.ReviewID
	}
	url := address + "/?" + kind + "=" + id
	if open {
		command := "xdg-open"
		if runtime.GOOS == "darwin" {
			command = "open"
		}
		if err := exec.CommandContext(ctx, command, url).Run(); err != nil {
			return nil, err
		}
	}
	return map[string]any{"url": url, "focus": q.Focus}, nil
}

const cliHelp = `usage: prompt-editor [command] [options]

A maintainer tool for the checkout's prompt catalog. Omitting the command starts the editor.
Every operation supports --repo PATH and --json. Text files accept - for stdin.

  serve [--port N] [--base REF]           start the browser editor
  list                                   list events
  inspect RECIPIENT/EVENT [--scenario ID] inspect sources, inputs and declarations
  uses FRAGMENT_OR_PATH                  direct and producer usages
  check [--scenario ID]                   validate catalog and saved scenarios
  compare --base REF [--scenario ID]      affected events and scenario diffs
  refresh                                regenerate and reload Go declarations
  scenarios                              list repository scenarios
  scenario save ID --event R/E --values FILE [--expect HASH]

  draft create --title TEXT              start a persistent shared draft
  draft list | draft get ID              inspect shared work
  draft put ID PATH --file FILE --expect HASH
  draft reset ID PATH --expect HASH      discard one draft edit
  draft apply ID --revision N            validate and save changed files
  draft sync ID --revision N             adopt refreshed checkout definitions
  draft focus ID --event R/E [--scenario ID] [--source PATH]
  draft archive ID --revision N          hide a draft; restore reverses this
  draft restore ID --revision N

  review create --draft ID --revision N [--title TEXT]
  review list | review get ID            inspect immutable snapshots and feedback
  review comment ID --message TEXT [--target source|prompt --selection TEXT]
  show --draft ID|--review ID [--open]    print an exact editor link
  watch --draft ID|--review ID --after N [--timeout 30s]

Use --draft ID or --review ID with inspect/check/compare to inspect shared work.
Use --set name=value for scenario inputs; --input field=scenario binds a producer.
Use --author NAME on changes. Revision conflicts exit 3; invalid input exits 2;
a failed scenario check exits 1. Reviews retain their own sources and definitions.
`
