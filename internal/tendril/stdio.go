package tendril

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/types"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const ioSource = `package io

// Writer is the interface that wraps the basic Write method.
type Writer interface {
	Write(p []byte) (n int, err error)
}

// StringWriter is the interface that wraps the WriteString method.
type StringWriter interface {
	WriteString(s string) (n int, err error)
}

// WriteString writes the contents of the string s to w.
func WriteString(w Writer, s string) (n int, err error)

// EOF is the error returned when no more input is available.
var EOF error
`

const osSource = `package os

// A File is an output stream. Programs only have Stdout and Stderr.
type File struct{ fd uintptr }

func (f *File) Write(b []byte) (n int, err error)
func (f *File) WriteString(s string) (n int, err error)

var (
	Stdout *File
	Stderr *File
)

// Exit ends the program with the given status code. Deferred functions do
// not run.
func Exit(code int)
`

const fmtSource = `package fmt

import "io"

// Stringer is implemented by any value that has a String method.
type Stringer interface {
	String() string
}

func Print(a ...any) (n int, err error)
func Println(a ...any) (n int, err error)
func Printf(format string, a ...any) (n int, err error)
func Sprint(a ...any) string
func Sprintln(a ...any) string
func Sprintf(format string, a ...any) string
func Errorf(format string, a ...any) error
func Fprint(w io.Writer, a ...any) (n int, err error)
func Fprintln(w io.Writer, a ...any) (n int, err error)
func Fprintf(w io.Writer, format string, a ...any) (n int, err error)
`

const stringsSource = `package strings

import "iter"

func Compare(a, b string) int
func Contains(s, substr string) bool
func ContainsAny(s, chars string) bool
func ContainsRune(s string, r rune) bool
func Count(s, substr string) int
func Cut(s, sep string) (before, after string, found bool)
func CutPrefix(s, prefix string) (after string, found bool)
func CutSuffix(s, suffix string) (before string, found bool)
func EqualFold(s, t string) bool
func Fields(s string) []string
func FieldsSeq(s string) iter.Seq[string]
func HasPrefix(s, prefix string) bool
func HasSuffix(s, suffix string) bool
func Index(s, substr string) int
func IndexByte(s string, c byte) int
func IndexRune(s string, r rune) int
func Join(elems []string, sep string) string
func LastIndex(s, substr string) int
func Lines(s string) iter.Seq[string]
func Repeat(s string, count int) string
func Replace(s, old, new string, n int) string
func ReplaceAll(s, old, new string) string
func Split(s, sep string) []string
func SplitN(s, sep string, n int) []string
func SplitSeq(s, sep string) iter.Seq[string]
func ToLower(s string) string
func ToUpper(s string) string
func Trim(s, cutset string) string
func TrimLeft(s, cutset string) string
func TrimPrefix(s, prefix string) string
func TrimRight(s, cutset string) string
func TrimSpace(s string) string
func TrimSuffix(s, suffix string) string

// A Builder is used to efficiently build a string using Write methods.
type Builder struct{ buf uintptr }

func (b *Builder) Grow(n int)
func (b *Builder) Len() int
func (b *Builder) Reset()
func (b *Builder) String() string
func (b *Builder) Write(p []byte) (int, error)
func (b *Builder) WriteByte(c byte) error
func (b *Builder) WriteRune(r rune) (int, error)
func (b *Builder) WriteString(s string) (int, error)
`

const jsonSource = `package json

// Marshal returns the JSON encoding of v, following the struct field tags
// and omitempty rules of encoding/json.
func Marshal(v any) ([]byte, error)

// MarshalIndent is like Marshal but applies Indent to format the output.
func MarshalIndent(v any, prefix, indent string) ([]byte, error)

// Unmarshal parses the JSON-encoded data and stores the result in the value
// pointed to by v.
func Unmarshal(data []byte, v any) error

// Valid reports whether data is a valid JSON encoding.
func Valid(data []byte) bool
`

type osFile struct{ fd int }

func goArgs(m *machine, vs Value) []any {
	list := vs.([]Value)
	out := make([]any, len(list))
	for i, v := range list {
		out[i] = m.toGo(v, anyType, anyRType).Interface()
	}
	return out
}

func bytesOf(v Value) []byte {
	list := v.([]Value)
	out := make([]byte, len(list))
	for i, b := range list {
		out[i] = b.(byte)
	}
	return out
}

func byteValues(m *machine, b []byte) []Value {
	m.alloc(headerBytes + slotBytes*int64(len(b)))
	out := make([]Value, len(b))
	for i, c := range b {
		out[i] = c
	}
	return out
}

func (m *machine) stream(fd int) io.Writer {
	if fd == 2 {
		return m.stderr
	}
	return m.stdout
}

func (m *machine) writeTo(w Value, s string) Value {
	if w == nil {
		throw("invalid memory address or nil pointer dereference")
	}
	iv := w.(iface)
	if box, ok := iv.v.(*structVal); ok && box != nil && len(box.f) > 0 {
		switch st := box.f[0].(type) {
		case *osFile:
			m.write(m.stream(st.fd), s)
			return tuple{len(s), nil}
		case *strings.Builder:
			m.alloc(int64(len(s)))
			st.WriteString(s)
			return tuple{len(s), nil}
		}
	}
	return m.callMethod(iv, "Write", []Value{byteValues(m, []byte(s))})
}

func errValue(m *machine, err error) Value {
	if err == nil {
		return nil
	}
	return iface{t: m.prog.env.hostErr, v: err}
}

func nativeBox(m *machine, pkg, name string, state any) *structVal {
	t := m.prog.env.pkgs[pkg].Scope().Lookup(name).Type()
	box := m.prog.structInfo(t).newZero()
	box.f[0] = state
	return box
}

func stdIO() *Package {
	return NewPackage("io").Source(ioSource).
		Native("WriteString", func(m *machine, _ []types.Type, a []Value) Value { return m.writeTo(a[0], a[1].(string)) }).
		nativeVar("EOF", func(m *machine) Value { return errValue(m, io.EOF) })
}

func stdOs() *Package {
	write := func(m *machine, recv Value, s string) Value {
		st := nativeState[osFile](recv)
		m.write(m.stream(st.fd), s)
		return tuple{len(s), nil}
	}
	return NewPackage("os").Source(osSource).
		Native("File.Write", func(m *machine, _ []types.Type, a []Value) Value { return write(m, a[0], string(bytesOf(a[1]))) }).
		Native("File.WriteString", func(m *machine, _ []types.Type, a []Value) Value { return write(m, a[0], a[1].(string)) }).
		nativeVar("Stdout", func(m *machine) Value { return nativeBox(m, "os", "File", &osFile{fd: 1}) }).
		nativeVar("Stderr", func(m *machine) Value { return nativeBox(m, "os", "File", &osFile{fd: 2}) }).
		Native("Exit", func(m *machine, _ []types.Type, a []Value) Value {
			m.stop(&ExitError{Code: a[0].(int)})
			return nil
		})
}

func stdFmt() *Package {
	fprint := func(format func(m *machine, a []Value) string) nativeImpl {
		return func(m *machine, _ []types.Type, a []Value) Value { return m.writeTo(a[0], format(m, a[1:])) }
	}
	return NewPackage("fmt").Source(fmtSource).
		Func("Print", func(ctx context.Context, a ...any) (int, error) {
			s := fmt.Sprint(a...)
			stdout(ctx, s)
			return len(s), nil
		}).
		Func("Println", func(ctx context.Context, a ...any) (int, error) {
			s := fmt.Sprintln(a...)
			stdout(ctx, s)
			return len(s), nil
		}).
		Func("Printf", func(ctx context.Context, format string, a ...any) (int, error) {
			Charge(ctx, paddingBound(format, a))
			s := fmt.Sprintf(format, a...)
			stdout(ctx, s)
			return len(s), nil
		}).
		Func("Sprint", func(a ...any) string { return fmt.Sprint(a...) }).
		Func("Sprintln", func(a ...any) string { return fmt.Sprintln(a...) }).
		Func("Sprintf", func(ctx context.Context, format string, a ...any) string {
			Charge(ctx, paddingBound(format, a))
			return fmt.Sprintf(format, a...)
		}).
		Func("Errorf", func(ctx context.Context, format string, a ...any) error {
			Charge(ctx, paddingBound(format, a))
			return fmt.Errorf(format, a...)
		}).
		Native("Fprint", fprint(func(m *machine, a []Value) string { return fmt.Sprint(goArgs(m, a[0])...) })).
		Native("Fprintln", fprint(func(m *machine, a []Value) string { return fmt.Sprintln(goArgs(m, a[0])...) })).
		Native("Fprintf", fprint(func(m *machine, a []Value) string {
			args := goArgs(m, a[1])
			m.alloc(paddingBound(a[0].(string), args))
			return fmt.Sprintf(a[0].(string), args...)
		}))
}

func stringSeq(next func(yield func(string) bool)) *nativeFn {
	return seqOf(func(m *machine, yield func(...Value) bool) {
		next(func(s string) bool { return yield(s) })
	})
}

func stdStrings() *Package {
	builder := func(f func(m *machine, b *strings.Builder, a []Value) Value) nativeImpl {
		return func(m *machine, _ []types.Type, a []Value) Value {
			return f(m, nativeState[strings.Builder](a[0]), a[1:])
		}
	}
	grow := func(ctx context.Context, s, old, new string, n int) {
		if extra := len(new) - len(old); extra > 0 {
			count := len(s) + 1
			if old != "" {
				count = strings.Count(s, old)
			}
			if n >= 0 {
				count = min(count, n)
			}
			chargeProduct(ctx, count, extra)
		}
	}
	return NewPackage("strings").Source(stringsSource).
		Func("Compare", strings.Compare).
		Func("Contains", strings.Contains).
		Func("ContainsAny", strings.ContainsAny).
		Func("ContainsRune", strings.ContainsRune).
		Func("Count", strings.Count).
		Func("Cut", strings.Cut).
		Func("CutPrefix", strings.CutPrefix).
		Func("CutSuffix", strings.CutSuffix).
		Func("EqualFold", strings.EqualFold).
		Func("Fields", strings.Fields).
		Func("HasPrefix", strings.HasPrefix).
		Func("HasSuffix", strings.HasSuffix).
		Func("Index", strings.Index).
		Func("IndexByte", strings.IndexByte).
		Func("IndexRune", strings.IndexRune).
		Func("Join", strings.Join).
		Func("LastIndex", strings.LastIndex).
		Func("Split", strings.Split).
		Func("SplitN", strings.SplitN).
		Func("ToLower", strings.ToLower).
		Func("ToUpper", strings.ToUpper).
		Func("Trim", strings.Trim).
		Func("TrimLeft", strings.TrimLeft).
		Func("TrimPrefix", strings.TrimPrefix).
		Func("TrimRight", strings.TrimRight).
		Func("TrimSpace", strings.TrimSpace).
		Func("TrimSuffix", strings.TrimSuffix).
		Func("Repeat", func(ctx context.Context, s string, count int) string {
			if count < 0 {
				panic("strings: negative Repeat count")
			}
			chargeProduct(ctx, len(s), count)
			return strings.Repeat(s, count)
		}).
		Func("Replace", func(ctx context.Context, s, old, new string, n int) string {
			grow(ctx, s, old, new, n)
			return strings.Replace(s, old, new, n)
		}).
		Func("ReplaceAll", func(ctx context.Context, s, old, new string) string {
			grow(ctx, s, old, new, -1)
			return strings.ReplaceAll(s, old, new)
		}).
		Native("Lines", func(m *machine, _ []types.Type, a []Value) Value {
			s := a[0].(string)
			return stringSeq(func(yield func(string) bool) {
				for line := range strings.Lines(s) {
					if !yield(line) {
						return
					}
				}
			})
		}).
		Native("SplitSeq", func(m *machine, _ []types.Type, a []Value) Value {
			s, sep := a[0].(string), a[1].(string)
			return stringSeq(func(yield func(string) bool) {
				for part := range strings.SplitSeq(s, sep) {
					if !yield(part) {
						return
					}
				}
			})
		}).
		Native("FieldsSeq", func(m *machine, _ []types.Type, a []Value) Value {
			s := a[0].(string)
			return stringSeq(func(yield func(string) bool) {
				for f := range strings.FieldsSeq(s) {
					if !yield(f) {
						return
					}
				}
			})
		}).
		Native("Builder.Grow", builder(func(m *machine, b *strings.Builder, a []Value) Value {
			if n := a[0].(int); n < 0 {
				throw("strings.Builder.Grow: negative count")
			}
			return nil
		})).
		Native("Builder.Len", builder(func(m *machine, b *strings.Builder, a []Value) Value { return b.Len() })).
		Native("Builder.Reset", builder(func(m *machine, b *strings.Builder, a []Value) Value {
			b.Reset()
			return nil
		})).
		Native("Builder.String", builder(func(m *machine, b *strings.Builder, a []Value) Value { return b.String() })).
		Native("Builder.Write", builder(func(m *machine, b *strings.Builder, a []Value) Value {
			p := bytesOf(a[0])
			m.alloc(int64(len(p)))
			b.Write(p)
			return tuple{len(p), nil}
		})).
		Native("Builder.WriteByte", builder(func(m *machine, b *strings.Builder, a []Value) Value {
			m.alloc(1)
			b.WriteByte(a[0].(byte))
			return nil
		})).
		Native("Builder.WriteRune", builder(func(m *machine, b *strings.Builder, a []Value) Value {
			r := a[0].(rune)
			m.alloc(int64(utf8.RuneLen(r)))
			n, _ := b.WriteRune(r)
			return tuple{n, nil}
		})).
		Native("Builder.WriteString", builder(func(m *machine, b *strings.Builder, a []Value) Value {
			s := a[0].(string)
			m.alloc(int64(len(s)))
			b.WriteString(s)
			return tuple{len(s), nil}
		}))
}

func stdJSON() *Package {
	marshal := func(m *machine, v Value) ([]byte, error) {
		var b bytes.Buffer
		if err := m.encodeJSON(&b, anyType, v, 0); err != nil {
			return nil, err
		}
		m.alloc(int64(b.Len()))
		return b.Bytes(), nil
	}
	result := func(m *machine, out []byte, err error) Value {
		if err != nil {
			return tuple{[]Value(nil), errValue(m, err)}
		}
		return tuple{byteValues(m, out), nil}
	}
	return NewPackage("encoding/json").Source(jsonSource).
		Native("Marshal", func(m *machine, _ []types.Type, a []Value) Value {
			out, err := marshal(m, a[0])
			return result(m, out, err)
		}).
		Native("MarshalIndent", func(m *machine, _ []types.Type, a []Value) Value {
			out, err := marshal(m, a[0])
			if err != nil {
				return result(m, nil, err)
			}
			var b bytes.Buffer
			if err := json.Indent(&b, out, a[1].(string), a[2].(string)); err != nil {
				return result(m, nil, err)
			}
			return result(m, b.Bytes(), nil)
		}).
		Native("Unmarshal", func(m *machine, _ []types.Type, a []Value) Value {
			return errValue(m, m.unmarshalJSON(bytesOf(a[0]), a[1]))
		}).
		Native("Valid", func(m *machine, _ []types.Type, a []Value) Value { return json.Valid(bytesOf(a[0])) })
}

const maxJSONDepth = 64

func jsonName(field *types.Var, tag string) (name string, omitEmpty, skip bool) {
	name = field.Name()
	value, ok := reflect.StructTag(tag).Lookup("json")
	if !ok {
		return name, false, false
	}
	if value == "-" {
		return "", false, true
	}
	parts := strings.Split(value, ",")
	if parts[0] != "" {
		name = parts[0]
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitEmpty = true
		}
	}
	return name, omitEmpty, false
}

func emptyJSON(t types.Type, v Value) bool {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return v == basicZero(u.Kind())
	case *types.Slice:
		return len(v.([]Value)) == 0
	case *types.Map:
		return v.(*mapVal).len() == 0
	case *types.Pointer, *types.Interface, *types.Signature, *types.Chan:
		return isNilPointer(v) || v == nil
	case *types.Array:
		return u.Len() == 0
	}
	return false
}

func writeJSONString(b *bytes.Buffer, s string) {
	out, _ := json.Marshal(s)
	b.Write(out)
}

func (m *machine) encodeJSON(b *bytes.Buffer, t types.Type, v Value, depth int) error {
	if depth > maxJSONDepth {
		return errors.New("json: value nested too deeply")
	}
	m.burn(1)
	switch u := t.Underlying().(type) {
	case *types.Interface:
		if v == nil {
			b.WriteString("null")
			return nil
		}
		iv := v.(iface)
		if gerr, ok := iv.v.(error); ok && iv.t == m.prog.env.hostErr {
			_ = gerr
			b.WriteString("{}")
			return nil
		}
		return m.encodeJSON(b, iv.t, iv.v, depth+1)
	case *types.Basic:
		switch {
		case u.Info()&types.IsString != 0:
			writeJSONString(b, v.(string))
		case u.Info()&types.IsBoolean != 0:
			b.WriteString(strconv.FormatBool(v.(bool)))
		case u.Info()&types.IsFloat != 0:
			out, err := json.Marshal(toFloat64(v))
			if err != nil {
				return err
			}
			b.Write(out)
		case u.Info()&types.IsUnsigned != 0:
			b.WriteString(strconv.FormatUint(toUint64(v), 10))
		default:
			b.WriteString(strconv.FormatInt(toInt64(v), 10))
		}
	case *types.Pointer:
		if isNilPointer(v) {
			b.WriteString("null")
			return nil
		}
		if p, ok := v.(*Value); ok {
			return m.encodeJSON(b, u.Elem(), *p, depth+1)
		}
		return m.encodeJSON(b, u.Elem(), v, depth+1)
	case *types.Struct:
		b.WriteByte('{')
		first := true
		if err := m.encodeFields(b, u, v.(*structVal), &first, depth); err != nil {
			return err
		}
		b.WriteByte('}')
	case *types.Slice:
		list := v.([]Value)
		if list == nil {
			b.WriteString("null")
			return nil
		}
		if eb, ok := u.Elem().Underlying().(*types.Basic); ok && eb.Kind() == types.Uint8 {
			writeJSONString(b, base64.StdEncoding.EncodeToString(bytesOf(list)))
			return nil
		}
		return m.encodeList(b, u.Elem(), list, depth)
	case *types.Array:
		return m.encodeList(b, u.Elem(), v.(*structVal).f, depth)
	case *types.Map:
		mv := v.(*mapVal)
		if mv == nil {
			b.WriteString("null")
			return nil
		}
		type entry struct {
			key string
			val Value
		}
		var entries []entry
		for _, kv := range mv.entries() {
			var key string
			switch k := kv[0].(type) {
			case string:
				key = k
			default:
				if _, ok := u.Key().Underlying().(*types.Basic); !ok {
					return fmt.Errorf("json: unsupported map key type %s", u.Key())
				}
				key = fmt.Sprint(k)
			}
			entries = append(entries, entry{key, kv[1]})
		}
		slicesSortByKey(entries, func(e entry) string { return e.key })
		b.WriteByte('{')
		for i, e := range entries {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, e.key)
			b.WriteByte(':')
			if err := m.encodeJSON(b, u.Elem(), e.val, depth+1); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("json: unsupported type: %s", t)
	}
	return nil
}

func (m *machine) encodeList(b *bytes.Buffer, elem types.Type, list []Value, depth int) error {
	b.WriteByte('[')
	for i, e := range list {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := m.encodeJSON(b, elem, e, depth+1); err != nil {
			return err
		}
	}
	b.WriteByte(']')
	return nil
}

func (m *machine) encodeFields(b *bytes.Buffer, u *types.Struct, sv *structVal, first *bool, depth int) error {
	for i := range u.NumFields() {
		f := u.Field(i)
		name, omitEmpty, skip := jsonName(f, u.Tag(i))
		_, tagged := reflect.StructTag(u.Tag(i)).Lookup("json")
		if f.Embedded() && !tagged {
			inner := f.Type()
			val := sv.f[i]
			if pointsToAggregate(inner) {
				if isNilPointer(val) {
					continue
				}
				inner = derefType(inner)
			}
			if st, ok := inner.Underlying().(*types.Struct); ok {
				if err := m.encodeFields(b, st, val.(*structVal), first, depth+1); err != nil {
					return err
				}
				continue
			}
		}
		if skip || !f.Exported() || (omitEmpty && emptyJSON(f.Type(), sv.f[i])) {
			continue
		}
		if !*first {
			b.WriteByte(',')
		}
		*first = false
		writeJSONString(b, name)
		b.WriteByte(':')
		if err := m.encodeJSON(b, f.Type(), sv.f[i], depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (m *machine) unmarshalJSON(data []byte, target Value) error {
	if target == nil {
		return errors.New("json: Unmarshal(nil)")
	}
	iv := target.(iface)
	pt, ok := iv.t.Underlying().(*types.Pointer)
	if !ok || isNilPointer(iv.v) {
		return fmt.Errorf("json: Unmarshal(non-pointer %s)", iv.t)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	m.burn(int64(len(data) / 16))
	if p, ok := iv.v.(*Value); ok {
		v, err := m.fromJSON(raw, pt.Elem(), *p)
		if err != nil {
			return err
		}
		*p = v
		return nil
	}
	_, err := m.fromJSON(raw, pt.Elem(), iv.v)
	return err
}

func (m *machine) fromJSON(raw any, t types.Type, current Value) (Value, error) {
	mismatch := func() error {
		kind := "value"
		switch raw.(type) {
		case string:
			kind = "string"
		case json.Number:
			kind = "number"
		case bool:
			kind = "bool"
		case []any:
			kind = "array"
		case map[string]any:
			kind = "object"
		}
		return fmt.Errorf("json: cannot unmarshal %s into Go value of type %s", kind, t)
	}
	if raw == nil {
		switch t.Underlying().(type) {
		case *types.Basic, *types.Struct, *types.Array:
			return current, nil
		}
		return m.prog.zero(t)(), nil
	}
	switch u := t.Underlying().(type) {
	case *types.Interface:
		return m.jsonAny(raw), nil
	case *types.Basic:
		switch r := raw.(type) {
		case string:
			if u.Info()&types.IsString != 0 {
				m.alloc(int64(len(r)))
				return r, nil
			}
		case bool:
			if u.Info()&types.IsBoolean != 0 {
				return r, nil
			}
		case json.Number:
			if u.Info()&types.IsFloat != 0 {
				f, err := r.Float64()
				return convertFloat(f, u.Kind()), err
			}
			if u.Info()&types.IsInteger != 0 {
				i, err := r.Int64()
				if err != nil {
					return nil, fmt.Errorf("json: cannot unmarshal number %s into Go value of type %s", r, t)
				}
				return convertBasic(i, u.Kind()), nil
			}
		}
		return nil, mismatch()
	case *types.Pointer:
		elem := u.Elem()
		if isAggregate(elem) {
			box, _ := current.(*structVal)
			if box == nil {
				box = m.prog.zero(elem)().(*structVal)
			}
			_, err := m.fromJSON(raw, elem, box)
			return box, err
		}
		v, err := m.fromJSON(raw, elem, m.prog.zero(elem)())
		if err != nil {
			return nil, err
		}
		c := &cell{v}
		return &c.v, nil
	case *types.Struct:
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, mismatch()
		}
		box := current.(*structVal)
		for key, val := range obj {
			for i := range u.NumFields() {
				f := u.Field(i)
				name, _, skip := jsonName(f, u.Tag(i))
				if skip || !f.Exported() || !strings.EqualFold(name, key) {
					continue
				}
				v, err := m.fromJSON(val, f.Type(), box.f[i])
				if err != nil {
					return nil, err
				}
				if isAggregate(f.Type()) {
					assignAgg(box.f[i].(*structVal), v.(*structVal))
				} else {
					box.f[i] = v
				}
				break
			}
		}
		return box, nil
	case *types.Slice:
		list, ok := raw.([]any)
		if !ok {
			if s, isStr := raw.(string); isStr {
				if eb, isByte := u.Elem().Underlying().(*types.Basic); isByte && eb.Kind() == types.Uint8 {
					decoded, err := base64.StdEncoding.DecodeString(s)
					if err != nil {
						return nil, err
					}
					return byteValues(m, decoded), nil
				}
			}
			return nil, mismatch()
		}
		m.alloc(headerBytes + slotBytes*int64(len(list)))
		out := make([]Value, len(list))
		zero := m.prog.zero(u.Elem())
		for i, e := range list {
			v, err := m.fromJSON(e, u.Elem(), zero())
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case *types.Map:
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, mismatch()
		}
		mv, _ := current.(*mapVal)
		if mv == nil {
			mv = newMap(u.Key(), len(obj))
		}
		zero := m.prog.zero(u.Elem())
		for key, val := range obj {
			v, err := m.fromJSON(val, u.Elem(), zero())
			if err != nil {
				return nil, err
			}
			k, err := m.fromJSON(json.Number(key), u.Key(), nil)
			if b, isBasic := u.Key().Underlying().(*types.Basic); isBasic && b.Info()&types.IsString != 0 {
				k, err = key, nil
			}
			if err != nil {
				return nil, err
			}
			mv.set(m, k, v)
		}
		return mv, nil
	}
	return nil, mismatch()
}

func (m *machine) jsonAny(raw any) Value {
	switch r := raw.(type) {
	case nil:
		return nil
	case string:
		return iface{t: types.Typ[types.String], v: r}
	case bool:
		return iface{t: types.Typ[types.Bool], v: r}
	case json.Number:
		f, _ := r.Float64()
		return iface{t: types.Typ[types.Float64], v: f}
	case []any:
		out := make([]Value, len(r))
		for i, e := range r {
			out[i] = m.jsonAny(e)
		}
		return iface{t: types.NewSlice(anyType), v: out}
	case map[string]any:
		mv := newMap(types.Typ[types.String], len(r))
		for k, e := range r {
			mv.set(m, k, m.jsonAny(e))
		}
		return iface{t: types.NewMap(types.Typ[types.String], anyType), v: mv}
	}
	return nil
}

func slicesSortByKey[T any](list []T, key func(T) string) {
	sort.SliceStable(list, func(i, j int) bool { return key(list[i]) < key(list[j]) })
}
