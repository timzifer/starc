# JavaScript: compiler and host

starc runs completely in JavaScript environments (browsers, Node.js,
Deno, Bun): the compiler itself is built as WebAssembly, and a small host
library loads and calls the modules it produces. A module compiled in the
browser is byte for byte the one that the native compiler produces, and
it gives the same results and uses the same fuel as with the Go host on a
server.

Try it in the [playground](https://timzifer.github.io/starc/), which is
built from [`web/`](../web) and deployed to GitHub Pages on every push to
`main`.

## Files

| File | Contents |
|---|---|
| `js/host.mjs` | runs modules: values, calls, host functions, fuel, errors, profile |
| `js/compiler.mjs` | starts the compiler and compiles programs |
| `js/index.d.ts` | TypeScript types of both |
| `starc.wasm` | the compiler: `GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o starc.wasm ./cmd/starc-js` |
| `wasm_exec.js` | Go's loader for it, from `$(go env GOROOT)/lib/wasm/` (of the Go release that built `starc.wasm`) |

`host.mjs` has no dependencies and does not need the compiler: modules
compiled elsewhere (for example by `starc` on a server) run with it alone.
Serve `.mjs` files as JavaScript and `.wasm` files as `application/wasm`.

## Compiling

```html
<script src="wasm_exec.js"></script>
<script type="module">
  import { loadCompiler } from "./compiler.mjs";

  const compiler = await loadCompiler("starc.wasm");
  const { wasm, errors } = compiler.compile(source, {
    filename: "bot.star",
    fuel: true,
    host: [{ name: "ctx.entity", cost: 10 }],
    files: { "lib.star": librarySource }, // modules for load statements
  });
  for (const e of errors) markers.add(e.file, e.line, e.col, e.msg);
</script>
```

The options are those of `starc.Options` (see the
[Go documentation](https://pkg.go.dev/github.com/timzifer/starc#Options)):
`filename`, `command`, `exports`, `noNames`, `host` (`{name, cost}`),
`noWasi`, `fuel`, `profile`, `maxMemoryPages`, `maxCallDepth`,
`maxInputBytes`, `maxResultBytes`, `finiteFloats`, `struct`,
`noBacktrace`, `dialect` (`while`, `topLevelControl`, `globalReassign`,
`recursion`, `set`) and `files`, the sources of the modules that `load`
statements name. `compile` returns the module, or `null` and the errors,
each with `file`, `line`, `col` (1-based; 0 if the error has no position)
and `msg`, ready for editor markers.

In Node.js, load `wasm_exec.js` with `require` (it defines `globalThis.Go`)
and pass the bytes of `starc.wasm` to `loadCompiler`.

### Size and load time

The compiler is about 10.8 MB, 2.5 MB compressed with gzip (GitHub Pages
and most servers compress it). Once downloaded, starting it is fast: in
Chrome on a desktop, compiling the WebAssembly takes about 35 ms, starting
the Go runtime 5 ms and compiling a first small program about 25 ms;
later compilations of the playground examples take 10–100 ms. The
download dominates; serve the file with long-lived caching.

## Running modules

```js
import { load, Tuple, StarlarkError, ErrorCode } from "./host.mjs";

const m = await load(wasm, {
  stdout: (text) => console.log(text),
  host: {
    "ctx.entity": (args, kwargs, call) => {
      call.chargeFuel(5); // like host.ChargeFuel in Go
      return { id: args[0], name: "door" };
    },
  },
  initFuel: 100_000, // budget of the top-level code
});

m.setFuel(50_000);
try {
  const res = m.call("think", 3, Tuple.of(1, 2));
  const res2 = m.callKw("greet", ["world"], { punct: "?" });
} catch (e) {
  if (e instanceof StarlarkError) {
    console.log(e.backtrace(), e.pos, e.code === ErrorCode.FUEL);
  }
}
console.log(m.fuel()); // remaining budget, a bigint
```

`load` instantiates the module and runs its top-level code; a command
module runs to completion, and a non-zero exit status is an `ExitError`.
The module stays usable after an error, as in Go.

**Values** are converted as follows:

| Starlark | to JavaScript | from JavaScript |
|---|---|---|
| `None` | `null` | `null`, `undefined` |
| `bool` | boolean | boolean |
| `int` | number, or bigint beyond ±2^53 | bigint, or a number that is a safe integer |
| `float` | number | any other number, or `new Float(x)` |
| `string` | string | string |
| `list` | array | array |
| `tuple` | `Tuple` (an array) | `Tuple` |
| `dict` | `Map` | `Map`, or a plain object (string keys) |
| `struct` | `Struct` (`fields` is a `Map`) | `Struct` |
| opaque | `Opaque` | `Opaque` |

Because JavaScript does not tell `2` from `2.0`, integral numbers are
passed as ints; wrap them in `Float` to pass a float. `encode`, `decode`,
`encodeCall` and `callRaw` work with the [encoded form](abi.md#value-encoding)
directly.

**Host functions** are synchronous. They receive the positional
arguments, the keyword arguments (a `Map`, or `null` if there are none) and
`call.chargeFuel(n)`. A thrown error becomes a Starlark error with its
message, as an error returned by a Go host function does.

**Errors** are `StarlarkError`s with `message`, `code` (`ErrorCode`), `pos`
(the failing operation in the program) and `callStack`; `backtrace()`
formats them as go.starlark.net does.

**Profiles** of modules built with `profile: true` are read with
`m.profile()` and cleared with `m.resetProfile()` (see
[debug.md](debug.md)). Debug builds load, but stopping them needs a
debugger, which only the Go host has so far.

## Same results as the server

`make js-test` checks that the JavaScript side matches the Go side. A Go
program (`internal/cmd/jsfixtures`) compiles a set of programs with the
native compiler and runs them with package `host` on wazero: calls with
every kind of value, errors with backtraces, fuel exhaustion, the call
depth limit, host functions with costs and `ChargeFuel`, `load`, profiles,
command modules and compile errors. The Node.js test compiles the same
programs with `starc.wasm`, requires the same module bytes (SHA-256), runs
them with `host.mjs` and requires the same output, results (encoded),
errors, backtraces, fuel and profile. CI runs it on every change.
