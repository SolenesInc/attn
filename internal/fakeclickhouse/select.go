package fakeclickhouse

import (
	"fmt"
	"slices"
)

type relation struct {
	columns []scopeColumn
	rows    func() [][]any
}

type grouping struct {
	keys       []compiled
	keySlots   map[string]int
	aggregated []aggregated
}

type aggregated struct {
	argument *compiled
	start    func() aggregateState
}

func (g *grouping) aggregate(called call, plain *compiler) compiled {
	definition := aggregates[called.name]
	if len(called.arguments) != definition.arity {
		panic(failure(wrongArgumentCount, "Aggregate function %s requires %d arguments, and the query passes %d", called.name, definition.arity, len(called.arguments)))
	}
	var entry aggregated
	var argumentType dataType
	if definition.arity == 1 {
		argument := plain.compile(called.arguments[0])
		entry.argument, argumentType = &argument, argument.typ
	}
	entry.start = func() aggregateState { return definition.start(argumentType) }
	slot := len(g.keys) + len(g.aggregated)
	g.aggregated = append(g.aggregated, entry)
	return compiled{
		typ:  definition.result(argumentType),
		eval: func(row []any) any { return row[slot] },
		key:  fmt.Sprintf("aggregate#%d", slot),
	}
}

func (g *grouping) rows(input [][]any) [][]any {
	type group struct {
		keys   []any
		states []aggregateState
	}
	var groups []*group
	byKey := map[string]*group{}
	open := func(keys []any) *group {
		opened := &group{keys: keys}
		for _, entry := range g.aggregated {
			opened.states = append(opened.states, entry.start())
		}
		groups = append(groups, opened)
		return opened
	}
	for _, row := range input {
		keys := make([]any, len(g.keys))
		for i, key := range g.keys {
			keys[i] = key.eval(row)
		}
		name := keyOf(keys)
		current := byKey[name]
		if current == nil {
			current = open(keys)
			byKey[name] = current
		}
		for i, entry := range g.aggregated {
			var value any
			if entry.argument != nil {
				value = entry.argument.eval(row)
			}
			current.states[i].add(value)
		}
	}
	if len(g.keys) == 0 && len(groups) == 0 {
		open(nil)
	}
	rows := make([][]any, len(groups))
	for i, current := range groups {
		rows[i] = slices.Clone(current.keys)
		for _, state := range current.states {
			rows[i] = append(rows[i], state.result())
		}
	}
	return rows
}

func (s *session) plan(query *selectQuery, reader *user) relation {
	input := relation{rows: func() [][]any { return [][]any{{}} }}
	if query.from != nil {
		input = s.joined(query, reader)
	}
	c := s.compiler(reader, input.columns)
	items := query.items
	written := make([]expr, len(items))
	for i, item := range items {
		written[i] = item.expr
		if item.alias != "" {
			c.aliases[item.alias] = item.expr
			written[i] = identifier{name: item.alias}
		}
	}
	var where *compiled
	if query.where != nil {
		condition := c.compile(query.where)
		requireCondition("WHERE", condition)
		where = &condition
	}
	grouped := len(query.groupBy) > 0
	for _, item := range written {
		grouped = grouped || c.containsAggregate(item)
	}
	for _, term := range query.orderBy {
		grouped = grouped || c.containsAggregate(term)
	}
	if grouped {
		groups := &grouping{keySlots: map[string]int{}}
		for _, keyed := range query.groupBy {
			key := c.compile(keyed)
			groups.keySlots[key.key] = len(groups.keys)
			groups.keys = append(groups.keys, key)
		}
		c.grouping = groups
	}
	selected := make([]compiled, len(items))
	columns := make([]scopeColumn, len(items))
	for i, item := range items {
		selected[i] = c.compile(written[i])
		columns[i] = scopeColumn{name: item.text, typ: selected[i].typ}
		if named, isIdentifier := item.expr.(identifier); isIdentifier {
			columns[i].name = named.name
		}
		if item.alias != "" {
			columns[i].name = item.alias
		}
	}
	ordering := make([]compiled, len(query.orderBy))
	for i, term := range query.orderBy {
		ordering[i] = c.compile(term)
		if ordering[i].typ.kind == kindArrayOfString {
			panic(unimplemented("ORDER BY an array"))
		}
	}
	return relation{columns: columns, rows: func() [][]any {
		rows := input.rows()
		if where != nil {
			rows = slices.DeleteFunc(slices.Clone(rows), func(row []any) bool { return !isTrue(where.eval(row)) })
		}
		if c.grouping != nil {
			rows = c.grouping.rows(rows)
		}
		type produced struct{ values, order []any }
		output := make([]produced, 0, len(rows))
		seen := map[string]bool{}
		for _, row := range rows {
			made := produced{values: make([]any, len(selected)), order: make([]any, len(ordering))}
			for i, value := range selected {
				made.values[i] = value.eval(row)
			}
			if query.distinct {
				key := keyOf(made.values)
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			for i, value := range ordering {
				made.order[i] = value.eval(row)
			}
			output = append(output, made)
		}
		slices.SortStableFunc(output, func(a, b produced) int {
			for i := range ordering {
				if order := compareNullsLast(a.order[i], b.order[i]); order != 0 {
					return order
				}
			}
			return 0
		})
		if query.limit != nil && uint64(len(output)) > *query.limit {
			output = output[:*query.limit]
		}
		values := make([][]any, len(output))
		for i, made := range output {
			values[i] = made.values
		}
		return values
	}}
}

func compareNullsLast(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	default:
		return compareValues(a, b)
	}
}

func (s *session) joined(query *selectQuery, reader *user) relation {
	left := s.relation(*query.from, reader)
	if query.arrayJoin != nil {
		left = s.arrayJoined(left, *query.arrayJoin, reader)
	}
	if query.join != nil {
		left = s.leftJoined(left, *query.join, reader)
	}
	return left
}

func (s *session) relation(from source, reader *user) relation {
	qualifier := from.alias
	var found relation
	if from.subquery != nil {
		if from.final {
			panic(unimplemented("FINAL on a subquery"))
		}
		found = s.plan(from.subquery, reader)
	} else {
		found = s.storedRelation(from.table, reader, from.final)
		if qualifier == "" {
			qualifier = from.table
		}
	}
	columns := slices.Clone(found.columns)
	for i := range columns {
		columns[i].qualifier = qualifier
	}
	return relation{columns: columns, rows: found.rows}
}

func (s *session) storedRelation(name string, reader *user, final bool) relation {
	kept := s.external[name]
	if kept == nil {
		entry := s.database().relations[name]
		if entry == nil {
			panic(failure(unknownTable, "Unknown table expression identifier '%s'", name))
		}
		reader.require("SELECT", s.databaseName, name)
		if entry.view != nil {
			return s.viewed(name, entry.view, final)
		}
		kept = entry.table
	}
	if final && !kept.replacing {
		panic(failure(illegalFinal, "Storage MergeTree doesn't support FINAL"))
	}
	columns := make([]scopeColumn, len(kept.columns))
	for i, every := range kept.columns {
		columns[i] = scopeColumn{name: every.name, typ: every.typ}
	}
	return relation{columns: columns, rows: func() [][]any {
		if final {
			return kept.merged()
		}
		return kept.rows
	}}
}

func (s *session) viewed(name string, shown *view, final bool) relation {
	if final {
		panic(unimplemented("FINAL on the view %s", name))
	}
	definer := s.server.users[shown.definer]
	if definer == nil {
		panic(failure(unknownUser, "There is no user `%s` in `user directories`, and the view %s runs as that user", shown.definer, name))
	}
	return s.plan(shown.query, definer)
}

func (s *session) arrayJoined(left relation, join arrayJoin, reader *user) relation {
	array := s.compiler(reader, left.columns).compile(join.array)
	if array.typ != (dataType{kind: kindArrayOfString}) {
		panic(unimplemented("ARRAY JOIN over %s, only Array(String)", array.typ))
	}
	columns := append(slices.Clone(left.columns), scopeColumn{name: join.alias, typ: dataType{kind: kindString}})
	return relation{columns: columns, rows: func() [][]any {
		var rows [][]any
		for _, row := range left.rows() {
			for _, item := range array.eval(row).([]string) {
				rows = append(rows, append(slices.Clone(row), item))
			}
		}
		return rows
	}}
}

func (s *session) leftJoined(left relation, join leftJoin, reader *user) relation {
	right := s.relation(join.right, reader)
	leftSide, rightSide := s.compiler(reader, left.columns), s.compiler(reader, right.columns)
	var leftKeys, rightKeys []compiled
	for _, condition := range conjuncts(join.on) {
		equality, isComparison := condition.(comparison)
		if !isComparison || equality.operator != "=" {
			panic(unimplemented("a JOIN condition other than equalities joined by AND"))
		}
		var leftKey, rightKey compiled
		if failureOf(func() { leftKey = leftSide.compile(equality.left) }) == nil {
			rightKey = rightSide.compile(equality.right)
		} else {
			leftKey, rightKey = leftSide.compile(equality.right), rightSide.compile(equality.left)
		}
		if leftKey.typ.kind != rightKey.typ.kind {
			panic(unimplemented("a JOIN condition between %s and %s", leftKey.typ, rightKey.typ))
		}
		leftKeys, rightKeys = append(leftKeys, leftKey), append(rightKeys, rightKey)
	}
	missing := make([]any, len(right.columns))
	for i, every := range right.columns {
		missing[i] = every.typ.zero()
	}
	return relation{columns: append(slices.Clone(left.columns), right.columns...), rows: func() [][]any {
		matches := map[string][][]any{}
		for _, row := range right.rows() {
			if key, complete := joinKey(rightKeys, row); complete {
				matches[key] = append(matches[key], row)
			}
		}
		var rows [][]any
		for _, row := range left.rows() {
			key, complete := joinKey(leftKeys, row)
			if !complete || len(matches[key]) == 0 {
				rows = append(rows, append(slices.Clone(row), missing...))
				continue
			}
			for _, match := range matches[key] {
				rows = append(rows, append(slices.Clone(row), match...))
			}
		}
		return rows
	}}
}

func conjuncts(condition expr) []expr {
	if both, isLogical := condition.(logical); isLogical && both.operator == "AND" {
		return append(conjuncts(both.left), conjuncts(both.right)...)
	}
	return []expr{condition}
}

func joinKey(keys []compiled, row []any) (string, bool) {
	values := make([]any, len(keys))
	for i, key := range keys {
		if values[i] = key.eval(row); values[i] == nil {
			return "", false
		}
	}
	return keyOf(values), true
}
