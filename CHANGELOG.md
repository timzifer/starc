# Changelog

All notable changes to this project are documented in this file. The
format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

* Ahead-of-time compiler from Starlark to standalone WebAssembly modules,
  with a generated runtime library and no embedded interpreter.
* Go API: `starc.Compile`, `starc.CompileReader`, `starc.CompileFile`.
* `starc` command with `build`, `run` and `version` subcommands.
* WASI reactor modules with exported functions and a binary value ABI
  (package `abi`), and WASI command modules.
* Package `host` for running compiled modules with wazero.
* Conformance test suite against go.starlark.net.
