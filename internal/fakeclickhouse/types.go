package fakeclickhouse

import (
	"cmp"
	"encoding/binary"
	"math"
	"strings"
	"time"
)

type kind int

const (
	kindString kind = iota
	kindInt64
	kindUInt8
	kindUInt32
	kindUInt64
	kindFloat64
	kindBool
	kindDateTime64
	kindArrayOfString
)

var kindNames = map[kind]string{
	kindString:        "String",
	kindInt64:         "Int64",
	kindUInt8:         "UInt8",
	kindUInt32:        "UInt32",
	kindUInt64:        "UInt64",
	kindFloat64:       "Float64",
	kindBool:          "Bool",
	kindDateTime64:    "DateTime64(3, 'UTC')",
	kindArrayOfString: "Array(String)",
}

var scalarKinds = map[string]kind{
	"String":  kindString,
	"Int64":   kindInt64,
	"UInt32":  kindUInt32,
	"UInt64":  kindUInt64,
	"Float64": kindFloat64,
	"Bool":    kindBool,
}

type dataType struct {
	kind     kind
	nullable bool
}

type column struct {
	name string
	typ  dataType
}

func (t dataType) String() string {
	if t.nullable {
		return "Nullable(" + kindNames[t.kind] + ")"
	}
	return kindNames[t.kind]
}

func (t dataType) truthy() bool {
	return t.kind == kindUInt8 || t.kind == kindBool
}

func (t dataType) zero() any {
	if t.nullable {
		return nil
	}
	switch t.kind {
	case kindString:
		return ""
	case kindInt64, kindDateTime64:
		return int64(0)
	case kindUInt8, kindUInt32, kindUInt64:
		return uint64(0)
	case kindFloat64:
		return float64(0)
	case kindBool:
		return false
	default:
		return []string(nil)
	}
}

func isTrue(value any) bool {
	switch value := value.(type) {
	case uint64:
		return value != 0
	case bool:
		return value
	default:
		return false
	}
}

func flag(set bool) any {
	if set {
		return uint64(1)
	}
	return uint64(0)
}

func compareValues(a, b any) int {
	switch a := a.(type) {
	case string:
		return strings.Compare(a, b.(string))
	case int64:
		return cmp.Compare(a, b.(int64))
	case uint64:
		return cmp.Compare(a, b.(uint64))
	case float64:
		return cmp.Compare(a, b.(float64))
	default:
		return cmp.Compare(flag(a.(bool)).(uint64), flag(b.(bool)).(uint64))
	}
}

func appendKey(key []byte, value any) []byte {
	switch value := value.(type) {
	case nil:
		return append(key, 0)
	case string:
		key = binary.AppendUvarint(append(key, 1), uint64(len(value)))
		return append(key, value...)
	case int64:
		return binary.AppendVarint(append(key, 2), value)
	case uint64:
		return binary.AppendUvarint(append(key, 3), value)
	case float64:
		return binary.BigEndian.AppendUint64(append(key, 4), math.Float64bits(value))
	case bool:
		return binary.AppendUvarint(append(key, 5), flag(value).(uint64))
	default:
		panic(unimplemented("%T values as keys", value))
	}
}

func keyOf(values []any) string {
	var key []byte
	for _, value := range values {
		key = appendKey(key, value)
	}
	return string(key)
}

var (
	basicDateTimeLayouts      = []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"}
	bestEffortDateTimeLayouts = append([]string{time.RFC3339}, basicDateTimeLayouts...)
)

func parseDateTime(text string, bestEffort bool) (int64, bool) {
	layouts := basicDateTimeLayouts
	if bestEffort {
		layouts = bestEffortDateTimeLayouts
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UnixMilli(), true
		}
	}
	return 0, false
}

func coerce(value any, from, to dataType, bestEffortDates bool) any {
	switch {
	case value == nil:
		return to.zero()
	case from.kind == to.kind:
		return value
	case from.kind == kindString && to.kind == kindDateTime64:
		parsed, ok := parseDateTime(value.(string), bestEffortDates)
		if !ok {
			panic(failure(cannotParseDateTime, "Cannot parse DateTime: while converting '%s' to %s", value, to))
		}
		return parsed
	default:
		panic(unimplemented("converting %s to %s", from, to))
	}
}
