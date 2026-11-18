package fakeclickhouse

import (
	"math"
	"regexp"
	"time"
)

type function func(name string, arguments []compiled) compiled

var functions = map[string]function{
	"length":              length,
	"startsWith":          startsWith,
	"concat":              concat,
	"extract":             extract,
	"if":                  choose,
	"toNullable":          toNullable,
	"dateDiff":            dateDiff,
	"now64":               now64,
	"toYYYYMM":            toYYYYMM,
	"JSONExtract":         jsonExtract,
	"JSONExtractArrayRaw": jsonExtractArrayRaw,
	"JSONExtractString": jsonExtractAs(dataType{kind: kindString}, func(value jsonValue) any {
		return value.asString()
	}),
	"JSONExtractInt": jsonExtractAs(dataType{kind: kindInt64}, func(value jsonValue) any {
		if whole, ok := value.asInt64(false); ok {
			return whole
		}
		return int64(0)
	}),
	"JSONExtractUInt": jsonExtractAs(dataType{kind: kindUInt64}, func(value jsonValue) any {
		if whole, ok := value.asUInt64(false); ok {
			return whole
		}
		return uint64(0)
	}),
}

func expectArguments(name string, arguments []compiled, kinds ...kind) {
	if len(arguments) != len(kinds) {
		panic(failure(wrongArgumentCount, "Number of arguments for function %s doesn't match: passed %d, should be %d", name, len(arguments), len(kinds)))
	}
	for i, argument := range arguments {
		if argument.typ.kind != kinds[i] {
			panic(failure(illegalArgumentType, "Illegal type %s of argument %d of function %s. %s", argument.typ, i+1, name, teach))
		}
	}
}

func constantString(name string, argument compiled) string {
	if !argument.constant || argument.typ != (dataType{kind: kindString}) {
		panic(unimplemented("an argument of %s that is not a constant string where the fake needs one", name))
	}
	return argument.eval(nil).(string)
}

func strict(result dataType, arguments []compiled, apply func(values []any) any) compiled {
	for _, argument := range arguments {
		result.nullable = result.nullable || argument.typ.nullable
	}
	return compiled{typ: result, eval: func(row []any) any {
		values := make([]any, len(arguments))
		for i, argument := range arguments {
			if values[i] = argument.eval(row); values[i] == nil {
				return nil
			}
		}
		return apply(values)
	}}
}

func length(name string, arguments []compiled) compiled {
	expectArguments(name, arguments, kindString)
	return strict(dataType{kind: kindUInt64}, arguments, func(values []any) any {
		return uint64(len(values[0].(string)))
	})
}

func startsWith(name string, arguments []compiled) compiled {
	expectArguments(name, arguments, kindString, kindString)
	return strict(dataType{kind: kindUInt8}, arguments, func(values []any) any {
		text, prefix := values[0].(string), values[1].(string)
		return flag(len(text) >= len(prefix) && text[:len(prefix)] == prefix)
	})
}

func concat(name string, arguments []compiled) compiled {
	kinds := make([]kind, max(len(arguments), 1))
	expectArguments(name, arguments, kinds...)
	return strict(dataType{kind: kindString}, arguments, func(values []any) any {
		joined := ""
		for _, value := range values {
			joined += value.(string)
		}
		return joined
	})
}

func extract(name string, arguments []compiled) compiled {
	expectArguments(name, arguments, kindString, kindString)
	pattern, err := regexp.Compile(constantString(name, arguments[1]))
	if err != nil {
		panic(failure(cannotCompileRegexp, "OptimizedRegularExpression: cannot compile re2: %v", err))
	}
	return strict(dataType{kind: kindString}, arguments[:1], func(values []any) any {
		found := pattern.FindStringSubmatch(values[0].(string))
		switch len(found) {
		case 0:
			return ""
		case 1:
			return found[0]
		default:
			return found[1]
		}
	})
}

func choose(name string, arguments []compiled) compiled {
	if len(arguments) != 3 {
		panic(failure(wrongArgumentCount, "Number of arguments for function %s doesn't match: passed %d, should be 3", name, len(arguments)))
	}
	condition, then, otherwise := arguments[0], arguments[1], arguments[2]
	requireCondition(name, condition)
	if then.typ.kind != otherwise.typ.kind {
		panic(failure(noCommonType, "There is no supertype for types %s, %s", then.typ, otherwise.typ))
	}
	return compiled{
		typ: dataType{kind: then.typ.kind, nullable: then.typ.nullable || otherwise.typ.nullable},
		eval: func(row []any) any {
			if isTrue(condition.eval(row)) {
				return then.eval(row)
			}
			return otherwise.eval(row)
		},
	}
}

func toNullable(name string, arguments []compiled) compiled {
	if len(arguments) != 1 {
		panic(failure(wrongArgumentCount, "Number of arguments for function %s doesn't match: passed %d, should be 1", name, len(arguments)))
	}
	return compiled{typ: dataType{kind: arguments[0].typ.kind, nullable: true}, eval: arguments[0].eval}
}

func dateDiff(name string, arguments []compiled) compiled {
	expectArguments(name, arguments, kindString, kindDateTime64, kindDateTime64)
	if unit := constantString(name, arguments[0]); unit != "second" {
		panic(unimplemented("dateDiff in the unit %s, only second", unit))
	}
	return strict(dataType{kind: kindInt64}, arguments[1:], func(values []any) any {
		return floorSeconds(values[1].(int64)) - floorSeconds(values[0].(int64))
	})
}

func floorSeconds(milliseconds int64) int64 {
	return int64(math.Floor(float64(milliseconds) / 1000))
}

func now64(name string, arguments []compiled) compiled {
	expectArguments(name, arguments)
	return compiled{typ: dataType{kind: kindDateTime64}, eval: func([]any) any { return time.Now().UnixMilli() }}
}

func toYYYYMM(name string, arguments []compiled) compiled {
	expectArguments(name, arguments, kindDateTime64)
	return strict(dataType{kind: kindUInt32}, arguments, func(values []any) any {
		at := time.UnixMilli(values[0].(int64)).UTC()
		return uint64(at.Year()*100 + int(at.Month()))
	})
}

func jsonPath(name string, arguments []compiled) (compiled, []string) {
	if len(arguments) == 0 || arguments[0].typ.kind != kindString {
		panic(failure(illegalArgumentType, "The first argument of function %s should be a string containing JSON", name))
	}
	path := make([]string, len(arguments)-1)
	for i, key := range arguments[1:] {
		path[i] = constantString(name, key)
	}
	return arguments[0], path
}

func jsonExtractAs(result dataType, convert func(found jsonValue) any) function {
	return func(name string, arguments []compiled) compiled {
		document, path := jsonPath(name, arguments)
		return strict(result, []compiled{document}, func(values []any) any {
			if found, ok := parsedOrNull(values[0].(string)).at(path); ok {
				return convert(found)
			}
			return result.zero()
		})
	}
}

func jsonExtractArrayRaw(name string, arguments []compiled) compiled {
	document, path := jsonPath(name, arguments)
	return strict(dataType{kind: kindArrayOfString}, []compiled{document}, func(values []any) any {
		raw := []string{}
		if found, ok := parsedOrNull(values[0].(string)).at(path); ok && found.kind == jsonArray {
			for _, item := range found.items {
				raw = append(raw, item.raw())
			}
		}
		return raw
	})
}

var jsonExtractConversions = map[kind]func(found jsonValue) (any, bool){
	kindInt64:   func(found jsonValue) (any, bool) { return typed(found.asInt64(true)) },
	kindUInt64:  func(found jsonValue) (any, bool) { return typed(found.asUInt64(true)) },
	kindFloat64: func(found jsonValue) (any, bool) { return typed(found.asFloat64()) },
	kindBool:    func(found jsonValue) (any, bool) { return found.asBool(), true },
}

func typed[Value any](value Value, ok bool) (any, bool) {
	return value, ok
}

func jsonExtract(name string, arguments []compiled) compiled {
	if len(arguments) < 2 {
		panic(failure(wrongArgumentCount, "Function %s requires at least two arguments", name))
	}
	target := parseDataType(constantString(name, arguments[len(arguments)-1]))
	convert, known := jsonExtractConversions[target.kind]
	if !known || target == (dataType{kind: kindBool, nullable: true}) {
		panic(unimplemented("%s to the type %s", name, target))
	}
	document, path := jsonPath(name, arguments[:len(arguments)-1])
	extracted := strict(dataType{kind: target.kind}, []compiled{document}, func(values []any) any {
		if found, ok := parsedOrNull(values[0].(string)).at(path); ok {
			if converted, ok := convert(found); ok {
				return converted
			}
		}
		return target.zero()
	})
	extracted.typ.nullable = extracted.typ.nullable || target.nullable
	return extracted
}

func parsedOrNull(text string) jsonValue {
	parsed, _ := parseJSON(text)
	return parsed
}

type aggregateState interface {
	add(value any)
	result() any
}

type aggregate struct {
	arity  int
	result func(argument dataType) dataType
	start  func(argument dataType) aggregateState
}

var aggregates = map[string]aggregate{
	"count": {
		result: func(dataType) dataType { return dataType{kind: kindUInt64} },
		start:  func(dataType) aggregateState { return &counted{} },
	},
	"uniqExact": {
		arity:  1,
		result: func(dataType) dataType { return dataType{kind: kindUInt64} },
		start:  func(dataType) aggregateState { return &distinctValues{seen: map[string]bool{}} },
	},
	"min": {
		arity:  1,
		result: func(argument dataType) dataType { return argument },
		start:  func(argument dataType) aggregateState { return &minimum{typ: argument} },
	},
}

type counted struct{ rows uint64 }

func (c *counted) add(any)     { c.rows++ }
func (c *counted) result() any { return c.rows }

type distinctValues struct{ seen map[string]bool }

func (d *distinctValues) add(value any) {
	if value != nil {
		d.seen[keyOf([]any{value})] = true
	}
}

func (d *distinctValues) result() any { return uint64(len(d.seen)) }

type minimum struct {
	typ   dataType
	found any
}

func (m *minimum) add(value any) {
	if value != nil && (m.found == nil || compareValues(value, m.found) < 0) {
		m.found = value
	}
}

func (m *minimum) result() any {
	if m.found == nil {
		return m.typ.zero()
	}
	return m.found
}
