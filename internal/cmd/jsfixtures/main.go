// Command jsfixtures writes the expectations of the JavaScript parity
// test (js/test/parity.test.mjs) as JSON: programs compiled with the
// native compiler and run with package host (wazero), with the hash of
// each module, its output, results, errors and fuel. The JavaScript test
// compiles the same programs with the compiler built for JavaScript, runs
// them with js/host.mjs and requires the same bytes, results and fuel.
//
// Usage: go run ./internal/cmd/jsfixtures > js/testdata/parity.json
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
	"github.com/timzifer/starc/internal/jsapi"
)

// budget is the fuel budget of every run; it is large enough to be
// unlimited except where a case sets a smaller one.
const budget = 1 << 40

type call struct {
	Fn     string
	Args   []any
	Kwargs *abi.Dict
	Budget int64 // 0: budget
}

type testCase struct {
	Name     string
	Src      string // or File
	File     string
	Options  jsapi.Options
	InitFuel int64 // 0: budget
	Calls    []call
	Debug    bool // record every stop of a debugger (Step and Errors)
}

// The host functions of the cases; js/test/parity.test.mjs implements the
// same.
var funcs = map[string]host.Func{
	"add": func(_ context.Context, args []any, _ *abi.Dict) (any, error) {
		var s int64
		for _, a := range args {
			s += a.(int64)
		}
		return s, nil
	},
	"charge": func(ctx context.Context, args []any, _ *abi.Dict) (any, error) {
		host.ChargeFuel(ctx, args[0].(int64))
		return nil, nil
	},
	"refuse": func(_ context.Context, args []any, _ *abi.Dict) (any, error) {
		return nil, fmt.Errorf("refused: %v", args[0])
	},
	"ctx.name": func(_ context.Context, _ []any, kwargs *abi.Dict) (any, error) {
		if v, ok := kwargs.Get("upper"); ok && v == true {
			return "STARC", nil
		}
		return "starc", nil
	},
}

var cases = []testCase{
	{Name: "fib", File: "examples/fib.star", Options: jsapi.Options{Fuel: true},
		Calls: []call{{Fn: "fib", Args: []any{int64(40)}}, {Fn: "fib_table", Args: []any{int64(6)}}}},
	{Name: "wordcount", File: "examples/wordcount.star", Options: jsapi.Options{Fuel: true},
		Calls: []call{{Fn: "count_words", Args: []any{"b a b. c, A b", int64(2)}}}},
	{Name: "values", Options: jsapi.Options{Fuel: true, Struct: true, Dialect: jsapi.Dialect{Set: true}}, Src: `
def echo(*args, **kwargs):
    return [args, kwargs]

def make():
    return [None, True, False, 1 << 62, -(1 << 62) * 2, 2.5, -0.0, 1e300, float("nan"), float("inf"),
            "h\u00e9llo \u2713", (1,), {"k": [1, {2: 3}]}, struct(b = 2, a = 1), sorted(set([3, 1, 2]))]
`, Calls: []call{
		{Fn: "echo", Args: []any{nil, true, int64(math.MinInt64), 0.5, math.NaN(), "x", []any{int64(1), "y"},
			abi.Tuple{int64(1)}, abi.NewDict("a", int64(1), int64(2), abi.Tuple{}), abi.NewStruct("z", int64(1), "y", nil),
			&abi.Opaque{Type: "action", Repr: "<action>", Payload: []byte{1, 2, 3}, Falsy: true}},
			Kwargs: abi.NewDict("key", "value")},
		{Fn: "make"},
	}},
	{Name: "errors", Options: jsapi.Options{Fuel: true, MaxCallDepth: 8, Dialect: jsapi.Dialect{Recursion: true}}, Src: `
def inner(x):
    if x < 0:
        fail("negative: %d" % x)
    return 10 // x

def outer(x):
    return inner(x) + 1

def loop():
    n = 0
    for i in range(1000000):
        n += i
    return n

def deep(n):
    return deep(n + 1)

def keyed(xs):
    return sorted(xs, key = lambda x: 1 // x)
`, Calls: []call{
		{Fn: "outer", Args: []any{int64(5)}},
		{Fn: "outer", Args: []any{int64(-1)}},
		{Fn: "outer", Args: []any{int64(0)}},
		{Fn: "loop", Budget: 5000},
		{Fn: "deep", Args: []any{int64(0)}},
		{Fn: "keyed", Args: []any{[]any{int64(2), int64(0)}}},
		{Fn: "outer", Args: []any{"x"}},
	}},
	{Name: "host", Options: jsapi.Options{Fuel: true, Host: []jsapi.HostFunc{{Name: "add", Cost: 7}, {Name: "charge"},
		{Name: "refuse"}, {Name: "ctx.name"}}}, Src: `
def use(n):
    charge(n)
    return [add(1, 2, n), ctx.name(), ctx.name(upper = True)]

def refused():
    return refuse("now")
`, Calls: []call{{Fn: "use", Args: []any{int64(100)}}, {Fn: "refused"}}},
	{Name: "load", Options: jsapi.Options{Fuel: true, Profile: true, Files: map[string]string{
		"lib.star": "def scale(xs, k):\n    return [x * k for x in xs]\nprint(\"lib loaded\")\n",
	}}, Src: `
load("lib.star", "scale")

def run(xs):
    return sorted(scale(xs, 3), reverse = True)
`, Calls: []call{{Fn: "run", Args: []any{[]any{int64(3), int64(1), int64(2)}}}}},
	{Name: "command", Options: jsapi.Options{Command: true, Fuel: true, Dialect: jsapi.Dialect{TopLevelControl: true}}, Src: `
for i in range(3):
    print("line", i)
x = {"a": 1}["b"]
`},
	{Name: "init_fuel", Options: jsapi.Options{Command: true, Fuel: true, Dialect: jsapi.Dialect{TopLevelControl: true}},
		InitFuel: 300, Src: "for i in range(1000):\n    print(i)\n"},
	{Name: "debug", Options: jsapi.Options{Fuel: true, Debug: true, Struct: true}, Debug: true, Src: `
LIMIT = 2

def scale(xs, k):
    out = []
    for x in xs:
        out.append(x * k)
    return out

def run(n):
    base = [i for i in range(n)]
    bump = lambda v: v + LIMIT
    return sorted(scale(base, 3), key = bump)

def broken(d):
    s = struct(d = d)
    return s.d["missing"]
`, Calls: []call{{Fn: "run", Args: []any{int64(3)}}, {Fn: "broken", Args: []any{abi.NewDict("a", int64(1))}}}},
	{Name: "compile_error", Options: jsapi.Options{Files: map[string]string{"m.star": "x = (\n"}}, Src: `
load("m.star", "x")
y = undefined_name
`},
}

type errorResult struct {
	Msg       string `json:"msg"`
	Code      int    `json:"code"`
	Backtrace string `json:"backtrace"`
	ExitCode  int    `json:"exitCode,omitempty"`
}

type callResult struct {
	Fn       string       `json:"fn"`
	Payload  string       `json:"payload"`
	Budget   int64        `json:"budget"`
	Result   string       `json:"result,omitempty"`
	Error    *errorResult `json:"error,omitempty"`
	FuelUsed int64        `json:"fuelUsed"`
	Stdout   string       `json:"stdout"`
}

type profileEntry struct {
	Name  string `json:"name"`
	Fuel  int64  `json:"fuel"`
	Calls int64  `json:"calls"`
}

type fixture struct {
	Name          string         `json:"name"`
	Src           string         `json:"src"`
	Options       jsapi.Options  `json:"options"`
	CompileErrors []jsapi.Error  `json:"compileErrors"`
	WasmSHA256    string         `json:"wasmSha256,omitempty"`
	InitFuel      int64          `json:"initFuel"`
	InitStdout    string         `json:"initStdout"`
	InitStderr    string         `json:"initStderr"`
	InitError     *errorResult   `json:"initError,omitempty"`
	InitFuelUsed  int64          `json:"initFuelUsed"`
	Calls         []callResult   `json:"calls"`
	Profile       []profileEntry `json:"profile,omitempty"`
	Stops         []stopRecord   `json:"stops,omitempty"`
}

func toError(err error) *errorResult {
	// A command module reports its error on stderr and in its exit status.
	var exit *host.ExitError
	if errors.As(err, &exit) {
		return &errorResult{ExitCode: int(exit.Code)}
	}
	var e *host.Error
	if errors.As(err, &e) {
		return &errorResult{Msg: e.Msg, Code: int(e.Code), Backtrace: e.Backtrace()}
	}
	return &errorResult{Msg: err.Error()}
}

func run(c testCase) (fixture, error) {
	ctx := context.Background()
	src := c.Src
	if c.File != "" {
		b, err := os.ReadFile(c.File)
		if err != nil {
			return fixture{}, err
		}
		src = string(b)
		c.Options.Filename = filepath.Base(c.File)
	}
	f := fixture{Name: c.Name, Src: src, Options: c.Options, InitFuel: c.InitFuel, Calls: []callResult{}}
	if f.InitFuel == 0 {
		f.InitFuel = budget
	}
	opts, _ := json.Marshal(c.Options)
	wasm, errs := jsapi.Compile(src, string(opts))
	f.CompileErrors = errs
	if errs == nil {
		f.CompileErrors = []jsapi.Error{}
	}
	if wasm == nil {
		return f, nil
	}
	sum := sha256.Sum256(wasm)
	f.WasmSHA256 = hex.EncodeToString(sum[:])
	var out, errb bytes.Buffer
	cfg := &host.Config{Stdout: &out, Stderr: &errb, InitFuel: f.InitFuel, Funcs: funcs}
	if c.Debug {
		cfg.Debug = &host.Debugger{Step: true, Errors: true, Stop: func(ctx context.Context, s *host.Stop) {
			f.Stops = append(f.Stops, recordStop(ctx, s))
		}}
	}
	m, err := host.Load(ctx, wasm, cfg)
	f.InitStdout, f.InitStderr = out.String(), errb.String()
	if err != nil {
		f.InitError = toError(err)
		return f, nil
	}
	defer m.Close(ctx)
	if rest, err := m.Fuel(ctx); err == nil {
		f.InitFuelUsed = f.InitFuel - rest
	}
	if c.Options.Command {
		return f, nil
	}
	if c.Options.Profile {
		_ = m.ResetProfile(ctx)
	}
	for _, cl := range c.Calls {
		b := cl.Budget
		if b == 0 {
			b = budget
		}
		payload, err := abi.EncodeCall(cl.Args, cl.Kwargs)
		if err != nil {
			return f, err
		}
		out.Reset()
		if err := m.SetFuel(ctx, b); err != nil {
			return f, err
		}
		r := callResult{Fn: cl.Fn, Payload: hex.EncodeToString(payload), Budget: b}
		res, err := m.CallKw(ctx, cl.Fn, cl.Args, cl.Kwargs)
		if err != nil {
			r.Error = toError(err)
		} else {
			enc, err := abi.Encode(res)
			if err != nil {
				return f, err
			}
			r.Result = hex.EncodeToString(enc)
		}
		rest, _ := m.Fuel(ctx)
		r.FuelUsed, r.Stdout = b-rest, out.String()
		f.Calls = append(f.Calls, r)
	}
	if c.Options.Profile {
		prof, err := m.Profile(ctx)
		if err != nil {
			return f, err
		}
		for _, p := range prof {
			f.Profile = append(f.Profile, profileEntry{p.Name, p.Fuel, p.Calls})
		}
	}
	return f, nil
}

func main() {
	var out []fixture
	for _, c := range cases {
		f, err := run(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "jsfixtures: %s: %v\n", c.Name, err)
			os.Exit(1)
		}
		out = append(out, f)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"version": 1, "cases": out}); err != nil {
		fmt.Fprintln(os.Stderr, "jsfixtures:", err)
		os.Exit(1)
	}
}

// A stop of the debugger, as recorded for the debug cases.
type stopRecord struct {
	Pos     string        `json:"pos"`
	Err     string        `json:"err"`
	Frames  []frameRecord `json:"frames"`
	Globals []varRecord   `json:"globals"`
}

type frameRecord struct {
	Name   string      `json:"name"`
	Kind   string      `json:"kind"`
	Pos    string      `json:"pos"`
	Locals []varRecord `json:"locals"`
}

type varRecord struct {
	Name  string `json:"name"`
	Bound bool   `json:"bound"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

func vars(vs []host.Variable) []varRecord {
	out := []varRecord{}
	for _, v := range vs {
		out = append(out, varRecord{v.Name, v.Bound, v.Type, v.Value})
	}
	return out
}

func recordStop(ctx context.Context, s *host.Stop) stopRecord {
	r := stopRecord{Pos: s.Pos.String(), Err: s.Err, Frames: []frameRecord{}}
	frames, err := s.Stack(ctx)
	if err != nil {
		panic(err)
	}
	for _, f := range frames {
		r.Frames = append(r.Frames, frameRecord{f.Name, f.Kind, f.Pos.String(), vars(f.Locals)})
	}
	globals, err := s.Globals(ctx)
	if err != nil {
		panic(err)
	}
	r.Globals = vars(globals)
	return r
}
