# Module interface (ABI)

This document specifies the interface of WebAssembly modules produced by
starc. It is versioned through the `version` field of the `starc.meta`
custom section; this is version 1.

## WebAssembly features

Modules use WebAssembly 2.0 core features only: mutable globals,
sign-extension operators, non-trapping float-to-int conversions and bulk
memory (`memory.copy`, `memory.fill`). One linear memory and one function
table are defined; nothing else is imported besides WASI.

## Imports

Only functions from `wasi_snapshot_preview1` are imported, and only when
needed:

| Import | Used for |
|---|---|
| `fd_write` | `print` (stdout, fd 1) and error output in command modules (fd 2) |
| `proc_exit` | terminating a command module after an error |

## Execution models

### Reactor (default)

| Export | Signature | Description |
|---|---|---|
| `memory` | memory | linear memory |
| `_initialize` | `() -> ()` | runs the top-level statements, then freezes all global values; call exactly once |
| `starc_alloc` | `(n: i32) -> i32` | resets the per-call arena and returns a pointer to `n` bytes for call arguments |
| `starc_error` | `() -> i64` | location of the last error message (see below), or 0 |
| *function* | `(ptr: i32, len: i32) -> i64` | one export per exported top-level `def` |

By default every top-level `def` whose name does not start with `_` is
exported under its own name; `Options.Exports` / `-export` selects a
subset. The names `memory`, `_initialize`, `_start`, `starc_alloc` and
`starc_error` are reserved.

### Command

With `ExecModel: Command` (`-command`) the module exports `memory` and
`_start`. `_start` runs the program. On error, `Error: <message>` is
written to stderr and the module calls `proc_exit(1)`.

## Calling a function

1. Encode the arguments: a **tuple** of positional arguments, optionally
   followed by a **dict** of keyword arguments (string keys).
2. Call `starc_alloc(len)` to obtain a buffer and copy the encoded bytes
   into memory at the returned pointer.
3. Call the export with `(ptr, len)`. Passing `ptr = 0, len = 0` calls the
   function without arguments (and also resets the arena).
4. The result is a packed `i64`: the low 32 bits are a pointer, the high
   32 bits a length. The bytes at that location are the encoded return
   value. They stay valid until the next call of `starc_alloc` or of an
   exported function.

Each call starts from a fresh arena: memory allocated during a call is
reclaimed by the next call. This is safe because all global values are
frozen after initialization, so no state can survive a call except
through the (immutable) globals.

### Errors

A Starlark error (a call to `fail`, a type error, an index out of range,
an integer overflow, …) traps. Afterwards, `starc_error()` returns the
error message as a packed pointer/length pair of UTF-8 bytes. The instance
remains usable for further calls.

## Value encoding

Every value starts with a one-byte tag. Multi-byte numbers are
little-endian. Values need not be aligned.

| Tag | Type | Payload |
|---|---|---|
| 0 | `None` | — |
| 1 | `False` | — |
| 2 | `True` | — |
| 3 | `int` | 8 bytes, two's complement int64 |
| 4 | `float` | 8 bytes, IEEE 754 binary64 |
| 5 | `string` | u32 byte length, UTF-8 bytes |
| 6 | `list` | u32 count, then `count` values |
| 7 | `tuple` | u32 count, then `count` values |
| 8 | `dict` | u32 count, then `count` key/value pairs |

Only these types can cross the boundary; returning another value (for
example a function) is an error.

## Metadata

The custom section `starc.meta` contains UTF-8 JSON:

```json
{
  "version": 1,
  "model": "reactor",
  "exports": [{"name": "greet", "params": ["name", "punct"]}]
}
```

Parameter names of `*args` and `**kwargs` parameters are prefixed with
`*` and `**`.

## Example: JavaScript host

```js
import { readFile } from "node:fs/promises";
import { WASI } from "node:wasi";

const wasi = new WASI({ version: "preview1" });
const { instance } = await WebAssembly.instantiate(
  await readFile("fib.wasm"), wasi.getImportObject());
wasi.initialize(instance); // calls _initialize

const { memory, starc_alloc, fib } = instance.exports;

// args = (40,) encoded as: tuple tag, count 1, int tag, int64 40
const args = new Uint8Array(14);
const view = new DataView(args.buffer);
args[0] = 7; view.setUint32(1, 1, true);
args[5] = 3; view.setBigInt64(6, 40n, true);

const ptr = starc_alloc(args.length);
new Uint8Array(memory.buffer, ptr, args.length).set(args);
const packed = fib(ptr, args.length);
const res = new DataView(memory.buffer, Number(packed & 0xffffffffn));
console.log(res.getUint8(0), res.getBigInt64(1, true)); // 3 102334155n
```
