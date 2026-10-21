package host_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
)

const limitsProg = `
def spin():
    while True:
        pass

def grow_str():
    x = "x"
    while True:
        x = x + x

def grow_list():
    x = [1]
    while True:
        x = x + x

def rec(n):
    return rec(n + 1)

def depth(n):
    return 1 if n == 0 else 1 + depth(n - 1)

def nested(n):
    l = []
    for i in range(n):
        l = [l]
    return l

def nested_repr(n):
    return len(str(nested(n)))

def nested_hash(n):
    t = ()
    for i in range(n):
        t = (t,)
    return hash(str(len({t: 1})))

def big_result(n):
    return "x" * n

def huge_result():
    s = "x" * (1 << 20)
    return [s] * 5000

def echo(x):
    return 1

def query(n):
    return len(ctx.big(n))

def ok():
    return "ok"
`

var limitsHost = []starc.HostFunc{{Name: "ctx.big"}}

var limitsFuncs = map[string]host.Func{
	"ctx.big": func(_ context.Context, args []any, _ *abi.Dict) (any, error) {
		return strings.Repeat("y", int(args[0].(int64))), nil
	},
}

// runtimes returns a wazero interpreter and compiler, whose stack limits
// differ widely.
func runtimes(t *testing.T) map[string]wazero.Runtime {
	ctx := context.Background()
	rts := map[string]wazero.Runtime{}
	for name, cfg := range map[string]wazero.RuntimeConfig{
		"interpreter": wazero.NewRuntimeConfigInterpreter(), "compiler": wazero.NewRuntimeConfigCompiler(),
	} {
		rt := wazero.NewRuntimeWithConfig(ctx, cfg.WithCustomSections(true))
		t.Cleanup(func() { rt.Close(ctx) })
		rts[name] = rt
	}
	return rts
}

func TestLimits(t *testing.T) {
	ctx := context.Background()
	wasm, err := starc.Compile("p.star", []byte(limitsProg), &starc.Options{
		Fuel: true, Host: limitsHost, Dialect: starc.Dialect{While: true, Recursion: true},
		MaxMemoryPages: 128, MaxResultBytes: 1 << 16, MaxInputBytes: 1 << 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	for engine, rt := range runtimes(t) {
		t.Run(engine, func(t *testing.T) {
			m, err := host.Load(ctx, wasm, &host.Config{Runtime: rt, Funcs: limitsFuncs})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close(ctx)
			if l := m.Meta().Limits; l != (host.Limits{MemoryPages: 128, CallDepth: 500, InputBytes: 1 << 12, ResultBytes: 1 << 16}) {
				t.Errorf("meta limits = %+v", l)
			}
			for _, tc := range []struct {
				fn   string
				args []any
				want error
				msg  string
			}{
				{"spin", nil, host.ErrFuelExhausted, "fuel exhausted"},
				{"grow_str", nil, host.ErrOutOfMemory, "out of memory"},
				{"grow_list", nil, host.ErrOutOfMemory, "out of memory"},
				{"rec", []any{0}, host.ErrCallDepth, "maximum call depth exceeded"},
				{"depth", []any{1000}, host.ErrCallDepth, "maximum call depth exceeded"},
				{"nested", []any{1000}, host.ErrCallDepth, "maximum call depth exceeded"},
				{"nested_repr", []any{1000}, host.ErrCallDepth, "maximum call depth exceeded"},
				{"nested_hash", []any{1000}, host.ErrCallDepth, "maximum call depth exceeded"},
				{"big_result", []any{1 << 17}, host.ErrTooLarge, "result too large: 131077 bytes, limit 65536"},
				{"huge_result", nil, host.ErrTooLarge, "value too large to pass to the host"},
				{"echo", []any{strings.Repeat("z", 5000)}, host.ErrTooLarge, "arguments too large: 5010 bytes, limit 4096"},
				{"query", []any{10 << 20}, host.ErrOutOfMemory, "out of memory"},
			} {
				_ = m.SetFuel(ctx, 5_000_000)
				res, err := m.Call(ctx, tc.fn, tc.args...)
				var se *host.Error
				if !errors.Is(err, tc.want) || !errors.As(err, &se) || se.Msg != tc.msg || res != nil {
					t.Errorf("%s: result %v, err %v, want %q", tc.fn, res, err, tc.msg)
				}
				// The instance remains usable.
				_ = m.SetFuel(ctx, 1000)
				if got, err := m.Call(ctx, "ok"); err != nil || got != "ok" {
					t.Errorf("%s: ok() = %v, %v", tc.fn, got, err)
				}
			}
			// Within the limits, everything works.
			_ = m.SetFuel(ctx, 50_000_000)
			for _, tc := range []struct {
				fn   string
				args []any
				want any
			}{
				{"depth", []any{499}, int64(500)},
				{"nested_repr", []any{200}, int64(402)},
				{"big_result", []any{1000}, strings.Repeat("x", 1000)},
				{"query", []any{1 << 20}, int64(1 << 20)},
			} {
				if got, err := m.Call(ctx, tc.fn, tc.args...); err != nil || fmt.Sprint(got) != fmt.Sprint(tc.want) {
					t.Errorf("%s: %.40v, %v", tc.fn, got, err)
				}
			}
			if _, err := m.Call(ctx, "depth", 500); !errors.Is(err, host.ErrCallDepth) {
				t.Errorf("depth(500) = %v, want call depth error", err)
			}
		})
	}
}

func TestDefaultLimits(t *testing.T) {
	ctx := context.Background()
	// Without options, the depth limit applies everywhere, also to deeply
	// nested arguments from the host.
	for engine, rt := range runtimes(t) {
		m, err := host.Load(ctx, mustCompile(t, limitsProg, &starc.Options{
			Host: limitsHost, Dialect: starc.Dialect{While: true, Recursion: true},
		}), &host.Config{Runtime: rt, Funcs: limitsFuncs})
		if err != nil {
			t.Fatal(err)
		}
		if l := m.Meta().Limits; l != (host.Limits{MemoryPages: 65535, CallDepth: 500}) {
			t.Errorf("%s: meta limits = %+v", engine, l)
		}
		for _, fn := range []string{"rec", "nested_repr"} {
			if _, err := m.Call(ctx, fn, 100000); !errors.Is(err, host.ErrCallDepth) {
				t.Errorf("%s: %s: %v", engine, fn, err)
			}
		}
		var arg any = int64(1)
		for i := 0; i < 1000; i++ {
			arg = []any{arg}
		}
		if _, err := m.Call(ctx, "echo", arg); !errors.Is(err, host.ErrCallDepth) {
			t.Errorf("%s: nested argument: %v", engine, err)
		}
		m.Close(ctx)
	}

	// MaxCallDepth changes the limit.
	m := loadHost(t, limitsProg, &starc.Options{MaxCallDepth: 50, Host: limitsHost,
		Dialect: starc.Dialect{While: true, Recursion: true}}, &host.Config{Funcs: limitsFuncs})
	if _, err := m.Call(ctx, "depth", 49); err != nil {
		t.Errorf("depth(49): %v", err)
	}
	if _, err := m.Call(ctx, "depth", 50); !errors.Is(err, host.ErrCallDepth) {
		t.Errorf("depth(50): %v", err)
	}
}

func mustCompile(t *testing.T, src string, opts *starc.Options) []byte {
	t.Helper()
	wasm, err := starc.Compile("p.star", []byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	return wasm
}

func TestLimitsCommand(t *testing.T) {
	wasm := mustCompile(t, "x = 'x'\nfor i in range(100):\n    x = x + x\n", &starc.Options{
		ExecModel: starc.Command, MaxMemoryPages: 64, Dialect: starc.Dialect{TopLevelControl: true, GlobalReassign: true},
	})
	var errb strings.Builder
	_, err := host.Load(context.Background(), wasm, &host.Config{Stderr: &errb})
	var exit *host.ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || !errors.Is(err, host.ErrOutOfMemory) || errb.String() != "Error: out of memory\n" {
		t.Errorf("err = %v, stderr %q", err, errb.String())
	}
}

func TestLimitOptions(t *testing.T) {
	for _, opts := range []*starc.Options{
		{MaxMemoryPages: 65536},
		{MaxCallDepth: -1},
		{MaxCallDepth: 1 << 31},
		{MaxMemoryPages: 1}, // static data does not fit
	} {
		if _, err := starc.Compile("p.star", []byte("def f():\n    return 1\n"), opts); err == nil {
			t.Errorf("%+v: compiled", opts)
		}
	}
}
