package store

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// The shadow must stay SQL-only: a Go re-implementation would test our understanding of SQLite instead of the machinery on top of it.

const modelField = "n"

func modelDeclaration() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  "app/model",
		Collection: "docs",
		Fields:     []docstore.FieldSpec{{Name: modelField, Type: docstore.FieldNumber}},
	}
}

// The sweep is every arrangement of this alphabet, so one more entry multiplies the whole run.
var modelBodies = []string{
	`{"n":1}`,
	`{"n":2}`,
	`{"n":"1"}`,
	`{"n":null}`,
	`{}`,
	`{"n":[1]}`,
}

// Receipt (2026-08-04): 3 documents = 85,536 checks in 1.1s, 4 = 855,360 in 13.5s, and every
// mutation this harness was falsified against is caught at three. ATTN_DOCSTORE_SWEEP raises it.
var modelIDs = sweepIDs()

func sweepIDs() []string {
	size := 3
	if raw := os.Getenv("ATTN_DOCSTORE_SWEEP"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 2 || n > 6 {
			panic(fmt.Sprintf("ATTN_DOCSTORE_SWEEP=%q: want a corpus size between 2 and 6 (the default is 3; 5 and up are a soak, not a suite run)", raw))
		}
		size = n
	}
	ids := make([]string, size)
	for i := range ids {
		ids[i] = string(rune('a' + i))
	}
	return ids
}

// Built once: a store per corpus would run all the migrations again and dominate the sweep.
type modelWorld struct {
	t      *testing.T
	s      *Store
	schema docstore.CollectionSchema
	table  string
	shadow string
	ids    []string

	anchors map[string]*docstore.Document
}

func newModelWorld(t *testing.T) *modelWorld {
	return newModelWorldFor(t, modelDeclaration(), modelIDs)
}

func newModelWorldFor(t *testing.T, decl docstore.CollectionSchema, ids []string) *modelWorld {
	t.Helper()
	s := New()
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(decl, base); err != nil {
		t.Fatalf("define: %v", err)
	}
	schema := declOf(t, s, decl.Namespace, decl.Collection)
	w := &modelWorld{t: t, s: s, schema: schema, table: schema.Table, shadow: "shadow_docs", ids: ids, anchors: map[string]*docstore.Document{}}
	w.createShadow()
	return w
}

func (w *modelWorld) createShadow() {
	w.t.Helper()
	cols := []string{`id TEXT PRIMARY KEY`, `body TEXT`, `created_at TEXT`, `updated_at TEXT`}
	for _, f := range w.schema.Fields {
		cols = append(cols, fmt.Sprintf("%s %s", shadowColumn(f.Name), docstore.ColumnAffinity(f.Type)))
	}
	stmt := fmt.Sprintf("CREATE TABLE %s (%s)", w.shadow, strings.Join(cols, ", "))
	if _, err := w.s.db.Exec(stmt); err != nil {
		w.t.Fatalf("create shadow: %v", err)
	}
}

// Declared fields get a prefix so a collection may declare a field called `id`.
func shadowColumn(field string) string {
	if field == docstore.FieldCreatedAt || field == docstore.FieldUpdatedAt {
		return field
	}
	return "s_" + field
}

func (w *modelWorld) loadCorpus(bodies []string) {
	w.t.Helper()
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	for i, id := range w.ids {
		if _, err := w.s.PutDocument(w.schema, id, []byte(bodies[i]), base.Add(time.Duration(i)*time.Second), nil); err != nil {
			w.t.Fatalf("put %s: %v", id, err)
		}
	}
	w.refillShadow()
}

// One INSERT ... SELECT: no stored value passes through Go, which keeps the two paths independent.
func (w *modelWorld) refillShadow() {
	w.t.Helper()
	w.anchors = map[string]*docstore.Document{}
	if _, err := w.s.db.Exec("DELETE FROM " + w.shadow); err != nil {
		w.t.Fatalf("clear shadow: %v", err)
	}
	names := []string{"id", "body", "created_at", "updated_at"}
	exprs := []string{"id", "body", "created_at", "updated_at"}
	for _, f := range w.schema.Fields {
		names = append(names, shadowColumn(f.Name))
		exprs = append(exprs, docstore.FieldExpression(f.Name))
	}
	stmt := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s",
		w.shadow, strings.Join(names, ", "), strings.Join(exprs, ", "), w.table)
	if _, err := w.s.db.Exec(stmt); err != nil {
		w.t.Fatalf("fill shadow: %v", err)
	}
}

func (w *modelWorld) naiveOrder(q docstore.Query) []string {
	w.t.Helper()
	var where []string
	var args []any
	for _, f := range q.Filters {
		where = append(where, fmt.Sprintf("%s %s ?", shadowColumn(f.Field), naiveOp(w.t, f.Op)))
		args = append(args, naiveBind(w.t, w.schema, f))
	}
	order := "id ASC"
	if q.Sort != nil {
		dir := "ASC"
		if q.Sort.Desc {
			dir = "DESC"
		}
		order = fmt.Sprintf("%s %s, id %s", shadowColumn(q.Sort.Field), dir, dir)
	}
	stmt := "SELECT id FROM " + w.shadow
	if len(where) > 0 {
		stmt += " WHERE " + strings.Join(where, " AND ")
	}
	stmt += " ORDER BY " + order
	return w.scanIDs(stmt, args)
}

// Never share these mappings with the compiler: a wrong one must show as a disagreement, not cancel out.
func naiveOp(t *testing.T, op docstore.Op) string {
	t.Helper()
	switch op {
	case docstore.OpEq:
		return "="
	case docstore.OpLt:
		return "<"
	case docstore.OpLte:
		return "<="
	case docstore.OpGt:
		return ">"
	case docstore.OpGte:
		return ">="
	}
	t.Fatalf("no naive SQL for operator %q", op)
	return ""
}

func naiveBind(t *testing.T, schema docstore.CollectionSchema, f docstore.Filter) any {
	t.Helper()
	if f.Field == docstore.FieldCreatedAt || f.Field == docstore.FieldUpdatedAt {
		switch v := f.Value.(type) {
		case string:
			return v
		case time.Time:
			return v.UTC().Format(docstore.TimeFormat)
		}
		t.Fatalf("no naive binding for %v (%T) on %s", f.Value, f.Value, f.Field)
	}
	var declared docstore.FieldType
	for _, spec := range schema.Fields {
		if spec.Name == f.Field {
			declared = spec.Type
		}
	}
	switch declared {
	case docstore.FieldString:
		return f.Value.(string)
	case docstore.FieldNumber:
		switch v := f.Value.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		}
	case docstore.FieldBool:
		if f.Value.(bool) {
			return 1
		}
		return 0
	}
	t.Fatalf("no naive binding for %v (%T) on a %q field", f.Value, f.Value, declared)
	return nil
}

func naivePage(matching, everything []string, q docstore.Query) []string {
	out := matching
	if q.After != "" {
		past := map[string]bool{}
		seen := false
		for _, id := range everything {
			if seen {
				past[id] = true
			}
			if id == q.After {
				seen = true
			}
		}
		out = nil
		for _, id := range matching {
			if past[id] {
				out = append(out, id)
			}
		}
	}
	limit := q.Limit
	if limit == 0 {
		limit = docstore.DefaultLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return append([]string{}, out...)
}

// A cursor naming a document that is not there stays a nil anchor, so the cache must remember the miss.
func (w *modelWorld) anchorFor(after string) (*docstore.Document, error) {
	if after == "" {
		return nil, nil
	}
	if doc, ok := w.anchors[after]; ok {
		return doc, nil
	}
	doc, found, err := w.s.GetDocument(w.schema, after)
	if err != nil {
		return nil, err
	}
	if !found {
		doc = nil
	}
	w.anchors[after] = doc
	return doc, nil
}

func (w *modelWorld) realIDs(q docstore.Query) ([]string, error) {
	anchor, err := w.anchorFor(q.After)
	if err != nil {
		return nil, err
	}
	c, err := q.Compile(w.schema, anchor)
	if err != nil {
		return nil, err
	}
	docs, err := w.s.queryDocuments(c)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

func (w *modelWorld) unindexedIDs(q docstore.Query) ([]string, error) {
	anchor, err := w.anchorFor(q.After)
	if err != nil {
		return nil, err
	}
	c, err := q.Compile(w.schema, anchor)
	if err != nil {
		return nil, err
	}
	stmt := "SELECT id FROM " + c.Table + " NOT INDEXED"
	if c.Where != "" {
		stmt += " WHERE " + c.Where
	}
	stmt += " ORDER BY " + c.Order + " LIMIT ?"
	return w.scanIDs(stmt, append(append([]any{}, c.Args...), c.Limit)), nil
}

func (w *modelWorld) scanIDs(stmt string, args []any) []string {
	w.t.Helper()
	rows, err := w.s.db.Query(stmt, args...)
	if err != nil {
		w.t.Fatalf("%s: %v", stmt, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			w.t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		w.t.Fatalf("rows: %v", err)
	}
	return out
}

func modelQueries() []docstore.Query {
	sorts := []*docstore.Sort{
		nil,
		{Field: modelField},
		{Field: modelField, Desc: true},
	}
	filterSets := [][]docstore.Filter{nil}
	for _, op := range []docstore.Op{docstore.OpEq, docstore.OpLt, docstore.OpLte, docstore.OpGt, docstore.OpGte} {
		for _, bound := range []float64{1, 2} {
			filterSets = append(filterSets, []docstore.Filter{{Field: modelField, Op: op, Value: bound}})
		}
	}
	afters := append([]string{""}, modelIDs...)

	var out []docstore.Query
	for _, s := range sorts {
		for _, filters := range filterSets {
			for limit := 1; limit <= len(modelIDs); limit++ {
				for _, after := range afters {
					out = append(out, docstore.Query{
						Namespace:  "app/model",
						Collection: "docs",
						Filters:    filters,
						Sort:       s,
						Limit:      limit,
						After:      after,
					})
				}
			}
		}
	}
	return out
}

func describeQuery(q docstore.Query) string {
	parts := []string{}
	for _, f := range q.Filters {
		parts = append(parts, fmt.Sprintf("%s %s %v", f.Field, f.Op, f.Value))
	}
	if len(parts) == 0 {
		parts = append(parts, "no filter")
	}
	sortDesc := "no sort"
	if q.Sort != nil {
		dir := "asc"
		if q.Sort.Desc {
			dir = "desc"
		}
		sortDesc = fmt.Sprintf("sort %s %s", q.Sort.Field, dir)
	}
	after := "from the start"
	if q.After != "" {
		after = "after " + q.After
	}
	return fmt.Sprintf("%s, %s, limit %d, %s", strings.Join(parts, " and "), sortDesc, q.Limit, after)
}

func describeCorpus(bodies []string) string {
	parts := make([]string, len(bodies))
	for i, b := range bodies {
		parts[i] = modelIDs[i] + "=" + b
	}
	return strings.Join(parts, " ")
}

func TestEverySmallCorpusAgreesWithTheDumbQuery(t *testing.T) {
	w := newModelWorld(t)
	queries := modelQueries()

	corpora := 0
	checks := 0
	for _, bodies := range allCorpora() {
		w.loadCorpus(bodies)
		corpora++

		cache := map[string][]string{}
		for _, q := range queries {
			matching, everything := w.dumbOrders(q, cache)
			want := naivePage(matching, everything, q)

			got, err := w.realIDs(q)
			if err != nil {
				t.Fatalf("corpus [%s]\nquery  %s\ncompiled or ran with an error: %v",
					describeCorpus(bodies), describeQuery(q), err)
			}
			if !sameIDs(got, want) {
				t.Fatalf("corpus [%s]\nquery   %s\nreal    %v\ndumb    %v\nmatched %v\norder   %v",
					describeCorpus(bodies), describeQuery(q), got, want, matching, everything)
			}
			checks++
		}
	}
	t.Logf("%d corpora x %d queries = %d checks", corpora, len(queries), checks)
}

func describeOrdering(q docstore.Query) string {
	var b strings.Builder
	for _, f := range q.Filters {
		fmt.Fprintf(&b, "%s|%s|%v;", f.Field, f.Op, f.Value)
	}
	if q.Sort != nil {
		fmt.Fprintf(&b, "sort:%s:%v", q.Sort.Field, q.Sort.Desc)
	}
	return b.String()
}

func allCorpora() [][]string {
	out := [][]string{{}}
	for range modelIDs {
		next := make([][]string, 0, len(out)*len(modelBodies))
		for _, prefix := range out {
			for _, body := range modelBodies {
				grown := append(append([]string{}, prefix...), body)
				next = append(next, grown)
			}
		}
		out = next
	}
	return out
}

type answer struct {
	real      []string
	dumb      []string
	unindexed []string
	matching  []string
	order     []string
}

func (w *modelWorld) dumbOrders(q docstore.Query, cache map[string][]string) (matching, everything []string) {
	w.t.Helper()
	orderOf := func(qq docstore.Query) []string {
		key := describeOrdering(qq)
		got, ok := cache[key]
		if !ok {
			got = w.naiveOrder(qq)
			cache[key] = got
		}
		return got
	}
	unfiltered := q
	unfiltered.Filters = nil
	return orderOf(q), orderOf(unfiltered)
}

func (w *modelWorld) ask(q docstore.Query, cache map[string][]string) answer {
	w.t.Helper()
	matching, everything := w.dumbOrders(q, cache)

	real, err := w.realIDs(q)
	if err != nil {
		w.t.Fatalf("query %s: %v", describeQuery(q), err)
	}
	unindexed, err := w.unindexedIDs(q)
	if err != nil {
		w.t.Fatalf("unindexed query %s: %v", describeQuery(q), err)
	}
	return answer{
		real:      real,
		dumb:      naivePage(matching, everything, q),
		unindexed: unindexed,
		matching:  matching,
		order:     everything,
	}
}

func (w *modelWorld) check(context string, q docstore.Query, a answer) {
	w.t.Helper()
	if !sameIDs(a.real, a.dumb) {
		w.t.Fatalf("%s\nquery   %s\nreal    %v\ndumb    %v\nmatched %v\norder   %v",
			context, describeQuery(q), a.real, a.dumb, a.matching, a.order)
	}
	if !sameIDs(a.real, a.unindexed) {
		w.t.Fatalf("%s\nquery     %s\nindexed   %v\nscanned   %v\nthe index disagrees with the column it indexes",
			context, describeQuery(q), a.real, a.unindexed)
	}
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Big enough that the planner prefers the index: the only regime where it can disagree with its column.
const (
	largeCorpusSize  = 4000
	largeQueriesEach = 400
)

func largeDeclaration() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  "app/model",
		Collection: "large",
		Fields: []docstore.FieldSpec{
			{Name: "n", Type: docstore.FieldNumber},
			{Name: "s", Type: docstore.FieldString},
			{Name: "b", Type: docstore.FieldBool},
		},
	}
}

// Values repeat heavily so tie groups are long, which is what the cursor's tuple comparison exists for.
func largeBody(rng *rand.Rand) string {
	fields := []string{}
	switch rng.Intn(8) {
	case 0:
	case 1:
		fields = append(fields, `"n":null`)
	case 2:
		fields = append(fields, fmt.Sprintf(`"n":"%d"`, rng.Intn(4)))
	case 3:
		fields = append(fields, fmt.Sprintf(`"n":[%d]`, rng.Intn(3)))
	case 4:
		fields = append(fields, `"n":{"deep":1}`)
	default:
		fields = append(fields, fmt.Sprintf(`"n":%d`, rng.Intn(4)))
	}
	switch rng.Intn(6) {
	case 0:
	case 1:
		fields = append(fields, `"s":null`)
	case 2:
		fields = append(fields, fmt.Sprintf(`"s":%d`, rng.Intn(3)))
	default:
		fields = append(fields, fmt.Sprintf(`"s":"v%d"`, rng.Intn(4)))
	}
	switch rng.Intn(4) {
	case 0:
	case 1:
		fields = append(fields, `"b":null`)
	default:
		fields = append(fields, fmt.Sprintf(`"b":%v`, rng.Intn(2) == 1))
	}
	fields = append(fields, fmt.Sprintf(`"note":"n%d"`, rng.Intn(3)))
	return "{" + strings.Join(fields, ",") + "}"
}

func largeQuery(rng *rand.Rand, ids []string, schema docstore.CollectionSchema) docstore.Query {
	q := docstore.Query{Namespace: schema.Namespace, Collection: schema.Collection}

	sortFields := []string{docstore.FieldCreatedAt, docstore.FieldUpdatedAt}
	for _, f := range schema.Fields {
		sortFields = append(sortFields, f.Name)
	}
	if rng.Intn(6) > 0 {
		q.Sort = &docstore.Sort{Field: sortFields[rng.Intn(len(sortFields))], Desc: rng.Intn(2) == 1}
	}

	ops := []docstore.Op{docstore.OpEq, docstore.OpLt, docstore.OpLte, docstore.OpGt, docstore.OpGte}
	for n := rng.Intn(3); n > 0 && len(schema.Fields) > 0; n-- {
		spec := schema.Fields[rng.Intn(len(schema.Fields))]
		f := docstore.Filter{Field: spec.Name, Op: ops[rng.Intn(len(ops))]}
		switch spec.Type {
		case docstore.FieldNumber:
			f.Value = float64(rng.Intn(5))
		case docstore.FieldString:
			f.Value = fmt.Sprintf("v%d", rng.Intn(5))
		case docstore.FieldBool:
			f.Value = rng.Intn(2) == 1
		}
		q.Filters = append(q.Filters, f)
	}

	q.Limit = 1 + rng.Intn(50)
	if len(ids) > 0 && rng.Intn(3) == 0 {
		q.After = ids[rng.Intn(len(ids))]
	}
	return q
}

func TestALargeRandomCorpusAgreesWithTheDumbQuery(t *testing.T) {
	for _, seed := range []int64{20260804, 7, 991, 40409, 1234567} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))

			ids := make([]string, largeCorpusSize)
			bodies := make([]string, largeCorpusSize)
			for i := range ids {
				// Zero-padded so lexicographic order — the tiebreaker the compiler appends — matches write order.
				ids[i] = fmt.Sprintf("doc-%05d", i)
				bodies[i] = largeBody(rng)
			}

			w := newModelWorldFor(t, largeDeclaration(), ids)
			w.loadCorpus(bodies)

			cache := map[string][]string{}
			indexed := 0
			for i := 0; i < largeQueriesEach; i++ {
				q := largeQuery(rng, ids, w.schema)
				a := w.ask(q, cache)
				w.check(fmt.Sprintf("seed %d, query %d, %d documents", seed, i, largeCorpusSize), q, a)
				if w.usesAnIndex(q) {
					indexed++
				}
			}

			// Without this the unindexed comparison is vacuous: the planner may never choose an index.
			if indexed == 0 {
				t.Fatalf("not one of %d queries reached an index, so the unindexed comparison checked nothing; the corpus is too small or the declaration lost its indexes", largeQueriesEach)
			}
			t.Logf("%d of %d queries used an index over %d documents", indexed, largeQueriesEach, largeCorpusSize)
		})
	}
}

func (w *modelWorld) usesAnIndex(q docstore.Query) bool {
	w.t.Helper()
	anchor, err := w.anchorFor(q.After)
	if err != nil {
		w.t.Fatalf("anchor: %v", err)
	}
	c, err := q.Compile(w.schema, anchor)
	if err != nil {
		w.t.Fatalf("compile: %v", err)
	}
	plan, err := w.s.QueryPlan(c)
	if err != nil {
		w.t.Fatalf("plan: %v", err)
	}
	for _, step := range plan {
		if strings.Contains(step, "USING INDEX") || strings.Contains(step, "USING COVERING INDEX") {
			return true
		}
	}
	return false
}

const movingSteps = 300

func TestAMovingCorpusAgreesWithTheDumbQuery(t *testing.T) {
	for _, seed := range []int64{20260804, 31337} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			w := newModelWorldFor(t, largeDeclaration(), nil)
			base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

			live := map[string]bool{}
			liveIDs := func() []string {
				out := make([]string, 0, len(live))
				for id := range live {
					out = append(out, id)
				}
				sort.Strings(out)
				return out
			}

			for i := 0; i < 40; i++ {
				id := fmt.Sprintf("doc-%05d", i)
				if _, err := w.s.PutDocument(w.schema, id, []byte(largeBody(rng)), base.Add(time.Duration(i)*time.Second), nil); err != nil {
					t.Fatalf("seed put %s: %v", id, err)
				}
				live[id] = true
			}

			redeclarations := 0
			for step := 0; step < movingSteps; step++ {
				context := fmt.Sprintf("seed %d, step %d, %d documents", seed, step, len(live))
				now := base.Add(time.Duration(1000+step) * time.Second)

				switch n := rng.Intn(12); {
				case n < 5:
					id := fmt.Sprintf("doc-%05d", 40+step)
					if _, err := w.s.PutDocument(w.schema, id, []byte(largeBody(rng)), now, nil); err != nil {
						t.Fatalf("%s: put %s: %v", context, id, err)
					}
					live[id] = true
				case n < 7:
					ids := liveIDs()
					if len(ids) == 0 {
						continue
					}
					id := ids[rng.Intn(len(ids))]
					if _, err := w.s.PutDocument(w.schema, id, []byte(largeBody(rng)), now, nil); err != nil {
						t.Fatalf("%s: rewrite %s: %v", context, id, err)
					}
				case n < 10:
					ids := liveIDs()
					if len(ids) == 0 {
						continue
					}
					id := ids[rng.Intn(len(ids))]
					existed, err := w.s.DeleteDocument(w.schema, id, nil)
					if err != nil {
						t.Fatalf("%s: delete %s: %v", context, id, err)
					}
					if !existed {
						t.Fatalf("%s: delete %s reported nothing was there, but it was written and never removed", context, id)
					}
					delete(live, id)
				default:
					w.redeclare(randomDeclaration(rng), now)
					redeclarations++
				}

				w.refillShadow()

				ids := liveIDs()
				if got := w.storedIDs(); !sameIDs(got, ids) {
					t.Fatalf("%s: the collection holds %v, the model says %v", context, got, ids)
				}
				if len(ids) == 0 {
					continue
				}

				cache := map[string][]string{}
				for i := 0; i < 4; i++ {
					q := largeQuery(rng, ids, w.schema)
					w.check(context, q, w.ask(q, cache))
				}

				w.checkFullPagination(context, rng, ids)
			}
			if redeclarations == 0 {
				t.Fatalf("no step redeclared the collection, so this run never exercised DDL over live documents")
			}
			t.Logf("%d steps, %d redeclarations, %d documents left", movingSteps, redeclarations, len(live))
		})
	}
}

func (w *modelWorld) checkFullPagination(context string, rng *rand.Rand, ids []string) {
	w.t.Helper()
	q := largeQuery(rng, ids, w.schema)
	q.After = ""
	q.Limit = 1 + rng.Intn(4)

	full := w.naiveOrder(q)
	var walked []string
	after := ""
	for page := 0; ; page++ {
		if page > len(ids)+2 {
			w.t.Fatalf("%s: paging %s never ran out after %d pages; it is repeating documents",
				context, describeQuery(q), page)
		}
		step := q
		step.After = after
		got, err := w.realIDs(step)
		if err != nil {
			w.t.Fatalf("%s: paging %s: %v", context, describeQuery(step), err)
		}
		if len(got) == 0 {
			break
		}
		walked = append(walked, got...)
		after = got[len(got)-1]
	}
	if !sameIDs(walked, full) {
		w.t.Fatalf("%s\nquery  %s\npaging through in pages of %d walked\n  %v\nbut the whole answer is\n  %v",
			context, describeQuery(q), q.Limit, walked, full)
	}
}

func (w *modelWorld) redeclare(decl docstore.CollectionSchema, now time.Time) {
	w.t.Helper()
	if _, err := w.s.DefineDocumentCollection(decl, now); err != nil {
		w.t.Fatalf("redeclare: %v", err)
	}
	w.schema = declOf(w.t, w.s, decl.Namespace, decl.Collection)
	if _, err := w.s.db.Exec("DROP TABLE " + w.shadow); err != nil {
		w.t.Fatalf("drop shadow: %v", err)
	}
	w.createShadow()
}

func randomDeclaration(rng *rand.Rand) docstore.CollectionSchema {
	types := []docstore.FieldType{docstore.FieldString, docstore.FieldNumber, docstore.FieldBool}
	decl := largeDeclaration()
	decl.Fields = nil
	for _, name := range []string{"n", "s", "b"} {
		if rng.Intn(4) == 0 {
			continue
		}
		decl.Fields = append(decl.Fields, docstore.FieldSpec{Name: name, Type: types[rng.Intn(len(types))]})
	}
	if len(decl.Fields) == 0 {
		decl.Fields = []docstore.FieldSpec{{Name: "n", Type: docstore.FieldNumber}}
	}
	return decl
}

func (w *modelWorld) storedIDs() []string {
	return w.scanIDs("SELECT id FROM "+w.table+" ORDER BY id ASC", nil)
}
