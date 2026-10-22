# Changelog

All notable changes to this project are documented in this file. The
format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

* Fuel profile: `Options.Profile` (`-profile`) records the fuel that
  each function of the program, built-in and host function uses, without
  changing the fuel consumption. `host.Module.Profile` and
  `ResetProfile` read and clear it, `starc run -profile` prints it.
  Profiled modules contain the custom section `starc.debug`, which maps
  functions and position ids to the source (`host.Module.DebugInfo`),
  and export `starc_profile`, `starc_profile_fuel`,
  `starc_profile_calls` and `starc_profile_reset`. See docs/debug.md.
* Backtraces: run-time errors report the position of the failing
  operation and the call stack, with the positions and frames of
  go.starlark.net (built-ins such as `sorted` appear as `<builtin>`).
  `host.Error` gains `Pos`, `CallStack` and `Backtrace()`, which formats
  them like `EvalError.Backtrace`; the CLI prints it. Reactor modules
  export `starc_frames`, `starc_frame` and `starc_frame_pos`, and
  `starc.meta` names the source `file`. `Options.NoBacktrace`
  (`-no-backtrace`) omits them.
* The `set` type with `Dialect.Set` (`-set`), as in go.starlark.net:
  `set()`, its methods, the operators `| & - ^` and subset comparisons.
* Structs as in `starlarkstruct`: attribute access, `getattr`,
  `hasattr`, `dir`, equality and hashing. The host exchanges them as
  `abi.Struct` (ABI tag 9); `Options.Struct` (`-struct`) provides the
  builtin `struct(**kwargs)`.
* Opaque host values (`abi.Opaque`, ABI tag 10) with a host-defined type
  name, string form, truth and hashability, for custom types such as
  actions.
* `Options.FiniteFloats` (`-finite-floats`) makes creating NaN or an
  infinity a Starlark error (`non-finite float value`).
* Hard limits: `Options.MaxMemoryPages` (`-max-pages`, default 65535)
  is declared in the module and allocating beyond it fails with
  `out of memory`. `Options.MaxCallDepth` (`-max-depth`, default 500)
  limits calls and recursion into nested values (`repr`, `hash`,
  freezing, encoding), so deep recursion fails the same way on every
  engine instead of overflowing its stack. `MaxInputBytes` and
  `MaxResultBytes` (`-max-input`, `-max-result`) limit the encoded
  arguments and result of a call. The limits appear in `starc.meta`.
* Error codes 3 (memory), 4 (call depth) and 5 (size), with
  `host.ErrOutOfMemory`, `host.ErrCallDepth` and `host.ErrTooLarge`.
* Fuel metering: `Options.Fuel` (`-fuel`) compiles a deterministic
  execution budget into the module. Statements, calls, loop iterations,
  runtime loops and memory traffic cost fuel per a versioned cost table
  (in `starc.meta`, see docs/fuel.md); top-level code is metered too.
  Exports `starc_fuel_set` and `starc_fuel`; an exhausted budget fails
  with `fuel exhausted`. `HostFunc.Cost` declares a cost per host call,
  and `host.ChargeFuel` charges during one. Package `host` adds
  `Config.InitFuel`, `Module.SetFuel`, `Module.Fuel` and
  `ErrFuelExhausted`; `starc run -fuel` reports the fuel used and
  `-budget N` limits it.
* `starc_error_code` export and `host.Error.Code` distinguish the cause
  of an error (Starlark error or fuel exhausted).
* Host functions: `Options.Host` (`-host`) declares functions that the
  embedder provides. They are imported from `starc_host` and called with
  the `abi` encoding; their errors become Starlark errors. Dotted names
  such as `ctx.entity` predeclare values of the new type `module`.
  Package `host` implements them through `Config.Funcs`.
* `Options.NoWASI` (`-no-wasi`) builds reactor modules that import
  nothing outside `starc_host`; `print` goes to the host import
  `starc_print`. `host.Load` verifies the imports of a module.
* `abi.DecodeCall` decodes encoded call arguments.
* `abi.MaxDepth`: the decoder rejects values nested more than 10000
  levels deep instead of exhausting the stack.
* `starc.meta` lists the imported host functions (`host`) and marks
  modules built without WASI (`no_wasi`).

### Changed

* A command module, and `starc run`, write the backtrace of an error to
  stderr instead of only `Error: <message>`.
* A command module that fails exits with its error code as status: 1
  for a Starlark error as before, 2 for exhausted fuel.
  `host.ExitError.Err` carries the cause.
* `host.Load` runs the start function of a command module itself, after
  setting the fuel budget.
* Modules check the bounds of encoded call arguments: truncated values,
  invalid tags and trailing bytes are Starlark errors instead of reads
  beyond the buffer.
* The error for a value that cannot cross the host boundary now reads
  "cannot pass value of type … to the host".
* `host.Module.Call` fails when a host function calls back into its own
  module, which would reset the arena of the suspended call.

### Fixed

* Containers with more than 2^28 elements fail with `out of memory`
  instead of wrapping their size computation, for example when decoding
  a huge encoded list.
* NaN is canonicalized to `0x7ff8000000000000` wherever a float is
  created, so NaN values hash, compare and encode identically on every
  platform, whichever operation produced them.
* Encoding a value of 2 GiB or more for the host (such as a list that
  repeats a large string) fails with an error instead of overflowing
  the size computation.
* Memory sizes no longer wrap at 4 GiB, because modules declare at most
  65535 pages.
* `host.Load` releases the compiled module once it is instantiated.
* Repeating an empty string, list or tuple (`"" * 3`) no longer traps
  with an integer division by zero.
* `repr` and everything that quotes strings escape like go.starlark.net:
  `\a \b \f \v` instead of `\x07`…, non-printable code points (such as
  U+00A0 or U+2028) as `\u`/`\U` escapes, and bytes that are not valid
  UTF-8 as `\x` escapes, so the result is always valid UTF-8. Printable
  means `strconv.IsPrint` of the Go release that builds starc.
* `hash` of strings with non-ASCII characters matches go.starlark.net:
  it combines code points rather than UTF-8 bytes.
* `count("")` and `replace("", x)` work on code points rather than
  bytes, so `replace` no longer splits multi-byte characters.
  `find("", start, end)` and `rfind` with `end < start` return `start`,
  as in go.starlark.net.
* `d |= x` updates the dict `d` in place, so other references to it see
  the change, as in go.starlark.net.
* `strip("")`, `lstrip("")` and `rstrip("")` strip whitespace like the
  methods without argument, and `None` as the argument is an error, as
  in go.starlark.net.
* `split()`, `rsplit()`, `strip()`, `lstrip()` and `rstrip()` without
  separator recognize Unicode white space (`unicode.IsSpace`, such as
  U+00A0 and U+3000), and stripping a cutset works on code points rather
  than bytes, as in go.starlark.net.
* `codepoints()`, `codepoint_ords()` and `ord` decode bytes that are not
  valid UTF-8 as U+FFFD, as in go.starlark.net; before, `codepoints()`
  grouped them by their leading byte and `codepoint_ords()` and `ord`
  failed.

## [0.1.1] - 2026-10-20

### Changed

* Iterating a range, or converting it to a list, fails with an explicit
  error when it has more than 2^31-1 elements. Before, the length was
  silently truncated.
* Ranges are unhashable, and bound methods compare by identity, as in
  go.starlark.net.

### Fixed

* `starc version` reports the module version for binaries installed with
  `go install`, instead of `dev`.
* `float(s)` is correctly rounded, and `str(x)` prints the shortest string
  that round-trips, matching go.starlark.net.
* `int()` binds `x` and `base` like function parameters: keyword arguments
  work, and missing, duplicate or unknown arguments are errors.
  `int(-9223372036854775808.0)` is accepted.
* `len`, indexing and slicing of ranges use 64-bit lengths.
* Method calls look up the method before evaluating the arguments.
* Recursive comparisons fail at Starlark's depth limit of 10 instead of
  overflowing the stack on cyclic values.

## [0.1.0] - 2026-10-20

### Added

* Ahead-of-time compiler from Starlark to standalone WebAssembly modules,
  with a generated runtime library and no embedded interpreter.
* Go API: `starc.Compile`, `starc.CompileReader`, `starc.CompileFile`.
* `starc` command with `build`, `run` and `version` subcommands.
* WASI reactor modules with exported functions and a binary value ABI
  (package `abi`), and WASI command modules.
* Package `host` for running compiled modules with wazero.
* Conformance test suite against go.starlark.net.

[Unreleased]: https://github.com/timzifer/starc/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/timzifer/starc/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/timzifer/starc/releases/tag/v0.1.0
