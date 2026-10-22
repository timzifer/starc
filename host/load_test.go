package host_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/host"
)

func mapLoader(files map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		src, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("no such module")
		}
		return []byte(src), nil
	}
}

func TestLoadBasic(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"math.star": "load(\"util.star\", \"twice\")\nprint(\"math init\")\ndef sq(x):\n    return twice(x) * x // 2\nPI = 3\n",
		"util.star": "print(\"util init\")\ndef twice(x):\n    return 2 * x\n",
	}
	src := "load(\"math.star\", \"sq\", pi = \"PI\")\nload(\"util.star\", \"twice\")\nprint(sq(4), pi, twice(5))\ndef f(n):\n    return sq(n) + pi\n"
	wasm, err := starc.Compile("main.star", []byte(src), &starc.Options{Loader: mapLoader(files)})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m, err := host.Load(ctx, wasm, &host.Config{Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if want := "util init\nmath init\n16 3 10\n"; out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	if res, err := m.Call(ctx, "f", int64(3)); err != nil || res != int64(12) {
		t.Errorf("f(3) = %v, %v", res, err)
	}
}

func TestLoadPositions(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"lib.star": "SCALE = 3\nfactor = SCALE * 2\ndef boom(x):\n    return x // 0\n",
	}
	src := "load(\"lib.star\", \"boom\")\ndef f(x):\n    return boom(x)\n"
	wasm, err := starc.Compile("main.star", []byte(src), &starc.Options{Loader: mapLoader(files), Fuel: true, Profile: true, Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	var stop string
	m, err := host.Load(ctx, wasm, &host.Config{Debug: &host.Debugger{Step: true, Stop: func(ctx context.Context, s *host.Stop) {
		if s.Pos.Filename != "lib.star" || s.Pos.Line != 2 {
			return
		}
		frames, err := s.Stack(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stop = fmt.Sprintf("%s:%d", s.Pos.Filename, s.Pos.Line)
		for _, f := range frames {
			stop += fmt.Sprintf(" | %s %s:%d", f.Name, f.Pos.Filename, f.Pos.Line)
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	// The top level of the module runs inside the load statement.
	if want := "lib.star:2 | <toplevel> main.star:1 | <toplevel> lib.star:2"; stop != want {
		t.Errorf("stop: %q, want %q", stop, want)
	}
	_, err = m.Call(ctx, "f", int64(1))
	var e *host.Error
	if !errors.As(err, &e) {
		t.Fatalf("f: %v", err)
	}
	if bt := e.Backtrace(); !strings.Contains(bt, "main.star:3:16: in f") || !strings.Contains(bt, "lib.star:4:14: in boom") {
		t.Errorf("backtrace:\n%s", bt)
	}
	prof, err := m.Profile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prof {
		if p.Name == "boom" && (p.Pos.Filename != "lib.star" || p.Pos.Line != 3) {
			t.Errorf("boom: %+v", p.Pos)
		}
	}
	if fs := m.Meta().Files; len(fs) != 2 || fs[0] != "main.star" || fs[1] != "lib.star" {
		t.Errorf("meta files %q", fs)
	}
}

func TestLoadFuel(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"heavy.star": "N = 0\nfor i in range(1000):\n    N += i\n"}
	src := "load(\"heavy.star\", \"N\")\ndef f():\n    return N\n"
	wasm, err := starc.Compile("main.star", []byte(src), &starc.Options{Loader: mapLoader(files), Fuel: true,
		Dialect: starc.Dialect{TopLevelControl: true, GlobalReassign: true}})
	if err != nil {
		t.Fatal(err)
	}
	// The module's top-level code runs, and is metered, in _initialize.
	if _, err := host.Load(ctx, wasm, &host.Config{InitFuel: 500}); !errors.Is(err, host.ErrFuelExhausted) {
		t.Errorf("Load with a small budget: %v", err)
	}
	m, err := host.Load(ctx, wasm, &host.Config{InitFuel: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if rest, _ := m.Fuel(ctx); 1<<30-rest < 2000 {
		t.Errorf("initialization used %d fuel", 1<<30-rest)
	}
	if res, err := m.Call(ctx, "f"); err != nil || res != int64(499500) {
		t.Errorf("f() = %v, %v", res, err)
	}
}
