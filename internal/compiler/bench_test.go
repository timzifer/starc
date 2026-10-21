package compiler

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

const benchSrc = `
def fib(n):
    return n if n < 2 else fib(n - 1) + fib(n - 2)

def work():
    d = {}
    for i in range(20000):
        d[str(i % 1000)] = d.get(str(i % 1000), 0) + i
    return fib(20) + len(d) + len(sorted(d.values()))

work()
`

var benchOpts = syntax.FileOptions{Recursion: true, TopLevelControl: true}

func BenchmarkInterpreter(b *testing.B) {
	for i := 0; i < b.N; i++ {
		thread := &starlark.Thread{}
		if _, err := starlark.ExecFileOptions(&benchOpts, thread, "b.star", benchSrc, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompiled(b *testing.B) { benchCompiled(b, Options{Command: true, Dialect: benchOpts}) }

// BenchmarkCompiledFuel measures the overhead of fuel metering.
func BenchmarkCompiledFuel(b *testing.B) {
	benchCompiled(b, Options{Command: true, Dialect: benchOpts, Fuel: true})
}

// BenchmarkCompiledNoBacktrace measures, against BenchmarkCompiled, the
// overhead of source positions and the shadow call stack.
func BenchmarkCompiledNoBacktrace(b *testing.B) {
	benchCompiled(b, Options{Command: true, Dialect: benchOpts, NoBacktrace: true})
}

func benchCompiled(b *testing.B, opts Options) {
	bin, err := Compile("b.star", []byte(benchSrc), opts)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	cm, err := r.CompileModule(ctx, bin)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, err := r.InstantiateModule(ctx, cm, wazero.NewModuleConfig().WithName(""))
		if err != nil {
			b.Fatal(err)
		}
		m.Close(ctx)
	}
}
