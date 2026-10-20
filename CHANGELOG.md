# Changelog

All notable changes to this project are documented in this file. The
format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

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
