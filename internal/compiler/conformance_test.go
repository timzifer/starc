package compiler

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// chunk is one independent program from a testdata file. Chunks are
// separated by lines consisting of "---".
type chunk struct {
	name string
	src  string
}

func readChunks(t testing.TB, path string) []chunk {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var chunks []chunk
	line := 1
	for i, part := range strings.Split(string(data), "\n---\n") {
		// Preserve line numbers in positions by padding with newlines.
		chunks = append(chunks, chunk{
			name: filepath.Base(path) + "#" + string(rune('0'+i/10)) + string(rune('0'+i%10)),
			src:  strings.Repeat("\n", line-1) + part,
		})
		line += strings.Count(part, "\n") + 2
	}
	return chunks
}

var testDialect = syntax.FileOptions{While: true, TopLevelControl: true, GlobalReassign: true}

// interpret runs src with the reference interpreter.
func interpret(name, src string) (string, error) {
	var out strings.Builder
	thread := &starlark.Thread{Print: func(_ *starlark.Thread, msg string) { out.WriteString(msg + "\n") }}
	thread.SetMaxExecutionSteps(1e8)
	_, err := starlark.ExecFileOptions(&testDialect, thread, name, src, nil)
	return out.String(), err
}

// newTestRuntime returns a wazero runtime with WASI whose modules stop
// when their context is done.
func newTestRuntime(t testing.TB) wazero.Runtime {
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	t.Cleanup(func() { rt.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	return rt
}

// execute compiles src and runs it as a WASI command, with a deadline.
func execute(t *testing.T, rt wazero.Runtime, name, src string) (string, string, error) {
	bin, err := Compile(name, []byte(src), Options{Command: true, Names: true, Dialect: testDialect})
	if err != nil {
		t.Fatalf("%s: compile: %v", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	mod, err := rt.InstantiateWithConfig(ctx, bin, wazero.NewModuleConfig().
		WithStdout(&out).WithStderr(&errb).WithName(""))
	if mod != nil {
		mod.Close(ctx)
	}
	return out.String(), errb.String(), err
}

func TestConformance(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/*.star")
	if len(files) == 0 {
		t.Fatal("no testdata")
	}
	rt := newTestRuntime(t)
	for _, file := range files {
		for _, c := range readChunks(t, file) {
			t.Run(c.name, func(t *testing.T) {
				want, werr := interpret(c.name, c.src)
				got, stderr, gerr := execute(t, rt, c.name, c.src)
				if werr != nil {
					if gerr == nil {
						t.Fatalf("interpreter failed with %v, compiled program succeeded with output:\n%s", werr, got)
					}
					// Only a Starlark error matches: a trap, stack overflow or
					// deadline is a bug even where the interpreter fails.
					var exit *sys.ExitError
					if !errors.As(gerr, &exit) || exit.ExitCode() != 1 || !strings.HasPrefix(stderr, "Error: ") {
						t.Fatalf("interpreter failed with %v, compiled program did not fail with a Starlark error: %v\nstderr: %s",
							werr, gerr, stderr)
					}
				} else if gerr != nil {
					t.Fatalf("compiled program failed: %v\nstderr: %s\noutput so far:\n%s", gerr, stderr, got)
				}
				if got != want {
					t.Errorf("output mismatch\n--- interpreter ---\n%s--- compiled ---\n%s", want, got)
				}
			})
		}
	}
}
