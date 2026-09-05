package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func runSeed() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeSeedHelp(os.Stdout)
		return
	}
	args := os.Args[3:]
	switch os.Args[2] {
	case "plant":
		runSeedPlant(args)
	case "plot":
		runSeedPlot(args)
	case "ls":
		runSeedList(args)
	case "show":
		runSeedShow(args)
	case "review":
		runSeedReview(args)
	case "edit":
		runSeedEdit(args)
	case "handover":
		runSeedHandover(args)
	case "send-to-chief":
		runSeedSendToChief(args)
	case "set-resume":
		runSeedSetResume(args)
	case "export":
		runSeedExport(args)
	case "tend", "park", "harvest", "wither", "replant":
		runSeedTransition(os.Args[2], args)
	case "note":
		runSeedNote(args)
	case "watch", "unwatch":
		runSeedWatch(os.Args[2], args)
	case "attach", "detach":
		runSeedArtifact(os.Args[2], args)
	case "notes":
		runSeedNotes(args)
	case "link", "unlink":
		runSeedLink(os.Args[2] == "unlink", args)
	case "ready":
		runSeedReady(args)
	case "prime":
		runSeedPrime(args)
	case "guide":
		runSeedGuide(args)
	default:
		fmt.Fprintf(os.Stderr, "seed: unknown command %q\n", os.Args[2])
		writeSeedHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeSeedHelp(w io.Writer) {
	fmt.Fprintf(w, `usage: attn seed <command>

A seed is the unit of work: one document, one short id, one slug made of the
title's key words, planted in the garden. Commands take the id; the slug is
how the seed is spoken of.
Anything worth handing off, parking, or attributing is a seed.

The garden lives at the home daemon. On an outpost every command here refuses,
naming the home to run it on.

commands:
  plant "<title>" [-m <body>] [--part-of <plot>] [--discovered-from <seed>] [resume flags] [flags]
        plant a seed and print its id, slug and title. -m takes markdown, or - to read stdin —
        if the seed gains children, that body is the plot's plan. --part-of
        plants it under a plot. --resume-session-id, --cwd and --agent
        together make a dead conversation resumable without a dispatch record.

  plot [-f <path>] [--json]
        plant a whole plot in one move from a JSON payload (-f, or stdin):
        {"title": …, "body": …, "children": [{"title": …, "body": …,
        "blocks": ["<sibling step slug>"]}]}. Children are parallel by
        default; blocks names the siblings a child holds back. Prints the
        plot's id and each child's id and step slug.

  ls [--stale [--window <duration>]] [--flat] [--json]
        the garden, newest first, with children nested under their plot.
        --flat prints one list. --stale narrows to open seeds whose log has not moved
        for the window (default %s) — a query for your judgment, never a
        reaper.

  ready [--plot <plot> | --all] [--json]
        what you can tend right now, oldest first: nothing open blocks it,
        nobody is holding it, and it is not a plot — a plot's work is its
        children. With no flags the scope is the whole garden — unless this
        session was dispatched at a plot, and then that plot; --all steps back
        out to the garden.

  link <a> blocks <b> | link <a> part-of <b> | link <a> discovered-from <b>
        relate two seeds. "a blocks b" keeps b out of ready until a closes;
        "a part-of b" puts a under b; b is then a plot. "a discovered-from b"
        records where work came from without ordering it. A structural cycle
        is refused, naming both seeds and the edge to remove.

  unlink <a> blocks <b> | unlink <a> part-of <b> | unlink <a> discovered-from <b>
        remove the edge. Every link has one.

  show <id> [--json]
        one seed: the freshest handoff left on it, its state, who tends it,
        every edge that touches it in both directions, its body, and the newest
        notes on its log.

  review start | show [<review-id>] | cancel <review-id> | retry <review-id> <seed-id> | keep <review-id> <seed-id> [--json]
        start an advisory Garden review, inspect its progressive results,
		cancel unfinished classification, retry one failed or changed item, or keep
		one seed growing and review it again after seven quiet days.
        The review never changes a seed's state.

  watch <id> | unwatch <id>
        ring this session when the seed or anything in its plot moves. A watch
        survives through the session's day; unwatch is the way out.

  edit <id> -m <body>
        replace the seed's markdown body without moving its state or claim.
        - reads stdin; an explicit empty -m clears the body.

  set-resume <id> (--resume-session-id <id> --cwd <path> --agent <name> | --clear)
        set or clear the seed-owned fallback used when attn has no dispatch
        record for the conversation. The three identity fields move together.

  handover <id> [-m "<what the new agent should know>"] [worker flags]
        give this seed to a new agent. The seed body remains the brief and the
        optional handoff lands on its log only after the new agent starts. The
        saved directory is reused; if it was removed, its verified branch is
        recreated when safe. Use --cwd when attn asks you to choose a place.

  send-to-chief <id> [-m "<optional guidance>"]
        give this seed to the Chief to decide its next working context. The
        saved folder, branch and placement problem are recorded automatically;
        -m adds an exception such as a special branch or directory.

  tend <id> [--member <name>] [--force]
        claim the seed and start growing it. One tender at a time: tending a
        seed somebody else still holds is refused, naming them, and takes
        --force to go through anyway. The freshest handoff prints on the
        claim, so picking a seed up primes you.

  park <id> [-m "<where you left it>"] [--member <name>] [--force]
        pause the seed deliberately — it goes dormant and lets go of its
        tender. An optional comment lands on the log in the same move. Tending
        it again picks it back up.

  harvest <id> -m "<what got done>" [--member <name>] [--force]
        close the seed as done. The reason is the point of the record.

  wither <id> [-m "<why>"] [--member <name>] [--force]
        close the seed as abandoned. Nobody is picking this up.

  replant <id> [--member <name>] [--force]
        put the seed back in the pool — planted, unclaimed, ready for whoever
        is free. Reopens a closed seed, un-parks a dormant one, and hands back
        one being grown.

  note <id> -m "<what happened>" [--handoff] [--ring]
        append to the seed's log — what happened and what you learned, for
        whoever tends it next. - reads stdin. --handoff addresses it to your
        successor on this seed: show renders the freshest one first and tend
        prints it on the claim, so it is read before any work.
        --ring tells watchers to look; ordinary notes stay quiet.

  attach <id> --path <file> (--move | --copy)
        put a local regular file in durable seed-owned storage. Move is the
        recommended choice and leaves one editable canonical file; Copy keeps
        an independent source snapshot. Git-tracked files must use Copy.
        Notebook, URL and repository references remain associations.

  detach <id> --path <filename> --to <destination>
        move a managed artifact back out without overwriting. An old linked
        path association can be removed explicitly with --reference instead.

  notes <id> [--limit <n>] [--json]
        the whole log, newest first. show renders the newest few and says
        how many more are here.

  guide
        print the craft: writing a body, deliverable types and what "done" is
        for each, artifacts, and handoffs and steering.

  prime
        print the standing garden contract and this session's live garden
        position.

  export <id> [--out <path>] [--json]
        write the seed's body to markdown, stamped as generated from the seed —
        the file to open, read and annotate. --out - writes to stdout; the
        default is <id>.md in the current directory. The seed stays the source:
        edit the seed and export again, never the file.

flags:
  --part-of <plot>   plant under a plot (plant)
  --discovered-from <seed>  record the seed this work came from (plant)
  --resume-session-id <id>  agent-native conversation id (plant, set-resume)
  --cwd <path>        directory to reopen in (plant, set-resume)
  --agent <name>      agent driver to reopen with, or the new Handover agent
                      (plant, set-resume, handover)
  --model <name>      model for the new agent (handover; defaults normally)
  --effort <level>    reasoning effort for the new agent (handover)
  --name <text>       name for the new agent (handover)
  --request-id <id>   stable retry key (handover; generated when omitted)
  --yolo              bypass agent approval prompts (handover)
  --clear             remove the fallback identity (set-resume)
  --plot <plot>      scope a ready answer to one plot
  --flat             print a listing without nesting
  --stale            only open seeds whose log has not moved (ls)
  --window <d>       the stale window, like 72h or 14d (ls --stale)
  -f <path>          the plot payload to read (plot; default stdin)
  --handoff          write a note to whoever tends the seed next (note)
  --ring             ring watchers after this note lands (note)
  --path <file>      local source, managed filename, or linked path
  --repo <name>      the repository that path lives in (attach, detach)
  --notebook <id>    a Notebook document (attach, detach)
  --url <url>        anything reachable by URL (attach, detach)
  --move             transfer a local source into seed ownership (attach)
  --copy             snapshot a local source into seed ownership (attach)
  --to <path>        destination that receives a managed artifact (detach)
  --reference        remove an old linked path association (detach)
  --force            act even though somebody else holds the seed; the log
                     records it (tend, park, harvest, wither, replant)
  --member <name>    the crew member asking, recorded as planter, tender or
                     note author
  --session <id>     the session asking (defaults to ATTN_SESSION_ID)
  --limit <n>        how many log entries to read (notes)
  --json             print the result as JSON
`, formatWindow(garden.DefaultStaleWindow))
}

func seedClient() *client.Client {
	return client.New(config.SocketPath())
}

var seedPrimeText = hooks.GardenGuidance

func seedPrimeTailFromReady(ready *protocol.SeedReadyResult) string {
	values := prompts.Values{
		"has_crown": fmt.Sprint(ready.Crown != nil),
		"has_ready": fmt.Sprint(len(ready.Seeds) > 0),
		"one_ready": fmt.Sprint(len(ready.Seeds) == 1),
		"count":     fmt.Sprint(len(ready.Seeds)),
		"is_plot":   "false",
		"seed_id":   "",
		"title":     "",
		"rows":      "",
	}
	if ready.Crown != nil {
		values["seed_id"] = ready.Crown.ID
		values["title"] = fmt.Sprintf("%q", ready.Crown.Title)
		values["is_plot"] = fmt.Sprint(ready.Crown.PlotProgress != nil)
	}
	var rows strings.Builder
	handoffs := freshestHandoffs(ready.Handoffs)
	for _, seed := range ready.Seeds {
		handoff := handoffs[seed.ID]
		rows.WriteString(prompts.RenderText("session", "garden-row", prompts.Values{"seed_id": seed.ID, "slug": seed.StepSlug, "title": seed.Title, "handoff": handoff.Body, "author": crew.HolderName(handoff.AuthorMember, handoff.AuthorSession)}))
	}

	values["rows"] = rows.String()
	return prompts.RenderText("session", "garden-ready", values)
}

func seedPrimeFromReady(ready *protocol.SeedReadyResult) string {
	return seedPrimeText + "\n\n" + seedPrimeTailFromReady(ready)
}

func runSeedPrime(args []string) {
	f := newSeedFlags("prime")
	if positionals := f.parse("prime", args); len(positionals) != 0 {
		seedFail("prime", fmt.Errorf("takes no arguments, got %q", positionals[0]))
	}
	ready, err := seedClient().SeedReady(f.sessionID(), "", false)
	if err != nil {
		seedFail("prime", err)
	}
	fmt.Print(seedPrimeFromReady(ready))
}

func seedFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "seed %s: %v\n", verb, err)
	os.Exit(1)
}

type seedFlags struct {
	fs             *flag.FlagSet
	session        *string
	member         *string
	json           *bool
	all            *bool
	flat           *bool
	stale          *bool
	window         *string
	plot           *string
	partOf         *string
	discoveredFrom *string
	file           *string
	message        *string
	out            *string
	limit          *int
	handoff        *bool
	ring           *bool
	path           *string
	repo           *string
	notebook       *string
	url            *string
	move           *bool
	copy           *bool
	to             *string
	reference      *bool
	resumeID       *string
	cwd            *string
	agent          *string
	model          *string
	effort         *string
	name           *string
	requestID      *string
	yolo           *bool
	clear          *bool
	force          *bool
}

func newSeedFlags(verb string) *seedFlags {
	fs := flag.NewFlagSet("seed "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return &seedFlags{
		fs:             fs,
		session:        fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)"),
		member:         fs.String("member", "", "crew member planting this seed"),
		json:           fs.Bool("json", false, "print the result as JSON"),
		all:            fs.Bool("all", false, "the whole garden, overriding a dispatched session's plot"),
		flat:           fs.Bool("flat", false, "print seeds as one list without nesting"),
		stale:          fs.Bool("stale", false, "only open seeds whose log has not moved for the window"),
		window:         fs.String("window", "", "the stale window, like 72h or 14d"),
		plot:           fs.String("plot", "", "scope to one plot"),
		partOf:         fs.String("part-of", "", "plant under this plot"),
		discoveredFrom: fs.String("discovered-from", "", "record the seed this work came from"),
		file:           fs.String("f", "", "the plot payload to read (- or empty reads stdin)"),
		message:        fs.String("m", "", "the seed's body, as markdown (- reads stdin)"),
		out:            fs.String("out", "", "file to write (- for stdout)"),
		limit:          fs.Int("limit", 0, "how many log entries to read"),
		handoff:        fs.Bool("handoff", false, "write this note to whoever tends the seed next"),
		ring:           fs.Bool("ring", false, "ring the seed's watchers after this note lands"),
		path:           fs.String("path", "", "a markdown document at this path"),
		repo:           fs.String("repo", "", "the repository the path lives in"),
		notebook:       fs.String("notebook", "", "a Notebook document, by its id"),
		url:            fs.String("url", "", "anything reachable by URL"),
		move:           fs.Bool("move", false, "move a local file into seed ownership"),
		copy:           fs.Bool("copy", false, "copy a local file into seed ownership"),
		to:             fs.String("to", "", "destination for a detached managed artifact"),
		reference:      fs.Bool("reference", false, "operate on an old linked path association"),
		resumeID:       fs.String("resume-session-id", "", "agent-native conversation id"),
		cwd:            fs.String("cwd", "", "directory to reopen in"),
		agent:          fs.String("agent", "", "agent driver to reopen with"),
		model:          fs.String("model", "", "model for the new agent"),
		effort:         fs.String("effort", "", "reasoning effort for the new agent"),
		name:           fs.String("name", "", "name for the new agent"),
		requestID:      fs.String("request-id", "", "stable retry key"),
		yolo:           fs.Bool("yolo", false, "bypass agent approval prompts"),
		clear:          fs.Bool("clear", false, "remove the seed-owned resume identity"),
		force:          fs.Bool("force", false, "act even though somebody else still holds the seed"),
	}
}

func (f *seedFlags) noteKind() string {
	if *f.handoff {
		return garden.NoteKindHandoff
	}
	return ""
}

func (f *seedFlags) text(verb string) string {
	if *f.message != "-" {
		return *f.message
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		seedFail(verb, fmt.Errorf("reading stdin: %w", err))
	}
	return string(raw)
}

// parse reads flags interleaved with positionals: Go's parser stops at the first
// positional, so a --json written after the title would otherwise be swallowed.
func (f *seedFlags) parse(verb string, args []string) []string {
	var positionals []string
	rest := args
	for {
		if err := f.fs.Parse(rest); err != nil {
			fmt.Fprintf(os.Stderr, "seed %s: %v\n", verb, err)
			writeSeedHelp(os.Stderr)
			os.Exit(2)
		}
		rest = f.fs.Args()
		if len(rest) == 0 {
			return positionals
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
}

func (f *seedFlags) sessionID() string {
	if id := strings.TrimSpace(*f.session); id != "" {
		return id
	}
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
}

func (f *seedFlags) wasSet(name string) bool {
	set := false
	f.fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			set = true
		}
	})
	return set
}

func (f *seedFlags) staleWindowSeconds() int {
	raw := strings.TrimSpace(*f.window)
	if raw == "" {
		return 0
	}
	window, err := parseWindow(raw)
	if err != nil {
		seedFail("ls", err)
	}
	return int(window / time.Second)
}

// parseWindow reads a stale window. Go's ParseDuration tops out at hours, and
// a stale window is naturally said in days, so `d` is accepted as 24h.
func parseWindow(raw string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		if n, err := time.ParseDuration(days + "h"); err == nil {
			return n * 24, nil
		}
	}
	window, err := time.ParseDuration(raw)
	if err != nil || window <= 0 {
		return 0, fmt.Errorf("%q is not a window; say it like 72h or 14d", raw)
	}
	return window, nil
}

func formatWindow(window time.Duration) string {
	if window >= 24*time.Hour && window%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", window/(24*time.Hour))
	}
	return window.String()
}

func runSeedPlant(args []string) {
	f := newSeedFlags("plant")
	positionals := f.parse("plant", args)
	if len(positionals) != 1 {
		seedFail("plant", fmt.Errorf(`needs exactly one title, got %d: attn seed plant "what this is" [-m "the detail"]`, len(positionals)))
	}
	result, err := seedClient().SeedPlant(
		f.sessionID(), positionals[0], f.text("plant"), strings.TrimSpace(*f.partOf), strings.TrimSpace(*f.discoveredFrom), strings.TrimSpace(*f.member),
		strings.TrimSpace(*f.resumeID), strings.TrimSpace(*f.cwd), strings.TrimSpace(*f.agent),
	)
	if err != nil {
		seedFail("plant", err)
	}
	if *f.json {
		writeJSON(result.Seed)
		return
	}
	fmt.Println(seedLine(result.Seed))
}

// The line an agent echoes: the id is what commands take, the slug is what the
// user hears, the title is what both mean.
func seedLine(seed protocol.Seed) string {
	return fmt.Sprintf("%s  %s  %s", seed.ID, seed.StepSlug, seed.Title)
}

func seedHandle(seed protocol.Seed) string {
	if seed.StepSlug == "" {
		return seed.ID
	}
	return seed.ID + " (" + seed.StepSlug + ")"
}

func runSeedList(args []string) {
	f := newSeedFlags("ls")
	if positionals := f.parse("ls", args); len(positionals) != 0 {
		seedFail("ls", fmt.Errorf("takes no arguments, got %q; to read one seed use `attn seed show <id>`", positionals[0]))
	}
	if !*f.stale && flagWasSet(f.fs, "window") {
		seedFail("ls", fmt.Errorf("--window is the stale window; it only means something with --stale"))
	}
	result, err := seedClient().SeedList(f.sessionID(), *f.stale, f.staleWindowSeconds())
	if err != nil {
		seedFail("ls", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	if *f.stale {
		fmt.Printf("open seeds whose log has not moved for %s — no note, no move, no edge. This is a query, not a reaper: tend, note or park what still matters.\n\n",
			staleWindowLabel(result.StaleWindowSeconds))
	}
	if len(result.Seeds) == 0 {
		if *f.stale {
			fmt.Println("none — every open seed has moved inside the window")
			return
		}
		fmt.Println("the garden is empty — `attn seed plant \"what this is\"` puts something in it")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSLUG\tSTATUS\tTENDER\tPLANTED\tTITLE")
	for _, row := range seedRows(result.Seeds, *f.flat) {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s%s%s\n",
			row.seed.ID, row.seed.StepSlug, row.seed.Status, orDash(crew.HolderName(row.seed.TenderMember, row.seed.TenderSession)),
			shortStamp(row.seed.CreatedAt), strings.Repeat("  ", row.depth), row.seed.Title, plotProgressSuffix(row.seed))
	}
	w.Flush()
	if result.Total > len(result.Seeds) {
		fmt.Printf("\nshowing the newest %d of %d seeds — one read is capped at %d. The %d not shown are the oldest; `attn seed show <id>` still reaches any of them.\n",
			len(result.Seeds), result.Total, len(result.Seeds), result.Total-len(result.Seeds))
	}
}

func runSeedReview(args []string) {
	if len(args) == 0 {
		seedFail("review", fmt.Errorf("needs start, show, cancel, retry, or keep"))
	}
	verb := args[0]
	positionals, jsonOutput, err := parseSeedReviewArgs(args[1:])
	if err != nil {
		seedFail("review "+verb, err)
	}

	var result *protocol.SeedReviewResult
	switch verb {
	case "start":
		if len(positionals) != 0 {
			seedFail("review start", fmt.Errorf("takes no arguments"))
		}
		result, err = seedClient().SeedReviewStart()
	case "show":
		if len(positionals) > 1 {
			seedFail("review show", fmt.Errorf("takes at most one review id"))
		}
		reviewID := ""
		if len(positionals) == 1 {
			reviewID = positionals[0]
		}
		result, err = seedClient().SeedReviewShow(reviewID)
	case "cancel":
		if len(positionals) != 1 {
			seedFail("review cancel", fmt.Errorf("needs exactly one review id"))
		}
		result, err = seedClient().SeedReviewCancel(positionals[0])
	case "retry":
		if len(positionals) != 2 {
			seedFail("review retry", fmt.Errorf("needs a review id and seed id"))
		}
		result, err = seedClient().SeedReviewRetry(positionals[0], positionals[1])
	case "keep":
		if len(positionals) != 2 {
			seedFail("review keep", fmt.Errorf("needs a review id and seed id"))
		}
		shown, showErr := seedClient().SeedReviewShow(positionals[0])
		if showErr != nil {
			seedFail("review keep", showErr)
		}
		var receipt *protocol.GardenReviewItem
		if shown.Review != nil {
			for i := range shown.Review.Items {
				if shown.Review.Items[i].SeedID == positionals[1] {
					receipt = &shown.Review.Items[i]
					break
				}
			}
		}
		if receipt == nil {
			seedFail("review keep", fmt.Errorf("seed %s is not part of Garden review %s", positionals[1], positionals[0]))
		}
		result, err = seedClient().SeedReviewKeep(positionals[1], protocol.SeedReviewActionContext{
			ReviewID: positionals[0], EvidenceVersion: receipt.EvidenceVersion,
		})
	default:
		seedFail("review", fmt.Errorf("unknown command %q; use start, show, cancel, retry, or keep", verb))
	}
	if err != nil {
		seedFail("review "+verb, err)
	}
	if jsonOutput {
		writeJSON(result)
		return
	}
	if result.Review == nil {
		fmt.Printf("%d seeds need review\n", result.CandidateCount)
		return
	}
	fprintSeedReview(os.Stdout, *result.Review, verb == "show")
}

func parseSeedReviewArgs(args []string) ([]string, bool, error) {
	positionals := make([]string, 0, len(args))
	jsonOutput := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOutput = true
		case strings.HasPrefix(arg, "-"):
			return nil, false, fmt.Errorf("unknown flag %q", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	return positionals, jsonOutput, nil
}

func fprintSeedReview(w io.Writer, review protocol.GardenReview, detailed bool) {
	run := review.Run
	fmt.Fprintf(w, "review %s\t%s\t%d seeds\n", run.ID, run.Status, len(review.Items))
	fmt.Fprintf(w, "captured %s\tadvisor %s / %s", run.CapturedAt, run.Recipe.Agent, run.Recipe.Model)
	if effort := protocol.Deref(run.Recipe.Effort); effort != "" {
		fmt.Fprintf(w, " / %s", effort)
	}
	fmt.Fprintln(w)
	if len(review.Items) == 0 {
		fmt.Fprintln(w, "no seeds needed review when this run started")
		return
	}

	if !detailed {
		fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SEED\tSTATUS\tRECOMMENDATION\tTITLE")
		for _, item := range review.Items {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", item.SeedID, item.Status,
				orDash(protocol.Deref(item.Recommendation)), item.Title)
		}
		tw.Flush()
		return
	}

	for _, item := range review.Items {
		fmt.Fprintf(w, "\n%s  %s\n", item.SeedID, item.Title)
		fmt.Fprintf(w, "status\t%s\n", item.Status)
		if recommendation := protocol.Deref(item.Recommendation); recommendation != "" {
			fmt.Fprintf(w, "advice\t%s: %s\n", recommendation, protocol.Deref(item.Explanation))
		}
		if item.Resolution != garden.ReviewResolutionUnresolved {
			fmt.Fprintf(w, "resolution\t%s\n", item.Resolution)
		}
		if len(item.Actions) > 0 {
			fmt.Fprintf(w, "actions\t%s\n", strings.Join(item.Actions, ", "))
		}
		if itemError := protocol.Deref(item.Error); itemError != "" {
			fmt.Fprintf(w, "error\t%s\n", itemError)
		}
		for _, evidence := range item.Evidence {
			fmt.Fprintf(w, "%s\t%s\n", evidence.Label, evidence.Text)
		}
	}
}

func staleWindowLabel(seconds *int) string {
	if seconds == nil || *seconds <= 0 {
		return formatWindow(garden.DefaultStaleWindow)
	}
	return formatWindow(time.Duration(*seconds) * time.Second)
}

func plotProgressSuffix(seed protocol.Seed) string {
	if seed.PlotProgress == nil {
		return ""
	}
	p := *seed.PlotProgress
	return fmt.Sprintf("  [%d/%d done · %d growing · %d ready · %d blocked]",
		p.Done, p.Total, p.Growing, p.Ready, p.Blocked)
}

func runSeedPlot(args []string) {
	f := newSeedFlags("plot")
	if positionals := f.parse("plot", args); len(positionals) != 0 {
		seedFail("plot", fmt.Errorf("takes no arguments, got %q; the plot is JSON on stdin or at -f <path>", positionals[0]))
	}
	spec, err := garden.ParsePlotSpec(readPlotPayload(strings.TrimSpace(*f.file)))
	if err != nil {
		seedFail("plot", err)
	}
	msg := protocol.SeedPlotMessage{Title: spec.Title}
	if spec.Body != "" {
		msg.Body = protocol.Ptr(spec.Body)
	}
	for _, child := range spec.Children {
		wire := protocol.SeedPlotChild{Title: child.Title, Blocks: child.Blocks}
		if child.Body != "" {
			wire.Body = protocol.Ptr(child.Body)
		}
		msg.Children = append(msg.Children, wire)
	}
	result, err := seedClient().SeedPlot(f.sessionID(), strings.TrimSpace(*f.member), msg)
	if err != nil {
		seedFail("plot", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	fmt.Println(seedLine(result.Crown))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, child := range result.Children {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", child.ID, child.StepSlug, child.Title)
	}
	w.Flush()
}

func readPlotPayload(path string) []byte {
	if path == "" || path == "-" {
		payload, err := io.ReadAll(os.Stdin)
		if err != nil {
			seedFail("plot", fmt.Errorf("read the plot from stdin: %w", err))
		}
		return payload
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		seedFail("plot", err)
	}
	return payload
}

type seedRow struct {
	seed  protocol.Seed
	depth int
}

func seedRows(seeds []protocol.Seed, flat bool) []seedRow {
	rows := make([]seedRow, 0, len(seeds))
	if flat {
		for _, seed := range seeds {
			rows = append(rows, seedRow{seed: seed})
		}
		return rows
	}
	byID := make(map[string]protocol.Seed, len(seeds))
	domain := make([]garden.Seed, 0, len(seeds))
	for _, seed := range seeds {
		byID[seed.ID] = seed
		domain = append(domain, gardenSeedFromWire(seed))
	}
	for _, row := range garden.Tree(domain) {
		rows = append(rows, seedRow{seed: byID[row.Seed.ID], depth: row.Depth})
	}
	return rows
}

func shortStamp(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.Local().Format("2006-01-02 15:04")
}

func runSeedShow(args []string) {
	f := newSeedFlags("show")
	positionals := f.parse("show", args)
	if len(positionals) != 1 {
		seedFail("show", fmt.Errorf("needs exactly one seed id, got %d: attn seed show s-7k3f9m", len(positionals)))
	}
	result, err := seedClient().SeedShow(f.sessionID(), positionals[0])
	if err != nil {
		seedFail("show", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	fprintSeedShow(os.Stdout, result)
}

func runSeedEdit(args []string) {
	f := newSeedFlags("edit")
	positionals := f.parse("edit", args)
	if len(positionals) != 1 {
		seedFail("edit", fmt.Errorf("needs exactly one seed id, got %d: attn seed edit s-7k3f9m -m -", len(positionals)))
	}
	if !f.wasSet("m") {
		seedFail("edit", fmt.Errorf("needs -m <body>; use -m - to read markdown from stdin, or -m '' to clear it"))
	}
	result, err := seedClient().SeedEdit(positionals[0], f.text("edit"))
	if err != nil {
		seedFail("edit", err)
	}
	if *f.json {
		writeJSON(result.Seed)
		return
	}
	fmt.Printf("updated %s at revision %d\n", result.Seed.ID, result.Seed.Rev)
}

func runSeedSetResume(args []string) {
	f := newSeedFlags("set-resume")
	positionals := f.parse("set-resume", args)
	if len(positionals) != 1 {
		seedFail("set-resume", fmt.Errorf("needs exactly one seed id, got %d", len(positionals)))
	}
	result, err := seedClient().SeedSetResume(
		positionals[0], strings.TrimSpace(*f.resumeID), strings.TrimSpace(*f.cwd), strings.TrimSpace(*f.agent), *f.clear,
	)
	if err != nil {
		seedFail("set-resume", err)
	}
	if *f.json {
		writeJSON(result.Seed)
		return
	}
	fprintSeed(os.Stdout, result.Seed)
}

func runSeedHandover(args []string) {
	f := newSeedFlags("handover")
	positionals := f.parse("handover", args)
	if len(positionals) != 1 {
		seedFail("handover", fmt.Errorf("needs exactly one seed id, got %d: attn seed handover s-7k3f9m", len(positionals)))
	}
	seedID := strings.TrimSpace(positionals[0])
	c := seedClient()
	document, err := c.SeedShow(f.sessionID(), seedID)
	if err != nil {
		seedFail("handover", err)
	}
	requestID := strings.TrimSpace(*f.requestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	handoff := strings.TrimSpace(f.text("handover"))
	request := &protocol.SeedHandoverRequest{
		SeedID:                document.Seed.ID,
		ExpectedRev:           document.Seed.Rev,
		ExpectedTenderSession: document.Seed.TenderSession,
		ExpectedTenderMember:  document.Seed.TenderMember,
	}
	if handoff != "" {
		request.Handoff = protocol.Ptr(handoff)
	}

	fmt.Fprintf(os.Stderr, "handover request: request_id=%s seed_id=%s\n", requestID, seedID)
	operation, err := c.StartDelegation(f.sessionID(), "", client.DelegateOptions{
		RequestID: requestID,
		Agent:     strings.TrimSpace(*f.agent),
		Model:     strings.TrimSpace(*f.model),
		Effort:    strings.TrimSpace(*f.effort),
		Label:     strings.TrimSpace(*f.name),
		Yolo:      *f.yolo,
		CWD:       strings.TrimSpace(*f.cwd),
		Handover:  request,
	})
	if err != nil {
		seedFail("handover", err)
	}
	fmt.Fprintf(os.Stderr, "handover accepted: request_id=%s operation_id=%s session_id=%s\n",
		operation.RequestID, operation.OperationID, operation.SessionID)
	operation, err = waitDelegationCLI(c, operation, requestID, os.Stderr)
	if err != nil {
		seedFail("handover", err)
	}
	if *f.json {
		writeJSON(operation.Result)
		return
	}
	fmt.Printf("%s handed over to session %s in %s\n", seedID, operation.Result.SessionID, operation.Result.Directory)
}

func runSeedSendToChief(args []string) {
	f := newSeedFlags("send-to-chief")
	positionals := f.parse("send-to-chief", args)
	if len(positionals) != 1 {
		seedFail("send-to-chief", fmt.Errorf(
			"needs exactly one seed id, got %d: attn seed send-to-chief s-7k3f9m", len(positionals)))
	}
	seedID := strings.TrimSpace(positionals[0])
	c := seedClient()
	document, err := c.SeedShow(f.sessionID(), seedID)
	if err != nil {
		seedFail("send-to-chief", err)
	}
	result, err := c.SeedSendToChief(
		f.sessionID(), document.Seed, strings.TrimSpace(f.text("send-to-chief")))
	if err != nil {
		seedFail("send-to-chief", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	fmt.Printf("%s sent to Chief (%s)\n", seedID, result.Detail)
}

func fprintArtifacts(w io.Writer, artifacts []protocol.SeedArtifact) {
	fmt.Fprintln(w, "artifacts:")
	for _, artifact := range artifacts {
		fmt.Fprintf(w, "  file  %s  %d bytes\n", artifact.Filename, artifact.Size)
	}
}

func fprintArtifactReferences(w io.Writer, artifacts []protocol.SeedArtifactReference) {
	fmt.Fprintln(w, "linked artifacts:")
	for _, artifact := range artifacts {
		line := protocol.Deref(artifact.Path)
		if line == "" {
			line = protocol.Deref(artifact.NotebookDocumentID)
		}
		if line == "" {
			line = protocol.Deref(artifact.URL)
		}
		if line == "" {
			line = protocol.Deref(artifact.Repository)
		}
		if repo := protocol.Deref(artifact.Repository); repo != "" && repo != line {
			line += " (" + repo + ")"
		}
		fmt.Fprintf(w, "  %s  %s\n", artifact.Kind, line)
	}
}

func fprintSeedShow(w io.Writer, result *protocol.SeedShowResult) {
	fprintHandoff(w, result.Handoff)
	fprintSeed(w, result.Seed, result.Watching)
	if len(result.Relations) > 0 {
		fmt.Fprintln(w)
		fprintRelations(w, result.Relations)
	}
	if len(result.Artifacts) > 0 {
		fmt.Fprintln(w)
		fprintArtifacts(w, result.Artifacts)
	}
	if len(result.References) > 0 {
		fmt.Fprintln(w)
		fprintArtifactReferences(w, result.References)
	}
	entries := withoutNote(result.Notes, result.Handoff)
	if len(entries) > 0 {
		fmt.Fprintln(w)
		fprintNotes(w, entries, result.Seed.ID, result.NotesTotal-len(result.Notes))
	}
}

func fprintHandoff(w io.Writer, handoff *protocol.SeedNote) {
	if handoff == nil {
		return
	}
	fmt.Fprintf(w, "handoff — %s, %s\n",
		orDash(crew.HolderName(handoff.AuthorMember, handoff.AuthorSession)), shortStamp(handoff.CreatedAt))
	for _, line := range strings.Split(strings.TrimRight(handoff.Body, "\n"), "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
	fmt.Fprintln(w)
}

func withoutNote(notes []protocol.SeedNote, drop *protocol.SeedNote) []protocol.SeedNote {
	if drop == nil {
		return notes
	}
	out := make([]protocol.SeedNote, 0, len(notes))
	for _, note := range notes {
		if note.ID != drop.ID {
			out = append(out, note)
		}
	}
	return out
}

func fprintRelations(out io.Writer, relations []protocol.SeedRelation) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, relation := range relations {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", relation.Label, relation.SeedID, relation.Status, relation.Title)
	}
	w.Flush()
}

func runSeedLink(unlink bool, args []string) {
	verb := "link"
	if unlink {
		verb = "unlink"
	}
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 3 {
		seedFail(verb, fmt.Errorf("reads as a sentence: `attn seed %s s-7k3f9m %s s-2p4qxv`, where the kind is %s",
			verb, garden.EdgeBlocks, strings.Join(garden.LinkableKinds, " or ")))
	}
	result, err := seedClient().SeedLink(positionals[0], positionals[1], positionals[2], unlink)
	if err != nil {
		seedFail(verb, err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	switch {
	case unlink:
		fmt.Printf("%s no longer %s %s\n", positionals[0], positionals[1], positionals[2])
	case !result.Changed:
		fmt.Printf("%s already %s %s\n", positionals[0], positionals[1], positionals[2])
	default:
		fmt.Printf("%s %s %s\n", positionals[0], positionals[1], positionals[2])
	}
}

func runSeedReady(args []string) {
	f := newSeedFlags("ready")
	if positionals := f.parse("ready", args); len(positionals) != 0 {
		seedFail("ready", fmt.Errorf("takes no arguments, got %q; scope it with --plot <plot> or --all", positionals[0]))
	}
	result, err := seedClient().SeedReady(f.sessionID(), strings.TrimSpace(*f.plot), *f.all)
	if err != nil {
		seedFail("ready", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	fprintSeedReady(os.Stdout, result)
}

func fprintSeedReady(out io.Writer, result *protocol.SeedReadyResult) {
	if result.Crown != nil {
		fmt.Fprintf(out, "%s%s\n\n", seedLine(*result.Crown), plotProgressSuffix(*result.Crown))
	}
	if len(result.Seeds) == 0 {
		fmt.Fprintf(out, "nothing is ready %s — `attn seed ls` shows what is planted and what holds it\n", readyScopeName(result))
		return
	}
	handoffs := freshestHandoffs(result.Handoffs)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSLUG\tSTATUS\tPLANTED\tTITLE")
	rows := make([]seedRow, 0, len(result.Plots)+len(result.Seeds))
	if result.Scope == "garden" && len(result.Plots) > 0 {
		rows = seedRows(append(slices.Clone(result.Plots), result.Seeds...), false)
	} else {
		for _, seed := range result.Seeds {
			rows = append(rows, seedRow{seed: seed})
		}
	}
	plotIDs := make(map[string]bool, len(result.Plots))
	for _, plot := range result.Plots {
		plotIDs[plot.ID] = true
	}
	for _, row := range rows {
		seed, status := row.seed, string(row.seed.Status)
		if plotIDs[seed.ID] {
			status = "plot"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s%s%s\n", seed.ID, seed.StepSlug, status, shortStamp(seed.CreatedAt),
			strings.Repeat("  ", row.depth), seed.Title, plotProgressSuffix(seed))
		if handoff, ok := handoffs[seed.ID]; ok {
			fmt.Fprintf(w, "\t\t\t\t↳ %s: %s\n",
				orDash(crew.HolderName(handoff.AuthorMember, handoff.AuthorSession)), firstLine(handoff.Body))
		}
	}
	w.Flush()
	fmt.Fprintf(out, "\n%d ready %s — `attn seed tend <id>` claims one\n", len(result.Seeds), readyScopeName(result))
}

// freshestHandoffs keeps the first handoff per seed; the daemon sends them
// newest first, so the first one is the one to read before any work.
func freshestHandoffs(notes []protocol.SeedNote) map[string]protocol.SeedNote {
	freshest := make(map[string]protocol.SeedNote, len(notes))
	for _, note := range notes {
		if _, seen := freshest[note.SeedID]; !seen {
			freshest[note.SeedID] = note
		}
	}
	return freshest
}

func firstLine(body string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	return line
}

func readyScopeName(result *protocol.SeedReadyResult) string {
	if result.Scope == "plot" {
		return fmt.Sprintf("in the plot under %s", result.ScopeID)
	}
	return "in the garden"
}

func fprintSeed(out io.Writer, seed protocol.Seed, watching ...bool) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%s\t%s\n", seed.ID, seed.Title)
	fmt.Fprintf(w, "slug\t%s\n", seed.StepSlug)
	fmt.Fprintf(w, "status\t%s\n", seed.Status)
	fmt.Fprintf(w, "planted\t%s by %s\n", shortStamp(seed.CreatedAt), orDash(crew.HolderName(seed.PlanterMember, seed.PlanterSession)))
	fmt.Fprintf(w, "tender\t%s\n", orDash(crew.HolderName(seed.TenderMember, seed.TenderSession)))
	if p := seed.PlotProgress; p != nil {
		fmt.Fprintf(w, "plot\t%d of %d done — %d growing, %d ready, %d blocked, %d dormant, %d withered\n",
			p.Done, p.Total, p.Growing, p.Ready, p.Blocked, p.Dormant, p.Withered)
	}
	if seed.Template {
		fmt.Fprintf(w, "packet\tyes\n")
	}
	if seed.Gate {
		fmt.Fprintf(w, "gate\tyes\n")
	}
	if seed.Reason != nil && *seed.Reason != "" {
		fmt.Fprintf(w, "reason\t%s\n", *seed.Reason)
	}
	if resumeID := protocol.Deref(seed.ResumeSessionID); resumeID != "" {
		fmt.Fprintf(w, "resume\t%s in %s on %s\n", resumeID, protocol.Deref(seed.ResumeCwd), protocol.Deref(seed.ResumeAgent))
	}
	if seed.Ready {
		fmt.Fprintf(w, "ready\tyes\n")
	}
	if len(watching) > 0 && watching[0] {
		fmt.Fprintf(w, "watching\tyes\n")
	}
	w.Flush()
	if body := strings.TrimRight(seed.Body, "\n"); body != "" {
		fmt.Printf("\n%s\n", body)
	}
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func runSeedTransition(verb string, args []string) {
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 1 {
		seedFail(verb, fmt.Errorf("needs exactly one seed id, got %d: attn seed %s s-7k3f9m", len(positionals), verb))
	}
	result, err := seedClient().SeedTransition(
		f.sessionID(), positionals[0], verb, f.text(verb), strings.TrimSpace(*f.member), *f.force)
	if err != nil {
		seedFail(verb, err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	fprintTransition(os.Stdout, result)
}

func fprintTransition(w io.Writer, result *protocol.SeedTransitionResult) {
	fmt.Fprintln(w, transitionLine(result.Seed))
	if open := openPlotSeeds(result.Seed); open > 0 && closedSeedStatus(string(result.Seed.Status)) {
		fmt.Fprintf(w, "its plot still holds %d open seed(s) — a closed plot over open work reads as done; close them too, or replant this one\n", open)
	}
	if result.Handoff != nil {
		fmt.Fprintln(w)
		fprintHandoff(w, result.Handoff)
	}
}

func closedSeedStatus(status string) bool {
	return status == "harvested" || status == "withered"
}

func openPlotSeeds(seed protocol.Seed) int {
	p := seed.PlotProgress
	if p == nil {
		return 0
	}
	open := p.Total - p.Done - p.Withered
	if open < 0 {
		return 0
	}
	return open
}

func transitionLine(seed protocol.Seed) string {
	line := fmt.Sprintf("%s is %s", seedHandle(seed), seed.Status)
	if tender := crew.HolderName(seed.TenderMember, seed.TenderSession); tender != "" {
		line += fmt.Sprintf(", tended by %s", tender)
	}
	if seed.Reason != nil && *seed.Reason != "" {
		line += fmt.Sprintf(" — %s", *seed.Reason)
	}
	return line
}

func runSeedNote(args []string) {
	f := newSeedFlags("note")
	positionals := f.parse("note", args)
	if len(positionals) != 1 {
		seedFail("note", fmt.Errorf(`needs exactly one seed id, got %d: attn seed note s-7k3f9m -m "what happened"`, len(positionals)))
	}
	result, err := seedClient().SeedNote(
		f.sessionID(), positionals[0], f.text("note"), strings.TrimSpace(*f.member), f.noteKind(), *f.ring, nil)
	if err != nil {
		seedFail("note", err)
	}
	if *f.json {
		writeJSON(result.Note)
		return
	}
	if result.Note.Kind == garden.NoteKindHandoff {
		fmt.Printf("handoff left on %s — whoever tends it next reads this first\n", result.Note.SeedID)
		return
	}
	fmt.Printf("noted on %s\n", result.Note.SeedID)
}

func runSeedWatch(verb string, args []string) {
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 1 {
		seedFail(verb, fmt.Errorf("needs exactly one seed id, got %d: attn seed %s s-7k3f9m", len(positionals), verb))
	}
	result, err := seedClient().SeedWatch(f.sessionID(), positionals[0], verb == "unwatch")
	if err != nil {
		seedFail(verb, err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	if result.Watching {
		if result.Changed {
			fmt.Printf("watching %s — its activity and anything below it will ring this session\n", result.SeedID)
		} else {
			fmt.Printf("already watching %s\n", result.SeedID)
		}
		return
	}
	if result.Changed {
		fmt.Printf("stopped watching %s\n", result.SeedID)
	} else {
		fmt.Printf("not watching %s\n", result.SeedID)
	}
}

func runSeedArtifact(verb string, args []string) {
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 1 {
		seedFail(verb, fmt.Errorf("needs exactly one seed id, got %d: attn seed %s s-7k3f9m --path docs/plans/thing.md", len(positionals), verb))
	}
	seedID := positionals[0]
	plan, handled, err := f.artifactTransferPlan(verb)
	if err != nil {
		seedFail(verb, err)
	}
	if handled {
		result, err := seedClient().SeedArtifactTransfer(
			f.sessionID(), seedID, plan.operation, plan.source, plan.filename, plan.destination, nil,
		)
		if err != nil {
			seedFail(verb, err)
		}
		if *f.json {
			writeJSON(result)
			return
		}
		fmt.Printf("%s %s\n", plan.operation, result.SourcePath)
		fmt.Printf("to %s\n", result.DestinationPath)
		if plan.operation != "detach" {
			fmt.Printf("markdown target %s\n", result.RelativeTarget)
		}
		return
	}
	path := *f.path
	repo := strings.TrimSpace(*f.repo)
	if *f.reference && verb != "detach" {
		seedFail(verb, fmt.Errorf("--reference only removes an existing linked path"))
	}
	if *f.reference && (path == "" || repo != "" || strings.TrimSpace(*f.notebook) != "" || strings.TrimSpace(*f.url) != "") {
		seedFail(verb, fmt.Errorf("--reference removes one old linked --path without --repo"))
	}
	artifact, err := f.artifact()
	if err != nil {
		seedFail(verb, err)
	}
	kind := garden.NoteKindAttach
	if verb == "detach" {
		kind = garden.NoteKindDetach
	}
	result, err := seedClient().SeedNote(
		f.sessionID(), seedID, f.text(verb), strings.TrimSpace(*f.member), kind, false, artifact)
	if err != nil {
		seedFail(verb, err)
	}
	if *f.json {
		writeJSON(result.Note)
		return
	}
	moved := garden.DefaultNoteBody(kind, garden.ArtifactReference{
		Kind:               artifact.Kind,
		NotebookDocumentID: protocol.Deref(artifact.NotebookDocumentID),
		Repository:         protocol.Deref(artifact.Repository),
		Path:               protocol.Deref(artifact.Path),
		URL:                protocol.Deref(artifact.URL),
	})
	fmt.Printf("%s %s\n", seedID, moved)
	if body := strings.TrimSpace(result.Note.Body); body != "" && body != moved {
		fmt.Printf("%s\n", body)
	}
}

type seedArtifactTransferPlan struct {
	operation   string
	source      string
	filename    string
	destination string
}

func (f *seedFlags) artifactTransferPlan(verb string) (*seedArtifactTransferPlan, bool, error) {
	path := *f.path
	repo := strings.TrimSpace(*f.repo)
	to := *f.to
	localFlags := *f.move || *f.copy || to != ""
	managedAttach := verb == "attach" && path != "" && repo == "" && strings.TrimSpace(*f.notebook) == "" && strings.TrimSpace(*f.url) == "" && !*f.reference
	managedDetach := verb == "detach" && path != "" && repo == "" && strings.TrimSpace(*f.notebook) == "" && strings.TrimSpace(*f.url) == "" && !*f.reference
	if !managedAttach && !managedDetach && !localFlags {
		return nil, false, nil
	}
	if f.wasSet("m") || strings.TrimSpace(*f.member) != "" {
		return nil, true, fmt.Errorf("managed artifact transfers carry their own receipt; -m and --member apply to log associations")
	}
	plan := &seedArtifactTransferPlan{}
	switch verb {
	case "attach":
		if !managedAttach {
			return nil, true, fmt.Errorf("--move and --copy apply only to a local --path without --repo")
		}
		if *f.move == *f.copy {
			return nil, true, fmt.Errorf("local attach requires exactly one of --move or --copy; --move is recommended")
		}
		if to != "" {
			return nil, true, fmt.Errorf("--to belongs to detach")
		}
		plan.operation = "copy"
		if *f.move {
			plan.operation = "move"
		}
		var err error
		plan.source, err = filepath.Abs(path)
		if err != nil {
			return nil, true, fmt.Errorf("resolve source path: %w", err)
		}
	case "detach":
		if !managedDetach {
			return nil, true, fmt.Errorf("--to applies only to a managed --path without --repo")
		}
		if *f.move || *f.copy {
			return nil, true, fmt.Errorf("detach moves the artifact out; do not pass --move or --copy")
		}
		if to == "" {
			return nil, true, fmt.Errorf("managed detach requires --to <destination>; use --reference to remove an old linked path")
		}
		plan.operation = "detach"
		plan.filename = path
		var err error
		plan.destination, err = filepath.Abs(to)
		if err != nil {
			return nil, true, fmt.Errorf("resolve detach destination: %w", err)
		}
	}
	return plan, true, nil
}

func (f *seedFlags) artifact() (*protocol.SeedArtifactReference, error) {
	path := strings.TrimSpace(*f.path)
	repo := strings.TrimSpace(*f.repo)
	notebook := strings.TrimSpace(*f.notebook)
	url := strings.TrimSpace(*f.url)
	named := []string{}
	for flag, value := range map[string]string{"--path": path, "--notebook": notebook, "--url": url} {
		if value != "" {
			named = append(named, flag)
		}
	}
	slices.Sort(named)
	switch len(named) {
	case 0:
		return nil, fmt.Errorf("name the document: --path <file> [--repo <repository>], --notebook <document-id>, or --url <url>")
	case 1:
	default:
		return nil, fmt.Errorf("%s were all given and a reference names one document; run it once per document", strings.Join(named, " and "))
	}
	switch {
	case path != "":
		ref := &protocol.SeedArtifactReference{Kind: garden.ArtifactMarkdownFile, Path: protocol.Ptr(path)}
		if repo != "" {
			ref.Repository = protocol.Ptr(repo)
		}
		return ref, nil
	case notebook != "":
		return &protocol.SeedArtifactReference{Kind: garden.ArtifactNotebook, NotebookDocumentID: protocol.Ptr(notebook)}, nil
	default:
		return &protocol.SeedArtifactReference{Kind: garden.ArtifactURL, URL: protocol.Ptr(url)}, nil
	}
}

func runSeedNotes(args []string) {
	f := newSeedFlags("notes")
	positionals := f.parse("notes", args)
	if len(positionals) != 1 {
		seedFail("notes", fmt.Errorf("needs exactly one seed id, got %d: attn seed notes s-7k3f9m", len(positionals)))
	}
	result, err := seedClient().SeedNotes(f.sessionID(), positionals[0], *f.limit)
	if err != nil {
		seedFail("notes", err)
	}
	if *f.json {
		writeJSON(result)
		return
	}
	if len(result.Notes) == 0 {
		fmt.Printf("nothing on this seed's log yet — `attn seed note %s -m \"what happened\"` starts it\n", positionals[0])
		return
	}
	fprintNotes(os.Stdout, result.Notes, positionals[0], result.Total-len(result.Notes))
}

func fprintNotes(w io.Writer, notes []protocol.SeedNote, seedID string, withheld int) {
	for i, note := range notes {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  %s%s\n", shortStamp(note.CreatedAt),
			orDash(crew.HolderName(note.AuthorMember, note.AuthorSession)), noteKindSuffix(note.Kind))
		fmt.Fprintf(w, "%s\n", strings.TrimRight(note.Body, "\n"))
	}
	if withheld > 0 {
		fmt.Fprintf(w, "\n%d more — `attn seed notes %s`\n", withheld, seedID)
	}
}

func noteKindSuffix(kind string) string {
	if kind == "" || kind == garden.NoteKindNote {
		return ""
	}
	return "  " + kind
}

func runSeedExport(args []string) {
	f := newSeedFlags("export")
	positionals := f.parse("export", args)
	if len(positionals) != 1 {
		seedFail("export", fmt.Errorf("needs exactly one seed id, got %d: attn seed export s-7k3f9m", len(positionals)))
	}
	result, err := seedClient().SeedShow(f.sessionID(), positionals[0])
	if err != nil {
		seedFail("export", err)
	}
	rendered := garden.Export(gardenSeedFromWire(result.Seed))

	out := strings.TrimSpace(*f.out)
	if out == "-" {
		fmt.Print(rendered)
		return
	}
	if out == "" {
		out = result.Seed.ID + ".md"
	}
	if err := os.WriteFile(out, []byte(rendered), 0644); err != nil {
		seedFail("export", fmt.Errorf("writing %s: %w", out, err))
	}
	absolute, err := filepath.Abs(out)
	if err != nil {
		absolute = out
	}
	if *f.json {
		writeJSON(struct {
			SeedID string `json:"seed_id"`
			Path   string `json:"path"`
			Bytes  int    `json:"bytes"`
		}{result.Seed.ID, absolute, len(rendered)})
		return
	}
	fmt.Printf("wrote %s from %s — edit the seed, not the file\n", absolute, result.Seed.ID)
}

func gardenSeedFromWire(seed protocol.Seed) garden.Seed {
	out := garden.Seed{
		ID: seed.ID, Title: seed.Title, Body: seed.Body, Status: seed.Status,
		Edges: make([]garden.Edge, 0, len(seed.Edges)),
	}
	for _, edge := range seed.Edges {
		out.Edges = append(out.Edges, garden.Edge{Kind: edge.Kind, To: edge.To})
	}
	return out
}
