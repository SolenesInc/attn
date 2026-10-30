package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/victorarias/attn/internal/automation"
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
	case "search":
		runSeedSearch(args)
	case "show":
		runSeedShow(args)
	case "review":
		runSeedReview(args)
	case "edit":
		runSeedEdit(args)
	case "rollback":
		runSeedRollback(args)
	case "send-to-chief":
		runSeedSendToChief(args)
	case "export":
		runSeedExport(args)
	case "tend", "park", "harvest", "wither", "replant":
		runSeedTransition(os.Args[2], args)
	case "note":
		runSeedNote(args)
	case "check":
		runSeedCheck(args)
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
		recordUsageError("seed", usageErrorUnknownCommand)
		fmt.Fprintf(os.Stderr, "seed: unknown command %q\n", os.Args[2])
		fmt.Fprintln(os.Stderr, "usage: attn seed <command> (attn seed --help)")
		os.Exit(2)
	}
}

type seedVerb struct {
	name     string
	synopsis string
	about    string
	flags    []string
	readOnly bool
}

const seedMoveAuthority = `
A live claim yields to its tender, sessions above it in the delegation chain,
or the profile's chief. A crew member's named claim yields only to that member
or the chief. The log records standing and forced moves and whose claim ended.
Everyone else needs --force. A call without a live session has no standing.`

var seedVerbs = []seedVerb{
	{`plant`, `plant "<title>" [-m <body>] [--part-of <plot>] [--discovered-from <seed>] [--check "C1=<statement>"]... [flags]`, `plant a seed and print its id, slug and title. -m takes markdown, or - to read stdin —
if the seed gains children, that body is the plot's plan. --part-of
plants it under a plot. --check defines what the seed must prove.
Lists up to 3 similar open seeds in your profile, leaving out that plot.
Planting always proceeds. If one is the same work, wither the new seed.
--json prints the new seed alone.`, []string{"m", "part-of", "discovered-from", "check", "member", "json"}, false},
	{`plot`, `plot [-f <path>] [--json]`, `plant a whole plot in one move from a JSON payload (-f, or stdin):
{"title": …, "body": …, "checks": [{"id": "C5", "text": …, "shared": true}],
"children": [{"title": …, "body": …, "checks": […],
"blocks": ["<sibling step slug>"]}]}. Children are parallel by
default; blocks names the siblings a child holds back. A shared
check is proven by every child. Prints the plot's id and each
child's id and step slug, then up to 3 similar open seeds across all
planted titles, each naming the new seed it resembles. Planting always
proceeds; wither a new seed if a suggestion is the same work.
--json prints the whole result, including similar seeds.`, []string{"f", "member", "json"}, false},
	{`ls`, `ls [--stale [--window <duration>]] [--flat] [--json]`, fmt.Sprintf(`the garden, newest first, with children nested under their plot.
--flat prints one list. --stale narrows to open seeds whose log has not moved
for the window (default %s) — a query for your judgment, never a
reaper.`, formatWindow(garden.DefaultStaleWindow)), []string{"stale", "window", "flat", "json"}, true},
	{`search`, `search <words> [--limit <n>] [--json]`, `find seeds by words across your profile's garden, harvested and withered
included: titles, bodies and log entries. Words match as stemmed prefixes;
identifier parts match too. Seeds carrying some words also appear.
Ranks by title coverage, word coverage, relevance and recency, with a boost
for open seeds. Prints status, last active and an original matching line.
Shows the best 10 by default; --limit asks for more, up to 1000.
Search before you plant, so a duplicate is found instead of created.`, []string{"limit", "json"}, true},
	{`ready`, `ready [--plot <plot> | --all] [--json]`, `what you can tend right now, oldest first: nothing open blocks it,
nobody is holding it, and it is not a plot — a plot's work is its
children. With no flags the scope is your profile's garden — unless this
session was dispatched at a plot, and then that plot; --all steps back
out to the garden.`, []string{"plot", "all", "json"}, true},
	{`link`, `link <a> <blocks|part-of|discovered-from> <b> [--json]`, `relate two seeds. "a blocks b" keeps b out of ready until a closes;
"a part-of b" puts a under b; b is then a plot. "a discovered-from b"
records where work came from without ordering it. A structural cycle
is refused, naming both seeds and the edge to remove.`, []string{"json"}, false},
	{`unlink`, `unlink <a> <blocks|part-of|discovered-from> <b> [--json]`, `remove the edge. Every link has one.`, []string{"json"}, false},
	{`show`, `show <id> [--json]`, `one seed: the freshest handoff left on it, its state, who tends it,
every edge that touches it in both directions, its body, and the newest
notes on its log.`, []string{"json"}, true},
	{`review`, `review <start|show|cancel|retry|keep>`, `start an advisory Garden review, inspect its progressive results,
cancel unfinished classification, retry one failed or changed item, or keep
one seed growing and review it again after seven quiet days.
The review never changes a seed's state.`, []string{}, false},
	{`review start`, `review start [--json]`, `Start an advisory Garden review. The review never changes a seed's state.`, []string{"json"}, false},
	{`review show`, `review show [<review-id>] [--json]`, `Inspect the review and its progressive results. With no id, show the latest review.`, []string{"json"}, true},
	{`review cancel`, `review cancel <review-id> [--json]`, `Cancel unfinished classification.`, []string{"json"}, false},
	{`review retry`, `review retry <review-id> <seed-id> [--json]`, `Retry one failed or changed review item.`, []string{"json"}, false},
	{`review keep`, `review keep <review-id> <seed-id> [--json]`, `Keep a seed growing and review it again after seven quiet days.`, []string{"json"}, false},
	{`watch`, `watch <id> [--json]`, `watch this seed and its descendants. Delegation starts the same watch.
Tending a plot already covers its descendants until the claim ends.
unwatch removes this seed's subscription; separate child or ancestor
watches remain. show names the subscriptions that still cover a seed.`, []string{"json"}, false},
	{`unwatch`, `unwatch <id> [--json]`, `Remove this seed's subscription. Separate child or ancestor watches remain.
show names the subscriptions that still cover a seed.`, []string{"json"}, false},
	{`edit`, `edit <id> -m - [--note "<what changed>"] [--json]`, `save an exported body after editing the file with any tool. Keep the first
stamp line. If the body changed since export, export again and reapply.
Empty bodies are refused. Each edit saves a numbered version and a log entry;
--note adds what changed to that entry.`, []string{"m", "note", "json"}, false},
	{`rollback`, `rollback <id> [<version>] [--json]`, `restore the body before the newest version, or the numbered version.
The restored body becomes a new version, so rollback can itself be rolled back.
Edit entries in notes show the versions and size changes.`, []string{"json"}, false},
	{`send-to-chief`, `send-to-chief <id> [-m "<optional guidance>"] [--json]`, `give this seed to the Chief to decide its next working context. The
saved folder, branch and placement problem are recorded automatically;
-m adds an exception such as a special branch or directory.`, []string{"m", "json"}, false},
	{`tend`, `tend <id> [-m "<why>"] [--member <name>] [--force] [--json]`, `claim the seed and start growing it. The freshest handoff prints on
the claim, so picking a seed up primes you. Tending your own claim again
changes nothing and rings nobody.` + seedMoveAuthority, []string{"m", "member", "force", "json"}, false},
	{`park`, `park <id> [-m "<where you left it>"] [--member <name>] [--force] [--json]`, `pause the seed deliberately — it goes dormant and lets go of its
tender. An optional comment lands on the log in the same move. Tending
it again picks it back up. Parking it again keeps the state and adds
only the optional comment. Moves print who they rang.` + seedMoveAuthority, []string{"m", "member", "force", "json"}, false},
	{`harvest`, `harvest <id> [-m "<what got done>"] [--when-merged [<pr-url>] [--clear]] [--member <name>] [--force] [--json]`, `close the seed as done, with a required -m reason. Repeating a harvest
succeeds with the earlier reason; a different reason goes on its log.
A withered seed must be replanted first. A plot refuses while any direct
child is open, including with --force or --when-merged; replant hands it
back if your part is done. Closing the last child leaves a ringing note
on the plot, names it here and keeps the plot's state. Moves print who they rang.
--when-merged releases your claim and harvests the seed with your -m
reason when the pull request merges. Without -m it uses "PR #N merged:
<title>". This command merges nothing. Use it only when the merge is
the last step. With no url it takes this session's single open pull
request; --when-merged --clear takes the arming back.` + seedMoveAuthority, []string{"m", "member", "force", "json", "when-merged", "clear"}, false},
	{`wither`, `wither <id> [-m "<why>"] [--member <name>] [--force] [--json]`, `close the seed as abandoned. Nobody is picking this up. Repeating it
keeps the earlier reason and adds a different reason to the log. A
harvested seed must be replanted first. A plot refuses while any direct
child is open, even with --force; replant hands it back if your part is done.
Closing its last child leaves a ringing note on the plot and names it here.
Moves print who they rang.` + seedMoveAuthority, []string{"m", "member", "force", "json"}, false},
	{`replant`, `replant <id> [-m "<why>"] [--member <name>] [--force] [--json]`, `put the seed back in the pool — planted, unclaimed, ready for whoever
is free. Reopens a closed seed, un-parks a dormant one, and hands back
one being grown. Replanting an already planted seed changes nothing
and rings nobody. Moves print who they rang.` + seedMoveAuthority, []string{"m", "member", "force", "json"}, false},
	{`note`, `note <id> -m "<what happened>" [--handoff] [--ring] [--json]`, `append to the seed's log — what happened and what you learned, for
whoever tends it next. - reads stdin. --handoff addresses it to your
successor on this seed: show renders the freshest one first and tend
prints it on the claim, so it is read before any work.
--ring tells current and covering plot tenders and watchers to look;
ordinary notes stay quiet.`, []string{"m", "handoff", "ring", "member", "json"}, false},
	{`check`, `check <add|edit|remove|restore|report|confirm|reject|uncheck|reopen>`, `Define, report, or judge what a seed must prove. Each move has its own --help.
Checks never block a move. harvest lists the checks still open or rejected.`, []string{}, false},
	{`check add`, `check add <id> <C#> "<statement>" [--shared]`, `Define a numbered statement (C1) that the body cites by id. On a plot,
--shared makes every seed below it prove the check with its own report.
Removed ids are never reused.`, []string{"m", "member", "ring", "json", "shared"}, false},
	{`check edit`, `check edit <id> <C#> "<statement>"`, `Replace the check's statement.`, []string{"m", "member", "ring", "json"}, false},
	{`check remove`, `check remove <id> <C#>`, `Remove a check. Its id stays reserved.`, []string{"m", "member", "ring", "json"}, false},
	{`check restore`, `check restore <id> <C#>`, `Bring a removed check back with its result.`, []string{"m", "member", "ring", "json"}, false},
	{`check report`, `check report <id> <C#> -m "<summary>" [--evidence <artifact|url>]... [--commit <rev>]`, `Record the proof for a check. --evidence is an artifact on the seed
or an http(s) URL, and can repeat. A new report clears the verdict.
If a check doesn't apply, report that too: say why and cite what
shows it. The verdict decides.`, []string{"m", "member", "ring", "json", "evidence", "commit"}, false},
	{`check confirm`, `check confirm <id> <C#> [-m "<how you verified it>"]`, `Confirm the check, with or without a report.`, []string{"m", "member", "ring", "json"}, false},
	{`check reject`, `check reject <id> <C#> -m "<what would prove it>"`, `Reject the check. -m names what would prove it.`, []string{"m", "member", "ring", "json"}, false},
	{`check uncheck`, `check uncheck <id> <C#> [-m "<why>"]`, `Clear the verdict and keep the report.`, []string{"m", "member", "ring", "json"}, false},
	{`check reopen`, `check reopen <id> <C#> [-m "<why>"]`, `Clear the report and verdict.`, []string{"m", "member", "ring", "json"}, false},
	{`attach`, `attach <id> (--path <file> (--move | --copy | --repo <repository>) | --notebook <id> | --url <url>)`, `put a local regular file in durable seed-owned storage. Move is the
recommended choice and leaves one editable canonical file; Copy keeps
an independent source snapshot. Git-tracked files must use Copy.
Notebook, URL and repository references remain associations.`, []string{"path", "repo", "notebook", "url", "m", "member", "json", "move", "copy"}, false},
	{`detach`, `detach <id> (--path <filename> (--to <destination> | --reference | --repo <repository>) | --notebook <id> | --url <url>)`, `move a managed artifact back out without overwriting. An old linked
path association can be removed explicitly with --reference instead.`, []string{"path", "repo", "notebook", "url", "m", "member", "json", "to", "reference"}, false},
	{`notes`, `notes <id> [--limit <n>] [--json]`, `the whole log, newest first. show renders the newest few and says
how many more are here.`, []string{"limit", "json"}, true},
	{`guide`, `guide`, `print the craft: writing a body, deliverable types and what "done" is
for each, artifacts, and handoffs and steering.`, []string{}, false},
	{`prime`, `prime`, `print the standing garden contract and this session's live garden
position.`, []string{}, true},
	{`export`, `export <id> [--out <path>] [--json]`, `write the raw body with a first-line stamp naming this seed and its body hash.
Edit the file with any tool, keep its first line, then submit it with
attn seed edit <id> -m - < <id>.md. --out - writes to stdout;
the default is <id>.md in the current directory.`, []string{"out", "json"}, true},
}

type seedFlagDef struct {
	name     string
	argument string
	about    string
	register func(*seedFlags)
}

var seedFlagDefs = []seedFlagDef{
	{`m`, `<body>`, `markdown, or - to read stdin; empty stdin is refused`, func(f *seedFlags) { f.fs.StringVar(&f.message, "m", "", "") }},
	{`note`, `<text>`, `what changed, saved on the edit entry`, func(f *seedFlags) { f.fs.StringVar(&f.editNote, "note", "", "") }},
	{`part-of`, `<plot>`, `plant under a plot`, func(f *seedFlags) { f.fs.StringVar(&f.partOf, "part-of", "", "") }},
	{`discovered-from`, `<seed>`, `record the seed this work came from`, func(f *seedFlags) { f.fs.StringVar(&f.discoveredFrom, "discovered-from", "", "") }},
	{`clear`, ``, `remove the harvest condition (with --when-merged)`, func(f *seedFlags) { f.fs.BoolVar(&f.clear, "clear", false, "") }},
	{`when-merged`, ``, `harvest the seed when its pull request merges`, func(f *seedFlags) { f.fs.BoolVar(&f.whenMerged, "when-merged", false, "") }},
	{`plot`, `<plot>`, `scope a ready answer to one plot`, func(f *seedFlags) { f.fs.StringVar(&f.plot, "plot", "", "") }},
	{`all`, ``, `the whole garden, overriding a dispatched session's plot`, func(f *seedFlags) { f.fs.BoolVar(&f.all, "all", false, "") }},
	{`flat`, ``, `print a listing without nesting`, func(f *seedFlags) { f.fs.BoolVar(&f.flat, "flat", false, "") }},
	{`stale`, ``, `only open seeds whose log has not moved`, func(f *seedFlags) { f.fs.BoolVar(&f.stale, "stale", false, "") }},
	{`window`, `<d>`, `the stale window, like 72h or 14d (with --stale)`, func(f *seedFlags) { f.fs.StringVar(&f.window, "window", "", "") }},
	{`f`, `<path>`, `the plot payload to read; - or omitted reads stdin`, func(f *seedFlags) { f.fs.StringVar(&f.file, "f", "", "") }},
	{`handoff`, ``, `write a note to whoever tends the seed next`, func(f *seedFlags) { f.fs.BoolVar(&f.handoff, "handoff", false, "") }},
	{`ring`, ``, `give the current tender and watchers a seed update after this note or check move lands, and print who gets it`, func(f *seedFlags) { f.fs.BoolVar(&f.ring, "ring", false, "") }},
	{`check`, `"C1=<statement>"`, `a check the seed must prove; repeats`, func(f *seedFlags) { f.fs.Var(&f.checks, "check", "") }},
	{`shared`, ``, `every seed below this plot proves the check`, func(f *seedFlags) { f.fs.BoolVar(&f.shared, "shared", false, "") }},
	{`evidence`, `<artifact|url>`, `an artifact on the seed or an http(s) URL; repeats`, func(f *seedFlags) { f.fs.Var(&f.evidence, "evidence", "") }},
	{`commit`, `<rev>`, `the commit the evidence came from, resolved with git rev-parse in the current directory`, func(f *seedFlags) { f.fs.StringVar(&f.commit, "commit", "", "") }},
	{`path`, `<file>`, `local source, managed filename, or linked path`, func(f *seedFlags) { f.fs.StringVar(&f.path, "path", "", "") }},
	{`repo`, `<name>`, `the repository that path lives in`, func(f *seedFlags) { f.fs.StringVar(&f.repo, "repo", "", "") }},
	{`notebook`, `<id>`, `a Notebook document`, func(f *seedFlags) { f.fs.StringVar(&f.notebook, "notebook", "", "") }},
	{`url`, `<url>`, `anything reachable by URL`, func(f *seedFlags) { f.fs.StringVar(&f.url, "url", "", "") }},
	{`move`, ``, `transfer a local source into seed ownership`, func(f *seedFlags) { f.fs.BoolVar(&f.move, "move", false, "") }},
	{`copy`, ``, `snapshot a local source into seed ownership`, func(f *seedFlags) { f.fs.BoolVar(&f.copy, "copy", false, "") }},
	{`to`, `<path>`, `destination that receives a managed artifact`, func(f *seedFlags) { f.fs.StringVar(&f.to, "to", "", "") }},
	{`reference`, ``, `remove an old linked path association`, func(f *seedFlags) { f.fs.BoolVar(&f.reference, "reference", false, "") }},
	{`force`, ``, `act even though somebody else holds the seed; the log records it`, func(f *seedFlags) { f.fs.BoolVar(&f.force, "force", false, "") }},
	{`member`, `<name>`, `the crew member asking, recorded as planter, tender or note author`, func(f *seedFlags) { f.fs.StringVar(&f.member, "member", "", "") }},
	{`profile`, `<name|id>`, `required outside attn when several profiles exist`, func(f *seedFlags) { f.fs.StringVar(&f.profile, "profile", "", "") }},
	{`session`, `<id>`, `the session asking (defaults to the current session)`, func(f *seedFlags) { f.fs.StringVar(&f.session, "session", "", "") }},
	{`out`, `<path>`, `file to write; - writes to stdout`, func(f *seedFlags) { f.fs.StringVar(&f.out, "out", "", "") }},
	{`limit`, `<n>`, fmt.Sprintf(`how many log entries to read (notes), or how many hits to answer with (search; default %d, at most %d)`, garden.DefaultSearchResults, garden.MaxSearchResults), func(f *seedFlags) { f.fs.IntVar(&f.limit, "limit", 0, "") }},
	{`json`, ``, `print the result as JSON`, func(f *seedFlags) { f.fs.BoolVar(&f.json, "json", false, "") }},
}

func seedVerbNamed(name string) seedVerb {
	for _, verb := range seedVerbs {
		if verb.name == name {
			return verb
		}
	}
	panic("unknown seed verb: " + name)
}

func seedVerbHasFlag(verb seedVerb, name string) bool {
	return name == "profile" || name == "session" || slices.Contains(verb.flags, name)
}

func writeSeedVerbEntry(w io.Writer, verb seedVerb, indent string) {
	fmt.Fprintln(w, indent+verb.synopsis)
	for _, line := range strings.Split(verb.about, "\n") {
		fmt.Fprintln(w, indent+"      "+line)
	}
}

func writeSeedVerbHelp(w io.Writer, name string) {
	verb := seedVerbNamed(name)
	writeSeedVerbEntry(w, verb, "")
	fmt.Fprintln(w, "\nflags:")
	for _, def := range seedFlagDefs {
		if seedVerbHasFlag(verb, def.name) {
			writeSeedFlagHelp(w, def, "")
		}
	}
}

func writeSeedFlagHelp(w io.Writer, def seedFlagDef, owners string) {
	prefix := "--"
	if len(def.name) == 1 {
		prefix = "-"
	}
	label := strings.TrimSpace(prefix + def.name + " " + def.argument)
	fmt.Fprintf(w, "  %-24s %s%s\n", label, def.about, owners)
}

func writeSeedHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn seed <command>

A seed is the unit of work: one document, one short id, one slug made of the
title's key words, planted in the garden. Commands take the id; the slug is
how the seed is spoken of.
Anything worth handing off, parking, or attributing is a seed.
Use attn seed <command> --help for one command's syntax and flags.

commands:
`)
	for _, verb := range seedVerbs {
		writeSeedVerbEntry(w, verb, "  ")
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "flags:")
	for _, def := range seedFlagDefs {
		if def.name == "profile" || def.name == "session" {
			writeSeedFlagHelp(w, def, " (every command)")
			continue
		}
		var owners []string
		for _, verb := range seedVerbs {
			if seedVerbHasFlag(verb, def.name) {
				owners = append(owners, verb.name)
			}
		}
		writeSeedFlagHelp(w, def, " ("+strings.Join(owners, ", ")+")")
	}
}

func seedUsageError(verb string, err error) {
	fmt.Fprintf(os.Stderr, "seed %s: %v\n", verb, err)
	fmt.Fprintf(os.Stderr, "usage: attn seed %s (attn seed %s --help)\n", seedVerbNamed(verb).synopsis, verb)
	os.Exit(2)
}

func (f *seedFlags) client() *client.Client {
	profile, session := f.scope()
	return client.New(config.SocketPath()).WithGardenProfile(profile, session)
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
		seedUsageError("prime", fmt.Errorf("takes no arguments, got %q", positionals[0]))
	}
	ready, err := f.client().SeedReady(f.sessionID(), "", false)
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
	verb           seedVerb
	fs             *flag.FlagSet
	profile        string
	session        string
	member         string
	json           bool
	all            bool
	flat           bool
	stale          bool
	window         string
	plot           string
	partOf         string
	discoveredFrom string
	file           string
	message        string
	editNote       string
	out            string
	limit          int
	handoff        bool
	ring           bool
	path           string
	repo           string
	notebook       string
	url            string
	move           bool
	copy           bool
	to             string
	reference      bool
	clear          bool
	whenMerged     bool
	force          bool
	evidence       repeatedFlag
	checks         repeatedFlag
	commit         string
	shared         bool
}

func newSeedFlags(verb string) *seedFlags {
	entry := seedVerbNamed(verb)
	f := &seedFlags{verb: entry, fs: flag.NewFlagSet("seed "+verb, flag.ContinueOnError)}
	f.fs.SetOutput(io.Discard)
	for _, def := range seedFlagDefs {
		if seedVerbHasFlag(entry, def.name) {
			def.register(f)
		}
	}
	return f
}

func (f *seedFlags) noteKind() string {
	if f.handoff {
		return garden.NoteKindHandoff
	}
	return ""
}

func (f *seedFlags) text(verb string) string {
	if f.message != "-" {
		return f.message
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		seedFail(verb, fmt.Errorf("reading stdin: %w", err))
	}
	if strings.TrimSpace(string(raw)) == "" {
		seedUsageError(verb, fmt.Errorf("-m - read nothing from stdin; provide nonempty text"))
	}
	return string(raw)
}

func (f *seedFlags) parse(verb string, args []string) []string {
	positionals, err := parseInterspersedFlagArgs(f.fs, args)
	if errors.Is(err, flag.ErrHelp) {
		writeSeedVerbHelp(os.Stdout, verb)
		os.Exit(0)
	}
	if err != nil {
		seedUsageError(verb, err)
	}
	return positionals
}

func (f *seedFlags) scope() (string, protocol.SessionID) {
	profile := strings.TrimSpace(f.profile)
	if id := strings.TrimSpace(f.session); id != "" {
		return profile, protocol.SessionID(id)
	}
	session, err := currentSession(client.New(config.SocketPath()))
	if errors.Is(err, errNoCurrentSession) {
		if f.verb.readOnly {
			if profile != "" {
				return profile, ""
			}
			if profile, ok := currentCodexProfile(); ok {
				return profile, ""
			}
		}
		err = fmt.Errorf("%w; pass --session <id> to identify the caller; --profile <name|id> selects a garden and does not identify a session", err)
	}
	if err != nil {
		seedFail(f.verb.name, err)
	}
	return profile, session
}

func (f *seedFlags) sessionID() protocol.SessionID {
	_, session := f.scope()
	return session
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
	raw := strings.TrimSpace(f.window)
	if raw == "" {
		return 0
	}
	window, err := parseWindow(raw)
	if err != nil {
		seedFail("ls", err)
	}
	return int(window / time.Second)
}

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
		seedUsageError("plant", fmt.Errorf(`needs exactly one title, got %d: attn seed plant "what this is" [-m "the detail"]`, len(positionals)))
	}
	checks, err := parsePlantChecks(f.checks)
	if err != nil {
		seedFail("plant", err)
	}
	msg := protocol.SeedPlantMessage{Title: positionals[0], Checks: checks}
	if sessionID := f.sessionID(); sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	msg.Body = nonEmpty(f.text("plant"))
	msg.PartOf = nonEmpty(strings.TrimSpace(f.partOf))
	msg.DiscoveredFrom = nonEmpty(strings.TrimSpace(f.discoveredFrom))
	msg.Member = nonEmpty(strings.TrimSpace(f.member))
	result, err := f.client().SeedPlantMessage(msg)
	if err != nil {
		seedFail("plant", err)
	}
	if f.json {
		writeJSON(result.Seed)
		return
	}
	fmt.Println(seedLine(result.Seed))
	fprintSeedSimilar(os.Stdout, result.Similar, false)
}

func seedLine(seed protocol.Seed) string {
	return fmt.Sprintf("%s  %s  %s", seed.ID, seed.StepSlug, seed.Title)
}

func seedHandle(seed protocol.Seed) string {
	if seed.StepSlug == "" {
		return seed.ID
	}
	return seed.ID + " (" + seed.StepSlug + ")"
}

func runSeedSearch(args []string) {
	f := newSeedFlags("search")
	query := strings.Join(f.parse("search", args), " ")
	if strings.TrimSpace(query) == "" {
		seedUsageError("search", fmt.Errorf("needs something to look for: `attn seed search <words>`"))
	}
	result, err := f.client().SeedSearch(f.sessionID(), query, f.limit)
	if err != nil {
		seedFail("search", err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fprintSeedSearch(os.Stdout, query, result)
}

func fprintSeedSearch(out io.Writer, query string, result *protocol.SeedSearchResult) {
	total := result.Matched + result.Partial
	if total == 0 {
		fmt.Fprintf(out, "no seed matches %q — searched %s: titles, bodies and every log entry\n", query, plural(result.Searched, "seed"))
		return
	}
	fmt.Fprintf(out, "%s %s every word, %d carry some — best first\n\n",
		plural(result.Matched, "seed"), carriesVerb(result.Matched), result.Partial)
	for _, hit := range result.Hits {
		fmt.Fprintf(out, "%s\n  %s · %s  %s  %s\n", seedLine(hit.Seed), hit.Seed.Status, seedLastActive(hit.LastActive), hit.Where, hit.Snippet)
	}
	if total > len(result.Hits) {
		fmt.Fprintf(out, "\nshowing %d of %d — `--limit <n>` for more, up to %d\n", len(result.Hits), total, garden.MaxSearchResults)
	}
}

func carriesVerb(n int) string {
	if n == 1 {
		return "carries"
	}
	return "carry"
}

func seedLastActive(value string) string {
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return humanAge(max(0, time.Since(at)))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func runSeedList(args []string) {
	f := newSeedFlags("ls")
	if positionals := f.parse("ls", args); len(positionals) != 0 {
		seedUsageError("ls", fmt.Errorf("takes no arguments, got %q; to read one seed use `attn seed show <id>`", positionals[0]))
	}
	if !f.stale && flagWasSet(f.fs, "window") {
		seedFail("ls", fmt.Errorf("--window is the stale window; it only means something with --stale"))
	}
	result, err := f.client().SeedList(f.sessionID(), f.stale, f.staleWindowSeconds())
	if err != nil {
		seedFail("ls", err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	if f.stale {
		fmt.Printf("open seeds whose log has not moved for %s — no note, no move, no edge. This is a query, not a reaper: tend, note or park what still matters.\n\n",
			staleWindowLabel(result.StaleWindowSeconds))
	}
	if len(result.Seeds) == 0 {
		if f.stale {
			fmt.Println("none — every open seed has moved inside the window")
			return
		}
		fmt.Println("the garden is empty — `attn seed plant \"what this is\"` puts something in it")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSLUG\tSTATUS\tTENDER\tPLANTED\tTITLE")
	for _, row := range seedRows(result.Seeds, f.flat) {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s%s%s\n",
			row.seed.ID, row.seed.StepSlug, row.seed.Status, orDash(crew.HolderName(row.seed.TenderMember, row.seed.TenderSession)),
			shortStamp(row.seed.CreatedAt), strings.Repeat("  ", row.depth), row.seed.Title, plotProgressSuffix(row.seed)+checkTallySuffix(row.seed)+harvestWhenSuffix(row.seed))
	}
	w.Flush()
	if result.Total > len(result.Seeds) {
		fmt.Printf("\nshowing the newest %d of %d seeds — one read is capped at %d. The %d not shown are the oldest; `attn seed show <id>` still reaches any of them.\n",
			len(result.Seeds), result.Total, len(result.Seeds), result.Total-len(result.Seeds))
	}
}

func runSeedReview(args []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		newSeedFlags("review").parse("review", args)
		seedUsageError("review", fmt.Errorf("needs start, show, cancel, retry, or keep"))
	}
	verb := args[0]
	switch verb {
	case "start", "show", "cancel", "retry", "keep":
	default:
		recordUsageError("seed review", usageErrorUnknownCommand)
		seedUsageError("review", fmt.Errorf("unknown command %q; use start, show, cancel, retry, or keep", verb))
	}
	name := "review " + verb
	f := newSeedFlags(name)
	positionals := f.parse(name, args[1:])
	var err error

	var result *protocol.SeedReviewResult
	switch verb {
	case "start":
		if len(positionals) != 0 {
			seedUsageError("review start", fmt.Errorf("takes no arguments"))
		}
		result, err = f.client().SeedReviewStart()
	case "show":
		if len(positionals) > 1 {
			seedUsageError("review show", fmt.Errorf("takes at most one review id"))
		}
		reviewID := ""
		if len(positionals) == 1 {
			reviewID = positionals[0]
		}
		result, err = f.client().SeedReviewShow(reviewID)
	case "cancel":
		if len(positionals) != 1 {
			seedUsageError("review cancel", fmt.Errorf("needs exactly one review id"))
		}
		result, err = f.client().SeedReviewCancel(positionals[0])
	case "retry":
		if len(positionals) != 2 {
			seedUsageError("review retry", fmt.Errorf("needs a review id and seed id"))
		}
		result, err = f.client().SeedReviewRetry(positionals[0], positionals[1])
	case "keep":
		if len(positionals) != 2 {
			seedUsageError("review keep", fmt.Errorf("needs a review id and seed id"))
		}
		shown, showErr := f.client().SeedReviewShow(positionals[0])
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
		result, err = f.client().SeedReviewKeep(positionals[1], protocol.SeedReviewActionContext{
			ReviewID: positionals[0], EvidenceVersion: receipt.EvidenceVersion,
		})
	}
	if err != nil {
		seedFail("review "+verb, err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	if result.Review == nil {
		fmt.Printf("%d seeds need review\n", result.CandidateCount)
		return
	}
	fprintSeedReview(os.Stdout, *result.Review, verb == "show")
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

func harvestWhenSuffix(seed protocol.Seed) string {
	if line := harvestWhenLine(seed.HarvestWhen); line != "" {
		return "  [" + line + "]"
	}
	return ""
}

func harvestWhenLine(condition *protocol.SeedHarvestCondition) string {
	if condition == nil {
		return ""
	}
	line := "harvests when " + pullRequestLabel(condition.PullRequest) + " merges"
	if reason := protocol.Deref(condition.Reason); reason != "" {
		line += " with your reason: " + reason
	}
	return line
}

func pullRequestLabel(prID string) string {
	if _, rest, found := strings.Cut(prID, ":"); found {
		return rest
	}
	return prID
}

func runSeedPlot(args []string) {
	f := newSeedFlags("plot")
	if positionals := f.parse("plot", args); len(positionals) != 0 {
		seedUsageError("plot", fmt.Errorf("takes no arguments, got %q; the plot is JSON on stdin or at -f <path>", positionals[0]))
	}
	spec, err := garden.ParsePlotSpec(readPlotPayload(strings.TrimSpace(f.file)))
	if err != nil {
		seedFail("plot", err)
	}
	msg := protocol.SeedPlotMessage{Title: spec.Title, Checks: checkDefinitionsToProtocol(spec.Checks)}
	if spec.Body != "" {
		msg.Body = protocol.Ptr(spec.Body)
	}
	for _, child := range spec.Children {
		wire := protocol.SeedPlotChild{Title: child.Title, Blocks: child.Blocks, Checks: checkDefinitionsToProtocol(child.Checks)}
		if child.Body != "" {
			wire.Body = protocol.Ptr(child.Body)
		}
		msg.Children = append(msg.Children, wire)
	}
	result, err := f.client().SeedPlot(f.sessionID(), strings.TrimSpace(f.member), msg)
	if err != nil {
		seedFail("plot", err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fmt.Println(seedLine(result.Crown))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, child := range result.Children {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", child.ID, child.StepSlug, child.Title)
	}
	w.Flush()
	fprintSeedSimilar(os.Stdout, result.Similar, true)
}

func fprintSeedSimilar(out io.Writer, similar []protocol.SeedSimilar, plot bool) {
	if len(similar) == 0 {
		return
	}
	fmt.Fprintln(out, "\nsimilar open seeds")
	for _, hit := range similar {
		fmt.Fprintf(out, "  %s  %s · %s", seedLine(hit.Seed), hit.Seed.Status, seedLastActive(hit.LastActive))
		if plot {
			fmt.Fprintf(out, "  like %s", hit.Like)
		}
		fmt.Fprintln(out)
	}
	seen := map[string]bool{}
	for _, hit := range similar {
		if !seen[hit.Like] {
			fmt.Fprintf(out, "if one is the same work: attn seed wither %s -m \"duplicate of <id>\"\n", hit.Like)
			seen[hit.Like] = true
		}
	}
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
		seedUsageError("show", fmt.Errorf("needs exactly one seed id, got %d: attn seed show s-7k3f9m", len(positionals)))
	}
	result, err := f.client().SeedShow(f.sessionID(), positionals[0])
	if err != nil {
		seedFail("show", err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fprintSeedShow(os.Stdout, result)
}

func runSeedEdit(args []string) {
	f := newSeedFlags("edit")
	positionals := f.parse("edit", args)
	if len(positionals) != 1 {
		seedUsageError("edit", fmt.Errorf("needs exactly one seed id, got %d: attn seed edit s-7k3f9m -m -", len(positionals)))
	}
	id := positionals[0]
	if !f.wasSet("m") {
		seedUsageError("edit", fmt.Errorf("%s", garden.EditRecipe(id)))
	}
	body, base, err := garden.ParseStamped(id, f.text("edit"))
	if err != nil {
		seedFail("edit", err)
	}
	result, err := f.client().SeedEdit(id, body, base, f.editNote)
	if err != nil {
		seedFail("edit", err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fmt.Printf("updated %s: body v%d, %d → %d bytes\n", result.Seed.ID, result.BodyVersion, result.PreviousBytes, result.NewBytes)
}

func runSeedRollback(args []string) {
	f := newSeedFlags("rollback")
	positionals := f.parse("rollback", args)
	if len(positionals) < 1 || len(positionals) > 2 {
		seedUsageError("rollback", fmt.Errorf("needs a seed id and optional version: attn seed rollback s-7k3f9m [<version>]"))
	}
	var version *int
	if len(positionals) == 2 {
		number, err := strconv.Atoi(strings.TrimPrefix(positionals[1], "v"))
		if err != nil || number < 1 {
			seedUsageError("rollback", fmt.Errorf("version %q must be a positive number, like 2 or v2", positionals[1]))
		}
		version = &number
	}
	result, err := f.client().SeedRollback(positionals[0], version)
	if err != nil {
		seedFail("rollback", err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fmt.Printf("rolled %s back to v%d as v%d: %d → %d bytes\n", result.Seed.ID, result.RestoredVersion, result.BodyVersion, result.PreviousBytes, result.NewBytes)
}

func runSeedSendToChief(args []string) {
	f := newSeedFlags("send-to-chief")
	positionals := f.parse("send-to-chief", args)
	if len(positionals) != 1 {
		seedUsageError("send-to-chief", fmt.Errorf(
			"needs exactly one seed id, got %d: attn seed send-to-chief s-7k3f9m", len(positionals)))
	}
	seedID := strings.TrimSpace(positionals[0])
	c := f.client()
	document, err := c.SeedShow(f.sessionID(), seedID)
	if err != nil {
		seedFail("send-to-chief", err)
	}
	result, err := c.SeedSendToChief(f.sessionID(), document.Seed, strings.TrimSpace(f.text("send-to-chief")))
	if err != nil {
		seedFail("send-to-chief", err)
	}
	if f.json {
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
	fprintSeedChecks(w, result.Seed)
	if result.ArtifactDir != nil {
		fmt.Fprintf(w, "artifact folder  %s\n", *result.ArtifactDir)
	}
	if continuation := result.Seed.Continuation; continuation != nil && continuation.KeptConversation != nil {
		kept := continuation.KeptConversation
		switch {
		case protocol.Deref(kept.DeletedAt) != "" && protocol.Deref(kept.DeletedBy) == "user":
			fmt.Fprintf(w, "conversation  you deleted attn's copy on %s\n", conversationDate(protocol.Deref(kept.DeletedAt)))
		case protocol.Deref(kept.DeletedAt) != "":
			fmt.Fprintf(w, "conversation  attn deleted its copy on %s\n", conversationDate(protocol.Deref(kept.DeletedAt)))
		case protocol.Deref(kept.PinnedAt) != "":
			fmt.Fprintf(w, "conversation  kept by attn (%s) forever (pinned %s)\n", formatConversationBytes(kept.Bytes), conversationDate(*kept.PinnedAt))
		case protocol.Deref(kept.DeleteAfter) != "":
			fmt.Fprintf(w, "conversation  kept by attn (%s) until %s; replant to keep it\n", formatConversationBytes(kept.Bytes), conversationDate(protocol.Deref(kept.DeleteAfter)))
		default:
			fmt.Fprintf(w, "conversation  kept by attn (%s) while an open seed points at it\n", formatConversationBytes(kept.Bytes))
		}
	}
	fprintSeedWatchCoverage(w, result.WatchingVia)
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
		tally := ""
		if relation.Checks != nil {
			tally = checkTallyShort(*relation.Checks)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s%s\n", relation.Label, relation.SeedID, relation.Status, relation.Title, tally)
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
		seedUsageError(verb, fmt.Errorf("reads as a sentence: `attn seed %s s-7k3f9m %s s-2p4qxv`, where the kind is %s",
			verb, garden.EdgeBlocks, strings.Join(garden.LinkableKinds, " or ")))
	}
	result, err := f.client().SeedLink(positionals[0], positionals[1], positionals[2], unlink)
	if err != nil {
		seedFail(verb, err)
	}
	if f.json {
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
		seedUsageError("ready", fmt.Errorf("takes no arguments, got %q; scope it with --plot <plot> or --all", positionals[0]))
	}
	result, err := f.client().SeedReady(f.sessionID(), strings.TrimSpace(f.plot), f.all)
	if err != nil {
		seedFail("ready", err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fprintSeedReady(os.Stdout, result)
}

func fprintSeedReady(out io.Writer, result *protocol.SeedReadyResult) {
	if result.Crown != nil {
		fmt.Fprintf(out, "%s%s%s\n\n", seedLine(*result.Crown), plotProgressSuffix(*result.Crown), checkTallySuffix(*result.Crown))
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
		seed, status := row.seed, row.seed.Status
		if plotIDs[seed.ID] {
			status = "plot"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s%s%s\n", seed.ID, seed.StepSlug, status, shortStamp(seed.CreatedAt),
			strings.Repeat("  ", row.depth), seed.Title, plotProgressSuffix(seed)+checkTallySuffix(seed)+harvestWhenSuffix(seed))
		if handoff, ok := handoffs[seed.ID]; ok {
			fmt.Fprintf(w, "\t\t\t\t↳ %s: %s\n",
				orDash(crew.HolderName(handoff.AuthorMember, handoff.AuthorSession)), firstLine(handoff.Body))
		}
	}
	w.Flush()
	fmt.Fprintf(out, "\n%d ready %s — `attn seed tend <id>` claims one\n", len(result.Seeds), readyScopeName(result))
}

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
		if tally := checkTallyLong(plotCheckTally(p)); tally != "" {
			fmt.Fprintf(w, "plot checks\t%s\n", tally)
		}
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
	if condition := seed.HarvestWhen; condition != nil {
		fmt.Fprintf(w, "harvests when\t%s merges\n", pullRequestLabel(condition.PullRequest))
		if reason := protocol.Deref(condition.Reason); reason != "" {
			fmt.Fprintf(w, "harvest reason\t%s\n", reason)
		}
		checkedAt := strings.TrimSpace(protocol.Deref(condition.CheckedAt))
		if checkedAt == "" {
			checkedAt = "not yet"
		} else {
			checkedAt = shortStamp(checkedAt)
		}
		fmt.Fprintf(w, "PR last checked\t%s\n", checkedAt)
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
	seedID, opts, err := harvestWhenArgs(verb, f, f.parse(verb, args))
	if err != nil {
		seedUsageError(verb, err)
	}
	result, err := f.client().SeedTransition(f.sessionID(), seedID, verb, f.text(verb), strings.TrimSpace(f.member), f.force, opts)
	if err != nil {
		seedFail(verb, err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fprintTransition(os.Stdout, result, opts.ClearHarvestWhen)
}

func harvestWhenArgs(verb string, f *seedFlags, positionals []string) (string, client.SeedTransitionOptions, error) {
	var none client.SeedTransitionOptions
	if !f.whenMerged && !f.clear {
		seedID, err := oneSeedID(verb, positionals, "")
		return seedID, none, err
	}
	switch {
	case verb != "harvest":
		return "", none, fmt.Errorf("harvest-on-merge belongs to harvest: `attn seed harvest <id> --when-merged [pr-url]`")
	case !f.whenMerged:
		return "", none, fmt.Errorf("--clear disarms harvest-on-merge; say the whole thing: `attn seed harvest <id> --when-merged --clear`")
	}
	if f.clear {
		if f.wasSet("m") {
			return "", none, fmt.Errorf("--clear takes no reason; put it on the log with `attn seed note <id> -m \"…\"`")
		}
		seedID, err := oneSeedID(verb, positionals, " --when-merged --clear")
		return seedID, client.SeedTransitionOptions{ClearHarvestWhen: true}, err
	}
	if len(positionals) == 2 {
		if _, _, _, _, err := automation.ParsePullRequestURL(positionals[1]); err != nil {
			return "", none, fmt.Errorf("%q is not a pull request url: %w", positionals[1], err)
		}
		return positionals[0], client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: positionals[1]}, nil
	}
	seedID, err := oneSeedID(verb, positionals, " --when-merged [pr-url]")
	return seedID, client.SeedTransitionOptions{WhenMerged: true}, err
}

func oneSeedID(verb string, positionals []string, tail string) (string, error) {
	if len(positionals) != 1 {
		return "", fmt.Errorf("needs exactly one seed id, got %d: attn seed %s s-7k3f9m%s", len(positionals), verb, tail)
	}
	return positionals[0], nil
}

func fprintTransition(w io.Writer, result *protocol.SeedTransitionResult, cleared ...bool) {
	if result.Moved != nil && !*result.Moved {
		line := fmt.Sprintf("%s was already %s at %s", seedHandle(result.Seed), result.Seed.Status, shortStamp(result.Seed.StateChangedAt))
		if reason := protocol.Deref(result.Seed.Reason); reason != "" {
			line += " — " + reason
		}
		fmt.Fprintln(w, line)
	} else {
		fmt.Fprintln(w, transitionLine(result.Seed))
	}
	if result.Audit != nil {
		fmt.Fprintln(w, result.Audit.Body)
	}
	if result.Noted != nil {
		fmt.Fprintf(w, "your reason is on its log: attn seed notes %s\n", result.Seed.ID)
	}
	if line := harvestWhenLine(result.Seed.HarvestWhen); line != "" {
		fmt.Fprintln(w, line)
		fmt.Fprintln(w, "this command merges nothing")
	} else if len(cleared) > 0 && cleared[0] {
		fmt.Fprintln(w, "harvest-on-merge cleared")
	}
	if unsettled := unsettledChecks(result.Seed.Checks); unsettled != "" && closedSeedStatus(result.Seed.Status) {
		fmt.Fprintf(w, "checks: %s Checks never block a move; the tally stays on the seed.\n", unsettled)
	}
	if plot := result.PlotEmptied; plot != nil {
		if closedSeedStatus(plot.Status) {
			fmt.Fprintf(w, "%s has no open child seeds left and stays %s\n", seedHandle(*plot), plot.Status)
		} else {
			fmt.Fprintf(w, "%s has no open child seeds left and stays open: close it or plan its next step\n", seedHandle(*plot))
		}
	}
	fprintUnblocked(w, result.Unblocked)
	if result.Moved != nil && !*result.Moved {
		fmt.Fprintln(w, "rang nobody: the seed did not move")
	} else {
		fprintRang(w, result.Seed.ID, result.RingRecipients)
	}
	if result.Handoff != nil {
		fmt.Fprintln(w)
		fprintHandoff(w, result.Handoff)
	}
}

func fprintUnblocked(out io.Writer, seeds []protocol.Seed) {
	if len(seeds) == 0 {
		return
	}
	fmt.Fprintf(out, "this unblocked %d seed(s):\n", len(seeds))
	free := false
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, seed := range seeds {
		tender := crew.HolderName(seed.TenderMember, seed.TenderSession)
		held := ""
		if tender == "" {
			free = true
		} else {
			held = " — tended by " + tender
		}
		fmt.Fprintf(w, "  %s\t%s\t%s%s\n", seed.ID, orDash(seed.StepSlug), seed.Title, held)
	}
	w.Flush()
	if free {
		fmt.Fprintln(out, "`attn seed tend <id>` claims one")
	}
}

func closedSeedStatus(status string) bool {
	return status == "harvested" || status == "withered"
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
		seedUsageError("note", fmt.Errorf(`needs exactly one seed id, got %d: attn seed note s-7k3f9m -m "what happened"`, len(positionals)))
	}
	result, err := f.client().SeedNote(f.sessionID(), positionals[0], f.text("note"), strings.TrimSpace(f.member), f.noteKind(), f.ring, nil)
	if err != nil {
		seedFail("note", err)
	}
	if f.json {
		writeJSON(result.Note)
		return
	}
	if result.Note.Kind == garden.NoteKindHandoff {
		fmt.Printf("handoff left on %s — whoever tends it next reads this first\n", result.Note.SeedID)
	} else {
		fmt.Printf("noted on %s\n", result.Note.SeedID)
	}
	if f.ring {
		fprintRang(os.Stdout, result.Note.SeedID, result.RingRecipients)
	}
	fprintCitedChecks(os.Stdout, result.Note.SeedID, result.CitedChecks)
}

func runSeedWatch(verb string, args []string) {
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 1 {
		seedUsageError(verb, fmt.Errorf("needs exactly one seed id, got %d: attn seed %s s-7k3f9m", len(positionals), verb))
	}
	result, err := f.client().SeedWatch(f.sessionID(), positionals[0], verb == "unwatch")
	if err != nil {
		seedFail(verb, err)
	}
	if f.json {
		writeJSON(result)
		return
	}
	fprintSeedWatchResult(os.Stdout, result, verb == "unwatch")
}

func fprintSeedWatchCoverage(w io.Writer, seedIDs []string) {
	for _, seedID := range seedIDs {
		fmt.Fprintf(w, "watching via %s (remove with attn seed unwatch %s)\n", seedID, seedID)
	}
}

func fprintSeedWatchResult(w io.Writer, result *protocol.SeedWatchResult, unwatch bool) {
	if unwatch {
		if result.Changed {
			fmt.Fprintf(w, "removed watch on %s\n", result.SeedID)
		} else {
			fmt.Fprintf(w, "no direct watch on %s\n", result.SeedID)
		}
		if result.Watching {
			fprintSeedWatchCoverage(w, result.WatchingVia)
		} else {
			fmt.Fprintf(w, "no remaining watch covers %s\n", result.SeedID)
		}
		return
	}
	if result.Changed {
		fmt.Fprintf(w, "watching %s and its descendants\n", result.SeedID)
	} else {
		fmt.Fprintf(w, "already watching %s\n", result.SeedID)
	}
}

func runSeedArtifact(verb string, args []string) {
	f := newSeedFlags(verb)
	positionals := f.parse(verb, args)
	if len(positionals) != 1 {
		seedUsageError(verb, fmt.Errorf("needs exactly one seed id, got %d: attn seed %s s-7k3f9m --path docs/plans/thing.md", len(positionals), verb))
	}
	seedID := positionals[0]
	plan, handled, err := f.artifactTransferPlan(verb)
	if err != nil {
		seedFail(verb, err)
	}
	if handled {
		result, err := f.client().SeedArtifactTransfer(f.sessionID(), seedID, plan.operation, plan.source, plan.filename, plan.destination, nil)
		if err != nil {
			seedFail(verb, err)
		}
		if f.json {
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
	path := f.path
	repo := strings.TrimSpace(f.repo)
	if f.reference && verb != "detach" {
		seedFail(verb, fmt.Errorf("--reference only removes an existing linked path"))
	}
	if f.reference && (path == "" || repo != "" || strings.TrimSpace(f.notebook) != "" || strings.TrimSpace(f.url) != "") {
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
	result, err := f.client().SeedNote(f.sessionID(), seedID, f.text(verb), strings.TrimSpace(f.member), kind, false, artifact)
	if err != nil {
		seedFail(verb, err)
	}
	if f.json {
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
	path := f.path
	repo := strings.TrimSpace(f.repo)
	to := f.to
	localFlags := f.move || f.copy || to != ""
	managedAttach := verb == "attach" && path != "" && repo == "" && strings.TrimSpace(f.notebook) == "" && strings.TrimSpace(f.url) == "" && !f.reference
	managedDetach := verb == "detach" && path != "" && repo == "" && strings.TrimSpace(f.notebook) == "" && strings.TrimSpace(f.url) == "" && !f.reference
	if !managedAttach && !managedDetach && !localFlags {
		return nil, false, nil
	}
	if f.wasSet("m") || strings.TrimSpace(f.member) != "" {
		return nil, true, fmt.Errorf("managed artifact transfers carry their own receipt; -m and --member apply to log associations")
	}
	plan := &seedArtifactTransferPlan{}
	switch verb {
	case "attach":
		if !managedAttach {
			return nil, true, fmt.Errorf("--move and --copy apply only to a local --path without --repo")
		}
		if f.move == f.copy {
			return nil, true, fmt.Errorf("local attach requires exactly one of --move or --copy; --move is recommended")
		}
		if to != "" {
			return nil, true, fmt.Errorf("--to belongs to detach")
		}
		plan.operation = "copy"
		if f.move {
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
		if f.move || f.copy {
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
	path := strings.TrimSpace(f.path)
	repo := strings.TrimSpace(f.repo)
	notebook := strings.TrimSpace(f.notebook)
	url := strings.TrimSpace(f.url)
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
		seedUsageError("notes", fmt.Errorf("needs exactly one seed id, got %d: attn seed notes s-7k3f9m", len(positionals)))
	}
	result, err := f.client().SeedNotes(f.sessionID(), positionals[0], f.limit)
	if err != nil {
		seedFail("notes", err)
	}
	if f.json {
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
		seedUsageError("export", fmt.Errorf("needs exactly one seed id, got %d: attn seed export s-7k3f9m", len(positionals)))
	}
	result, err := f.client().SeedShow(f.sessionID(), positionals[0])
	if err != nil {
		seedFail("export", err)
	}
	rendered := garden.Stamp(result.Seed.ID, result.Seed.Body)

	out := strings.TrimSpace(f.out)
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
	if f.json {
		writeJSON(struct {
			SeedID string `json:"seed_id"`
			Path   string `json:"path"`
			Bytes  int    `json:"bytes"`
		}{result.Seed.ID, absolute, len(rendered)})
		return
	}
	fmt.Printf("wrote %s; edit it with any tool, keep its first line, then: attn seed edit %s -m - < %s\n", absolute, result.Seed.ID, absolute)
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

func conversationDate(stamp string) string {
	if at, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		return at.Format("2006-01-02")
	}
	return stamp
}
