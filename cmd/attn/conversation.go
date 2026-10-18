package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

const conversationUsage = `usage: attn conversation list [--all] [--json]
       attn conversation keep <session-or-conversation-id>
       attn conversation unkeep <session-or-conversation-id>
       attn conversation forget <session-or-conversation-id>

  list      every conversation attn keeps or will keep; --all includes deleted copies
  keep      keep a Claude conversation forever
  unkeep    remove the pin; the next keep pass decides again
  forget    delete attn's copy now, leaving Claude's own files untouched

Use session:<id> or conversation:<id> when an identifier is ambiguous.
`

func runConversation() {
	args := os.Args[2:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, conversationUsage)
		os.Exit(1)
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Print(conversationUsage)
		return
	}
	switch args[0] {
	case "list":
		conversationList(args[1:])
	case "keep", "unkeep", "forget":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprint(os.Stderr, conversationUsage)
			os.Exit(1)
		}
		warnIfDaemonVersionMismatch()
		cli := client.New("")
		var err error
		if args[0] == "forget" {
			err = cli.KeptConversationForget(args[1])
		} else {
			err = cli.KeptConversationKeep(args[1], args[0] == "keep")
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		switch args[0] {
		case "keep":
			fmt.Printf("pinned conversation %s; attn keeps it forever\n", args[1])
		case "unkeep":
			fmt.Printf("unpinned conversation %s; the next keep pass decides again\n", args[1])
		case "forget":
			fmt.Printf("deleted attn's copy of conversation %s; Claude's own files are untouched\n", args[1])
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown conversation command: %s\n\n%s", args[0], conversationUsage)
		os.Exit(1)
	}
}

func conversationList(args []string) {
	fs := flag.NewFlagSet("conversation list", flag.ExitOnError)
	all := fs.Bool("all", false, "include deleted copies")
	asJSON := fs.Bool("json", false, "print the raw result")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprint(os.Stderr, conversationUsage)
		os.Exit(1)
	}
	warnIfDaemonVersionMismatch()
	result, err := client.New("").KeptConversationList(*all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		printWorktreeJSON(result)
		return
	}
	fmt.Printf("%d kept · %s", result.Count, formatConversationBytes(result.StoredBytes))
	if date := protocol.Deref(result.NextDeleteAfter); date != "" {
		fmt.Printf(" · next deletion %s", conversationDate(date))
	}
	if result.PendingCount > 0 {
		fmt.Printf(" · %d pending", result.PendingCount)
	}
	fmt.Println()
	if len(result.Rows) == 0 {
		return
	}
	rows := [][]string{{"CONVERSATION", "ID", "AGENT", "SIZE", "KEPT BECAUSE", "SESSION"}}
	for _, row := range result.Rows {
		kept := row.Kept
		var reasons []string
		size := "-"
		if kept != nil {
			size = formatConversationBytes(kept.Bytes)
		}
		switch {
		case kept == nil:
			reasons = append(reasons, protocol.Deref(row.PendingReason))
		case kept.DeletedAt != nil:
			who := "attn deleted"
			if protocol.Deref(kept.DeletedBy).Ref == "user" {
				who = "you deleted"
			}
			reasons = append(reasons, who+" "+conversationDate(*kept.DeletedAt))
		default:
			if kept.PinnedAt != nil {
				reasons = append(reasons, "pinned "+conversationDate(*kept.PinnedAt))
			}
			for _, seed := range row.Seeds {
				reasons = append(reasons, "open seed "+seed.Slug+" ("+seed.ID+")")
			}
			if kept.DeleteAfter != nil {
				reasons = append(reasons, "deletes "+conversationDate(*kept.DeleteAfter))
			}
			if len(reasons) == 0 {
				reasons = append(reasons, "awaiting next keep pass")
			}
		}
		rows = append(rows, []string{row.Title, row.ResumeID, row.Agent, size, strings.Join(reasons, "; "), joinSessionIDs(row.SessionIds, ", ")})
	}
	printWorktreeTable(rows)
}

func formatConversationBytes(bytes int) string {
	switch {
	case bytes < 1000:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1000000:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/1000000)
	}
}

func joinSessionIDs(ids []protocol.SessionID, sep string) string {
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = string(id)
	}
	return strings.Join(values, sep)
}
