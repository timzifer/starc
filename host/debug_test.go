package host_test

import (
	"bytes"
	"errors"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/host"
)

const debugProg = `
LIMIT = 3

def scale(xs, k):
    out = []
    for x in xs:
        y = x * k
        out.append(y)
    return out

def run(n):
    data = list(range(n))
    total = 0
    def add(v):
        return v + total
    res = sorted(scale(data, LIMIT), key = lambda v: -add(v))
    return res

print(run(4))
`

// dump formats the stack and globals at a stop.
func dump(ctx context.Context, t *testing.T, s *host.Stop) string {
	var b strings.Builder
	frames, err := s.Stack(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&b, "stop %d:%d\n", s.Pos.Line, s.Pos.Col)
	for _, f := range frames {
		fmt.Fprintf(&b, "  %s %s %d:%d", f.Kind, f.Name, f.Pos.Line, f.Pos.Col)
		for _, v := range f.Locals {
			if v.Bound {
				fmt.Fprintf(&b, " %s=%s(%s)", v.Name, v.Value, v.Type)
			} else {
				fmt.Fprintf(&b, " %s=?", v.Name)
			}
		}
		b.WriteString("\n")
	}
	g, err := s.Globals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range g {
		fmt.Fprintf(&b, "  global %s=%s\n", v.Name, v.Value)
	}
	return b.String()
}

func TestDebugStack(t *testing.T) {
	ctx := context.Background()
	wasm, err := starc.Compile("dbg.star", []byte(debugProg), &starc.Options{Debug: true, Fuel: true,
		ExecModel: starc.Command, Dialect: starc.Dialect{TopLevelControl: true}})
	if err != nil {
		t.Fatal(err)
	}
	var stops []string
	var out bytes.Buffer
	_, err = host.Load(ctx, wasm, &host.Config{Stdout: &out, InitFuel: 1 << 40, Debug: &host.Debugger{
		Lines: []int{8},
		Stop: func(ctx context.Context, s *host.Stop) {
			stops = append(stops, dump(ctx, t, s))
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "[9, 6, 3, 0]\n" {
		t.Errorf("output %q", out.String())
	}
	if len(stops) != 4 {
		t.Fatalf("%d stops", len(stops))
	}
	for _, s := range stops {
		t.Log(s)
	}
}

// runChunk loads src as a reactor and returns its output, its error and
// the fuel that the top-level code used.
func runChunk(t *testing.T, src string, debug *host.Debugger) (string, string, int64) {
	t.Helper()
	ctx := context.Background()
	opts := &starc.Options{Fuel: true, Debug: debug != nil, Struct: true, Exports: []string{},
		Dialect: starc.Dialect{Set: true, While: true, TopLevelControl: true, GlobalReassign: true}}
	wasm, err := starc.Compile("chunk.star", []byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	const budget = 1 << 40
	m, err := host.Load(ctx, wasm, &host.Config{Stdout: &out, InitFuel: budget, Debug: debug})
	if err != nil {
		return out.String(), err.Error(), 0
	}
	defer m.Close(ctx)
	rest, err := m.Fuel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), "", budget - rest
}

// TestDebugTransparent runs the conformance programs as release builds
// and as debug builds that stop at every statement and read the whole
// stack and all globals there: output, errors and fuel must be the same.
func TestDebugTransparent(t *testing.T) {
	files, _ := filepath.Glob("../testdata/*.star")
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, src := range strings.Split(string(data), "\n---\n") {
			name := fmt.Sprintf("%s#%d", filepath.Base(file), i)
			t.Run(name, func(t *testing.T) {
				out0, err0, fuel0 := runChunk(t, src, nil)
				stops := 0
				out1, err1, fuel1 := runChunk(t, src, &host.Debugger{Step: true, Errors: true, Stop: func(ctx context.Context, s *host.Stop) {
					stops++
					if stops > 2000 {
						return
					}
					if _, err := s.Stack(ctx); err != nil {
						t.Errorf("stack: %v", err)
					}
					if _, err := s.Globals(ctx); err != nil {
						t.Errorf("globals: %v", err)
					}
				}})
				if out0 != out1 || err0 != err1 || fuel0 != fuel1 {
					t.Errorf("release: %q %q fuel %d\ndebug: %q %q fuel %d", out0, err0, fuel0, out1, err1, fuel1)
				}
				if stops == 0 {
					t.Error("no stops")
				}
			})
		}
	}
}

func TestDebugCall(t *testing.T) {
	ctx := context.Background()
	src := `
def by(v):
    return -v

def pick(xs):
    deep = []
    for _ in range(600):
        deep = [deep]
    best = sorted(xs, key = lambda v: by(v))
    return best[0]
`
	wasm, err := starc.Compile("call.star", []byte(src), &starc.Options{Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	var stops []string
	m, err := host.Load(ctx, wasm, &host.Config{Debug: &host.Debugger{
		Lines: []int{3},
		Stop: func(ctx context.Context, s *host.Stop) {
			frames, err := s.Stack(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var parts []string
			for _, f := range frames {
				parts = append(parts, f.Kind+":"+f.Name)
				for _, v := range f.Locals {
					// A value nested beyond the depth limit cannot be read,
					// which does not affect the program.
					if v.Name == "deep" && v.Value != "<error: maximum call depth exceeded>" {
						t.Errorf("deep = %.40s", v.Value)
					}
				}
			}
			stops = append(stops, strings.Join(parts, " "))
			// Step through the rest of the first call of by.
			if err := s.SetStep(ctx, len(stops) == 1); err != nil {
				t.Fatal(err)
			}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	res, err := m.Call(ctx, "pick", []any{int64(3), int64(9), int64(4)})
	if err != nil || res != int64(9) {
		t.Fatalf("pick = %v, %v", res, err)
	}
	// The first stop steps to the next statement: the body of the lambda
	// for the next element (a return statement); the others are
	// breakpoints in by.
	want := "function:pick prelude:sorted function:lambda function:by"
	if len(stops) != 4 || stops[0] != want || stops[1] != "function:pick prelude:sorted function:lambda" || stops[3] != want {
		t.Errorf("stops:\n%s", strings.Join(stops, "\n"))
	}
}

func TestDebugRequiresDebugBuild(t *testing.T) {
	ctx := context.Background()
	wasm, err := starc.Compile("x.star", []byte("x = 1\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Load(ctx, wasm, &host.Config{Debug: &host.Debugger{Step: true}}); err == nil {
		t.Error("Load of a release build with Config.Debug succeeded")
	}
	if _, err := starc.Compile("x.star", []byte("x = 1\n"), &starc.Options{Debug: true, NoBacktrace: true}); err == nil {
		t.Error("Debug with NoBacktrace compiled")
	}
}

func TestDebugPreludeFrames(t *testing.T) {
	ctx := context.Background()
	src := `
def key(s):
    return s[-1]

def f():
    return "b2,a1".split(",") + sorted(["x3", "y1"], key = key)
`
	wasm, err := starc.Compile("p.star", []byte(src), &starc.Options{Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	var stacks []string
	m, err := host.Load(ctx, wasm, &host.Config{Debug: &host.Debugger{Lines: []int{3}, Stop: func(ctx context.Context, s *host.Stop) {
		frames, err := s.Stack(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, f := range frames {
			parts = append(parts, f.Kind+":"+f.Name)
		}
		stacks = append(stacks, strings.Join(parts, " "))
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if _, err := m.Call(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if len(stacks) != 2 || stacks[0] != "function:f prelude:sorted function:key" {
		t.Errorf("stacks: %q", stacks)
	}
}

func TestDebugErrorStop(t *testing.T) {
	ctx := context.Background()
	src := `
def avg(xs):
    total = 0
    for x in xs:
        total += x
    return total / len(xs)

def report(name, xs):
    label = name.upper()
    return label + ": " + str(avg(xs))
`
	wasm, err := starc.Compile("err.star", []byte(src), &starc.Options{Debug: true, Fuel: true})
	if err != nil {
		t.Fatal(err)
	}
	var stops []string
	m, err := host.Load(ctx, wasm, &host.Config{InitFuel: 1 << 30, Debug: &host.Debugger{Errors: true, Stop: func(ctx context.Context, s *host.Stop) {
		frames, err := s.Stack(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stop := fmt.Sprintf("%q at %d:%d", s.Err, s.Pos.Line, s.Pos.Col)
		for _, f := range frames {
			stop += " | " + f.Name
			for _, v := range f.Locals {
				stop += " " + v.Name + "=" + v.Value
			}
		}
		stops = append(stops, stop)
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if res, err := m.Call(ctx, "report", "ok", []any{int64(2), int64(4)}); err != nil || res != "OK: 3.0" || len(stops) != 0 {
		t.Fatalf("report = %v, %v; stops %q", res, err, stops)
	}
	_, err = m.Call(ctx, "report", "empty", []any{})
	var e *host.Error
	if !errors.As(err, &e) || e.Msg != "floating-point division by zero" {
		t.Fatalf("error: %v", err)
	}
	// The stop comes before the call ends, with the frames and variables
	// at the error (as at the start of the failing statement).
	want := `"floating-point division by zero" at 6:18 | report name="empty" xs=[] label="EMPTY" | avg xs=[] total=0 x`
	if len(stops) != 1 || !strings.HasPrefix(stops[0], want) {
		t.Errorf("stops: %q\nwant prefix %q", stops, want)
	}
}
