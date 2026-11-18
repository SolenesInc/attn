package fakeclickhouse

import (
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"
)

type jsonKind int

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonNumber
	jsonString
	jsonArray
	jsonObject
)

type jsonValue struct {
	kind    jsonKind
	boolean bool
	number  string
	text    string
	items   []jsonValue
	members []jsonMember
}

type jsonMember struct {
	name  string
	value jsonValue
}

func parseJSON(text string) (jsonValue, bool) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	value, ok := readJSON(decoder)
	if !ok {
		return jsonValue{}, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return jsonValue{}, false
	}
	return value, true
}

func readJSON(decoder *json.Decoder) (jsonValue, bool) {
	next, err := decoder.Token()
	if err != nil {
		return jsonValue{}, false
	}
	switch next := next.(type) {
	case string:
		return jsonValue{kind: jsonString, text: next}, true
	case json.Number:
		return jsonValue{kind: jsonNumber, number: next.String()}, true
	case bool:
		return jsonValue{kind: jsonBool, boolean: next}, true
	case json.Delim:
		value := jsonValue{kind: jsonArray}
		if next == '{' {
			value.kind = jsonObject
		}
		for decoder.More() {
			var member jsonMember
			if value.kind == jsonObject {
				name, err := decoder.Token()
				if err != nil {
					return jsonValue{}, false
				}
				member.name = name.(string)
			}
			var ok bool
			if member.value, ok = readJSON(decoder); !ok {
				return jsonValue{}, false
			}
			if value.kind == jsonObject {
				value.members = append(value.members, member)
			} else {
				value.items = append(value.items, member.value)
			}
		}
		if _, err := decoder.Token(); err != nil {
			return jsonValue{}, false
		}
		return value, true
	default:
		return jsonValue{kind: jsonNull}, true
	}
}

func (v jsonValue) at(path []string) (jsonValue, bool) {
	for _, name := range path {
		found := false
		for _, member := range v.members {
			if v.kind == jsonObject && member.name == name {
				v, found = member.value, true
				break
			}
		}
		if !found {
			return jsonValue{}, false
		}
	}
	return v, true
}

func (v jsonValue) raw() string {
	var out strings.Builder
	v.writeRaw(&out)
	return out.String()
}

func (v jsonValue) writeRaw(out *strings.Builder) {
	switch v.kind {
	case jsonNull:
		out.WriteString("null")
	case jsonBool:
		out.WriteString(strconv.FormatBool(v.boolean))
	case jsonNumber:
		out.WriteString(v.numberText())
	case jsonString:
		writeJSONString(out, v.text, false)
	case jsonArray:
		out.WriteByte('[')
		for i, item := range v.items {
			if i > 0 {
				out.WriteByte(',')
			}
			item.writeRaw(out)
		}
		out.WriteByte(']')
	case jsonObject:
		out.WriteByte('{')
		for i, member := range v.members {
			if i > 0 {
				out.WriteByte(',')
			}
			writeJSONString(out, member.name, false)
			out.WriteByte(':')
			member.value.writeRaw(out)
		}
		out.WriteByte('}')
	}
}

func (v jsonValue) isWholeNumber() bool {
	return !strings.ContainsAny(v.number, ".eE")
}

func (v jsonValue) numberText() string {
	if v.isWholeNumber() {
		return v.number
	}
	fraction, err := strconv.ParseFloat(v.number, 64)
	if err != nil {
		return v.number
	}
	return formatFloat(fraction)
}

func (v jsonValue) asString() string {
	switch v.kind {
	case jsonNull:
		return ""
	case jsonString:
		return v.text
	default:
		return v.raw()
	}
}

func (v jsonValue) asInt64(boolCounts bool) (int64, bool) {
	switch v.kind {
	case jsonNumber:
		if v.isWholeNumber() {
			whole, err := strconv.ParseInt(v.number, 10, 64)
			return whole, err == nil
		}
		fraction, err := strconv.ParseFloat(v.number, 64)
		if err != nil || fraction < math.MinInt64 || fraction >= math.MaxInt64 {
			return 0, false
		}
		return int64(fraction), true
	case jsonString:
		whole, err := strconv.ParseInt(v.text, 10, 64)
		return whole, err == nil
	case jsonBool:
		if v.boolean {
			return 1, boolCounts
		}
		return 0, boolCounts
	default:
		return 0, false
	}
}

func (v jsonValue) asUInt64(boolCounts bool) (uint64, bool) {
	switch v.kind {
	case jsonNumber:
		if v.isWholeNumber() {
			whole, err := strconv.ParseUint(v.number, 10, 64)
			return whole, err == nil
		}
		fraction, err := strconv.ParseFloat(v.number, 64)
		if err != nil || fraction < 0 || fraction >= math.MaxUint64 {
			return 0, false
		}
		return uint64(fraction), true
	case jsonString:
		whole, err := strconv.ParseUint(v.text, 10, 64)
		return whole, err == nil
	case jsonBool:
		if v.boolean {
			return 1, boolCounts
		}
		return 0, boolCounts
	default:
		return 0, false
	}
}

func (v jsonValue) asFloat64() (float64, bool) {
	switch v.kind {
	case jsonNumber:
		if v.isWholeNumber() {
			if _, fits := v.asInt64(false); !fits {
				if _, fits := v.asUInt64(false); !fits {
					return 0, false
				}
			}
		}
		fraction, err := strconv.ParseFloat(v.number, 64)
		return fraction, err == nil
	case jsonString:
		fraction, err := strconv.ParseFloat(v.text, 64)
		return fraction, err == nil
	case jsonBool:
		if v.boolean {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func (v jsonValue) asBool() bool {
	switch v.kind {
	case jsonBool:
		return v.boolean
	case jsonNumber:
		fraction, err := strconv.ParseFloat(v.number, 64)
		return err == nil && fraction != 0
	default:
		return false
	}
}
