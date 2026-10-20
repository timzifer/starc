package starc_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/host"
)

func Example() {
	src := []byte(`
def fib(n):
    a, b = 0, 1
    for _ in range(n):
        a, b = b, a + b
    return a
`)
	wasm, err := starc.Compile("fib.star", src, nil)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	m, err := host.Load(ctx, wasm, nil)
	if err != nil {
		log.Fatal(err)
	}
	res, err := m.Call(ctx, "fib", 50)
	m.Close(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res)
	// Output: 12586269025
}

func ExampleCompile_command() {
	wasm, err := starc.Compile("hello.star", []byte(`print("Hello from WebAssembly!")`),
		&starc.Options{ExecModel: starc.Command})
	if err != nil {
		log.Fatal(err)
	}
	// Run it as a WASI command, e.g. with wasmtime or wazero.
	if _, err := host.Load(context.Background(), wasm, &host.Config{Stdout: os.Stdout}); err != nil {
		log.Fatal(err)
	}
	// Output: Hello from WebAssembly!
}
