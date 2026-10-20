# Security policy

## Supported versions

starc is in early development. Security fixes are made on the `main`
branch and included in the next release; only the latest release is
supported.

## Reporting a vulnerability

Please **do not open a public issue** for security problems. Instead,
report them privately through GitHub's
[private vulnerability reporting](https://github.com/timzifer/starc/security/advisories/new).

Include a description of the problem, the affected version or commit, and
a program or steps that reproduce it. You can expect an initial response
within a week. Once a fix is available, the advisory is published with
credit to the reporter unless you prefer to stay anonymous.

## Scope

starc compiles untrusted Starlark into WebAssembly. Relevant issues
include, for example:

* generated modules that read or write memory outside their own data
  (beyond what the WebAssembly sandbox prevents), or that behave
  differently from the source program in ways that break isolation
  assumptions, such as mutating frozen values;
* crashes or unbounded resource use of the compiler itself on malicious
  input;
* problems in the `host` package when handling untrusted modules.

The WebAssembly runtime executing a module (wazero, wasmtime, a browser)
is responsible for sandboxing; vulnerabilities in those runtimes should be
reported to their projects.
