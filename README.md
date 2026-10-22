# starc

<p align="center">
  <img src="docs/images/gopher.png" alt="starc gopher" width="200">
</p>

[![CI](https://github.com/timzifer/starc/actions/workflows/ci.yml/badge.svg)](https://github.com/timzifer/starc/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/timzifer/starc/badges/coverage.json)](https://github.com/timzifer/starc/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/timzifer/starc.svg)](https://pkg.go.dev/github.com/timzifer/starc)
[![Release](https://img.shields.io/github/v/release/timzifer/starc?sort=semver)](https://github.com/timzifer/starc/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/timzifer/starc)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**starc** is an ahead-of-time compiler from [Starlark](https://github.com/bazelbuild/starlark)
to WebAssembly, written in Go.

It translates a Starlark program into a standalone `.wasm` module that
contains native WebAssembly code for the program, plus only those parts of
a small runtime library (memory management, values, operators, built-ins)
that the program actually uses. **No interpreter is embedded** — the
module runs anywhere WebAssembly and WASI preview 1 are available:
[wazero](https://wazero.io), wasmtime, Node.js, browsers (with a WASI shim).

```text
Starlark source ──parse/resolve──► AST ──compile──► WebAssembly module (+ used runtime functions)
```

> **Status:** early development. The core language and most of the
> standard library work and are tested against the reference
> implementation [go.starlark.net](https://github.com/google/starlark-go);
> see [docs/language.md](docs/language.md) for the supported subset and
> known deviations.

## Installation

```sh
go install github.com/timzifer/starc/cmd/starc@latest
```

or download a binary from the [releases page](https://github.com/timzifer/starc/releases).

## Command-line usage

```sh
starc examples/fib.star                 # writes examples/fib.wasm (reactor with exports)
starc -o out.wasm -command prog.star    # WASI command: runs top-level code in _start
cat prog.star | starc - > prog.wasm     # stdin to stdout

starc run examples/fib.star             # compile and run the top-level code
starc run examples/fib.wasm fib 40      # call an exported function (JSON arguments)
starc run examples/wordcount.star count_words "a b a" 1
```

Flags: `-o`, `-command`, `-export a,b`, `-no-names`, `-host ctx.entity,now`,
`-no-wasi`, `-fuel`, `-budget N` (run), the limits `-max-pages`,
`-max-depth`, `-max-input`, `-max-result`, `-finite-floats`, `-struct`,
and the dialect flags `-while`, `-toplevel-control`, `-global-reassign`,
`-recursion`, `-set`. Run `starc help` for details.

## Go package

The compiler is an importable package. It accepts a byte slice, any
`io.Reader` (for example an `*os.File`), or a path and returns the module
as a byte slice:

```go
import "github.com/timzifer/starc"

wasm, err := starc.Compile("prog.star", src, nil)          // []byte
wasm, err  = starc.CompileReader("prog.star", file, nil)   // io.Reader / *os.File
wasm, err  = starc.CompileFile("prog.star", &starc.Options{
	ExecModel: starc.Command,
	Dialect:   starc.Dialect{While: true},
})
```

Compile errors are returned as a `starc.ErrorList` with source positions.

To run modules from Go, use the `host` package (based on wazero):

```go
import "github.com/timzifer/starc/host"

m, err := host.Load(ctx, wasm, &host.Config{Stdout: os.Stdout})
defer m.Close(ctx)
res, err := m.Call(ctx, "fib", 50)                                  // int64(12586269025)
res, err  = m.CallKw(ctx, "greet", []any{"world"}, abi.NewDict("punct", "?"))
```

A run-time error is a `*host.Error` with the position of the failing
operation and the call stack, as go.starlark.net's `EvalError` reports
them; `Backtrace()` formats them the same way:

```text
Traceback (most recent call last):
  prog.star:7:6: in g
  prog.star:4:9: in f
Error in fail: fail: boom 1
```

`Options.NoBacktrace` (`-no-backtrace`) leaves out the positions and the
call stack, which cost about 5–8 % of the run time of call-heavy code.

### Host functions

Embedders can give programs their own functions: declare them when
compiling and implement them when loading. Dotted names become
predeclared modules. With `NoWASI`, the module imports nothing but these
functions, for sandboxes where WASI is not available:

```go
wasm, err := starc.Compile("bot.star", src, &starc.Options{
	Host:   []starc.HostFunc{{Name: "ctx.entity"}, {Name: "ctx.now"}},
	NoWASI: true,
})
m, err := host.Load(ctx, wasm, &host.Config{Funcs: map[string]host.Func{
	"ctx.entity": func(ctx context.Context, args []any, kw *abi.Dict) (any, error) {
		return abi.NewDict("id", args[0], "pos", []any{1.5, 2.0}), nil
	},
	"ctx.now": func(context.Context, []any, *abi.Dict) (any, error) { return 12.5, nil },
}})
```

The program calls `ctx.entity(7)`; an error returned by a host function
becomes a Starlark error.

Host functions can exchange structs (`abi.Struct`, read as `e.position`
in the program) and opaque values (`abi.Opaque`): custom types such as
actions that the program can hold, compare and return, but not look
into. Together with `set` (`Dialect.Set`) and `struct()` (`Options.Struct`),
programs written for go.starlark.net embedders compile unchanged.

### Fuel

With `Options.Fuel`, the module meters its own execution against a
budget, deterministically on every platform. Loops, recursion and
library calls all cost fuel, so untrusted programs always terminate:

```go
wasm, err := starc.Compile("bot.star", src, &starc.Options{Fuel: true})
m, err := host.Load(ctx, wasm, &host.Config{InitFuel: 100_000})
m.SetFuel(ctx, 50_000)
_, err = m.Call(ctx, "think")
errors.Is(err, host.ErrFuelExhausted)  // budget used up
left, _ := m.Fuel(ctx)
```

See [docs/fuel.md](docs/fuel.md) for the cost table.

With `Options.Profile` (`starc run -profile`), the module also records
the fuel used by each function, without changing its fuel consumption;
`host.Module.Profile` reads it. A debug build (`Options.Debug`,
`starc run -break LINE`) stops at statements for `host.Config.Debug` to
inspect the call stack and variables, again without changing results or
fuel, so recorded calls can be replayed and inspected. See
[docs/debug.md](docs/debug.md).

Hard limits complete the sandbox: memory (`MaxMemoryPages`), call depth
(`MaxCallDepth`, 500 by default) and the size of arguments and results
(`MaxInputBytes`, `MaxResultBytes`). Exceeding one is a defined error
(`host.ErrOutOfMemory`, `host.ErrCallDepth`, `host.ErrTooLarge`) on
every platform; see [docs/abi.md](docs/abi.md#limits).

## Module interface

By default, a module follows the WASI *reactor* model:

| Export | Purpose |
|---|---|
| `memory` | linear memory |
| `_initialize()` | runs the top-level statements, then freezes all globals |
| `starc_alloc(n) -> ptr` | resets the per-call arena and reserves `n` bytes for arguments |
| `<name>(ptr, len) -> i64` | calls a top-level function; returns `ptr \| len << 32` of the encoded result |
| `starc_error() -> i64` | message of the last Starlark error (after a trap) |
| `starc_frames()`, `starc_frame(i)`, `starc_frame_pos(i)` | call stack of the last error |

Values are exchanged with a compact tag-prefixed binary encoding
(package [`abi`](https://pkg.go.dev/github.com/timzifer/starc/abi)); a
`starc.meta` custom section lists the exported functions and their
parameters. See [docs/abi.md](docs/abi.md) for the full specification,
including an example for JavaScript hosts.

With `-command` (`ExecModel: starc.Command`) the module is a WASI
*command*: `_start` runs the program, `print` writes to stdout, and an
error is written to stderr with its backtrace and exits with status 1.

## How it works

* Parsing and name resolution reuse `go.starlark.net/syntax` and
  `go.starlark.net/resolve`.
* Each `def`/`lambda` becomes a WebAssembly function; closures capture
  cells, and calls go through a function table.
* The runtime library is generated by the compiler as WebAssembly code
  (package `internal/rt`); unused runtime functions are dropped at link
  time. Library functions that are easiest to express in Starlark
  (`sorted`, `str.split`, `%`-formatting, …) live in a Starlark prelude
  that is compiled together with the program, again only as far as it is
  used. A minimal module is about 8 KB.
* Memory is a bump-allocated arena. After initialization, the globals are
  frozen (as Starlark requires), so each exported call can safely reset the
  arena to the post-initialization state.

## Development

```sh
make test     # go test ./...
make race     # with the race detector
make cover    # coverage profile + HTML report (coverage.html), fails below 80%
make lint     # golangci-lint
make bench    # compiled code vs. the go.starlark.net interpreter
```

The conformance suite in `testdata/*.star` runs every program both with
the reference interpreter and as compiled WebAssembly and compares the
output. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
