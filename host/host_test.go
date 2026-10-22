package host_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
)

const prog = `
GREETING = "hello"
TABLE = {"a": [1, 2], "b": (3.5, None)}
print("init", GREETING)

def greet(name, punct = "!"):
    return GREETING + ", " + name + punct

def table():
    return TABLE

def stats(xs):
    return {"n": len(xs), "sum": sum_(xs), "max": max(xs) if xs else None}

def sum_(xs):
    t = 0
    for x in xs:
        t += x
    return t

def boom(x):
    fail("bad input:", x)

def mutate():
    TABLE["c"] = 1

def echo(*args, **kwargs):
    return [list(args), kwargs]

def _private():
    pass
`

func load(t *testing.T, src string, out *bytes.Buffer) *host.Module {
	t.Helper()
	wasm, err := starc.Compile("prog.star", []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := host.Load(context.Background(), wasm, &host.Config{Stdout: out})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })
	return m
}

func TestCalls(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	m := load(t, prog, &out)
	if out.String() != "init hello\n" {
		t.Errorf("init output %q", out.String())
	}
	var names []string
	for _, e := range m.Exports() {
		names = append(names, e.Name)
	}
	if want := []string{"greet", "table", "stats", "sum_", "boom", "mutate", "echo"}; !reflect.DeepEqual(names, want) {
		t.Errorf("exports = %v, want %v", names, want)
	}
	if got, err := m.Call(ctx, "greet", "world"); err != nil || got != "hello, world!" {
		t.Errorf("greet = %v, %v", got, err)
	}
	if got, err := m.CallKw(ctx, "greet", []any{"you"}, abi.NewDict("punct", "?")); err != nil || got != "hello, you?" {
		t.Errorf("greet kw = %v, %v", got, err)
	}
	got, err := m.Call(ctx, "table")
	want := abi.NewDict("a", []any{int64(1), int64(2)}, "b", abi.Tuple{3.5, nil})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("table = %#v, %v", got, err)
	}
	// Many calls must not exhaust memory: each call resets the arena.
	xs := make([]any, 1000)
	for i := range xs {
		xs[i] = i
	}
	for i := 0; i < 200; i++ {
		got, err := m.Call(ctx, "stats", xs)
		if err != nil {
			t.Fatal(err)
		}
		if s, _ := got.(*abi.Dict).Get("sum"); s != int64(499500) {
			t.Fatalf("sum = %v", s)
		}
	}
	got, err = m.CallKw(ctx, "echo", []any{1, "x"}, abi.NewDict("k", true))
	if want := []any{[]any{int64(1), "x"}, abi.NewDict("k", true)}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("echo = %#v, %v", got, err)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	m := load(t, prog, &bytes.Buffer{})
	_, err := m.Call(ctx, "boom", 42)
	var se *host.Error
	if !errors.As(err, &se) || se.Msg != "fail: bad input: 42" {
		t.Errorf("boom: %v", err)
	}
	// The instance stays usable after a Starlark error.
	if got, err := m.Call(ctx, "greet", "again"); err != nil || got != "hello, again!" {
		t.Errorf("greet after error = %v, %v", got, err)
	}
	_, err = m.Call(ctx, "mutate")
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "frozen") {
		t.Errorf("mutate: %v", err)
	}
	if _, err := m.Call(ctx, "greet"); err == nil || !strings.Contains(err.Error(), "missing argument") {
		t.Errorf("greet(): %v", err)
	}
	if _, err := m.Call(ctx, "_private"); err == nil {
		t.Error("private function was callable")
	}
}

func TestCommandModule(t *testing.T) {
	wasm, err := starc.Compile("cmd.star", []byte("print('hi')\nfail('oops')\n"), &starc.Options{ExecModel: starc.Command})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	_, err = host.Load(context.Background(), wasm, &host.Config{Stdout: &out, Stderr: &errb})
	var exit *host.ExitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("err = %v", err)
	}
	if want := "Traceback (most recent call last):\n  cmd.star:2:5: in <toplevel>\nError in fail: fail: oops\n"; out.String() != "hi\n" || errb.String() != want {
		t.Errorf("stdout %q stderr %q", out.String(), errb.String())
	}
}

func TestReadMeta(t *testing.T) {
	wasm, err := starc.Compile("m.star", []byte("def f(a, b = 1, *c, **d):\n    pass\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := host.ReadMeta(context.Background(), wasm)
	if err != nil {
		t.Fatal(err)
	}
	want := []host.Export{{Name: "f", Params: []string{"a", "b", "*c", "**d"}}}
	if meta.Model != "reactor" || !reflect.DeepEqual(meta.Exports, want) {
		t.Errorf("meta = %+v", meta)
	}
}

func TestMalformedModule(t *testing.T) {
	wasm, err := starc.Compile("m.star", []byte("def f(x):\n    return x\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Renaming an export keeps the binary valid but breaks the ABI.
	for _, name := range []string{"_initialize", "starc_alloc", "starc_error"} {
		broken := bytes.ReplaceAll(wasm, []byte(name), []byte(name[:len(name)-1]+"X"))
		_, err := host.Load(context.Background(), broken, nil)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s renamed: err = %v", name, err)
		}
	}
	// An export listed in starc.meta must exist.
	broken := bytes.ReplaceAll(wasm, []byte(`"name":"f"`), []byte(`"name":"g"`))
	if _, err := host.Load(context.Background(), broken, nil); err == nil || !strings.Contains(err.Error(), `"g"`) {
		t.Errorf("missing export: err = %v", err)
	}
	broken = bytes.ReplaceAll(wasm, []byte(`"model":"reactor"`), []byte(`"model":"reactox"`))
	if _, err := host.Load(context.Background(), broken, nil); err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("unknown model: err = %v", err)
	}
}

// A module with an incomplete set of profile functions is rejected when it
// is loaded, not when its profile is read (issue #47).
func TestMalformedProfile(t *testing.T) {
	wasm, err := starc.Compile("p.star", []byte("def f(x):\n    return x + 1\n"), &starc.Options{Fuel: true, Profile: true, NoWASI: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"starc_profile_fuel", "starc_profile_calls", "starc_profile_reset"} {
		broken := bytes.ReplaceAll(wasm, []byte(name), []byte(name[:len(name)-1]+"X"))
		m, err := host.Load(context.Background(), broken, nil)
		if err == nil {
			m.Close(context.Background())
		}
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s renamed: err = %v", name, err)
		}
	}
}
