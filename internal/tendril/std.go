package tendril

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func Std() []*Package {
	return []*Package{stdStrings(), stdStrconv(), stdFmt(), stdErrors(), stdRegexp(), stdPath(), stdMath(), stdSlices(), stdSync(), stdErrgroup(), stdLog(), stdOs(), stdTime(), stdSort(), stdCmp(), stdIter(), stdMaps(), stdIO(), stdJSON()}
}

func chargeProduct(ctx context.Context, a, b int) {
	if a > 0 && b > math.MaxInt64/a {
		Charge(ctx, -1)
	}
	Charge(ctx, int64(a)*int64(b))
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

func stdout(ctx context.Context, s string) {
	m := machineFrom(ctx)
	m.write(m.stdout, s)
}

func stderr(ctx context.Context, s string) {
	m := machineFrom(ctx)
	m.write(m.stderr, s)
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

func bitsLen(n int) int {
	b := 0
	for ; n > 0; n >>= 1 {
		b++
	}
	return b
}

func exit(ctx context.Context, code int) {
	machineFrom(ctx).stop(&ExitError{Code: code})
}

func stdLog() *Package {
	line := func(s string) string {
		if strings.HasSuffix(s, "\n") {
			return s
		}
		return s + "\n"
	}
	return NewPackage("log").
		Func("Print", func(ctx context.Context, a ...any) { stderr(ctx, line(fmt.Sprint(a...))) }).
		Func("Println", func(ctx context.Context, a ...any) { stderr(ctx, fmt.Sprintln(a...)) }).
		Func("Printf", func(ctx context.Context, format string, a ...any) { stderr(ctx, line(fmt.Sprintf(format, a...))) }).
		Func("Fatal", func(ctx context.Context, a ...any) {
			stderr(ctx, line(fmt.Sprint(a...)))
			exit(ctx, 1)
		}).
		Func("Fatalf", func(ctx context.Context, format string, a ...any) {
			stderr(ctx, line(fmt.Sprintf(format, a...)))
			exit(ctx, 1)
		}).
		Func("Fatalln", func(ctx context.Context, a ...any) {
			stderr(ctx, fmt.Sprintln(a...))
			exit(ctx, 1)
		})
}

const timeSource = `package time

// A Duration represents the elapsed time between two instants as an int64
// nanosecond count.
type Duration int64

const (
	Nanosecond  Duration = 1
	Microsecond          = 1000 * Nanosecond
	Millisecond          = 1000 * Microsecond
	Second               = 1000 * Millisecond
	Minute               = 60 * Second
	Hour                 = 60 * Minute
)

// Sleep pauses the current goroutine for at least the duration d.
func Sleep(d Duration)

func (d Duration) String() string
func (d Duration) Seconds() float64
func (d Duration) Milliseconds() int64
`

func stdTime() *Package {
	return NewPackage("time").Source(timeSource).Concurrent().
		Type("Duration", time.Duration(0)).
		Func("Sleep", func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
			case <-ctx.Done():
			}
		}).
		Func("Duration.String", time.Duration.String).
		Func("Duration.Seconds", time.Duration.Seconds).
		Func("Duration.Milliseconds", time.Duration.Milliseconds)
}
