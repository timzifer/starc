package host_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
	"github.com/timzifer/starc/internal/wasm"
)

var testDialectHost = syntax.FileOptions{}

func hostFuncs(names ...string) []starc.HostFunc {
	var hs []starc.HostFunc
	for _, n := range names {
		hs = append(hs, starc.HostFunc{Name: n})
	}
	return hs
}

func loadHost(t *testing.T, src string, opts *starc.Options, cfg *host.Config) *host.Module {
	t.Helper()
	wasm, err := starc.Compile("prog.star", []byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	m, err := host.Load(context.Background(), wasm, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })
	return m
}

const entityProg = `
START = ctx.now()

def info(id):
    e = ctx.entity(id, detail = True)
    return {"entity": e, "start": START, "now": ctx.now(), "half": half(3.0)}

def missing(id):
    return ctx.entity(id)

def unencodable():
    return half(len)
`

func TestHostFuncs(t *testing.T) {
	ctx := context.Background()
	var calls []string
	clock := 0.0
	funcs := map[string]host.Func{
		"ctx.now": func(context.Context, []any, *abi.Dict) (any, error) {
			clock += 1.5
			return clock, nil
		},
		"ctx.entity": func(_ context.Context, args []any, kwargs *abi.Dict) (any, error) {
			calls = append(calls, fmt.Sprintf("%v %v", args, kwargs))
			if args[0] != int64(7) {
				return nil, fmt.Errorf("no entity %v", args[0])
			}
			detail, _ := kwargs.Get("detail")
			return abi.NewDict("id", args[0], "pos", []any{1.5, -2.25}, "detail", detail), nil
		},
		"half": func(_ context.Context, args []any, _ *abi.Dict) (any, error) {
			f, ok := args[0].(float64)
			if !ok {
				return nil, fmt.Errorf("half: got %T", args[0])
			}
			return f / 2, nil
		},
	}
	m := loadHost(t, entityProg, &starc.Options{Host: hostFuncs("ctx.now", "ctx.entity", "half")},
		&host.Config{Funcs: funcs})
	if got, want := m.Meta().Host, []string{"ctx.entity", "ctx.now", "half"}; !reflect.DeepEqual(got, want) {
		t.Errorf("meta host = %v, want %v", got, want)
	}
	got, err := m.Call(ctx, "info", 7)
	if err != nil {
		t.Fatal(err)
	}
	want := abi.NewDict(
		"entity", abi.NewDict("id", int64(7), "pos", []any{1.5, -2.25}, "detail", true),
		"start", 1.5, "now", 3.0, "half", 1.5)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("info(7) = %#v", got)
	}
	if want := []string{"[7] &{[{detail true}]}"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %q", calls)
	}

	// A host error is a Starlark error, and the instance stays usable.
	_, err = m.Call(ctx, "missing", 8)
	var se *host.Error
	if !errors.As(err, &se) || se.Msg != "no entity 8" {
		t.Errorf("missing(8): %v", err)
	}
	_, err = m.Call(ctx, "unencodable")
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "cannot pass value of type builtin_function_or_method to the host") {
		t.Errorf("unencodable(): %v", err)
	}
	if _, err := m.Call(ctx, "info", 7); err != nil {
		t.Errorf("info after errors: %v", err)
	}
}

func TestHostFuncResultErrors(t *testing.T) {
	ctx := context.Background()
	src := "def f():\n    return bad()\n"
	m := loadHost(t, src, &starc.Options{Host: hostFuncs("bad")}, &host.Config{Funcs: map[string]host.Func{
		"bad": func(context.Context, []any, *abi.Dict) (any, error) { return struct{}{}, nil },
	}})
	_, err := m.Call(ctx, "f")
	var se *host.Error
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "bad: invalid result") {
		t.Errorf("f(): %v", err)
	}
}

func TestHostFuncMissing(t *testing.T) {
	wasm, err := starc.Compile("prog.star", []byte(entityProg), &starc.Options{Host: hostFuncs("ctx.now", "ctx.entity", "half")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = host.Load(context.Background(), wasm, &host.Config{Funcs: map[string]host.Func{
		"ctx.now": func(context.Context, []any, *abi.Dict) (any, error) { return 0.0, nil },
	}})
	if err == nil || !strings.Contains(err.Error(), `"ctx.entity"`) {
		t.Errorf("Load without ctx.entity: %v", err)
	}
}

func TestHostFuncNames(t *testing.T) {
	for _, tc := range []struct {
		names []string
		err   string
	}{
		{[]string{"a.b", "a"}, "both a function and a module"},
		{[]string{"a", "a.b"}, "both a function and a module"},
		{[]string{"a..b"}, "invalid host function name"},
		{[]string{"a.b "}, "invalid host function name"},
		{[]string{"for"}, "invalid host function name"},
		{[]string{"x.if"}, "invalid host function name"},
		{[]string{"1x"}, "invalid host function name"},
		{[]string{"None"}, "cannot be redefined"},
		{[]string{"starc_result"}, "reserved"},
	} {
		_, err := starc.Compile("p.star", []byte("pass\n"), &starc.Options{Host: hostFuncs(tc.names...)})
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%q: err = %v, want %q", tc.names, err, tc.err)
		}
	}
	// A missing member of a module is a compile error.
	_, err := starc.Compile("p.star", []byte("x = ctx.nope\n"), &starc.Options{Host: hostFuncs("ctx.now")})
	var el starc.ErrorList
	if !errors.As(err, &el) || el[0].Msg != "module ctx has no .nope field or method" || el[0].Pos.Line != 1 {
		t.Errorf("ctx.nope: %v", err)
	}
	// Duplicates are allowed.
	if _, err := starc.Compile("p.star", []byte("x = f()\n"), &starc.Options{Host: hostFuncs("f", "f")}); err != nil {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := starc.Compile("p.star", []byte("pass\n"), &starc.Options{ExecModel: starc.Command, NoWASI: true}); err == nil {
		t.Error("command without WASI compiled")
	}
}

// moduleProg exercises module values. It runs with go.starlark.net,
// where ctx is a starlarkstruct.Module, and compiled.
const moduleProg = `
print(type(ctx), str(ctx), repr(ctx.sub))
print(dir(ctx), dir(ctx.sub))
c = ctx
print(c.now(), c.sub.pair(1, k = "x"))
print(getattr(ctx, "now")(), getattr(ctx, "nope", 42))
print(hasattr(ctx, "now"), hasattr(ctx, "nope"), hasattr(ctx.sub, "pair"))
print(type(ctx.now), ctx.now, c.sub.pair)
print(ctx == c, ctx != c, ctx == ctx.sub, bool(ctx))
print([m for m in (ctx, ctx.sub)])
f = ctx.sub.pair
print(f(*[2], **{"k": None}))
print(top(), [top][0]())
def g(m):
    return m.now()
print(g(ctx))
`

// stubHost implements the host functions of moduleProg.
func stubHost(name string, args []any, kwargs *abi.Dict) (any, error) {
	switch name {
	case "ctx.now":
		return 2.5, nil
	case "ctx.sub.pair":
		k, _ := kwargs.Get("k")
		return []any{args[0], k}, nil
	case "top":
		return "top", nil
	}
	return nil, fmt.Errorf("unknown host function %s", name)
}

func TestModuleValues(t *testing.T) {
	names := []string{"ctx.now", "ctx.sub.pair", "top"}

	// Reference: go.starlark.net with the same stubs.
	builtin := func(name string) *starlark.Builtin {
		short := name[strings.LastIndex(name, ".")+1:]
		return starlark.NewBuiltin(short, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var gargs []any
			for _, a := range args {
				gargs = append(gargs, toGo(a))
			}
			kw := &abi.Dict{}
			for _, p := range kwargs {
				kw.Set(string(p[0].(starlark.String)), toGo(p[1]))
			}
			res, err := stubHost(name, gargs, kw)
			if err != nil {
				return nil, err
			}
			return toStarlark(res), nil
		})
	}
	sub := &starlarkstruct.Module{Name: "ctx.sub", Members: starlark.StringDict{"pair": builtin("ctx.sub.pair")}}
	predeclared := starlark.StringDict{
		"ctx": &starlarkstruct.Module{Name: "ctx", Members: starlark.StringDict{"now": builtin("ctx.now"), "sub": sub}},
		"top": builtin("top"),
	}
	var want strings.Builder
	thread := &starlark.Thread{Print: func(_ *starlark.Thread, msg string) { want.WriteString(msg + "\n") }}
	if _, err := starlark.ExecFileOptions(&testDialectHost, thread, "prog.star", moduleProg, predeclared); err != nil {
		t.Fatal(err)
	}

	funcs := map[string]host.Func{}
	for _, n := range names {
		funcs[n] = func(_ context.Context, args []any, kwargs *abi.Dict) (any, error) { return stubHost(n, args, kwargs) }
	}
	for _, noWASI := range []bool{false, true} {
		var out bytes.Buffer
		loadHost(t, moduleProg, &starc.Options{Host: hostFuncs(names...), NoWASI: noWASI},
			&host.Config{Funcs: funcs, Stdout: &out})
		if out.String() != want.String() {
			t.Errorf("NoWASI=%v: output mismatch\n--- go.starlark.net ---\n%s--- compiled ---\n%s", noWASI, want.String(), out.String())
		}
	}

	// Errors in both implementations.
	for _, src := range []string{"d = {ctx: 1}\n", "c = ctx\nc.nope\n", "c = ctx\nc.nope()\n", "ctx < ctx\n"} {
		thread := &starlark.Thread{}
		_, werr := starlark.ExecFileOptions(&testDialectHost, thread, "e.star", src, predeclared)
		wasm, err := starc.Compile("e.star", []byte(src), &starc.Options{Host: hostFuncs(names...)})
		if err != nil {
			t.Fatal(err)
		}
		_, gerr := host.Load(context.Background(), wasm, &host.Config{Funcs: funcs})
		var se *host.Error
		if werr == nil || !errors.As(gerr, &se) {
			t.Errorf("%q: reference error %v, compiled error %v", src, werr, gerr)
		}
	}
}

func toGo(v starlark.Value) any {
	switch v := v.(type) {
	case starlark.NoneType:
		return nil
	case starlark.Int:
		i, _ := v.Int64()
		return i
	case starlark.Float:
		return float64(v)
	case starlark.String:
		return string(v)
	}
	panic(fmt.Sprintf("toGo: %T", v))
}

func toStarlark(v any) starlark.Value {
	switch v := v.(type) {
	case nil:
		return starlark.None
	case int64:
		return starlark.MakeInt64(v)
	case float64:
		return starlark.Float(v)
	case string:
		return starlark.String(v)
	case []any:
		var xs []starlark.Value
		for _, x := range v {
			xs = append(xs, toStarlark(x))
		}
		return starlark.NewList(xs)
	}
	panic(fmt.Sprintf("toStarlark: %T", v))
}

func TestNoWASI(t *testing.T) {
	ctx := context.Background()
	src := "print('init', now())\ndef f(x):\n    print('f', x)\n    return x\n"
	wasm, err := starc.Compile("p.star", []byte(src), &starc.Options{Host: hostFuncs("now"), NoWASI: true})
	if err != nil {
		t.Fatal(err)
	}
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	cm, err := r.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, d := range cm.ImportedFunctions() {
		mod, name, _ := d.Import()
		imports = append(imports, mod+"."+name)
	}
	if want := []string{"starc_host.starc_print", "starc_host.starc_result", "starc_host.now"}; !sameSet(imports, want) {
		t.Errorf("imports = %v, want %v", imports, want)
	}
	meta, err := host.ReadMeta(ctx, wasm)
	if err != nil || !meta.NoWASI {
		t.Errorf("meta = %+v, %v", meta, err)
	}

	// A shared runtime without WASI suffices.
	var out bytes.Buffer
	m, err := host.Load(ctx, wasm, &host.Config{Runtime: r, Stdout: &out, Funcs: map[string]host.Func{
		"now": func(context.Context, []any, *abi.Dict) (any, error) { return int64(3), nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if got, err := m.Call(ctx, "f", "y"); err != nil || got != "y" {
		t.Errorf("f = %v, %v", got, err)
	}
	if out.String() != "init 3\nf y\n" {
		t.Errorf("output %q", out.String())
	}

	// Without host functions and print, nothing is imported but
	// starc_result, which is dropped as well.
	wasm, err = starc.Compile("p.star", []byte("def f(x):\n    return x\n"), &starc.Options{NoWASI: true})
	if err != nil {
		t.Fatal(err)
	}
	if cm, err = r.CompileModule(ctx, wasm); err != nil {
		t.Fatal(err)
	}
	if n := len(cm.ImportedFunctions()); n != 0 {
		t.Errorf("%d imports", n)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		if seen[x] == 0 {
			return false
		}
		seen[x]--
	}
	return true
}

// TestHostProtocolGuest checks that a malicious or broken guest cannot
// make the host misbehave: invalid buffers end in an error.
func TestHostProtocolGuest(t *testing.T) {
	ctx := context.Background()
	f := func(context.Context, []any, *abi.Dict) (any, error) { return "result", nil }
	for _, tc := range []struct {
		name string
		body func(m *wasm.Module, call, result *wasm.Import, args uint32) []wasm.Expr
		err  string
	}{
		{"bad args pointer", func(_ *wasm.Module, call, _ *wasm.Import, _ uint32) []wasm.Expr {
			return []wasm.Expr{wasm.Drop(wasm.Call(call, wasm.U32c(0xfffffff0), wasm.I32c(100)))}
		}, "argument buffer"},
		{"bad args encoding", func(_ *wasm.Module, call, _ *wasm.Import, args uint32) []wasm.Expr {
			return []wasm.Expr{wasm.Drop(wasm.Call(call, wasm.U32c(args), wasm.I32c(3)))}
		}, "truncated"},
		{"bad result pointer", func(_ *wasm.Module, call, result *wasm.Import, args uint32) []wasm.Expr {
			return []wasm.Expr{
				wasm.Drop(wasm.Call(call, wasm.U32c(args), wasm.I32c(5))),
				wasm.Call(result, wasm.U32c(0xfffffff0)),
			}
		}, "out of range"},
		{"result without call", func(_ *wasm.Module, _, result *wasm.Import, _ uint32) []wasm.Expr {
			return []wasm.Expr{wasm.Call(result, wasm.I32c(64))}
		}, "without a pending host result"},
		{"result twice", func(_ *wasm.Module, call, result *wasm.Import, args uint32) []wasm.Expr {
			return []wasm.Expr{
				wasm.Drop(wasm.Call(call, wasm.U32c(args), wasm.I32c(5))),
				wasm.Call(result, wasm.I32c(64)),
				wasm.Call(result, wasm.I32c(64)),
			}
		}, "without a pending host result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &wasm.Module{ExportMemory: "memory", MinPages: 1}
			call := m.AddImport(&wasm.Import{Module: "starc_host", Name: "f",
				Type: wasm.FuncType{Params: []wasm.ValType{wasm.I32, wasm.I32}, Results: []wasm.ValType{wasm.I32}}})
			result := m.AddImport(&wasm.Import{Module: "starc_host", Name: "starc_result",
				Type: wasm.FuncType{Params: []wasm.ValType{wasm.I32}}})
			args := m.StaticBytes([]byte{abi.TagTuple, 0, 0, 0, 0}, 1) // ()
			initF := m.AddFunc(wasm.NewFunc("init", nil))
			initF.Add(tc.body(m, call, result, args)...)
			m.Export("_initialize", initF)
			alloc := m.AddFunc(wasm.NewFunc("alloc", []wasm.ValType{wasm.I32}, wasm.I32))
			alloc.Add(wasm.I32c(1024))
			m.Export("starc_alloc", alloc)
			errF := m.AddFunc(wasm.NewFunc("err", nil, wasm.I64))
			errF.Add(wasm.I64c(0))
			m.Export("starc_error", errF)
			m.Customs = append(m.Customs, wasm.Custom{Name: "starc.meta",
				Payload: []byte(`{"version":1,"model":"reactor","exports":[],"host":["f"]}`)})
			bin, err := m.Encode()
			if err != nil {
				t.Fatal(err)
			}
			_, err = host.Load(ctx, bin, &host.Config{Funcs: map[string]host.Func{"f": f}})
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("err = %v, want %q", err, tc.err)
			}
		})
	}
}

// TestHostProtocolHost checks that the guest rejects malformed results
// from a host that does not use package host.
func TestHostProtocolHost(t *testing.T) {
	ctx := context.Background()
	wasm, err := starc.Compile("p.star", []byte("def g():\n    return f()\n"), &starc.Options{Host: hostFuncs("f"), NoWASI: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		n       int32
		payload []byte
		err     string
	}{
		{"string too long", 9, []byte{abi.TagStr, 0xe8, 3, 0, 0, 'a', 'b', 'c', 'd'}, "abi: truncated value"},
		{"list count too large", 5, []byte{abi.TagList, 0xff, 0xff, 0xff, 0xff}, "abi: truncated value"},
		{"dict count too large", 7, []byte{abi.TagDict, 2, 0, 0, 0, abi.TagNone, abi.TagNone}, "abi: truncated value"},
		{"trailing bytes", 2, []byte{abi.TagNone, abi.TagNone}, "abi: trailing bytes"},
		{"invalid tag", 1, []byte{42}, "abi: invalid value tag 42"},
		{"unhashable key", 11, []byte{abi.TagDict, 1, 0, 0, 0, abi.TagList, 0, 0, 0, 0, abi.TagNone}, "unhashable"},
		{"error", ^int32(4), []byte("oops"), "oops"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := wazero.NewRuntime(ctx)
			defer r.Close(ctx)
			_, err := r.NewHostModuleBuilder("starc_host").
				NewFunctionBuilder().WithFunc(func(int32, int32) int32 { return tc.n }).Export("f").
				NewFunctionBuilder().WithFunc(func(_ context.Context, mod api.Module, dst uint32) {
				mod.Memory().Write(dst, tc.payload)
			}).Export("starc_result").
				Instantiate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mod, err := r.InstantiateWithConfig(ctx, wasm, wazero.NewModuleConfig().WithStartFunctions("_initialize"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mod.ExportedFunction("g").Call(ctx, 0, 0); err == nil {
				t.Fatal("g() succeeded")
			}
			res, err := mod.ExportedFunction("starc_error").Call(ctx)
			if err != nil || res[0] == 0 {
				t.Fatalf("starc_error = %v, %v", res, err)
			}
			msg, _ := mod.Memory().Read(uint32(res[0]), uint32(res[0]>>32))
			if !strings.Contains(string(msg), tc.err) {
				t.Errorf("error %q, want %q", msg, tc.err)
			}
		})
	}
}

func TestHostFuncReentry(t *testing.T) {
	ctx := context.Background()
	var m *host.Module
	var inner error
	m = loadHost(t, "def f():\n    return cb()\n\ndef g():\n    return 1\n", &starc.Options{Host: hostFuncs("cb")},
		&host.Config{Funcs: map[string]host.Func{
			"cb": func(ctx context.Context, _ []any, _ *abi.Dict) (any, error) {
				_, inner = m.Call(ctx, "g")
				return "outer", nil
			},
		}})
	if got, err := m.Call(ctx, "f"); err != nil || got != "outer" {
		t.Errorf("f = %v, %v", got, err)
	}
	if inner == nil || !strings.Contains(inner.Error(), "own host functions") {
		t.Errorf("re-entrant call: %v", inner)
	}
	if got, err := m.Call(ctx, "g"); err != nil || got != int64(1) {
		t.Errorf("g after re-entry = %v, %v", got, err)
	}
}
