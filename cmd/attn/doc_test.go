package main

import (
	"testing"

	"github.com/victorarias/attn/internal/docstore"
)

func TestWhereReadsTheBoundAsJSONWhenItIsJSON(t *testing.T) {
	for _, tc := range []struct {
		expr  string
		field string
		op    docstore.Op
		value string
	}{
		{"status=pending", "status", docstore.OpEq, `"pending"`},
		{"attempts>=5", "attempts", docstore.OpGte, "5"},
		{"attempts>2", "attempts", docstore.OpGt, "2"},
		{"attempts<=9", "attempts", docstore.OpLte, "9"},
		{"attempts<9", "attempts", docstore.OpLt, "9"},
		{"urgent=true", "urgent", docstore.OpEq, "true"},
		{`status="5"`, "status", docstore.OpEq, `"5"`},
		{"updated_at>2026-08-03T00:00:00Z", "updated_at", docstore.OpGt, `"2026-08-03T00:00:00Z"`},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := parseDocWhere(tc.expr)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.Field != tc.field || got.Op != string(tc.op) || got.ValueJson != tc.value {
				t.Fatalf("parsed %+v, want field=%s op=%s value=%s", got, tc.field, tc.op, tc.value)
			}
		})
	}
}

func TestWhereMatchesTheLongestOperatorFirst(t *testing.T) {
	got, err := parseDocWhere("attempts>=5")
	if err != nil {
		t.Fatal(err)
	}
	if got.Op != string(docstore.OpGte) || got.ValueJson != "5" {
		t.Fatalf("parsed %+v", got)
	}
}

func TestWhereRejectsAnExpressionWithNoOperator(t *testing.T) {
	if _, err := parseDocWhere("status"); err == nil {
		t.Fatal("an expression with no operator was accepted")
	}
	if _, err := parseDocWhere("=pending"); err == nil {
		t.Fatal("an expression with no field was accepted")
	}
}

// The parser advances the loop index from inside a closure; Go 1.22's
// per-iteration loop variable is what could silently break that.
func TestQueryFlagsConsumeTheirValues(t *testing.T) {
	query, opts := parseDocQueryFlags("query", "app/approval-gate", "requests", []string{
		"--where", "status=pending",
		"--sort", "created_at",
		"--desc",
		"--limit", "7",
		"--json",
	})
	if !opts.asJSON {
		t.Fatal("--json not seen")
	}
	if query.Namespace != "app/approval-gate" || query.Collection != "requests" {
		t.Fatalf("target = %s/%s", query.Namespace, query.Collection)
	}
	if len(query.Filters) != 1 || query.Filters[0].Field != "status" || query.Filters[0].ValueJson != `"pending"` {
		t.Fatalf("filters = %+v", query.Filters)
	}
	if query.Sort == nil || query.Sort.Field != "created_at" || query.Sort.Desc == nil || !*query.Sort.Desc {
		t.Fatalf("sort = %+v", query.Sort)
	}
	if query.Limit == nil || *query.Limit != 7 {
		t.Fatalf("limit = %v", query.Limit)
	}
}

func TestResumeBelongsToWatch(t *testing.T) {
	_, opts := parseDocQueryFlags("watch", "app/a", "c", []string{"--resume"})
	if !opts.resume {
		t.Fatal("--resume not seen on watch")
	}
}

func TestDescBeforeSortStillReversesIt(t *testing.T) {
	query, _ := parseDocQueryFlags("query", "app/a", "c", []string{"--desc", "--sort", "updated_at"})
	if query.Sort == nil || query.Sort.Field != "updated_at" || query.Sort.Desc == nil || !*query.Sort.Desc {
		t.Fatalf("sort = %+v", query.Sort)
	}
}

func TestWhereRepeatsToAccumulateFilters(t *testing.T) {
	query, _ := parseDocQueryFlags("query", "app/a", "c", []string{
		"--where", "status=pending", "--where", "attempts>=2",
	})
	if len(query.Filters) != 2 {
		t.Fatalf("filters = %+v", query.Filters)
	}
	if query.Filters[1].Op != string(docstore.OpGte) || query.Filters[1].ValueJson != "2" {
		t.Fatalf("second filter = %+v", query.Filters[1])
	}
}

func TestAfterFlagCarriesTheCursor(t *testing.T) {
	query, _ := parseDocQueryFlags("query", "app/a", "c", []string{"--sort", "attempts", "--after", "b7"})
	if query.After == nil || *query.After != "b7" {
		t.Fatalf("after = %v", query.After)
	}
}

func TestExpectFlagLeavesThePositionalArgumentsAlone(t *testing.T) {
	rest, expect := takeExpectFlag("put", []string{"r1", `{"a":1}`, "--expect", "3"}, true)
	if len(rest) != 2 || rest[0] != "r1" || rest[1] != `{"a":1}` {
		t.Fatalf("positional arguments = %v", rest)
	}
	if expect == nil || *expect != 3 {
		t.Fatalf("expect = %v, want 3", expect)
	}
}

func TestExpectAbsentIsTheZeroRevision(t *testing.T) {
	_, expect := takeExpectFlag("put", []string{"r1", `{"a":1}`, "--expect", "absent"}, true)
	if expect == nil || int64(*expect) != docstore.ExpectAbsent {
		t.Fatalf("expect = %v, want %d", expect, docstore.ExpectAbsent)
	}
}

func TestWithoutExpectTheWriteIsUnconditional(t *testing.T) {
	rest, expect := takeExpectFlag("put", []string{"r1", `{"a":1}`}, true)
	if expect != nil {
		t.Fatalf("expect = %v, want none", *expect)
	}
	if len(rest) != 2 {
		t.Fatalf("positional arguments = %v", rest)
	}
}
