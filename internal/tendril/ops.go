package tendril

import (
	"cmp"
	"go/token"
	"go/types"
)

type binop func(m *machine, a, b Value) Value

type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

type float interface{ ~float32 | ~float64 }

func intArith[T integer](op token.Token) binop {
	switch op {
	case token.ADD:
		return func(_ *machine, a, b Value) Value { return a.(T) + b.(T) }
	case token.SUB:
		return func(_ *machine, a, b Value) Value { return a.(T) - b.(T) }
	case token.MUL:
		return func(_ *machine, a, b Value) Value { return a.(T) * b.(T) }
	case token.QUO:
		return func(_ *machine, a, b Value) Value { return a.(T) / b.(T) }
	case token.REM:
		return func(_ *machine, a, b Value) Value { return a.(T) % b.(T) }
	case token.AND:
		return func(_ *machine, a, b Value) Value { return a.(T) & b.(T) }
	case token.OR:
		return func(_ *machine, a, b Value) Value { return a.(T) | b.(T) }
	case token.XOR:
		return func(_ *machine, a, b Value) Value { return a.(T) ^ b.(T) }
	case token.AND_NOT:
		return func(_ *machine, a, b Value) Value { return a.(T) &^ b.(T) }
	}
	return nil
}

func floatArith[T float](op token.Token) binop {
	switch op {
	case token.ADD:
		return func(_ *machine, a, b Value) Value { return a.(T) + b.(T) }
	case token.SUB:
		return func(_ *machine, a, b Value) Value { return a.(T) - b.(T) }
	case token.MUL:
		return func(_ *machine, a, b Value) Value { return a.(T) * b.(T) }
	case token.QUO:
		return func(_ *machine, a, b Value) Value { return a.(T) / b.(T) }
	}
	return nil
}

func concat(m *machine, a, b Value) Value {
	x, y := a.(string), b.(string)
	m.alloc(int64(len(x) + len(y)))
	return x + y
}

func ordered[T cmp.Ordered](op token.Token) binop {
	switch op {
	case token.LSS:
		return func(_ *machine, a, b Value) Value { return a.(T) < b.(T) }
	case token.LEQ:
		return func(_ *machine, a, b Value) Value { return a.(T) <= b.(T) }
	case token.GTR:
		return func(_ *machine, a, b Value) Value { return a.(T) > b.(T) }
	case token.GEQ:
		return func(_ *machine, a, b Value) Value { return a.(T) >= b.(T) }
	}
	return nil
}

func shift[T integer](op token.Token) binop {
	if op == token.SHL {
		return func(_ *machine, a, b Value) Value { return a.(T) << shiftCount(b) }
	}
	return func(_ *machine, a, b Value) Value { return a.(T) >> shiftCount(b) }
}

func shiftCount(v Value) uint64 {
	switch v.(type) {
	case uint, uint8, uint16, uint32, uint64, uintptr:
		return toUint64(v)
	}
	n := toInt64(v)
	if n < 0 {
		throw("negative shift amount")
	}
	return uint64(n)
}

func arithOp(op token.Token, k types.BasicKind) binop {
	switch k {
	case types.Int:
		return intArith[int](op)
	case types.Int8:
		return intArith[int8](op)
	case types.Int16:
		return intArith[int16](op)
	case types.Int32:
		return intArith[int32](op)
	case types.Int64:
		return intArith[int64](op)
	case types.Uint:
		return intArith[uint](op)
	case types.Uint8:
		return intArith[uint8](op)
	case types.Uint16:
		return intArith[uint16](op)
	case types.Uint32:
		return intArith[uint32](op)
	case types.Uint64:
		return intArith[uint64](op)
	case types.Uintptr:
		return intArith[uintptr](op)
	case types.Float32:
		return floatArith[float32](op)
	case types.Float64:
		return floatArith[float64](op)
	case types.String:
		if op == token.ADD {
			return concat
		}
	}
	return nil
}

func compareOp(op token.Token, k types.BasicKind) binop {
	switch k {
	case types.Int:
		return ordered[int](op)
	case types.Int8:
		return ordered[int8](op)
	case types.Int16:
		return ordered[int16](op)
	case types.Int32:
		return ordered[int32](op)
	case types.Int64:
		return ordered[int64](op)
	case types.Uint:
		return ordered[uint](op)
	case types.Uint8:
		return ordered[uint8](op)
	case types.Uint16:
		return ordered[uint16](op)
	case types.Uint32:
		return ordered[uint32](op)
	case types.Uint64:
		return ordered[uint64](op)
	case types.Uintptr:
		return ordered[uintptr](op)
	case types.Float32:
		return ordered[float32](op)
	case types.Float64:
		return ordered[float64](op)
	case types.String:
		return ordered[string](op)
	}
	return nil
}

func shiftOp(op token.Token, k types.BasicKind) binop {
	switch k {
	case types.Int:
		return shift[int](op)
	case types.Int8:
		return shift[int8](op)
	case types.Int16:
		return shift[int16](op)
	case types.Int32:
		return shift[int32](op)
	case types.Int64:
		return shift[int64](op)
	case types.Uint:
		return shift[uint](op)
	case types.Uint8:
		return shift[uint8](op)
	case types.Uint16:
		return shift[uint16](op)
	case types.Uint32:
		return shift[uint32](op)
	case types.Uint64:
		return shift[uint64](op)
	case types.Uintptr:
		return shift[uintptr](op)
	}
	return nil
}

func neg[T integer | float](v Value) Value { return -v.(T) }

func complement[T integer](v Value) Value { return ^v.(T) }

func unaryOp(op token.Token, k types.BasicKind) func(Value) Value {
	if op == token.XOR {
		switch k {
		case types.Int:
			return complement[int]
		case types.Int8:
			return complement[int8]
		case types.Int16:
			return complement[int16]
		case types.Int32:
			return complement[int32]
		case types.Int64:
			return complement[int64]
		case types.Uint:
			return complement[uint]
		case types.Uint8:
			return complement[uint8]
		case types.Uint16:
			return complement[uint16]
		case types.Uint32:
			return complement[uint32]
		case types.Uint64:
			return complement[uint64]
		case types.Uintptr:
			return complement[uintptr]
		}
	}
	switch k {
	case types.Int:
		return neg[int]
	case types.Int8:
		return neg[int8]
	case types.Int16:
		return neg[int16]
	case types.Int32:
		return neg[int32]
	case types.Int64:
		return neg[int64]
	case types.Uint:
		return neg[uint]
	case types.Uint8:
		return neg[uint8]
	case types.Uint16:
		return neg[uint16]
	case types.Uint32:
		return neg[uint32]
	case types.Uint64:
		return neg[uint64]
	case types.Uintptr:
		return neg[uintptr]
	case types.Float32:
		return neg[float32]
	case types.Float64:
		return neg[float64]
	}
	return nil
}

func isFloatKind(k types.BasicKind) bool {
	return k == types.Float32 || k == types.Float64 || k == types.UntypedFloat
}

func isUnsignedKind(k types.BasicKind) bool {
	switch k {
	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
		return true
	}
	return false
}

func numericConv(from, to types.BasicKind) func(Value) Value {
	switch {
	case from == to:
		return func(v Value) Value { return v }
	case isFloatKind(from):
		return func(v Value) Value { return convertFloat(toFloat64(v), to) }
	case isUnsignedKind(from):
		return func(v Value) Value { return convertUnsigned(toUint64(v), to) }
	}
	return func(v Value) Value { return convertBasic(toInt64(v), to) }
}

func convertBasic(i int64, k types.BasicKind) Value {
	switch k {
	case types.Int, types.UntypedInt:
		return int(i)
	case types.Int8:
		return int8(i)
	case types.Int16:
		return int16(i)
	case types.Int32, types.UntypedRune:
		return int32(i)
	case types.Int64:
		return i
	case types.Uint:
		return uint(i)
	case types.Uint8:
		return uint8(i)
	case types.Uint16:
		return uint16(i)
	case types.Uint32:
		return uint32(i)
	case types.Uint64:
		return uint64(i)
	case types.Uintptr:
		return uintptr(i)
	case types.Float32:
		return float32(i)
	case types.Float64, types.UntypedFloat:
		return float64(i)
	}
	panic("convertBasic: unsupported kind")
}

func convertUnsigned(u uint64, k types.BasicKind) Value {
	switch k {
	case types.Float32:
		return float32(u)
	case types.Float64:
		return float64(u)
	case types.Uint64:
		return u
	}
	return convertBasic(int64(u), k)
}

func convertFloat(f float64, k types.BasicKind) Value {
	switch k {
	case types.Float32:
		return float32(f)
	case types.Float64, types.UntypedFloat:
		return f
	case types.Uint64:
		return uint64(f)
	case types.Uint, types.Uintptr:
		return convertUnsigned(uint64(f), k)
	}
	return convertBasic(int64(f), k)
}

func toInt64(v Value) int64 {
	switch v := v.(type) {
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint:
		return int64(v)
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		return int64(v)
	case uintptr:
		return int64(v)
	case float32:
		return int64(v)
	case float64:
		return int64(v)
	}
	panic("toInt64: not a number")
}

func toUint64(v Value) uint64 {
	switch v := v.(type) {
	case uint:
		return uint64(v)
	case uint8:
		return uint64(v)
	case uint16:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	case uintptr:
		return uint64(v)
	}
	return uint64(toInt64(v))
}

func toFloat64(v Value) float64 {
	switch v := v.(type) {
	case float32:
		return float64(v)
	case float64:
		return v
	}
	if isUnsigned(v) {
		return float64(toUint64(v))
	}
	return float64(toInt64(v))
}

func isUnsigned(v Value) bool {
	switch v.(type) {
	case uint, uint8, uint16, uint32, uint64, uintptr:
		return true
	}
	return false
}
