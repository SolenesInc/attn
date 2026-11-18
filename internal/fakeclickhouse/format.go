package fakeclickhouse

import (
	"math"
	"strconv"
	"strings"
	"time"
)

type result struct {
	columns     []column
	rows        [][]any
	jsonEachRow bool
}

const formatJSONEachRow = "JSONEachRow"

func (r *result) write(out *strings.Builder, isoDates bool) {
	for _, row := range r.rows {
		for i, value := range row {
			if r.jsonEachRow {
				opening := byte('{')
				if i > 0 {
					opening = ','
				}
				out.WriteByte(opening)
				writeJSONString(out, r.columns[i].name, true)
				out.WriteByte(':')
				writeJSONValue(out, value, r.columns[i].typ, isoDates)
				continue
			}
			if i > 0 {
				out.WriteByte('\t')
			}
			writeTabSeparated(out, value, r.columns[i].typ, isoDates)
		}
		if r.jsonEachRow {
			out.WriteByte('}')
		}
		out.WriteByte('\n')
	}
}

var tabSeparatedEscapes = strings.NewReplacer("\\", `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`, "\x00", `\0`, "\b", `\b`, "\f", `\f`, "'", `\'`)

func writeTabSeparated(out *strings.Builder, value any, typ dataType, isoDates bool) {
	switch value := value.(type) {
	case nil:
		out.WriteString(`\N`)
	case string:
		_, _ = tabSeparatedEscapes.WriteString(out, value)
	default:
		out.WriteString(plainText(value, typ, isoDates))
	}
}

func writeJSONValue(out *strings.Builder, value any, typ dataType, isoDates bool) {
	switch value := value.(type) {
	case nil:
		out.WriteString("null")
	case string:
		writeJSONString(out, value, true)
	default:
		text := plainText(value, typ, isoDates)
		if typ.kind == kindDateTime64 {
			text = `"` + text + `"`
		}
		out.WriteString(text)
	}
}

func plainText(value any, typ dataType, isoDates bool) string {
	switch value := value.(type) {
	case bool:
		return strconv.FormatBool(value)
	case uint64:
		return strconv.FormatUint(value, 10)
	case float64:
		return formatFloat(value)
	case int64:
		if typ.kind != kindDateTime64 {
			return strconv.FormatInt(value, 10)
		}
		layout := "2006-01-02 15:04:05.000"
		if isoDates {
			layout = "2006-01-02T15:04:05.000Z"
		}
		return time.UnixMilli(value).UTC().Format(layout)
	default:
		panic(unimplemented("writing a %T value", value))
	}
}

func writeJSONString(out *strings.Builder, text string, escapeSlash bool) {
	out.WriteByte('"')
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '"' || c == '\\':
			out.WriteByte('\\')
			out.WriteByte(c)
		case c == '/' && escapeSlash:
			out.WriteString(`\/`)
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\t':
			out.WriteString(`\t`)
		case c == '\r':
			out.WriteString(`\r`)
		case c == '\b':
			out.WriteString(`\b`)
		case c == '\f':
			out.WriteString(`\f`)
		case c < 0x20:
			out.WriteString(`\u00`)
			out.WriteByte("0123456789abcdef"[c>>4])
			out.WriteByte("0123456789abcdef"[c&0xf])
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte('"')
}

func formatFloat(value float64) string {
	if value == 0 {
		if math.Signbit(value) {
			return "-0"
		}
		return "0"
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponentText, _ := strings.Cut(scientific, "e")
	exponent, _ := strconv.Atoi(exponentText)
	if exponent >= -6 && exponent <= 20 {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	return mantissa + "e" + strconv.Itoa(exponent)
}
