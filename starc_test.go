package starc_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/host"
)

func TestCompileVariants(t *testing.T) {
	src := "def double(x):\n    return 2 * x\n"
	path := filepath.Join(t.TempDir(), "double.star")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	a, err := starc.Compile("double.star", []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := starc.CompileReader("double.star", f, nil)
	if err != nil {
		t.Fatal(err)
	}
	// CompileFile names the file by its path, which appears in backtraces.
	c, err := starc.CompileFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := starc.Compile(path, []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) || string(c) != string(d) {
		t.Error("compilation is not deterministic across entry points")
	}
	if string(a[:4]) != "\x00asm" {
		t.Fatal("not a wasm binary")
	}
	m, err := host.Load(context.Background(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	if got, err := m.Call(context.Background(), "double", 21); err != nil || got != int64(42) {
		t.Errorf("double(21) = %v, %v", got, err)
	}
}

func TestCompileErrors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"x = (", "x.star:1:6: got end of file, want primary expression"},
		{"print(undefined)", "x.star:1:7: undefined: undefined"},
		{"load('m', 'x')", "cannot load m: no loader"},
		{"x = b'bytes'", "bytes literals are not supported"},
		{"x = 123456789012345678901234567890", "big integers are not supported"},
		{"s = set()", "undefined: set"},
		{"while True:\n    pass", "does not support while loops"},
	} {
		_, err := starc.Compile("x.star", []byte(c.src), nil)
		var el starc.ErrorList
		if !errors.As(err, &el) {
			t.Errorf("%q: got %v, want ErrorList", c.src, err)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %q, want %q", c.src, err, c.want)
		}
	}
}

func TestDialect(t *testing.T) {
	src := "def f(n):\n    return f(n - 1) if n > 0 else 0\nwhile False:\n    pass\nx = f(10)\n"
	if _, err := starc.Compile("d.star", []byte(src), &starc.Options{
		ExecModel: starc.Command,
		Dialect:   starc.Dialect{While: true, TopLevelControl: true, Recursion: true},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExportsOption(t *testing.T) {
	src := "def a():\n    pass\ndef b():\n    pass\n"
	wasm, err := starc.Compile("e.star", []byte(src), &starc.Options{Exports: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := host.ReadMeta(context.Background(), wasm)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Exports) != 1 || meta.Exports[0].Name != "b" {
		t.Errorf("exports = %+v", meta.Exports)
	}
	if _, err := starc.Compile("e.star", []byte(src), &starc.Options{Exports: []string{"c"}}); err == nil {
		t.Error("exporting an unknown function succeeded")
	}
	if _, err := starc.Compile("e.star", []byte("def starc_alloc():\n    pass\n"), nil); err == nil {
		t.Error("reserved export name accepted")
	}
}
