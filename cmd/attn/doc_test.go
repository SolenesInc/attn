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
		{"status", "", "", ""},
		{"=pending", "", "", ""},
		{">=5", "", "", ""},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := parseDocWhere(tc.expr)
			if tc.op == "" {
				if err == nil {
					t.Fatalf("parsed %+v, want a refusal: the expression has no field or no operator", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.Field != tc.field || got.Op != string(tc.op) || got.ValueJson != tc.value {
				t.Fatalf("parsed %+v, want field=%s op=%s value=%s", got, tc.field, tc.op, tc.value)
			}
		})
	}
}
