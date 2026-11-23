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

type targets []string

func (t *targets) String() string { return strings.Join(*t, ", ") }
func (t *targets) Set(value string) error {
	*t = append(*t, value)
	return nil
}

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
	flags.StringVar(&q.Scenario, "scenario", "", "Saved scenario id")
	flags.StringVar(&q.Situation, "situation", "", "Saved situation id")
	flags.StringVar(&q.Event, "event", "", "recipient/event")
	flags.StringVar(&q.Path, "source", "", "Markdown source path")
	flags.StringVar(&q.Title, "title", "", "Scenario title")
	flags.StringVar(&q.Expect, "expect", "", "Expected scenario revision")
	flags.StringVar(&q.Base, "base", "", "Comparison branch, tag, or commit")
	flags.StringVar(&q.Mode, "mode", "merge-base", "merge-base or tip")
	valuesFile := flags.String("values", "", "Read scenario values as JSON; - reads stdin")
	open := flags.Bool("open", false, "Open the editor link in a browser")
	flags.Var(assignments(q.Values), "set", "Scenario input name=value; repeatable")
	flags.Var(assignments(q.Inputs), "input", "Producer binding field=scenario; repeatable")
	flags.Var((*targets)(&q.Include), "include", "Related event, fragment or source to include in context; repeatable")
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
	if q.Op == "scenario" {
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
	if q.Op == "show" && q.Event == "" {
		q.Event = q.ID
	}
	var result any
	if q.Op == "show" {
		result, err = e.show(ctx, q.Event, q.Situation, *open)
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
	case map[string]any:
		if q.Op == "authoring" {
			fmt.Fprint(w, value["workflow"])
			return
		}
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
			situations, _ := value["situations"].([]situationCheck)
			fmt.Fprintf(w, "Catalog valid: %v; %d scenarios; %d situations\n", value["valid"], len(checks), len(situations))
			for _, s := range situations {
				if s.Error != "" {
					fmt.Fprintf(w, "  situation %s: %s\n", s.ID, s.Error)
				}
			}
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

func (e *editor) show(ctx context.Context, event, situation string, open bool) (any, error) {
	if event != "" && situation != "" {
		return nil, fmt.Errorf("show opens an event or a situation, not both")
	}
	repo, err := os.OpenRoot(e.repo)
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	var server struct {
		URL  string `json:"url"`
		Repo string `json:"repo"`
	}
	if err := readJSON(repo, serverRecord, &server); err != nil {
		return nil, fmt.Errorf("start prompt-editor serve in this checkout before show: %w", err)
	}
	if server.Repo != e.repo {
		return nil, fmt.Errorf("editor belongs to another checkout")
	}
	address := server.URL
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
	url := address + "/"
	if situation != "" {
		situations, err := e.situations()
		if err != nil {
			return nil, err
		}
		if _, ok := situations[situation]; !ok {
			return nil, fmt.Errorf("unknown situation %s; list them with situations", situation)
		}
		url += "#situation=" + situation
	}
	if event != "" {
		if _, _, err := eventParts(event); err != nil {
			return nil, err
		}
		url += "#event=" + event
	}
	if open {
		command := "xdg-open"
		if runtime.GOOS == "darwin" {
			command = "open"
		}
		if err := exec.CommandContext(ctx, command, url).Run(); err != nil {
			return nil, err
		}
	}
	return map[string]any{"url": url}, nil
}

const cliHelp = `usage: prompt-editor [command] [options]

A maintainer tool for the checkout's prompt catalog. Omitting the command starts the editor.
Every operation supports --repo PATH and --json. --values accepts - for stdin.
Start prompt changes with context. Read its workflow and complete instructions before editing.

  serve [--port N] [--base REF]           start the browser editor; --base is the diff's default branch
  show [RECIPIENT/EVENT] [--open]         print the running editor's link to an event
  show --situation ID [--open]            print the running editor's link to a situation
  authoring                              read the prompt revision workflow
  context EVENT_OR_SOURCE [--base REF]   full sources, uses, scenarios and coverage gaps
  context --situation ID [--base REF]    the same for every event of a situation
  list                                   list events
  inspect RECIPIENT/EVENT [--scenario ID] inspect sources, inputs and declarations
  uses FRAGMENT_OR_PATH                  direct and producer usages
  check [--scenario ID]                   validate catalog, saved scenarios and situations
  compare --base REF [--scenario ID]      affected events and scenario diffs
  refresh                                regenerate and reload Go declarations
  scenarios                              list repository scenarios
  situations                             list repository situations
  scenario save ID --event R/E --values FILE [--expect HASH]

Use --include EVENT_OR_SOURCE (repeatable) with context to include semantically
related guidance. --scenario ID selects context; custom --set/--input values add
an example without replacing saved scenarios.
Use --set name=value for scenario inputs; --input field=scenario binds a producer.
Revision conflicts exit 3; invalid input exits 2; a failed scenario check exits 1.
`
