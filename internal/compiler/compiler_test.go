package compiler

import (
	"bytes"
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

func runCommand(t *testing.T, src string) (string, error) {
	t.Helper()
	bin, err := Compile("test.star", []byte(src), Options{Command: true, Names: true})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	var out, errb bytes.Buffer
	_, err = r.InstantiateWithConfig(ctx, bin, wazero.NewModuleConfig().WithStdout(&out).WithStderr(&errb))
	return out.String() + errb.String(), err
}

func TestSmoke(t *testing.T) {
	out, err := runCommand(t, `
def fib(n):
    a, b = 0, 1
    for _ in range(n):
        a, b = b, a + b
    return a
print("hello", fib(10), [1, 2.5, "x"], {"a": (1,)}, None, True)
`)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out != "hello 55 [1, 2.5, \"x\"] {\"a\": (1,)} None True\n" {
		t.Fatalf("got %q", out)
	}
}
