package host_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/host"
)

const nanProg = `
INF = float("inf")

def nans(x):
    return [
        INF - INF,
        0.0 * INF,
        INF / INF,
        float("nan"),
        float("-nan"),
        float("NaN"),
        -float("nan"),
        abs(INF - INF),
        (INF - INF) + 1.0,
        x,
        -x,
        x * 2.0,
        [x][0],
        {"k": x}["k"],
        max([1.0, x]),
    ]

def keys(x):
    # NaN equals itself in Starlark, so every NaN finds the same entry.
    d = {INF - INF: "a"}
    d[float("nan")] = "b"
    d[x] = "c"
    return [d[0.0 * INF], d[-float("nan")], d[x], len(d)]

def finite(x):
    return x * x + float("1.5")

def ok():
    return 1
`

// TestNaN checks that every NaN, however it is produced, has the same
// bit pattern on every engine, including in the host encoding and in
// dict keys.
func TestNaN(t *testing.T) {
	ctx := context.Background()
	wasm := mustCompile(t, nanProg, nil)
	inputs := []float64{
		math.NaN(),
		math.Float64frombits(0x7ff4000000000001), // signaling NaN
		math.Float64frombits(0xfff8000000000000), // negative quiet NaN
		math.Float64frombits(0x7fffffffffffffff),
	}
	for engine, rt := range runtimes(t) {
		m, err := host.Load(ctx, wasm, &host.Config{Runtime: rt})
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range inputs {
			got, err := m.Call(ctx, "nans", in)
			if err != nil {
				t.Fatal(err)
			}
			for i, v := range got.([]any) {
				if bits := math.Float64bits(v.(float64)); bits != 0x7ff8000000000000 {
					t.Errorf("%s: nans(%#x)[%d] has bits %#x", engine, math.Float64bits(in), i, bits)
				}
			}
			got, err = m.Call(ctx, "keys", in)
			if err != nil || len(got.([]any)) != 4 || got.([]any)[3] != int64(1) {
				t.Errorf("%s: keys(%#x) = %v, %v", engine, math.Float64bits(in), got, err)
			}
		}
		m.Close(ctx)
	}
}

func TestFiniteFloats(t *testing.T) {
	ctx := context.Background()
	src := strings.Replace(nanProg, `INF = float("inf")`, `INF = 1e308`, 1)
	m := loadHost(t, src, &starc.Options{FiniteFloats: true}, nil)
	if !m.Meta().FiniteFloats {
		t.Error("meta lacks finite_floats")
	}
	if got, err := m.Call(ctx, "finite", 2.0); err != nil || got.(float64) != 5.5 {
		t.Errorf("finite(2) = %v, %v", got, err)
	}
	for _, tc := range []struct {
		fn  string
		arg any
	}{
		{"nans", 1.0},           // float("nan")
		{"finite", math.Inf(1)}, // from the host
		{"finite", math.NaN()},
		{"finite", 1e300}, // overflow
	} {
		_, err := m.Call(ctx, tc.fn, tc.arg)
		var se *host.Error
		if !errors.As(err, &se) || se.Msg != "non-finite float value" {
			t.Errorf("%s(%v): %v", tc.fn, tc.arg, err)
		}
		if got, err := m.Call(ctx, "ok"); err != nil || got != int64(1) {
			t.Errorf("ok() after %s: %v, %v", tc.fn, got, err)
		}
	}
	// float("inf") at top level. (Float literals are always finite: the
	// parser rejects 1e999.)
	for _, src := range []string{"x = float('inf')\n", "x = float('-infinity')\n"} {
		wasm := mustCompile(t, src, &starc.Options{FiniteFloats: true})
		if _, err := host.Load(ctx, wasm, nil); err == nil || !strings.Contains(err.Error(), "non-finite float value") {
			t.Errorf("%q: %v", src, err)
		}
	}
}
