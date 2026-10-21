# Changelog

All notable changes to this project are documented in this file. The
format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

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

* Encoding a value of 2 GiB or more for the host (such as a list that
  repeats a large string) fails with an error instead of overflowing
  the size computation.
* Memory sizes no longer wrap at 4 GiB, because modules declare at most
  65535 pages.
* `host.Load` releases the compiled module once it is instantiated.

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
