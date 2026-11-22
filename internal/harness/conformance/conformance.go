// Package conformance is the suite every registered harness adapter passes, each run against its fake.
package conformance

import (
	"context"
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/harness"
)

// Row is what one adapter states while its fake is installed.
type Row struct {
	Facts   harness.Facts
	Offered bool
}

// Run checks that the registered adapters and the table name the same harnesses, and that each adapter answers its row.
func Run(t *testing.T, registered []harness.Harness, table []Row) {
	t.Helper()
	rows := make(map[harness.ID]Row, len(table))
	for _, row := range table {
		rows[row.Facts.ID] = row
	}
	for _, h := range registered {
		facts := h.Facts()
		row, listed := rows[facts.ID]
		delete(rows, facts.ID)
		t.Run(facts.ID, func(t *testing.T) {
			if !listed {
				t.Fatalf("the adapter %q has no row in the facts table", facts.ID)
			}
			if facts.DisplayName == "" {
				t.Errorf("the adapter %q states no display name", facts.ID)
			}
			if !reflect.DeepEqual(facts, row.Facts) {
				t.Errorf("the adapter %q states\n  %+v\nwant\n  %+v", facts.ID, facts, row.Facts)
			}
			health := h.Health(context.Background())
			if health.Installation == harness.Missing {
				t.Errorf("the adapter %q reports its fake missing: %s", facts.ID, health.Reason)
			}
			if health.Offered != row.Offered {
				t.Errorf("the adapter %q reports offered %v, want %v", facts.ID, health.Offered, row.Offered)
			}
		})
	}
	for id := range rows {
		t.Errorf("the facts table names %q, which no adapter registers", id)
	}
}
