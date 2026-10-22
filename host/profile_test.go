package host_test

import (
	"context"
	"errors"
	"testing"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
)

// profProg exercises functions of the program, closures, a key function
// called by the prelude, built-ins implemented natively and in the
// prelude, and host functions.
const profProg = `
TABLE = [i * i for i in range(50)]

def square(x):
    return x * x

def by_last(s):
    return s[-1]

def words(n):
    out = []
    for i in range(n):
        out.append("w%d" % (i * 7 % 13))
    return out

def work(n):
    total = 0
    for i in range(n):
        total += square(i)
    names = sorted(words(n), key = by_last)
    adder = lambda v: v + total
    joined = ",".join(names).upper().split(",")
    return [adder(len(joined)), costly(), charge(n)]

def fail_in(n):
    return square(n) + None
`

func profModule(t *testing.T, profile bool) *host.Module {
	t.Helper()
	ctx := context.Background()
	wasm, err := starc.Compile("prof.star", []byte(profProg), &starc.Options{Fuel: true, Profile: profile,
		Host: []starc.HostFunc{{Name: "costly", Cost: 100}, {Name: "charge"}}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := host.Load(ctx, wasm, &host.Config{InitFuel: 1 << 40, Funcs: map[string]host.Func{
		"costly": func(context.Context, []any, *abi.Dict) (any, error) { return nil, nil },
		"charge": func(ctx context.Context, args []any, _ *abi.Dict) (any, error) {
			host.ChargeFuel(ctx, args[0].(int64))
			return nil, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(ctx) })
	return m
}

// fuelUsed returns the fuel used by the top-level code and by work(n).
func fuelUsed(t *testing.T, m *host.Module, n int) (init, call int64) {
	t.Helper()
	ctx := context.Background()
	rest, err := m.Fuel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	init = 1<<40 - rest
	const budget = 1 << 40
	if err := m.SetFuel(ctx, budget); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Call(ctx, "work", n); err != nil {
		t.Fatal(err)
	}
	if rest, err = m.Fuel(ctx); err != nil {
		t.Fatal(err)
	}
	return init, budget - rest
}

func TestProfile(t *testing.T) {
	ctx := context.Background()
	plain := profModule(t, false)
	if _, err := plain.Profile(ctx); !errors.Is(err, host.ErrNoProfile) {
		t.Errorf("Profile without profile: %v", err)
	}
	init0, call0 := fuelUsed(t, plain, 40)

	m := profModule(t, true)
	init, call := fuelUsed(t, m, 40)
	if init != init0 || call != call0 {
		t.Errorf("fuel with profile = %d, %d; without = %d, %d", init, call, init0, call0)
	}
	prof, err := m.Profile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]host.ProfileEntry{}
	var sum int64
	for _, e := range prof {
		t.Logf("%-12s %-8s %6d fuel %4d calls %v", e.Name, e.Kind, e.Fuel, e.Calls, e.Pos)
		byName[e.Name] = e
		sum += e.Fuel
	}
	if total := init + call; sum < total*99/100 || sum > total {
		t.Errorf("profile adds up to %d of %d fuel", sum, total)
	}
	for name, calls := range map[string]int64{"square": 40, "by_last": 40, "words": 1, "work": 1, "sorted": 1,
		"lambda": 1, "costly": 1, "charge": 1, "append": 40, "upper": 1} {
		if e := byName[name]; e.Calls != calls {
			t.Errorf("%s: %d calls, want %d", name, e.Calls, calls)
		}
	}
	if e := byName["charge"]; e.Fuel < 40 {
		t.Errorf("charge: %d fuel, want the 40 that the host charged", e.Fuel)
	}
	if e := byName["costly"]; e.Fuel < 100 || e.Kind != "host" {
		t.Errorf("costly: %+v", e)
	}
	if e := byName["square"]; e.Kind != "function" || e.Pos.Line != 4 || e.Pos.Col != 1 {
		t.Errorf("square: %+v", e)
	}
	if e := byName["<toplevel>"]; e.Fuel == 0 || e.Kind != "toplevel" {
		t.Errorf("<toplevel>: %+v", e)
	}

	// An error ends the call; its fuel counts toward the failing function.
	if err := m.ResetProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Call(ctx, "fail_in", 3); err == nil {
		t.Fatal("fail_in succeeded")
	}
	prof, _ = m.Profile(ctx)
	for _, e := range prof {
		want := int64(0)
		if e.Name == "fail_in" || e.Name == "square" {
			want = 1
		}
		if e.Calls != want {
			t.Errorf("after failure: %s has %d calls, want %d", e.Name, e.Calls, want)
		}
	}
}

func TestProfileDebugInfo(t *testing.T) {
	m := profModule(t, true)
	d := m.DebugInfo()
	if d == nil || d.Version != 1 || d.File != "prof.star" || len(d.Positions) < 10 || d.Positions[0] != [3]int{0, 0, 0} || len(d.Files) != 1 {
		t.Fatalf("debug info: %+v", d)
	}
	found := false
	for _, f := range d.Functions {
		if f.Name == "square" {
			found = f.Kind == "function" && f.Line == 4 && f.Wasm == "main.square"
		}
	}
	if !found {
		t.Errorf("square missing or wrong: %+v", d.Functions)
	}
	if profModule(t, false).DebugInfo() != nil {
		t.Error("debug info without profile")
	}
}

func TestProfileOptions(t *testing.T) {
	for _, opts := range []*starc.Options{{Profile: true}, {Profile: true, Fuel: true, NoBacktrace: true}} {
		if _, err := starc.Compile("p.star", []byte("x = 1\n"), opts); err == nil {
			t.Errorf("%+v: no error", opts)
		}
	}
}
