package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

func runDoc() {
	if len(os.Args) < 3 || os.Args[2] != "watch" {
		fmt.Fprintln(os.Stderr, "attnd doc: only watch runs here; use attn doc for every other command")
		exit(2)
	}
	setCommandPath("doc watch")
	runDocWatch(os.Args[3:])
}

func docClient() *client.Client {
	return client.New(config.SocketPath())
}

func docFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "doc %s: %v\n", verb, err)
	exit(1)
}

func docWatchTarget(args []string) (string, string, []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "doc watch: needs <namespace> <collection>")
		exit(2)
	}
	return args[0], args[1], args[2:]
}

func runDocWatch(args []string) {
	namespace, collection, rest := docWatchTarget(args)
	query, opts := parseDocQueryFlags(namespace, collection, rest)

	var held []protocol.StoredDocument
	watching := false
	for {
		err := docClient().DocSubscribe(query, held, func(window client.DocWindow) bool {
			watching = true
			held = window.Documents
			printDocWindow(window, opts.asJSON)
			return true
		})
		_, ended := client.DocSubscriptionCode(err)
		switch {
		case opts.resume && client.DocConnectionLost(err):
		case opts.resume && watching && !ended:
			time.Sleep(daemonReconnectInterval)
		default:
			docFail("watch", err)
		}
	}
}

const daemonReconnectInterval = 200 * time.Millisecond

func printDocWindow(window client.DocWindow, asJSON bool) {
	if asJSON {
		writeJSON(struct {
			Delivery  int            `json:"delivery"`
			AsOfSeq   int64          `json:"as_of_seq"`
			Documents []jsonDocument `json:"documents"`
			Changed   []string       `json:"changed"`
		}{window.Delivery, window.AsOfSeq, docsForJSON(window.Documents), window.Changed})
		return
	}
	fmt.Printf("--- delivery %d (%d document(s), %d body(s) sent, as of seq %d) ---\n",
		window.Delivery, len(window.Documents), len(window.Changed), window.AsOfSeq)
	printDocuments(window.Documents, false)
}

type docQueryOptions struct {
	asJSON bool
	resume bool
}

func parseDocQueryFlags(namespace, collection string, args []string) (protocol.DocumentQuery, docQueryOptions) {
	query := protocol.DocumentQuery{Namespace: namespace, Collection: collection}
	var opts docQueryOptions
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 >= len(args) {
				docFail("watch", fmt.Errorf("%s needs a value", args[i]))
			}
			i++
			return args[i]
		}
		switch args[i] {
		case "--json":
			opts.asJSON = true
		case "--resume":
			opts.resume = true
		case "--desc":
			desc := true
			if query.Sort == nil {
				query.Sort = &protocol.DocumentSort{}
			}
			query.Sort.Desc = &desc
		case "--sort":
			field := next()
			if query.Sort == nil {
				query.Sort = &protocol.DocumentSort{}
			}
			query.Sort.Field = field
		case "--after":
			after := next()
			query.After = &after
		case "--limit":
			raw := next()
			n, err := strconv.Atoi(raw)
			if err != nil {
				docFail("watch", fmt.Errorf("--limit %q is not a number", raw))
			}
			query.Limit = &n
		case "--where":
			filter, err := parseDocWhere(next())
			if err != nil {
				docFail("watch", err)
			}
			query.Filters = append(query.Filters, filter)
		default:
			recordUsageError("doc watch", usageErrorUnknownFlag)
			docFail("watch", fmt.Errorf("unknown flag %q", args[i]))
		}
	}
	if query.Sort != nil && query.Sort.Field == "" {
		docFail("watch", fmt.Errorf("--desc needs --sort <field>"))
	}
	return query, opts
}

var docWhereOps = []struct {
	token string
	op    docstore.Op
}{
	{">=", docstore.OpGte},
	{"<=", docstore.OpLte},
	{"=", docstore.OpEq},
	{">", docstore.OpGt},
	{"<", docstore.OpLt},
}

func parseDocWhere(expr string) (protocol.DocumentFilter, error) {
	for _, candidate := range docWhereOps {
		field, value, ok := strings.Cut(expr, candidate.token)
		if !ok {
			continue
		}
		if field == "" {
			return protocol.DocumentFilter{}, fmt.Errorf("--where %q has no field name", expr)
		}
		return protocol.DocumentFilter{
			Field:     field,
			Op:        string(candidate.op),
			ValueJson: docBoundAsJSON(value),
		}, nil
	}
	return protocol.DocumentFilter{}, fmt.Errorf("--where %q needs one of = < <= > >= (for example status=pending)", expr)
}

func docBoundAsJSON(raw string) string {
	var probe any
	if err := json.Unmarshal([]byte(raw), &probe); err == nil {
		return raw
	}
	encoded, _ := json.Marshal(raw)
	return string(encoded)
}

type jsonDocument struct {
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	Rev       int             `json:"rev"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func docsForJSON(docs []protocol.StoredDocument) []jsonDocument {
	out := make([]jsonDocument, 0, len(docs))
	for _, doc := range docs {
		out = append(out, jsonDocument{
			ID:        doc.ID,
			Body:      json.RawMessage(doc.Body),
			Rev:       doc.Rev,
			CreatedAt: doc.CreatedAt,
			UpdatedAt: doc.UpdatedAt,
		})
	}
	return out
}

func printDocuments(docs []protocol.StoredDocument, asJSON bool) {
	if asJSON {
		writeJSON(docsForJSON(docs))
		return
	}
	if len(docs) == 0 {
		fmt.Println("no documents")
		return
	}
	w := newTableWriter(os.Stdout)
	fmt.Fprintln(w, "ID\tREV\tUPDATED\tBODY")
	for _, doc := range docs {
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", doc.ID, doc.Rev, doc.UpdatedAt, doc.Body)
	}
	w.Flush()
}

func writeJSON(v any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "doc: encoding output: %v\n", err)
		exit(1)
	}
}
