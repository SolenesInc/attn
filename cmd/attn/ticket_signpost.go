package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Tickets retired with the garden era: every `attn ticket` write verb is now a
// signpost that exits nonzero. `show` and `list` are the deliberate exception.

type ticketSignpost struct {
	Lead  string
	Moves [][2]string
	Note  string
}

// A verb missing from this table falls through to the router's unknown-command
// path; TestEveryTicketWriteVerbSignposts walks the router's cases against it.
var ticketSignposts = map[string]ticketSignpost{
	"status": {
		Lead: "reporting your work state",
		Moves: [][2]string{
			{"progress", `attn seed note <seed-id> -m "<what happened and what you learned>"`},
			{"blocked", `attn seed note <seed-id> -m "<the decision you need>"`},
			{"finished", `attn seed harvest <seed-id> -m "<what got done>"`},
			{"giving up", `attn seed wither <seed-id> -m "<why nobody should pick this up>"`},
			{"pausing", `attn seed park <seed-id>`},
		},
		Note: "Your seed id is in the brief you launched with; `attn seed ls` lists the garden.",
	},
	"inbox": {
		Lead: "reading unread activity on your work",
		Moves: [][2]string{
			{"the whole log", `attn seed notes <seed-id>`},
			{"the seed itself", `attn seed show <seed-id>`},
		},
		Note: "A seed's log is read, not delivered: there is no cursor to consume.",
	},
	"new": {
		Lead: "creating a backlog item nobody is working on yet",
		Moves: [][2]string{
			{"plant it", `attn seed plant "<title>" -m "<the brief>"`},
			{"a whole plot", `attn seed plot -f <payload.json>`},
		},
	},
	"comment": {
		Lead: "leaving a note on somebody else's work",
		Moves: [][2]string{
			{"note it", `attn seed note <seed-id> -m "<what you want them to know>"`},
		},
	},
	"attach": {
		Lead: "associating a document with your work",
		Moves: [][2]string{
			{"a file", `attn seed attach <seed-id> --path <file> [--repo <repository>]`},
			{"a Notebook document", `attn seed attach <seed-id> --notebook <document-id>`},
			{"anything with a URL", `attn seed attach <seed-id> --url <url>`},
			{"take it back", `attn seed detach <seed-id> --path <file>`},
		},
	},
	"attach-plan": {
		Lead: "handing over a durable plan or design",
		Moves: [][2]string{
			{"a committed plan", `attn seed attach <seed-id> --path <file> --repo <repository>`},
			{"a Notebook document", `attn seed attach <seed-id> --notebook <document-id>`},
		},
		Note: "Where a document lives has not changed; the seed records the association only.",
	},
	"take": {
		Lead: "claiming work as yours",
		Moves: [][2]string{
			{"claim it", `attn seed tend <seed-id>`},
			{"what is free", `attn seed ready`},
		},
		Note: "One tender at a time: tending a seed somebody holds is refused, naming them.",
	},
	"subscribe": {
		Lead: "following somebody else's work",
		Moves: [][2]string{
			{"watch it", `attn seed watch <seed-id>`},
			{"read the log", `attn seed notes <seed-id>`},
			{"read the seed", `attn seed show <seed-id>`},
		},
		Note: "A watch rings on lifecycle moves; notes stay quiet unless their author uses --ring.",
	},
	"unsubscribe": {
		Lead: "unfollowing somebody else's work",
		Moves: [][2]string{
			{"stop watching", `attn seed unwatch <seed-id>`},
		},
		Note: "Unwatch removes an explicit watch; a delegation's dispatcher watch follows the dispatch relationship.",
	},
}

// Exit 2 is the router's own usage-error code.
func signpostTicketVerb(verb string) {
	fprintTicketSignpost(os.Stderr, verb)
	os.Exit(2)
}

func fprintTicketSignpost(w io.Writer, verb string) {
	post, ok := ticketSignposts[verb]
	if !ok {
		fmt.Fprintf(w, "attn ticket %s: tickets retired; work lives in the garden. Run `attn seed --help`.\n", verb)
		return
	}
	fmt.Fprintf(w, "attn ticket %s retired: tickets are gone and %s happens in the garden now.\n\n", verb, post.Lead)
	width := 0
	for _, move := range post.Moves {
		if len(move[0]) > width {
			width = len(move[0])
		}
	}
	for _, move := range post.Moves {
		fmt.Fprintf(w, "  %-*s  %s\n", width, move[0], move[1])
	}
	if post.Note != "" {
		fmt.Fprintf(w, "\n%s\n", post.Note)
	}
	fmt.Fprint(w, "\nDone tickets stay readable: `attn ticket show <id>` and `attn ticket list`.\n")
}

func ticketSignpostVerbs() []string {
	verbs := make([]string, 0, len(ticketSignposts))
	for verb := range ticketSignposts {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)
	return verbs
}

func ticketSignpostVerbList() string {
	return strings.Join(ticketSignpostVerbs(), ", ")
}
