package tendril

import (
	"context"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"math"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func Std() []*Package {
	return []*Package{stdStrings(), stdStrconv(), stdFmt(), stdErrors(), stdRegexp(), stdPath(), stdMath(), stdSlices(), stdSync(), stdErrgroup()}
}

func chargeProduct(ctx context.Context, a, b int) {
	if a > 0 && b > math.MaxInt64/a {
		Charge(ctx, -1)
	}
	Charge(ctx, int64(a)*int64(b))
}

func stdStrings() *Package {
	return NewPackage("strings").
		Func("Contains", strings.Contains).
		Func("ContainsAny", strings.ContainsAny).
		Func("ContainsRune", strings.ContainsRune).
		Func("Count", strings.Count).
		Func("Cut", strings.Cut).
		Func("EqualFold", strings.EqualFold).
		Func("Fields", strings.Fields).
		Func("HasPrefix", strings.HasPrefix).
		Func("HasSuffix", strings.HasSuffix).
		Func("Index", strings.Index).
		Func("Join", strings.Join).
		Func("LastIndex", strings.LastIndex).
		Func("Split", strings.Split).
		Func("SplitN", strings.SplitN).
		Func("ToLower", strings.ToLower).
		Func("ToUpper", strings.ToUpper).
		Func("Trim", strings.Trim).
		Func("TrimPrefix", strings.TrimPrefix).
		Func("TrimSpace", strings.TrimSpace).
		Func("TrimSuffix", strings.TrimSuffix).
		Func("Repeat", func(ctx context.Context, s string, count int) string {
			if count < 0 {
				panic("strings: negative Repeat count")
			}
			chargeProduct(ctx, len(s), count)
			return strings.Repeat(s, count)
		}).
		Func("ReplaceAll", func(ctx context.Context, s, old, new string) string {
			if grow := len(new) - len(old); grow > 0 {
				n := len(s) + 1
				if old != "" {
					n = strings.Count(s, old)
				}
				chargeProduct(ctx, n, grow)
			}
			return strings.ReplaceAll(s, old, new)
		})
}

func stdStrconv() *Package {
	return NewPackage("strconv").
		Func("Itoa", strconv.Itoa).
		Func("Atoi", strconv.Atoi).
		Func("ParseBool", strconv.ParseBool).
		Func("ParseFloat", strconv.ParseFloat).
		Func("ParseInt", strconv.ParseInt).
		Func("FormatInt", strconv.FormatInt).
		Func("Quote", strconv.Quote)
}

func stdFmt() *Package {
	return NewPackage("fmt").
		Func("Sprint", func(ctx context.Context, a ...any) string { return fmt.Sprint(a...) }).
		Func("Sprintf", func(ctx context.Context, format string, a ...any) string {
			Charge(ctx, paddingBound(format, a))
			return fmt.Sprintf(format, a...)
		}).
		Func("Errorf", func(ctx context.Context, format string, a ...any) error {
			Charge(ctx, paddingBound(format, a))
			return fmt.Errorf(format, a...)
		})
}

func paddingBound(format string, args []any) int64 {
	var total int64
	arg := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		for i++; i < len(format); i++ {
			c := format[i]
			switch {
			case c == '*':
				if arg < len(args) {
					if n, ok := args[arg].(int); ok {
						total += int64(min(max(n, -n), 1e6))
					}
				}
				arg++
			case c >= '0' && c <= '9':
				n := 0
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					n = min(n*10+int(format[i]-'0'), 1e6)
					i++
				}
				i--
				total += int64(n)
			case strings.IndexByte("+-# .[]", c) >= 0:
			default:
				if c != '%' {
					arg++
				}
				goto next
			}
		}
	next:
	}
	return total
}

func stdErrors() *Package {
	return NewPackage("errors").
		Func("New", errors.New).
		Func("Is", errors.Is)
}

func stdRegexp() *Package {
	return NewPackage("regexp").
		Func("MatchString", func(ctx context.Context, pattern, s string) (bool, error) {
			Burn(ctx, int64(len(pattern)*len(s)/64))
			return regexp.MatchString(pattern, s)
		}).
		Func("QuoteMeta", regexp.QuoteMeta)
}

func stdPath() *Package {
	return NewPackage("path").
		Func("Match", func(ctx context.Context, pattern, name string) (bool, error) {
			Burn(ctx, int64(len(pattern)*len(name)/16))
			return path.Match(pattern, name)
		}).
		Func("Base", path.Base).
		Func("Dir", path.Dir).
		Func("Ext", path.Ext).
		Func("Join", path.Join)
}

func stdMath() *Package {
	return NewPackage("math").
		Func("Abs", math.Abs).
		Func("Ceil", math.Ceil).
		Func("Floor", math.Floor).
		Func("Round", math.Round).
		Func("Sqrt", math.Sqrt).
		Func("Pow", math.Pow).
		Const("MaxInt", math.MaxInt).
		Const("MinInt", math.MinInt)
}

func orderedConstraint() *types.Interface {
	var terms []*types.Term
	for _, k := range []types.BasicKind{
		types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr,
		types.Float32, types.Float64, types.String,
	} {
		terms = append(terms, types.NewTerm(true, types.Typ[k]))
	}
	return types.NewInterfaceType(nil, []types.Type{types.NewUnion(terms)}).Complete()
}

func genericSliceFunc(constraint func() types.Type, extra []func(e types.Type) types.Type, result types.Type) func(*types.Package) *types.Signature {
	return func(pkg *types.Package) *types.Signature {
		e := types.NewTypeParam(types.NewTypeName(token.NoPos, pkg, "E", nil), constraint())
		params := []*types.Var{types.NewParam(token.NoPos, pkg, "s", types.NewSlice(e))}
		for _, x := range extra {
			params = append(params, types.NewParam(token.NoPos, pkg, "v", x(e)))
		}
		var results *types.Tuple
		if result != nil {
			results = types.NewTuple(types.NewParam(token.NoPos, pkg, "", result))
		}
		return types.NewSignatureType(nil, nil, []*types.TypeParam{e}, types.NewTuple(params...), results, false)
	}
}

func stdSlices() *Package {
	ordered := func() types.Type { return orderedConstraint() }
	comparable := func() types.Type { return types.Universe.Lookup("comparable").Type() }
	elem := func(e types.Type) types.Type { return e }
	index := func(m *machine, typeArgs []types.Type, args []Value) int {
		eq := equalFn(typeArgs[0])
		s := args[0].([]Value)
		m.burn(int64(len(s) / 8))
		for i, v := range s {
			if eq(v, args[1]) {
				return i
			}
		}
		return -1
	}
	return NewPackage("slices").
		nativeFunc("Sort", genericSliceFunc(ordered, nil, nil), func(m *machine, _ []types.Type, args []Value) Value {
			s := args[0].([]Value)
			m.burn(int64(len(s)) * int64(1+bitsLen(len(s))) / 4)
			slices.SortStableFunc(s, compareKeys)
			return nil
		}).
		nativeFunc("Contains", genericSliceFunc(comparable, []func(types.Type) types.Type{elem}, types.Typ[types.Bool]),
			func(m *machine, typeArgs []types.Type, args []Value) Value { return index(m, typeArgs, args) >= 0 }).
		nativeFunc("Index", genericSliceFunc(comparable, []func(types.Type) types.Type{elem}, types.Typ[types.Int]),
			func(m *machine, typeArgs []types.Type, args []Value) Value { return index(m, typeArgs, args) })
}

func bitsLen(n int) int {
	b := 0
	for ; n > 0; n >>= 1 {
		b++
	}
	return b
}
