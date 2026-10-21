package host_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
)

// refError runs src with go.starlark.net and calls fn with args, which
// must fail.
func refError(t *testing.T, src, fn string, args ...starlark.Value) *starlark.EvalError {
	t.Helper()
	thread := new(starlark.Thread)
	predeclared := starlark.StringDict{
		"lookup": starlark.NewBuiltin("lookup", func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
			return nil, errors.New("no such entity")
		}),
	}
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, "p.star", src, predeclared)
	if err == nil {
		_, err = starlark.Call(thread, globals[fn], args, nil)
	}
	var ee *starlark.EvalError
	if !errors.As(err, &ee) {
		t.Fatalf("go.starlark.net: %v, want an evaluation error", err)
	}
	return ee
}

// lookupFunc is the host function lookup of refError.
var lookupFunc = map[string]host.Func{
	"lookup": func(context.Context, []any, *abi.Dict) (any, error) { return nil, errors.New("no such entity") },
}

func loadModule(t *testing.T, src string, opts *starc.Options) *host.Module {
	t.Helper()
	wasm, err := starc.Compile("p.star", []byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m, err := host.Load(ctx, wasm, &host.Config{Funcs: lookupFunc})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(ctx) })
	return m
}

// TestBacktraceCall compares the call stacks of errors in exported
// functions with go.starlark.net.
func TestBacktraceCall(t *testing.T) {
	for _, tc := range []struct {
		src  string
		args []any
	}{
		{"def f(x):\n    return g(x)\ndef g(x):\n    fail('bad', x)\n", []any{int64(1)}},
		{"def f(x):\n    return [1, 2][x]\n", []any{int64(7)}},
		{"def f(x):\n    return x + 1\n", []any{"s"}},
		{"def f(d):\n    return d['missing']\n", []any{abi.NewDict()}},
		{"def f(x):\n    return sorted([1, 2], key = lambda v: v + x)\n", []any{"s"}},
		{"def f(x):\n    return x.upper()\n", []any{int64(1)}},
		{"def f():\n    return lookup(1)\n", nil},
		{"def f(a, b):\n    return a\n", []any{int64(1)}},
	} {
		t.Run("", func(t *testing.T) {
			src := tc.src
			if !strings.Contains(src, "lookup") {
				src += "def _use():\n    return lookup\n"
			}
			var args starlark.Tuple
			for _, a := range tc.args {
				switch a := a.(type) {
				case int64:
					args = append(args, starlark.MakeInt64(a))
				case string:
					args = append(args, starlark.String(a))
				case *abi.Dict:
					args = append(args, starlark.NewDict(0))
				}
			}
			want := refError(t, src, "f", args...)
			m := loadModule(t, src, &starc.Options{Host: []starc.HostFunc{{Name: "lookup"}}})
			_, err := m.Call(context.Background(), "f", tc.args...)
			var e *host.Error
			if !errors.As(err, &e) {
				t.Fatalf("err = %v, want a Starlark error", err)
			}
			if got, want := stackString(e.CallStack), refStackString(want.CallStack); got != want {
				t.Errorf("call stack\n got: %s\nwant: %s", got, want)
			}
			if e.Msg == want.Msg && e.Backtrace() != want.Backtrace() {
				t.Errorf("backtrace\n got: %s\nwant: %s", e.Backtrace(), want.Backtrace())
			}
			// Pos is the innermost position in the program.
			for i := len(want.CallStack) - 1; i >= 0; i-- {
				if p := want.CallStack[i].Pos; p.Line != 0 {
					if e.Pos.String() != p.String() {
						t.Errorf("Pos = %s, want %s", e.Pos, p)
					}
					break
				}
			}
		})
	}
}

func stackString(s []host.Frame) string {
	var parts []string
	for _, fr := range s {
		parts = append(parts, fr.Pos.String()+" "+fr.Name)
	}
	return strings.Join(parts, " | ")
}

func refStackString(s starlark.CallStack) string {
	var parts []string
	for _, fr := range s {
		parts = append(parts, fr.Pos.String()+" "+fr.Name)
	}
	return strings.Join(parts, " | ")
}

// TestBacktraceInit checks an error in the top-level code of a reactor.
func TestBacktraceInit(t *testing.T) {
	src := "def f():\n    return {}['k']\nx = f()\n"
	wasm, err := starc.Compile("p.star", []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = host.Load(context.Background(), wasm, nil)
	var e *host.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v", err)
	}
	want := refError(t, src+"def g():\n    pass\n", "g")
	if got, want := stackString(e.CallStack), refStackString(want.CallStack); got != want {
		t.Errorf("call stack\n got: %s\nwant: %s", got, want)
	}
}

// TestBacktraceLimits checks the positions of errors that go.starlark.net
// does not have: limits and int overflow.
func TestBacktraceLimits(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, src string
		opts      starc.Options
		args      []any
		stack     string
		code      host.ErrorCode
	}{
		{"overflow", "def f(x):\n    return x * x\n", starc.Options{}, []any{int64(1) << 40},
			"p.star:2:14 f", host.CodeStarlark},
		{"memory", "def f(n):\n    s = 'x'\n    for i in range(n):\n        s = s + s\n    return s\n",
			starc.Options{MaxMemoryPages: 64}, []any{int64(40)}, "p.star:4:15 f", host.CodeMemory},
		{"depth", "def f():\n    return g(1)\ndef g(n):\n    return g(n + 1)\n",
			starc.Options{MaxCallDepth: 5, Dialect: starc.Dialect{Recursion: true}}, nil,
			// The innermost call fails on entry, like a call with wrong
			// arguments.
			"p.star:2:13 f | p.star:4:13 g | p.star:4:13 g | p.star:4:13 g | p.star:4:13 g | p.star:4:12 g", host.CodeDepth},
		{"fuel", "def f():\n    return sorted(range(100))\n", starc.Options{Fuel: true}, nil,
			"p.star:2:18 f | <builtin> sorted", host.CodeFuel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := loadModule(t, tc.src, &tc.opts)
			if tc.opts.Fuel {
				if err := m.SetFuel(ctx, 100); err != nil {
					t.Fatal(err)
				}
			}
			_, err := m.Call(ctx, "f", tc.args...)
			var e *host.Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("err = %v, want code %d", err, tc.code)
			}
			if got := stackString(e.CallStack); got != tc.stack {
				t.Errorf("call stack\n got: %s\nwant: %s", got, tc.stack)
			}
		})
	}
}

// TestNoBacktrace checks that errors of a module built with NoBacktrace
// carry only their message.
func TestNoBacktrace(t *testing.T) {
	m := loadModule(t, "def f():\n    fail('x')\n", &starc.Options{NoBacktrace: true})
	if m.Meta().File != "" {
		t.Errorf("Meta().File = %q", m.Meta().File)
	}
	_, err := m.Call(context.Background(), "f")
	var e *host.Error
	if !errors.As(err, &e) || e.Msg != "fail: x" || e.CallStack != nil || e.Pos != (host.Position{}) {
		t.Fatalf("err = %#v", err)
	}
	if got := e.Backtrace(); got != "Error: fail: x" {
		t.Errorf("Backtrace() = %q", got)
	}

	wasm, err := starc.Compile("p.star", []byte("fail('x')\n"), &starc.Options{NoBacktrace: true, ExecModel: starc.Command})
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	_, err = host.Load(context.Background(), wasm, &host.Config{Stderr: &stderr})
	if err == nil || stderr.String() != "Error: fail: x\n" {
		t.Errorf("command: %v, stderr %q", err, stderr.String())
	}
}

// TestBacktraceNoFilename checks that a module compiled without a file
// name still reports its call stack.
func TestBacktraceNoFilename(t *testing.T) {
	wasm, err := starc.Compile("", []byte("def f():\n    fail('x')\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m, err := host.Load(ctx, wasm, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	_, err = m.Call(ctx, "f")
	var e *host.Error
	if !errors.As(err, &e) || stackString(e.CallStack) != ":2:9 f | <builtin> fail" {
		t.Fatalf("err = %v, stack %s", err, stackString(e.CallStack))
	}
}

// TestBacktraceReuse checks that the frames of an error do not leak into
// a later error.
func TestBacktraceReuse(t *testing.T) {
	m := loadModule(t, "def f(x):\n    return g(x)\ndef g(x):\n    return 1 // x\ndef h():\n    fail('h')\n", nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := m.Call(ctx, "f", int64(0))
		var e *host.Error
		if !errors.As(err, &e) || stackString(e.CallStack) != "p.star:2:13 f | p.star:4:14 g" {
			t.Fatalf("f: %v %s", err, fmt.Sprint(e))
		}
		if _, err := m.Call(ctx, "f", int64(1)); err != nil {
			t.Fatal(err)
		}
		_, err = m.Call(ctx, "h")
		if !errors.As(err, &e) || stackString(e.CallStack) != "p.star:6:9 h | <builtin> fail" {
			t.Fatalf("h: %v %s", err, stackString(e.CallStack))
		}
	}
}
