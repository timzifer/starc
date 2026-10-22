# Module interface (ABI)

This document specifies the interface of WebAssembly modules produced by
starc. It is versioned through the `version` field of the `starc.meta`
custom section; this is version 1.

## WebAssembly features

Modules use WebAssembly 2.0 core features only: mutable globals,
sign-extension operators, non-trapping float-to-int conversions and bulk
memory (`memory.copy`, `memory.fill`). One linear memory and one function
table are defined; nothing is imported besides the functions below.

## Imports

Functions are imported only when the program needs them. From
`wasi_snapshot_preview1`:

| Import | Used for |
|---|---|
| `fd_write` | `print` (stdout, fd 1) and error output in command modules (fd 2) |
| `proc_exit` | terminating a command module after an error |

From `starc_host`, if the program calls host functions (see
[Host functions](#host-functions)), was built without WASI or is a debug
build:

| Import | Signature | Used for |
|---|---|---|
| *name* | `(ptr: i32, len: i32) -> i32` | one import per host function, under its dotted name, e.g. `ctx.entity` |
| `starc_result` | `(dst: i32) -> ()` | copies the pending outcome of the last host call to `dst` |
| `starc_print` | `(ptr: i32, len: i32) -> ()` | `print` without WASI: the UTF-8 text of one line, including the trailing newline |
| `starc_dbg` | `(pos: i32) -> ()` | a stop of a debug build at the statement with position id `pos` (see [debug.md](debug.md#debug-builds)) |

### Modules without WASI

With `Options.NoWASI` (`-no-wasi`) a module imports nothing outside
`starc_host`: `print` calls `starc_print`, and errors are reported only
through `starc_error`. This profile is available for reactor modules
only. `starc.meta` then contains `"no_wasi": true`, and package `host`
rejects such a module if it imports anything from WASI.

## Execution models

### Reactor (default)

| Export | Signature | Description |
|---|---|---|
| `memory` | memory | linear memory |
| `_initialize` | `() -> ()` | runs the top-level statements, then freezes all global values; call exactly once |
| `starc_alloc` | `(n: i32) -> i32` | resets the per-call arena and returns a pointer to `n` bytes for call arguments |
| `starc_error` | `() -> i64` | location of the last error message (see below), or 0 |
| `starc_error_code` | `() -> i32` | cause of the last error (see [Errors](#errors)), or 0 |
| `starc_frames` | `() -> i32` | number of frames of the last error (see [Backtraces](#backtraces)) |
| `starc_frame` | `(i: i32) -> i64` | name of frame `i` of the last error, as a packed pointer/length pair |
| `starc_frame_pos` | `(i: i32) -> i64` | position of frame `i`: line in the low 32 bits, then the column (20 bits) and the file index; -1 for a built-in |
| `starc_fuel_set` | `(n: i64) -> ()` | sets the remaining fuel budget (only with fuel metering, see [fuel.md](fuel.md)) |
| `starc_fuel` | `() -> i64` | returns the remaining fuel budget (only with fuel metering) |
| `starc_profile`, … | | the fuel profile (only with `Options.Profile`, see [debug.md](debug.md)) |
| `starc_dbg_*` | | inspection during a stop (only in debug builds, see [debug.md](debug.md#debug-builds)) |
| *function* | `(ptr: i32, len: i32) -> i64` | one export per exported top-level `def` |

By default every top-level `def` whose name does not start with `_` is
exported under its own name; `Options.Exports` / `-export` selects a
subset. The names `memory`, `_initialize`, `_start`, `starc_alloc`,
`starc_error`, `starc_error_code`, `starc_frames`, `starc_frame`,
`starc_frame_pos`, `starc_fuel_set`, `starc_fuel`, `starc_profile`,
`starc_profile_fuel`, `starc_profile_calls`, `starc_profile_reset` and
all names starting with `starc_dbg_` are reserved. The three frame
functions are missing in modules built with `Options.NoBacktrace`
(`-no-backtrace`).

### Command

With `ExecModel: Command` (`-command`) the module exports `memory` and
`_start`, plus the two fuel functions with fuel metering. `_start` runs
the program. On error, the backtrace (see [Backtraces](#backtraces)) is
written to stderr and the module calls `proc_exit` with the error code
as status (see [Errors](#errors)): 1 for a Starlark error, 2 for
exhausted fuel, 3–5 for the other limits. Without backtraces, only
`Error: <message>` is written.

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

The module decodes the argument buffer defensively: a value that does
not fit into `len` bytes, an invalid tag or trailing bytes are Starlark
errors (`abi: truncated value`, …), never reads beyond the buffer.

`starc_error_code()` tells the causes apart:

| Code | Cause | Message |
|---|---|---|
| 0 | no error | |
| 1 | an ordinary Starlark error | as raised |
| 2 | fuel exhausted ([fuel.md](fuel.md)) | `fuel exhausted` |
| 3 | memory limit reached | `out of memory` |
| 4 | call depth limit exceeded | `maximum call depth exceeded` |
| 5 | arguments or result too large | `arguments too large: …`, `result too large: …`, `value too large to pass to the host` |

### Backtraces

Unless built with `Options.NoBacktrace`, a module records where an error
occurred and the calls that led there, as go.starlark.net does in
`EvalError.CallStack`: the positions are those that go.starlark.net
reports. After an error, `starc_frames()` returns the number of frames,
outermost first, and `starc_frame(i)` and `starc_frame_pos(i)` describe
frame `i`. The outermost frame of an error in top-level code is
`<toplevel>`; that of an exported call is the called function. A frame
with position -1 is a built-in, such as `sorted` or a host function; the
file of all other positions is `file` in [`starc.meta`](#metadata), or in
a program that loads modules the entry of `files` that the file index
selects. A line of 0 means that the position is not known. The frames
stay valid until the next call of `starc_alloc` or of an exported
function. An error in decoding the arguments or encoding the result has
no frames.

A command module writes the backtrace in the format of go.starlark.net's
`EvalError.Backtrace`, with a built-in innermost frame folded into the
last line:

```
Traceback (most recent call last):
  prog.star:9:2: in <toplevel>
  prog.star:7:6: in g
  prog.star:4:9: in f
Error in fail: fail: boom 1
```

Recording costs a store before every operation that can fail and a few
instructions per call: about 5–8 % of the run time of call-heavy code and
4–5 % of the module size.

## Limits

Every module has hard limits, so that a call fails with a defined
error (and code) on every platform instead of running into the limits of
the WebAssembly engine. A failed call returns no partial result, and the
instance remains usable.

* **Memory:** the module declares a maximum memory size,
  `Options.MaxMemoryPages` (`-max-pages`), by default 65535 pages (one
  page short of 4 GiB). An allocation beyond it fails with `out of
  memory`. Memory use is deterministic, so the failure point is the same
  everywhere.
* **Call depth:** every call of a Starlark function counts toward a
  depth limit, `Options.MaxCallDepth` (`-max-depth`), by default 500. So
  does every level of recursion into nested values (`repr`/`str`,
  `hash`, freezing, encoding and decoding values that cross the host
  boundary). The default fits into the call stack of all common engines,
  including wazero's interpreter (2000 frames). Much larger limits can
  exhaust the stack of some engines before the limit is reached, which
  traps without a defined error.
* **Sizes:** `Options.MaxInputBytes` (`-max-input`) limits the encoded
  arguments of an exported call (checked by `starc_alloc`), and
  `Options.MaxResultBytes` (`-max-result`) the encoded result. Both are
  unlimited by default. An encoding that would reach 2 GiB always fails.

A command module that fails exits with the error code as its status.
The limits appear in `starc.meta`.

## Host functions

The embedder declares host functions at compile time (`Options.Host`,
`-host`) by name: `now`, or dotted like `ctx.entity`. To the program, a
plain name is a predeclared builtin. For a dotted name, the leading
names are predeclared values of type `module` whose members are host
functions and further modules:

```python
e = ctx.entity(id, detail = True)   # calls the import starc_host."ctx.entity"
```

A missing member such as `ctx.nope` is a compile error. Names must be
identifiers separated by dots; `None`, `True`, `False` and names starting
with `starc_` are reserved. Only host functions that the program
references are imported.

A call works like an export call in the other direction:

1. The module encodes the arguments as for an export call (a tuple of
   positional arguments, followed by a dict of keyword arguments if there
   are any) into its arena and calls the import with `(ptr, len)`.
2. The host decodes the arguments and runs the function. It keeps the
   outcome pending and returns its length: `n >= 0` for an encoded result
   of `n` bytes, or `^n` (that is, `-n - 1`) for a UTF-8 error message of
   `n` bytes.
3. The module reserves `n` bytes at `dst` and calls `starc_result(dst)`;
   the host copies the pending bytes there and discards them.
4. The module decodes the result, with the same checks as for call
   arguments, or raises the error message as a Starlark error.

The host must check that the buffers it reads and writes lie within
linear memory; package `host` aborts the call with an error otherwise.
Values that cannot be encoded (functions, modules, …) cannot be passed to
a host function.

## Value encoding

Every value starts with a one-byte tag. Multi-byte numbers are
little-endian. Values need not be aligned.

| Tag | Type | Payload |
|---|---|---|
| 0 | `None` | — |
| 1 | `False` | — |
| 2 | `True` | — |
| 3 | `int` | 8 bytes, two's complement int64 |
| 4 | `float` | 8 bytes, IEEE 754 binary64; a NaN is always `0x7ff8000000000000` |
| 5 | `string` | u32 byte length, UTF-8 bytes |
| 6 | `list` | u32 count, then `count` values |
| 7 | `tuple` | u32 count, then `count` values |
| 8 | `dict` | u32 count, then `count` key/value pairs |
| 9 | `struct` | u32 count, then `count` fields: u32 name length, name bytes, value |
| 10 | opaque | u8 flags (1: false in a boolean context, 2: unhashable), then the type name, the string form and the payload, each as u32 length and bytes |

Struct fields are sorted by name when decoded, and names must be
unique. Only these types can cross the boundary; returning another value
(for example a function, a set or a module) is an error.

## Metadata

The custom section `starc.meta` contains UTF-8 JSON:

```json
{
  "version": 1,
  "model": "reactor",
  "exports": [{"name": "greet", "params": ["name", "punct"]}],
  "host": ["ctx.entity", "ctx.now"],
  "no_wasi": true,
  "fuel": {"version": 1, "statement": 1, "call": 1, "iteration": 1,
           "runtime_iteration": 1, "bytes_per_unit": 16},
  "limits": {"memory_pages": 65535, "call_depth": 500, "result_bytes": 65536},
  "file": "greet.star"
}
```

Parameter names of `*args` and `**kwargs` parameters are prefixed with
`*` and `**`. `host` lists the imported host functions and is omitted if
there are none; `no_wasi` is omitted for modules that may import WASI.
`fuel` is the cost table of a module built with fuel metering
([fuel.md](fuel.md)) and is omitted otherwise. `limits` lists the
[limits](#limits); the size limits are omitted when unlimited. `file` is
the name of the source file in [backtraces](#backtraces) and is omitted
for a module built without them. A program that loads modules also has
`files`, the source files by index, the main file first.

Modules built with a profile or for debugging also contain the section
`starc.debug`,
which maps functions and positions to the source (see
[debug.md](debug.md#debug-information)).

## Example: JavaScript host

`js/host.mjs` implements all of this (see [js.md](js.md)); the example
below shows the bare protocol.

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
