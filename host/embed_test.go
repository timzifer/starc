package host_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
)

// botProg is a program in the style of a game bot: context values are
// structs from the host, actions and wake-ups are opaque host values,
// parameters are declared with module members, and sets track state.
const botProg = `
DECL = {
    "speed": param.int(default = 3, min = 1, max = 9),
    "reach": param.int(default = 25),
}

def dist2(a, b):
    dx = a[0] - b[0]
    dy = a[1] - b[1]
    return dx * dx + dy * dy

def cycle(state):
    me = ctx.self()
    visited = set(state.get("visited", []))
    best = None
    for b in ctx.bodies():
        if b.id in visited:
            continue
        d = dist2(me.position, b.position)
        if best == None or d < best[0]:
            best = (d, b)
    actions = []
    if best:
        target = best[1]
        visited.add(target.id)
        actions.append(action.move(target.id, dv = best[0] / 10.0))
        if target.mass > me.metal_kg / 10:
            actions.append(action.mine(target.id))
    w = wake(ctx.now() + 5.0)
    print("me", me, me.name, type(me), dir(me))
    print("actions", actions, len(actions), [type(a) for a in actions], bool(w), type(w))
    print("same", actions[0] == action.move(best[1].id, dv = best[0] / 10.0), actions[0] == w, {w: "w"}[w])
    print("visited", visited, sorted(visited | set([0])), DECL, DECL["speed"])
    print("struct", struct(at = me.position, n = len(visited)), hasattr(me, "metal_kg"), getattr(me, "nope", None))
    return {"visited": sorted(visited), "actions": actions, "wake": w, "me": me, "note": struct(ok = True)}
`

var botHost = []string{"ctx.self", "ctx.bodies", "ctx.now", "action.move", "action.mine", "wake", "param.int"}

// botStub implements the host functions with abi values. It serves both
// the compiled module and, through conversion, go.starlark.net.
func botStub(name string, args []any, kw *abi.Dict) (any, error) {
	kwarg := func(k string, def any) any {
		if v, ok := kw.Get(k); ok {
			return v
		}
		return def
	}
	opaque := func(typ, repr string, data any) *abi.Opaque {
		payload, _ := json.Marshal(data)
		return &abi.Opaque{Type: typ, Repr: repr, Payload: payload}
	}
	switch name {
	case "ctx.self":
		return abi.NewStruct("id", int64(7), "name", "probe", "position", abi.Tuple{1.0, 2.0}, "metal_kg", 40.0), nil
	case "ctx.bodies":
		return []any{
			abi.NewStruct("id", int64(1), "position", abi.Tuple{10.0, 2.0}, "mass", 3.0),
			abi.NewStruct("id", int64(2), "position", abi.Tuple{2.0, 4.0}, "mass", 9.0),
			abi.NewStruct("id", int64(3), "position", abi.Tuple{-1.0, 2.0}, "mass", 1.0),
		}, nil
	case "ctx.now":
		return 100.0, nil
	case "action.move":
		dv := kwarg("dv", 0.0)
		return opaque("action", fmt.Sprintf("action(move, %v, dv=%v)", args[0], dv), []any{"move", args[0], dv}), nil
	case "action.mine":
		return opaque("action", fmt.Sprintf("action(mine, %v)", args[0]), []any{"mine", args[0]}), nil
	case "wake":
		return opaque("wake", fmt.Sprintf("wake(%v)", args[0]), args[0]), nil
	case "param.int":
		o := opaque("param", fmt.Sprintf("param.int(%v)", kwarg("default", int64(0))),
			[]any{kwarg("default", int64(0)), kwarg("min", nil), kwarg("max", nil)})
		o.Unhashable = true
		return o, nil
	}
	return nil, fmt.Errorf("unknown host function %s", name)
}

// hostValue is an opaque host value in go.starlark.net, as an embedder
// would define it for actions, wake-ups and parameters.
type hostValue struct{ o *abi.Opaque }

func (v hostValue) String() string       { return v.o.Repr }
func (v hostValue) Type() string         { return v.o.Type }
func (v hostValue) Freeze()              {}
func (v hostValue) Truth() starlark.Bool { return starlark.Bool(!v.o.Falsy) }
func (v hostValue) Hash() (uint32, error) {
	if v.o.Unhashable {
		return 0, fmt.Errorf("unhashable type: %s", v.o.Type)
	}
	h := fnv.New32a()
	h.Write([]byte(v.o.Type + "\x00"))
	h.Write(v.o.Payload)
	return h.Sum32(), nil
}

func (v hostValue) CompareSameType(op syntax.Token, y starlark.Value, _ int) (bool, error) {
	w := y.(hostValue)
	eq := v.o.Type == w.o.Type && bytes.Equal(v.o.Payload, w.o.Payload)
	switch op {
	case syntax.EQL:
		return eq, nil
	case syntax.NEQ:
		return !eq, nil
	}
	return false, fmt.Errorf("%s %s %s not implemented", v.Type(), op, w.Type())
}

func toStarlarkValue(v any) starlark.Value {
	switch v := v.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(v)
	case int64:
		return starlark.MakeInt64(v)
	case float64:
		return starlark.Float(v)
	case string:
		return starlark.String(v)
	case []any:
		var xs []starlark.Value
		for _, x := range v {
			xs = append(xs, toStarlarkValue(x))
		}
		return starlark.NewList(xs)
	case abi.Tuple:
		var xs starlark.Tuple
		for _, x := range v {
			xs = append(xs, toStarlarkValue(x))
		}
		return xs
	case *abi.Dict:
		d := starlark.NewDict(v.Len())
		for _, e := range v.Entries {
			_ = d.SetKey(toStarlarkValue(e.Key), toStarlarkValue(e.Value))
		}
		return d
	case *abi.Struct:
		sd := starlark.StringDict{}
		for _, f := range v.Fields {
			sd[f.Name] = toStarlarkValue(f.Value)
		}
		return starlarkstruct.FromStringDict(starlarkstruct.Default, sd)
	case *abi.Opaque:
		return hostValue{v}
	}
	panic(fmt.Sprintf("toStarlarkValue: %T", v))
}

func toAbiValue(v starlark.Value) any {
	switch v := v.(type) {
	case starlark.NoneType:
		return nil
	case starlark.Bool:
		return bool(v)
	case starlark.Int:
		i, _ := v.Int64()
		return i
	case starlark.Float:
		return float64(v)
	case starlark.String:
		return string(v)
	case *starlark.List:
		xs := []any{}
		for x := range v.Elements() {
			xs = append(xs, toAbiValue(x))
		}
		return xs
	case starlark.Tuple:
		xs := abi.Tuple{}
		for _, x := range v {
			xs = append(xs, toAbiValue(x))
		}
		return xs
	case *starlark.Dict:
		d := &abi.Dict{}
		for _, kv := range v.Items() {
			d.Set(toAbiValue(kv[0]), toAbiValue(kv[1]))
		}
		return d
	case *starlarkstruct.Struct:
		s := &abi.Struct{}
		for _, name := range v.AttrNames() {
			x, _ := v.Attr(name)
			s.Fields = append(s.Fields, abi.Field{Name: name, Value: toAbiValue(x)})
		}
		sort.Slice(s.Fields, func(i, j int) bool { return s.Fields[i].Name < s.Fields[j].Name })
		return s
	case hostValue:
		return v.o
	}
	panic(fmt.Sprintf("toAbiValue: %T", v))
}

// TestEmbedderProgram runs botProg with go.starlark.net and compiled, with
// the same stub host, and compares output and result.
func TestEmbedderProgram(t *testing.T) {
	ctx := context.Background()
	state := abi.NewDict("visited", []any{int64(3)})

	// Reference.
	modules := map[string]starlark.StringDict{}
	predeclared := starlark.StringDict{"struct": starlark.NewBuiltin("struct", starlarkstruct.Make)}
	for _, name := range botHost {
		short := name[strings.LastIndex(name, ".")+1:]
		b := starlark.NewBuiltin(short, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var gargs []any
			for _, a := range args {
				gargs = append(gargs, toAbiValue(a))
			}
			kw := &abi.Dict{}
			for _, p := range kwargs {
				kw.Set(string(p[0].(starlark.String)), toAbiValue(p[1]))
			}
			res, err := botStub(name, gargs, kw)
			if err != nil {
				return nil, err
			}
			return toStarlarkValue(res), nil
		})
		if i := strings.IndexByte(name, '.'); i >= 0 {
			if modules[name[:i]] == nil {
				modules[name[:i]] = starlark.StringDict{}
			}
			modules[name[:i]][short] = b
		} else {
			predeclared[name] = b
		}
	}
	for m, members := range modules {
		predeclared[m] = &starlarkstruct.Module{Name: m, Members: members}
	}
	var want strings.Builder
	thread := &starlark.Thread{Print: func(_ *starlark.Thread, msg string) { want.WriteString(msg + "\n") }}
	opts := &syntax.FileOptions{Set: true}
	globals, err := starlark.ExecFileOptions(opts, thread, "bot.star", botProg, predeclared)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := starlark.Call(thread, globals["cycle"], starlark.Tuple{toStarlarkValue(state)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantRes := toAbiValue(ref)

	// Compiled, without WASI, as a sandboxed embedder would build it.
	var hs []starc.HostFunc
	funcs := map[string]host.Func{}
	for _, name := range botHost {
		hs = append(hs, starc.HostFunc{Name: name})
		funcs[name] = func(_ context.Context, args []any, kw *abi.Dict) (any, error) { return botStub(name, args, kw) }
	}
	var out bytes.Buffer
	m := loadHost(t, botProg, &starc.Options{Host: hs, NoWASI: true, Struct: true, Dialect: starc.Dialect{Set: true}},
		&host.Config{Funcs: funcs, Stdout: &out})
	got, err := m.Call(ctx, "cycle", state)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != want.String() {
		t.Errorf("output mismatch\n--- go.starlark.net ---\n%s--- compiled ---\n%s", want.String(), out.String())
	}
	if !reflect.DeepEqual(got, wantRes) {
		t.Errorf("result mismatch\ngo.starlark.net: %#v\ncompiled:        %#v", wantRes, got)
	}
}

// TestHostValues checks structs and opaque values that cross the
// boundary in both directions.
func TestHostValues(t *testing.T) {
	ctx := context.Background()
	src := `
def echo(x):
    return x

def inspect(s, o):
    return [type(s), s.a, s.b.c, str(s), type(o), str(o), bool(o), o == o, hash_ok(o)]

def hash_ok(o):
    return len({o: 1}) == 1

def falsy(o):
    return "yes" if o else "no"

def key(o):
    return {o: 1}
`
	m := loadHost(t, src, nil, nil)
	s := abi.NewStruct("b", abi.NewStruct("c", "deep"), "a", int64(1))
	o := &abi.Opaque{Type: "handle", Repr: "<handle 42>", Payload: []byte{0, 42, 255}}
	for _, v := range []any{s, o, []any{s, o}, abi.NewStruct()} {
		if got, err := m.Call(ctx, "echo", v); err != nil || !reflect.DeepEqual(got, v) {
			t.Errorf("echo(%#v) = %#v, %v", v, got, err)
		}
	}
	got, err := m.Call(ctx, "inspect", s, o)
	want := []any{"struct", int64(1), "deep", `struct(a = 1, b = struct(c = "deep"))`, "handle", "<handle 42>", true, true, true}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("inspect = %#v, %v", got, err)
	}
	if got, err := m.Call(ctx, "falsy", &abi.Opaque{Type: "none", Falsy: true}); err != nil || got != "no" {
		t.Errorf("falsy = %v, %v", got, err)
	}
	_, err = m.Call(ctx, "key", &abi.Opaque{Type: "param", Unhashable: true})
	var se *host.Error
	if !errors.As(err, &se) || se.Msg != "unhashable type: param" {
		t.Errorf("key(unhashable): %v", err)
	}
	// Malformed structs from the host are errors.
	if _, err := m.Call(ctx, "echo", &abi.Struct{Fields: []abi.Field{{Name: "a"}, {Name: "a"}}}); err == nil ||
		!strings.Contains(err.Error(), "duplicate field") {
		t.Errorf("duplicate field: %v", err)
	}
}
