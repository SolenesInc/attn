package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func writeMemoryHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn memory <verb> [flags]

Crew memory is experimental: attn settings set crew_memory_enabled true.
Only the session bound to a crew member writes its memory. A delegate reports
on its seed with attn seed note <id> -m "…".

  add "<line>"           remember one UTF-8 line, at most 280 bytes
  recall '<regex>'       search entries, case insensitive; newest matches fit
  zoom <id|range>        open a summary's halves, or an entry's day references
  summarize             list every block ready to summarize and its sources
  summarize -           read answers from stdin: <range> <one line> per block
  forget <id>           blank an entry; dependent summaries need rewriting
  rewrite <range>       drop a summary and the summaries built above it
  import -              read <letter filename> <one line> per letter from stdin

Entry ids start at 0. Ranges include both ends: 16-31. Summaries also fit in
280 bytes. At closure, file with attn handoff --day "<one line>" -m -,
then summarize until no blocks remain. The last answer completes the requested
turnover. A failed turnover can be retried with attn handoff --retry.

Import letters oldest first, before the first summary exists. Import can shift
the ids already handed out that day. Imported letters have no known session;
new day lines link to both the letter and attn session transcript <session>.

memory/ is written by attn. Members and the user change it only through these
commands. Forget preserves the id and day references. Rewrite preserves entries.

flags:
  --session <id>    defaults to the current session
  --member <name>   read another member with recall or zoom, in the same profile
  --profile <name|id>  choose the profile for a read outside attn
  --json            print the result as JSON
`)
}

func runMemory(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		writeMemoryHelp(os.Stdout)
		return
	}
	if err := memoryCommand(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeMemoryHelp(os.Stdout)
			return
		}
		fmt.Fprintf(os.Stderr, "memory: %v\n", err)
		os.Exit(1)
	}
}

func memoryCommand(args []string) error {
	verb := args[0]
	switch verb {
	case "add", "recall", "zoom", "summarize", "forget", "rewrite", "import":
	default:
		return fmt.Errorf("unknown verb %q; attn memory --help lists the verbs", verb)
	}
	remaining := args[1:]
	arg := ""
	hasArg := len(remaining) > 0 && (remaining[0] == "-" || !strings.HasPrefix(remaining[0], "-"))
	if hasArg {
		arg, remaining = remaining[0], remaining[1:]
	}
	fs := flag.NewFlagSet("memory "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	session := fs.String("session", "", "current session")
	member := fs.String("member", "", "member to read")
	profile := fs.String("profile", "", "profile outside attn")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, remaining); err != nil {
		return err
	}
	if (*member != "" || *profile != "") && verb != "recall" && verb != "zoom" {
		return fmt.Errorf("--member and --profile apply only to recall and zoom; only a bound session writes memory")
	}
	cli := client.New("")
	sid := protocol.SessionID(*session)
	if sid == "" {
		var err error
		sid, err = currentSession(cli)
		if err != nil {
			return err
		}
	}
	if !hasArg && fs.NArg() == 1 {
		arg = fs.Arg(0)
		hasArg = true
	} else if fs.NArg() != 0 {
		return fmt.Errorf("memory %s takes one argument; attn memory --help shows the format", verb)
	}
	if !hasArg && verb != "summarize" {
		return fmt.Errorf("memory %s takes one argument; attn memory --help shows the format", verb)
	}
	var result any
	var err error
	switch verb {
	case "add":
		var r *protocol.CrewMemoryAddResult
		r, err = cli.MemoryAdd(sid, arg)
		result = r
		if err == nil && !*jsonOut {
			fmt.Printf("Remembered as #%s.\n", r.ID)
		}
	case "recall", "zoom":
		var r *protocol.CrewMemoryReadResult
		if verb == "recall" {
			r, err = cli.MemoryRecall(sid, *member, *profile, arg)
		} else {
			r, err = cli.MemoryZoom(sid, *member, *profile, arg)
		}
		result = r
		if err == nil && !*jsonOut {
			for _, line := range r.Lines {
				fmt.Println(line)
			}
			if r.Matches != nil {
				if *r.Matches > len(r.Lines) {
					fmt.Printf("Newest %d of %d matches; %d left out by the %d-byte entry output cap. Narrow the regex.\n", len(r.Lines), *r.Matches, *r.Matches-len(r.Lines), crew.MemoryRecallBytes)
				} else {
					fmt.Printf("%d matches.\n", *r.Matches)
				}
			}
			if r.Letter != nil {
				fmt.Printf("Letter: %s\n", *r.Letter)
			}
			if r.SessionID != nil {
				fmt.Printf("Transcript: attn session transcript %s\n", *r.SessionID)
			}
		}
	case "summarize":
		var answers []protocol.CrewMemoryAnswer
		if arg != "" {
			lines, readErr := memoryInput(arg)
			if readErr != nil {
				return readErr
			}
			for _, line := range lines {
				answers = append(answers, protocol.CrewMemoryAnswer{Range: line[0], Text: line[1]})
			}
		}
		var r *protocol.CrewMemorySummarizeResult
		r, err = cli.MemorySummarize(sid, answers)
		result = r
		if err == nil && !*jsonOut {
			fmt.Printf("%d summaries pending.\n", r.PendingSummaries)
			for _, block := range r.Blocks {
				fmt.Printf("\n%s: summarize into one line of at most 280 bytes. Keep what has lasting effect; invent nothing.\n", block.Range)
				for _, line := range block.Lines {
					fmt.Println(line)
				}
			}
			if len(r.Blocks) != 0 {
				fmt.Println("\nPipe answers to attn memory summarize -: <range> <one line> per block.")
			}
			if r.Handoff != nil {
				printHandoffResult(r.Handoff, true)
			}
		}
	case "forget":
		id, parseErr := strconv.Atoi(arg)
		if parseErr != nil {
			return fmt.Errorf("%q is not an entry id", arg)
		}
		result, err = cli.MemoryForget(sid, id)
	case "rewrite":
		result, err = cli.MemoryRewrite(sid, arg)
	case "import":
		lines, readErr := memoryInput(arg)
		if readErr != nil {
			return readErr
		}
		var imports []protocol.CrewMemoryImportLine
		for _, line := range lines {
			imports = append(imports, protocol.CrewMemoryImportLine{Letter: line[0], Text: line[1]})
		}
		result, err = cli.MemoryImport(sid, imports)
	default:
		return fmt.Errorf("unknown verb %q; attn memory --help lists the verbs", verb)
	}
	if err != nil {
		return err
	}
	if *jsonOut {
		printJSON(result)
	} else if change, ok := result.(*protocol.CrewMemoryChangeResult); ok {
		if change.Imported != nil {
			fmt.Printf("Imported %d letters. Entry ids may have shifted.\n", *change.Imported)
		}
		fmt.Printf("Memory updated; %d summaries pending for closure.\n", change.PendingSummaries)
	}
	return nil
}

func memoryInput(arg string) ([][2]string, error) {
	if arg != "-" {
		return nil, fmt.Errorf("pass - to read lines from stdin")
	}
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var lines [][2]string
	for i, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		head, text, ok := strings.Cut(line, " ")
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("stdin line %d needs an id or filename, a space, and one line of text", i+1)
		}
		lines = append(lines, [2]string{head, text})
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("stdin has no lines")
	}
	return lines, nil
}
