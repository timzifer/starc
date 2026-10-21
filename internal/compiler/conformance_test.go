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

var testDialect = syntax.FileOptions{Set: true, While: true, TopLevelControl: true, GlobalReassign: true}

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
	return newRuntime(t, wazero.NewRuntimeConfig())
}

func newRuntime(t testing.TB, cfg wazero.RuntimeConfig) wazero.Runtime {
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, cfg.WithCloseOnContextDone(true))
	t.Cleanup(func() { rt.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	return rt
}

// execute compiles src and runs it as a WASI command, with a deadline.
func execute(t *testing.T, rt wazero.Runtime, name, src string, fuel bool) (string, string, error) {
	bin, err := Compile(name, []byte(src), Options{Command: true, Names: true, Dialect: testDialect, Fuel: fuel})
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
				// Fuel metering (with an unlimited budget) must not change
				// the behavior.
				for _, fuel := range []bool{false, true} {
					got, stderr, gerr := execute(t, rt, c.name, c.src, fuel)
					compareRuns(t, fmt.Sprintf("fuel=%v: ", fuel), want, werr, got, stderr, gerr, false)
				}
			})
		}
	}
}

// compareRuns fails t if the output or the failure of a compiled program
// differs from that of the reference interpreter; label prefixes the
// messages. With overflowOK, a compiled program that stops because an int
// does not fit in 64 bits matches if its output so far is a prefix of the
// interpreter's, which went on with a big integer.
func compareRuns(t *testing.T, label, want string, werr error, got, stderr string, gerr error, overflowOK bool) {
	t.Helper()
	if gerr != nil {
		// Only a Starlark error matches: a trap, stack overflow or
		// deadline is a bug even where the interpreter fails.
		var exit *sys.ExitError
		if !errors.As(gerr, &exit) || exit.ExitCode() != 1 || !strings.HasPrefix(stderr, "Error: ") {
			t.Fatalf("%scompiled program did not fail with a Starlark error: %v (interpreter: %v)\nstderr: %s\noutput so far:\n%s",
				label, gerr, werr, stderr, got)
		}
		if overflowOK && (strings.Contains(stderr, "int overflow") ||
			strings.Contains(stderr, "cannot convert float to integer: out of range")) {
			if !strings.HasPrefix(want, got) {
				t.Fatalf("%soutput before int overflow differs\n--- interpreter ---\n%s--- compiled ---\n%s", label, want, got)
			}
			return
		}
	}
	switch {
	case werr != nil && gerr == nil:
		t.Fatalf("%sinterpreter failed with %v, compiled program succeeded with output:\n%s", label, werr, got)
	case werr == nil && gerr != nil:
		t.Fatalf("%scompiled program failed: %v\nstderr: %s\noutput so far:\n%s", label, gerr, stderr, got)
	}
	if got != want {
		t.Errorf("%soutput mismatch at %s\n--- interpreter ---\n%s--- compiled ---\n%s", label, firstDiff(want, got), want, got)
	}
}

// firstDiff describes the first line in which want and got differ.
func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range wl {
		if i >= len(gl) || wl[i] != gl[i] {
			g := "<missing>"
			if i < len(gl) {
				g = gl[i]
			}
			return fmt.Sprintf("line %d:\n  interpreter: %s\n  compiled:    %s", i+1, wl[i], g)
		}
	}
	return fmt.Sprintf("line %d: compiled output has extra lines", len(wl)+1)
}
