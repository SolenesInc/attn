package fakeclickhouse

import (
	"fmt"
	"slices"
	"strings"
)

type compiled struct {
	typ      dataType
	eval     func(row []any) any
	key      string
	constant bool
}

type scopeColumn struct {
	qualifier string
	name      string
	typ       dataType
}

type compiler struct {
	session   *session
	reader    *user
	columns   []scopeColumn
	aliases   map[string]expr
	expanding map[string]bool
	grouping  *grouping
}

func (s *session) compiler(reader *user, columns []scopeColumn) *compiler {
	return &compiler{session: s, reader: reader, columns: columns, aliases: map[string]expr{}, expanding: map[string]bool{}}
}

func constant(typ dataType, value any) compiled {
	return compiled{typ: typ, eval: func([]any) any { return value }, key: fmt.Sprintf("%s:%#v", typ, value), constant: true}
}

func (c *compiler) ungrouped() *compiler {
	plain := *c
	plain.grouping = nil
	return &plain
}

func (c *compiler) compile(e expr) compiled {
	if c.grouping != nil {
		if grouped, ok := c.compileGrouped(e); ok {
			return grouped
		}
	}
	switch e := e.(type) {
	case literal:
		return constant(e.typ, e.value)
	case parameter:
		return c.session.parameter(e)
	case identifier:
		return c.compileIdentifier(e)
	case call:
		return c.compileCall(e)
	case comparison:
		return compare(e.operator, c.compile(e.left), c.compile(e.right))
	case logical:
		return connect(e.operator, c.compile(e.left), c.compile(e.right))
	case membership:
		return c.compileMembership(e)
	default:
		panic(unimplemented("a tuple outside the left side of IN"))
	}
}

func (c *compiler) compileGrouped(e expr) (compiled, bool) {
	if called, isCall := e.(call); isCall {
		if _, isAggregate := aggregates[called.name]; isAggregate {
			return c.grouping.aggregate(called, c.ungrouped()), true
		}
	}
	if c.containsAggregate(e) {
		return compiled{}, false
	}
	plain := c.ungrouped().compile(e)
	if slot, isKey := c.grouping.keySlots[plain.key]; isKey {
		return compiled{typ: plain.typ, eval: func(row []any) any { return row[slot] }, key: plain.key}, true
	}
	return plain, plain.constant
}

func (c *compiler) containsAggregate(e expr) bool {
	switch e := e.(type) {
	case call:
		_, isAggregate := aggregates[e.name]
		return isAggregate || slices.ContainsFunc(e.arguments, c.containsAggregate)
	case identifier:
		aliased, isAlias := c.aliases[e.name]
		if e.qualifier != "" || !isAlias || c.expanding[e.name] {
			return false
		}
		c.expanding[e.name] = true
		defer delete(c.expanding, e.name)
		return c.containsAggregate(aliased)
	case comparison:
		return c.containsAggregate(e.left) || c.containsAggregate(e.right)
	case logical:
		return c.containsAggregate(e.left) || c.containsAggregate(e.right)
	case membership:
		return c.containsAggregate(e.left)
	case tuple:
		return slices.ContainsFunc(e.items, c.containsAggregate)
	default:
		return false
	}
}

func (c *compiler) compileIdentifier(e identifier) compiled {
	if aliased, isAlias := c.aliases[e.name]; isAlias && e.qualifier == "" && !c.expanding[e.name] {
		c.expanding[e.name] = true
		defer delete(c.expanding, e.name)
		return c.compile(aliased)
	}
	written := e.name
	if e.qualifier != "" {
		written = e.qualifier + "." + e.name
	}
	for index, candidate := range c.columns {
		if candidate.name != e.name || (e.qualifier != "" && candidate.qualifier != e.qualifier) {
			continue
		}
		if c.grouping != nil {
			panic(failure(notAnAggregate, "Column '%s' is not under aggregate function and not in GROUP BY keys", written))
		}
		return compiled{typ: candidate.typ, eval: func(row []any) any { return row[index] }, key: fmt.Sprintf("#%d", index)}
	}
	panic(failure(unknownIdentifier, "Unknown expression identifier `%s`", written))
}

func (c *compiler) compileCall(e call) compiled {
	define, known := functions[e.name]
	if !known {
		if _, isAggregate := aggregates[e.name]; isAggregate {
			panic(failure(illegalAggregation, "Aggregate function %s is found in WHERE, in GROUP BY or inside another aggregate function", e.name))
		}
		panic(failure(unknownFunction, "Function with name `%s` does not exist. %s", e.name, teach))
	}
	arguments := make([]compiled, len(e.arguments))
	keys := make([]string, len(e.arguments))
	for i, argument := range e.arguments {
		arguments[i] = c.compile(argument)
		keys[i] = arguments[i].key
	}
	built := define(e.name, arguments)
	built.key = e.name + "(" + strings.Join(keys, ",") + ")"
	return built
}

func (c *compiler) compileMembership(e membership) compiled {
	items := []expr{e.left}
	if written, isTuple := e.left.(tuple); isTuple {
		items = written.items
	}
	left := make([]compiled, len(items))
	keys := make([]string, len(items))
	for i, item := range items {
		left[i] = c.compile(item)
		keys[i] = left[i].key
	}
	listed := c.session.storedRelation(e.table, c.reader, false)
	if len(listed.columns) != len(left) {
		panic(failure(wrongArgumentCount, "The left side of IN has %d values, and %s has %d columns", len(left), e.table, len(listed.columns)))
	}
	for i, every := range listed.columns {
		if every.typ.kind != left[i].typ.kind {
			panic(unimplemented("IN between %s and the %s column %s of %s", left[i].typ, every.typ, every.name, e.table))
		}
	}
	members := map[string]bool{}
	for _, row := range listed.rows() {
		members[keyOf(row)] = true
	}
	return compiled{
		typ: dataType{kind: kindUInt8},
		key: "(" + strings.Join(keys, ",") + ") IN " + e.table,
		eval: func(row []any) any {
			values := make([]any, len(left))
			for i, side := range left {
				values[i] = side.eval(row)
			}
			return flag(members[keyOf(values)])
		},
	}
}

func (s *session) parameter(e parameter) compiled {
	if e.typ != (dataType{kind: kindString}) {
		panic(unimplemented("a query parameter of type %s", e.typ))
	}
	value, set := s.parameters[e.name]
	if !set {
		panic(failure(unknownQueryParameter, "Substitution `%s` is not set", e.name))
	}
	return constant(e.typ, value)
}

var comparisons = map[string]func(order int) bool{
	"=":  func(order int) bool { return order == 0 },
	"!=": func(order int) bool { return order != 0 },
	"<":  func(order int) bool { return order < 0 },
	"<=": func(order int) bool { return order <= 0 },
	">":  func(order int) bool { return order > 0 },
	">=": func(order int) bool { return order >= 0 },
}

func compare(operator string, left, right compiled) compiled {
	left, right = stringConstantAs(left, right.typ), stringConstantAs(right, left.typ)
	if left.typ.kind != right.typ.kind || left.typ.kind == kindArrayOfString {
		panic(failure(noCommonType, "There is no supertype for types %s, %s", left.typ, right.typ))
	}
	holds := comparisons[operator]
	return compiled{
		typ: dataType{kind: kindUInt8, nullable: left.typ.nullable || right.typ.nullable},
		key: "(" + left.key + operator + right.key + ")",
		eval: func(row []any) any {
			a, b := left.eval(row), right.eval(row)
			if a == nil || b == nil {
				return nil
			}
			return flag(holds(compareValues(a, b)))
		},
	}
}

func stringConstantAs(side compiled, other dataType) compiled {
	text := dataType{kind: kindString}
	if !side.constant || side.typ != text || other.kind == kindString || other.kind == kindArrayOfString {
		return side
	}
	target := dataType{kind: other.kind}
	return constant(target, coerce(side.eval(nil), text, target, false))
}

func requireCondition(operator string, operands ...compiled) {
	for _, operand := range operands {
		if !operand.typ.truthy() {
			panic(failure(illegalArgumentType, "Illegal type %s of argument of %s", operand.typ, operator))
		}
	}
}

func connect(operator string, left, right compiled) compiled {
	requireCondition(operator, left, right)
	decisive := operator == "OR"
	return compiled{
		typ: dataType{kind: kindUInt8, nullable: left.typ.nullable || right.typ.nullable},
		key: "(" + left.key + " " + operator + " " + right.key + ")",
		eval: func(row []any) any {
			a, b := left.eval(row), right.eval(row)
			if (a != nil && isTrue(a) == decisive) || (b != nil && isTrue(b) == decisive) {
				return flag(decisive)
			}
			if a == nil || b == nil {
				return nil
			}
			return flag(!decisive)
		},
	}
}
