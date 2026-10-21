package compiler

import (
	"fmt"
	"math"
	"runtime/metrics"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// The differential fuzz targets run programs both compiled and with the
// reference interpreter and compare the results. Their programs always
// compile, so the fuzzer spends its time in the code generator and the
// runtime rather than in the parser.

// newFuzzRuntime returns a runtime whose modules get at most 64 MiB of
// memory, so that programs whose values grow without bound fail with
// "out of memory" instead of exhausting the fuzzer.
func newFuzzRuntime(t testing.TB) wazero.Runtime {
	return newRuntime(t, wazero.NewRuntimeConfig().WithMemoryLimitPages(1024))
}

// differential compares the compiled program src, optionally with fuel
// metering, with the reference interpreter, allowing for 64-bit integer
// overflow. Runs that hit a resource limit on either side are
// inconclusive and pass.
func differential(t *testing.T, rt wazero.Runtime, src string, fuel bool) {
	t.Helper()
	t.Logf("fuel=%v, program:\n%s", fuel, src)
	got, stderr, gerr := execute(t, rt, "fuzz.star", src, fuel)
	if strings.Contains(stderr, "out of memory") {
		return
	}
	want, werr := interpretBounded("fuzz.star", src)
	if werr != nil && strings.Contains(werr.Error(), "Starlark computation cancelled") {
		return
	}
	compareRuns(t, "", want, werr, got, stderr, gerr, true)
}

// interpretBounded is like interpret, but cancels programs that run for
// more than a million steps or grow the heap by more than 1 GiB.
func interpretBounded(name, src string) (string, error) {
	var out strings.Builder
	thread := &starlark.Thread{Print: func(_ *starlark.Thread, msg string) { out.WriteString(msg + "\n") }}
	thread.SetMaxExecutionSteps(1e6)
	done := make(chan struct{})
	defer close(done)
	go func() {
		heap := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		metrics.Read(heap)
		limit := heap[0].Value.Uint64() + 1<<30
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if metrics.Read(heap); heap[0].Value.Uint64() > limit {
					thread.Cancel("heap limit exceeded")
					return
				}
			}
		}
	}()
	_, err := starlark.ExecFileOptions(&testDialect, thread, name, src, nil)
	return out.String(), err
}

// FuzzStrings checks string methods, formatting and conversions on
// arbitrary strings s and t. Operations that may fail are chosen one at
// a time by op, so that a failure does not hide the others.
func FuzzStrings(f *testing.F) {
	for i, s := range [][2]string{
		{"hello world", "o"}, {"a,b,,c", ","}, {"  Mixed Case\tline\r\nnext ", " "}, {"", ""},
		{"0x1F", "x"}, {"-12_3", "1"}, {"1e10", "e"}, {" +inf ", "n"}, {"{}{0}{x!r}{{", "y"},
		{"Title Case Words", "Case"}, {"aaaa", "aa"}, {"a\nb\r\n\nc", "\n"}, {"%s %d %%", "%"},
	} {
		f.Add(s[0], s[1], uint8(i))
	}
	rt := newFuzzRuntime(f)
	f.Fuzz(func(t *testing.T, s, u string, op uint8) {
		// String literals cannot express invalid UTF-8.
		s, u = strings.ToValidUTF8(s, string(utf8.RuneError)), strings.ToValidUTF8(u, string(utf8.RuneError))
		differential(t, rt, stringProgram(s, u, op), false)
	})
}

// stringOps are operations on the strings s and t that cannot fail.
// Case conversion and classification handle ASCII only (a documented
// deviation), so stringASCIIOps run only on ASCII strings.
const (
	stringOps = `print(repr(s), str(s), len(s), s == t, s < t, s >= t, s + t, s * 2, t in s, s not in t)
print(s.count(t), s.find(t), s.rfind(t), s.find(t, 1), s.rfind(t, -2), s.count(t, 1, -1), s.find(t, -3, 5))
print(s.startswith(t), s.endswith(t), s.startswith((t, "a")), s.removeprefix(t), s.removesuffix(t))
print(s.split(), s.rsplit(), s.split(None, 1), s.rsplit(None, 1), s.splitlines(), s.splitlines(True))
print(s.strip(), s.lstrip(), s.rstrip(), s.strip(t), s.lstrip(t), s.rstrip(t))
print(s.replace(t, "<>"), s.replace(t, "", 1), s.replace(t, t + t, 2))
print(t.join([s, s, ""]), s.join(t.elems()), list(s.elems()), list(s.elem_ords()), list(s.codepoints()), list(s.codepoint_ords()))
print(s[1:], s[:-1], s[::-1], s[::2], s[-3:7], s[len(t):], s[1:-1:3])
print(sorted([s, t]), {s: 1, t: 2}, [s] + [t], (s, t) == (t, s), hash(s), "%s|%r|%s" % (s, t, (s, t)))
print("{}|{!r}|{x}".format(s, t, x = s), bool(s), len(list(s.codepoints())))
`
	stringASCIIOps = `print(s.upper(), s.lower(), s.title(), s.capitalize())
print(s.isalnum(), s.isalpha(), s.isdigit(), s.islower(), s.isspace(), s.istitle(), s.isupper())
`
)

// stringRisky are operations on s and t that may fail.
var stringRisky = []string{
	"s.split(t)", "s.rsplit(t)", "s.split(t, 1)", "s.rsplit(t, 2)", "s.partition(t)", "s.rpartition(t)",
	"s.index(t)", "s.rindex(t)", "s.index(t, 1, -1)", "int(s)", "int(s, 0)", "int(s, 16)", "int(s, 36)",
	"float(s)", "s.format(t, s, x = t)", "ord(s)", "s[len(t)]", "list(s.elems())[-len(t)]",
}

func stringProgram(s, t string, op uint8) string {
	var b strings.Builder
	fmt.Fprintf(&b, "s = %s\nt = %s\n", syntax.Quote(s, false), syntax.Quote(t, false))
	b.WriteString(stringOps)
	if isASCII(s) && isASCII(t) {
		b.WriteString(stringASCIIOps)
	}
	fmt.Fprintf(&b, "print(%s)\n", stringRisky[int(op)%len(stringRisky)])
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// FuzzNumbers checks arithmetic, comparisons, conversions and
// formatting on arbitrary ints a, b and floats x, y. As in FuzzStrings,
// op chooses one operation that may fail.
func FuzzNumbers(f *testing.F) {
	for i, v := range []struct {
		a, b int64
		x, y float64
	}{
		{7, -3, 2.5, -0.5}, {0, 0, 0, math.Copysign(0, -1)}, {math.MaxInt64, math.MinInt64, math.Inf(1), math.NaN()},
		{-1, 63, 1e308, 5e-324}, {1 << 53, (1 << 53) + 1, 9007199254740993, 1e22}, {math.MinInt64, -1, -1e18, 0.1},
		{255, 4, 9.2e18, 123456789.125}, {-7, 2, -7.5, 2}, {1114111, 55296, 1.5e-7, 1e21},
	} {
		f.Add(v.a, v.b, v.x, v.y, uint8(i))
	}
	rt := newFuzzRuntime(f)
	f.Fuzz(func(t *testing.T, a, b int64, x, y float64, op uint8) {
		differential(t, rt, numberProgram(a, b, x, y, op), false)
	})
}

// numberOps are operations on the ints a, b and floats x, y that cannot
// fail.
const numberOps = `print(a, b, x, y, -x, repr(y), str(a), float(a), bool(x), bool(a), type(x))
print(a == b, a < b, a >= b, a == x, a < x, a >= y, x == y, x < y, x != x, a != y)
print(a & b, a | b, a ^ b, ~a, a >> (b % 64), a >> 63, a >> 100)
print(x + y, x - y, x * y, -y, abs(x), x + a, x * b, a - y)
print(min(a, b), max(x, y), min(a, x, b, y), max([b, y, a, x]), sorted([a, b, x, y]), sorted([x, y, a], reverse = True))
print({a: 1, b: 2, x: 3, y: 4}, hash(a), a in [x, y], x in {a: 0, b: 1})
print("%d %i %x %X %o %s %r" % (a, b, a, b, a, a, b), "%s %r %g %G" % (x, y, x, y))
print(str(x) == repr(x), float(str(x)) == x or x != x, float(str(a)) == float(a))
print(len(range(a % 1000, b % 1000, 1 + b % 7)), list(range(a % 20, b % 20, -3)), range(a % 50)[::3])
`

// numberRisky are operations on a, b, x and y that may fail.
var numberRisky = []string{
	"a + b", "a - b", "a * b", "a // b", "a % b", "a / b", "-a", "abs(a)", "a << (b % 64)", "a >> b",
	"x // y", "x % y", "x / y", "int(x)", "int(y) - a", "a // y", "x % b", "b / x", "chr(a)", "ord(chr(a % 1114112))",
	"len(range(a, b))", "len(range(a, b, b))", "range(a, b, b)[a % 5]", "\"%c\" % b", "int(str(x))",
	"int(\"%d\" % a) == a", "a * x", "x - a",
}

func numberProgram(a, b int64, x, y float64, op uint8) string {
	var p strings.Builder
	fmt.Fprintf(&p, "a = %s\nb = %s\nx = %s\ny = %s\n", intLit(a), intLit(b), floatLit(x), floatLit(y))
	p.WriteString(numberOps)
	// %e and %f support magnitudes below about 9.2e18 only.
	if math.Abs(x) < 1e18 && math.Abs(y) < 1e18 {
		p.WriteString(`print("%e %E %f %F" % (x, y, x, y))` + "\n")
	}
	fmt.Fprintf(&p, "print(%s)\n", numberRisky[int(op)%len(numberRisky)])
	return p.String()
}

func intLit(v int64) string {
	if v == math.MinInt64 {
		return "(-9223372036854775807 - 1)"
	}
	return strconv.FormatInt(v, 10)
}

func floatLit(v float64) string {
	switch {
	case math.IsNaN(v):
		return `float("nan")`
	case math.IsInf(v, 0):
		return fmt.Sprintf("float(%q)", strconv.FormatFloat(v, 'g', -1, 64))
	}
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}
