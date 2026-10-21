# Contributing to starc

Thanks for your interest in improving starc! Bug reports, feature requests
and pull requests are welcome.

## Reporting issues

* Search the [existing issues](https://github.com/timzifer/starc/issues) first.
* For bugs, include a minimal Starlark program, the command or Go code you
  ran, and the expected and actual results. If the program behaves
  differently from [go.starlark.net](https://github.com/google/starlark-go),
  say so — that is always a bug unless it is listed in
  [docs/language.md](docs/language.md).
* Report security problems privately, see [SECURITY.md](SECURITY.md).

## Development setup

You need Go (see `go.mod` for the minimum version) and, for linting,
[golangci-lint](https://golangci-lint.run) v2.

```sh
git clone https://github.com/timzifer/starc
cd starc
make test      # unit and conformance tests
make race      # tests with the race detector
make cover     # coverage report in coverage.html (CI requires >= 80%)
make lint      # golangci-lint
```

## Project layout

| Path | Contents |
|---|---|
| `starc.go` | public compiler API |
| `cmd/starc` | command-line tool |
| `abi` | value encoding for calls into compiled modules |
| `host` | wazero-based host for running modules from Go |
| `internal/wasm` | WebAssembly module builder and binary encoder |
| `internal/rt` | runtime library, generated as WebAssembly code |
| `internal/compiler` | Starlark to WebAssembly compiler and the Starlark prelude |
| `testdata` | conformance programs |
| `docs` | ABI and language documentation |

## Tests

The most valuable tests are conformance tests: add Starlark programs to
`testdata/*.star` (chunks are separated by lines containing only `---`).
Each chunk runs with the reference interpreter and as compiled
WebAssembly, and the printed output must match. A chunk may end with an
error, in which case both implementations must fail after the same output.

Fuzz targets run nightly in CI and can be run locally, one at a time:

```sh
go test -run '^$' -fuzz '^FuzzPrograms$' -fuzztime 5m ./internal/compiler/
```

`FuzzPrograms` generates whole programs, `FuzzStrings` and `FuzzNumbers`
check built-ins on arbitrary values, all comparing against the reference
interpreter; `FuzzCompile` checks that arbitrary source never crashes the
compiler, and `FuzzDecode` and `FuzzDecodeCall` (in `abi`) check the
decoder. A failing
input is saved under `testdata/fuzz/` in the package and then runs as part
of `go test`; fix the bug and commit the input as a regression test.

## Pull requests

* Keep changes focused; one topic per pull request.
* Add tests for new features and bug fixes.
* Run `make lint test` before pushing; CI runs the same checks on Linux,
  macOS and Windows.
* Update `docs/` and `CHANGELOG.md` when you change behavior.
* By contributing you agree that your contribution is licensed under the
  [MIT License](LICENSE).

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).
