# Fuel metering

With `Options.Fuel` (`-fuel`), starc compiles an execution budget into
the module. Every unit of work costs fuel according to a fixed cost
table, and the program fails with `fuel exhausted` as soon as the budget
is used up. The count is part of the generated code, not of the
WebAssembly runtime: the same module with the same inputs consumes the
same fuel on every platform, engine and machine (wazero's interpreter
and compiler, Node.js, browsers), independent of JIT, CPU or wall-clock
time. This makes fuel suitable as a deterministic limit for untrusted
programs, for example in replicated simulations.

Without `Options.Fuel` a module contains no metering code at all.

## Budget

The remaining budget is a signed 64-bit counter in the module. It starts
at 2^63-1 (unlimited) and is controlled through two exports:

| Export | Signature | Description |
|---|---|---|
| `starc_fuel_set` | `(n: i64) -> ()` | sets the remaining budget |
| `starc_fuel` | `() -> i64` | returns the remaining budget |

Work subtracts its cost; when the counter drops below zero, the module
sets it to 0 and raises the error `fuel exhausted`.
`starc_error_code()` then returns 2, compared with 1 for other Starlark
errors. A command module writes `Error: fuel exhausted` to stderr and
exits with status 2 (its exit status is the error code). The instance remains usable: set a new budget and
call again. Nothing of a failed call survives it (see the arena model
in [abi.md](abi.md)).

The budget is not reset between calls. To give each call its own budget,
set it before the call. The fuel consumed is the budget minus the
remaining fuel afterwards. Set the budget before `_initialize` (or
`_start`) to limit the top-level code, which is metered as well,
including the initialization of the runtime library.

In Go, package `host` provides this as `Config.InitFuel` (the budget
for the top-level code), `Module.SetFuel` and `Module.Fuel`; the error
matches `errors.Is(err, host.ErrFuelExhausted)` and has
`Code == host.CodeFuel`. On the command line, `starc run -fuel` reports
the fuel used, and `-budget N` sets the budget for both the top-level
code and the call.

## Cost table

The table is versioned; `starc.meta` contains it:

```json
"fuel": {"version": 1, "statement": 1, "call": 1, "iteration": 1,
         "runtime_iteration": 1, "bytes_per_unit": 16, "host": {"ctx.entity": 10}}
```

| Work | Cost |
|---|---|
| each statement | `statement` (charged on entry to its statement list) |
| each call of a Starlark function (`def`, `lambda`) | `call` |
| each iteration of `for`, `while` (each test of the condition) and each `for` clause of a comprehension | `iteration` |
| each iteration of a loop inside the runtime library (comparing, hashing, searching, formatting, encoding, …) | `runtime_iteration` |
| allocating or copying memory in the runtime library | 1 per started `bytes_per_unit` bytes |
| each call of a host function | `call` plus its declared cost (`host`), plus whatever the host charges during the call |

Library functions written in Starlark (the prelude: `sorted`,
`str.split`, `str.replace`, `%` formatting, …) are metered like user
code. Together with the runtime rules, the cost of every operation is
proportional to the work it does: `sorted(xs)` costs about
n·log n, and copying, concatenating or converting values costs in
proportion to their size. Library calls are never a way to get free work.

Fuel never depends on the history of an instance: caches, such as the
hash a string remembers once computed, do not make later calls cheaper.
Clearing the previous call's arena at the start of a call is not charged
for the same reason; its size is bounded by allocations that the
previous call has already paid for.

The version identifies the fuel *behavior*, not just these numbers:
fuel depends on how the compiler translates a program and how the runtime
library implements an operation. Any change that alters the fuel of some
program increments the version, so embedders that persist or compare
fuel can detect it.

## Host functions

`HostFunc.Cost` declares a fixed cost per call at compile time, which
appears in the cost table. A host function can also charge a variable
amount while it runs, for example in proportion to the size of a query
result. It does so by lowering the budget with `starc_fuel`/
`starc_fuel_set`; in Go, call `host.ChargeFuel(ctx, n)` with the context
the function received. The module checks the budget when the host
function returns. Passing arguments to the host and decoding its result
are charged like any other runtime work, so large results cost in
proportion to their size.

## Overhead

Metering adds a subtraction and a comparison per statement list, loop
iteration, call, runtime loop iteration and allocation. In `make bench`
(the `BenchmarkCompiled` and `BenchmarkCompiledFuel` workload: dict
updates, string conversion, sorting and recursion), metering costs about
3% (median of 8 runs, 12.0 ms vs. 12.3 ms per run, wazero compiler,
Windows amd64). Tight loops over cheap operations pay relatively more.
