package host_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
)

const fuelProg = `
def spin():
    while True:
        pass

def nested():
    for i in range(1000000):
        for j in range(1000000):
            pass

def sorts(xs):
    for i in range(1000000):
        sorted(xs)

def rec(n):
    return rec(n + 1)

def builds():
    s = "x"
    for i in range(30):
        s = s + s
    return len(s)

def work(n):
    d = {}
    for i in range(n):
        d[str(i % 7)] = d.get(str(i % 7), 0) + i
    return [sorted(d.items()), "%d-%s" % (n, "x" * 3), [x * 2 for x in range(n) if x % 3]]

def ok():
    return 1
`

var fuelOpts = &starc.Options{Fuel: true, Dialect: starc.Dialect{While: true, Recursion: true}}

func used(t *testing.T, m *host.Module, budget int64) int64 {
	t.Helper()
	rest, err := m.Fuel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return budget - rest
}

func TestFuelExhausted(t *testing.T) {
	ctx := context.Background()
	m := loadHost(t, fuelProg, fuelOpts, nil)
	xs := make([]any, 200)
	for i := range xs {
		xs[i] = len(xs) - i
	}
	for _, tc := range []struct {
		name string
		args []any
	}{
		{"spin", nil}, {"nested", nil}, {"sorts", []any{xs}}, {"builds", nil},
	} {
		const budget = 200000
		if err := m.SetFuel(ctx, budget); err != nil {
			t.Fatal(err)
		}
		_, err := m.Call(ctx, tc.name, tc.args...)
		var se *host.Error
		if !errors.Is(err, host.ErrFuelExhausted) || !errors.As(err, &se) || se.Msg != "fuel exhausted" || se.Code != host.CodeFuel {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if rest, _ := m.Fuel(ctx); rest != 0 {
			t.Errorf("%s: %d fuel left after exhaustion", tc.name, rest)
		}
		// The instance remains usable.
		_ = m.SetFuel(ctx, 1000)
		if got, err := m.Call(ctx, "ok"); err != nil || got != int64(1) {
			t.Errorf("%s: ok() = %v, %v", tc.name, got, err)
		}
	}
	// An ordinary error has its own code.
	_, err := m.Call(ctx, "ok", 1)
	var se *host.Error
	if !errors.As(err, &se) || se.Code != host.CodeStarlark || errors.Is(err, host.ErrFuelExhausted) {
		t.Errorf("ok(1): %v", err)
	}
}

// fuelOf runs work(n) with a fresh instance on rt and returns the fuel
// used by initialization and by the call.
func fuelOf(t *testing.T, rt wazero.Runtime, wasm []byte, n int) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	const budget = 1 << 40
	m, err := host.Load(ctx, wasm, &host.Config{Runtime: rt, InitFuel: budget})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	init := used(t, m, budget)
	_ = m.SetFuel(ctx, budget)
	if _, err := m.Call(ctx, "work", n); err != nil {
		t.Fatal(err)
	}
	return init, used(t, m, budget)
}

func TestFuelDeterministic(t *testing.T) {
	ctx := context.Background()
	wasm, err := starc.Compile("p.star", []byte(fuelProg), fuelOpts)
	if err != nil {
		t.Fatal(err)
	}
	var results [][2]int64
	for _, cfg := range []wazero.RuntimeConfig{wazero.NewRuntimeConfigInterpreter(), wazero.NewRuntimeConfigCompiler()} {
		rt := wazero.NewRuntimeWithConfig(ctx, cfg.WithCustomSections(true))
		defer rt.Close(ctx)
		init, call := fuelOf(t, rt, wasm, 500)
		results = append(results, [2]int64{init, call})
	}
	if results[0] != results[1] {
		t.Errorf("interpreter %v, compiler %v", results[0], results[1])
	}
	// The exact numbers are part of the cost table: if they change, so
	// must rt.FuelVersion (and docs/fuel.md).
	if want := [2]int64{2456, 14760}; results[0] != want {
		t.Errorf("fuel = %v, want %v (bump the fuel version if this is intended)", results[0], want)
	}
	// Work is charged proportionally.
	_, small := fuelOf(t, nil, wasm, 100)
	_, large := fuelOf(t, nil, wasm, 1000)
	if r := float64(large) / float64(small); r < 8 || r > 13 {
		t.Errorf("work(1000) / work(100) = %d / %d", large, small)
	}
}

func TestFuelInit(t *testing.T) {
	ctx := context.Background()
	src := "X = [i for i in range(100000)]\ndef f():\n    return len(X)\n"
	wasm, err := starc.Compile("p.star", []byte(src), &starc.Options{Fuel: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Load(ctx, wasm, &host.Config{InitFuel: 1000}); !errors.Is(err, host.ErrFuelExhausted) {
		t.Errorf("Load with small budget: %v", err)
	}
	m, err := host.Load(ctx, wasm, &host.Config{InitFuel: 10000000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if got, err := m.Call(ctx, "f"); err != nil || got != int64(100000) {
		t.Errorf("f() = %v, %v", got, err)
	}

	// A command module is metered as well.
	wasm, err = starc.Compile("p.star", []byte("for i in range(10):\n    print(i)\n"),
		&starc.Options{Fuel: true, ExecModel: starc.Command, Dialect: starc.Dialect{TopLevelControl: true}})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	_, err = host.Load(ctx, wasm, &host.Config{InitFuel: 2600, Stdout: &out, Stderr: &errb})
	var exit *host.ExitError
	if !errors.As(err, &exit) || exit.Code != 2 || !errors.Is(err, host.ErrFuelExhausted) || errb.String() != "Error: fuel exhausted\n" || out.Len() == 0 {
		t.Errorf("command: %v, stdout %q, stderr %q", err, out.String(), errb.String())
	}
}

func TestFuelHost(t *testing.T) {
	ctx := context.Background()
	src := "def cheap():\n    return free()\n\ndef dear():\n    return costly()\n\ndef dyn(n):\n    return charge(n)\n"
	wasm, err := starc.Compile("p.star", []byte(src), &starc.Options{Fuel: true, NoWASI: true,
		Host: []starc.HostFunc{{Name: "free"}, {Name: "costly", Cost: 1000}, {Name: "charge"}}})
	if err != nil {
		t.Fatal(err)
	}
	none := func(context.Context, []any, *abi.Dict) (any, error) { return nil, nil }
	m, err := host.Load(ctx, wasm, &host.Config{Funcs: map[string]host.Func{
		"free": none, "costly": none,
		"charge": func(ctx context.Context, args []any, _ *abi.Dict) (any, error) {
			host.ChargeFuel(ctx, args[0].(int64))
			host.ChargeFuel(ctx, args[0].(int64))
			return nil, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if m.Meta().Fuel == nil || m.Meta().Fuel.Version != 1 || m.Meta().Fuel.Host["costly"] != 1000 {
		t.Errorf("meta fuel = %+v", m.Meta().Fuel)
	}
	cost := func(fn string, args ...any) int64 {
		const budget = 1 << 40
		_ = m.SetFuel(ctx, budget)
		if _, err := m.Call(ctx, fn, args...); err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		return used(t, m, budget)
	}
	if d := cost("dear") - cost("cheap"); d != 1000 {
		t.Errorf("declared cost: difference %d", d)
	}
	if d := cost("dyn", 500) - cost("dyn", 0); d != 1000 {
		t.Errorf("charged cost: difference %d", d)
	}
	_ = m.SetFuel(ctx, 5000)
	if _, err := m.Call(ctx, "dyn", math.MaxInt64); !errors.Is(err, host.ErrFuelExhausted) {
		t.Errorf("dyn(max): %v", err)
	}
	// Without a call in progress, ChargeFuel does nothing.
	host.ChargeFuel(ctx, 10)
}

func TestNoFuel(t *testing.T) {
	ctx := context.Background()
	m := loadHost(t, "def f():\n    return 1\n", nil, nil)
	if err := m.SetFuel(ctx, 10); err == nil || !strings.Contains(err.Error(), "without fuel") {
		t.Errorf("SetFuel: %v", err)
	}
	if _, err := m.Fuel(ctx); err == nil {
		t.Error("Fuel succeeded")
	}
	if m.Meta().Fuel != nil {
		t.Error("meta has a cost table")
	}
	mf := loadHost(t, "def f():\n    return 1\n", &starc.Options{Fuel: true}, nil)
	if err := mf.SetFuel(ctx, -1); err == nil {
		t.Error("negative budget accepted")
	}
	if _, err := starc.Compile("p.star", []byte("pass\n"), &starc.Options{Host: []starc.HostFunc{{Name: "f", Cost: -1}}}); err == nil {
		t.Error("negative cost accepted")
	}
	if _, err := starc.Compile("p.star", []byte("pass\n"), &starc.Options{Host: []starc.HostFunc{{Name: "f", Cost: 1}, {Name: "f", Cost: 2}}}); err == nil {
		t.Error("conflicting costs accepted")
	}
}
