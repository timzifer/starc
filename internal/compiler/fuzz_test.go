package compiler

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
)

// FuzzCompile checks that the compiler never panics and that every
// module it produces is valid WebAssembly.
func FuzzCompile(f *testing.F) {
	files, _ := filepath.Glob("../../testdata/*.star")
	for _, file := range files {
		for _, c := range readChunks(f, file) {
			f.Add(c.src)
		}
	}
	f.Add("def f(x, *a, y = [1], **k):\n    return lambda: (x, a, y, k)\n")
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	f.Fuzz(func(t *testing.T, src string) {
		bin, err := Compile("fuzz.star", []byte(src), Options{Dialect: testDialect})
		if err != nil {
			var el ErrorList
			if !errors.As(err, &el) {
				t.Fatalf("non-source error: %v", err)
			}
			return
		}
		cm, err := r.CompileModule(ctx, bin)
		if err != nil {
			t.Fatalf("invalid module: %v\n%s", err, src)
		}
		cm.Close(ctx)
	})
}
