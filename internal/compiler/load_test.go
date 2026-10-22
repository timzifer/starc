package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// readArchive reads a test case of several files: each starts with a
// line "-- name --"; lines before the first one are a comment, which
// says "compile error" if starc rejects the program. The program is
// main.star.
func readArchive(t *testing.T, path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	name := "" // the comment
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "-- ") && strings.HasSuffix(s, " --") {
			name = strings.TrimSuffix(strings.TrimPrefix(s, "-- "), " --")
			files[name] = ""
			continue
		}
		files[name] += line
	}
	if _, ok := files["main.star"]; !ok {
		t.Fatalf("%s: no main.star", path)
	}
	return files
}

// interpretLoad runs main.star with the reference interpreter, loading
// modules as go.starlark.net's example loader does: each once, with its
// own thread, and with an error for a cycle.
func interpretLoad(files map[string]string) (string, error) {
	var out strings.Builder
	print := func(_ *starlark.Thread, msg string) { out.WriteString(msg + "\n") }
	predeclared := starlark.StringDict{"struct": starlark.NewBuiltin("struct", starlarkstruct.Make)}
	type entry struct {
		globals starlark.StringDict
		err     error
	}
	cache := map[string]*entry{}
	var load func(*starlark.Thread, string) (starlark.StringDict, error)
	load = func(_ *starlark.Thread, module string) (starlark.StringDict, error) {
		e, ok := cache[module]
		if e == nil {
			if ok {
				return nil, errors.New("cycle in load graph")
			}
			src, found := files[module]
			if !found {
				return nil, errors.New("no such module")
			}
			cache[module] = nil
			thread := &starlark.Thread{Name: "exec " + module, Load: load, Print: print}
			globals, err := starlark.ExecFileOptions(&testDialect, thread, module, src, predeclared)
			e = &entry{globals, err}
			cache[module] = e
		}
		return e.globals, e.err
	}
	thread := &starlark.Thread{Load: load, Print: print}
	thread.SetMaxExecutionSteps(1e8)
	_, err := starlark.ExecFileOptions(&testDialect, thread, "main.star", files["main.star"], predeclared)
	return out.String(), err
}

// executeLoad compiles main.star with a loader of files and runs it.
func executeLoad(rt wazero.Runtime, files map[string]string, fuel bool) (string, string, error, error) {
	loader := func(name string) ([]byte, error) {
		src, ok := files[name]
		if !ok {
			return nil, errors.New("no such module")
		}
		return []byte(src), nil
	}
	bin, cerr := Compile("main.star", []byte(files["main.star"]), Options{Command: true, Names: true, Dialect: testDialect,
		Fuel: fuel, Profile: fuel, Struct: true, Loader: loader})
	if cerr != nil {
		return "", "", nil, cerr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	mod, err := rt.InstantiateWithConfig(ctx, bin, wazero.NewModuleConfig().WithStdout(&out).WithStderr(&errb).WithName(""))
	if mod != nil {
		mod.Close(ctx)
	}
	return out.String(), errb.String(), err, nil
}

// TestLoadConformance runs the programs of testdata/load, which load
// modules, compiled and with the reference interpreter. A program that
// starc rejects at compile time (a cycle, a missing module or name) must
// fail in the interpreter as well.
func TestLoadConformance(t *testing.T) {
	cases, _ := filepath.Glob("../../testdata/load/*.txt")
	if len(cases) == 0 {
		t.Fatal("no testdata")
	}
	rt := newTestRuntime(t)
	for _, path := range cases {
		t.Run(filepath.Base(path), func(t *testing.T) {
			files := readArchive(t, path)
			want, werr := interpretLoad(files)
			for _, fuel := range []bool{false, true} {
				got, stderr, gerr, cerr := executeLoad(rt, files, fuel)
				if cerr != nil {
					if !strings.Contains(strings.ToLower(files[""]), "compile error") {
						t.Fatalf("unexpected compile error: %v", cerr)
					}
					if werr == nil {
						t.Fatalf("compile error %v, but the interpreter succeeded with output:\n%s", cerr, want)
					}
					t.Logf("compile error: %v; interpreter: %v", cerr, werr)
					return
				}
				if strings.Contains(strings.ToLower(files[""]), "compile error") {
					t.Fatal("compiled, but a compile error is expected")
				}
				compareRuns(t, fmt.Sprintf("fuel=%v: ", fuel), want, werr, got, stderr, gerr, false)
			}
		})
	}
}

// TestLoadDropsUnused checks that unused functions of loaded modules are
// not compiled.
func TestLoadDropsUnused(t *testing.T) {
	files := readArchive(t, "../../testdata/load/unused_defs.txt")
	bin, err := Compile("main.star", []byte(files["main.star"]), Options{Names: true, Dialect: testDialect,
		Loader: func(name string) ([]byte, error) { return []byte(files[name]), nil }})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"used": true, "helper": true, "unused_plain": false, "unused_effect": true} {
		if got := bytes.Contains(bin, []byte("load:lib.star."+name)); got != want {
			t.Errorf("%s compiled: %v, want %v", name, got, want)
		}
	}
}
