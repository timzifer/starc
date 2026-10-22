# Profiling and debug information

Because execution is deterministic, the same inputs always use the same
fuel. A fuel profile shows where that fuel goes, broken down by function,
for the program as written and not for the WebAssembly it compiles to.

## Fuel profile

`Options.Profile` (`-profile`) records the fuel that each function uses.
It requires fuel metering (`Options.Fuel`, implied by `-profile` on the
command line) and backtraces (not `NoBacktrace`). Profiling never charges
fuel: a profiled module uses exactly the same fuel as one without
profile and gives the same results, so a profile measured with a
profiled build applies to the release build.

```go
wasm, err := starc.Compile("bot.star", src, &starc.Options{Fuel: true, Profile: true})
m, err := host.Load(ctx, wasm, nil)
m.ResetProfile(ctx) // forget the top-level code
_, err = m.Call(ctx, "think")
prof, err := m.Profile(ctx)
for _, e := range prof {
	fmt.Println(e.Name, e.Kind, e.Pos, e.Fuel, e.Calls)
}
```

```
$ starc run -profile bot.star think
fuel used: 3650
fuel  %     calls  function
1783  48.9  1      sorted
1590  43.6  30     fib  bot.star:1:1
132   3.6   1      think  bot.star:7:1
82    2.2   30     lambda  bot.star:9:29
62    1.7   31     range
```

Each entry counts the fuel of the function's own code, not of the
functions it calls ("self" or "flat" fuel), and the number of calls.
There is an entry for

* `<toplevel>`: the top-level code, which runs when the module is
  loaded;
* every `def` and `lambda` of the program (kind `function`), with the
  position of its `def`;
* every built-in that the program uses (kind `builtin`), whether it is
  implemented natively or in the prelude. As in backtraces, what a
  built-in calls for itself counts toward the built-in, so `sorted` is
  one entry, and a function of the program that it calls, such as a key
  function, has its own;
* every host function (kind `host`), including the fuel that it charges
  with `HostFunc.Cost` or `host.ChargeFuel`.

The entries add up to the fuel that the top-level code and the calls
used. Only the work that the host requests between calls, such as
reserving memory for arguments (`starc_alloc`), is not attributed.
Errors end the call; the fuel up to the error counts toward the function
that failed.

The profile accumulates over calls until `ResetProfile`. Command
modules record it as well, but exit before the host can read it, so
`starc run -profile` without a function runs the program as a reactor.

## Debug information

A module built with a profile or for debugging contains the custom
section `starc.debug` (UTF-8 JSON), which maps the module back to the
source:

```json
{
  "version": 1,
  "file": "bot.star",
  "functions": [
    {"name": "<toplevel>", "kind": "toplevel"},
    {"name": "sorted", "kind": "builtin"},
    {"name": "fib", "kind": "function", "line": 1, "col": 1, "wasm": "main.fib"}
  ],
  "positions": [[0, 0], [3, 14], [4, 9]],
  "statements": [2]
}
```

`functions` lists the entries of the profile in the order of their
indices. For a function of the program, `line` and `col` locate its
`def` and `wasm` names the WebAssembly function that implements it in
the name section. `positions` maps the position ids that the module
records before each operation that can fail (see
[backtraces](abi.md#backtraces)) to `[line, col]`; id 0 is unknown.
`statements` lists the position ids of the statements at which a debug
build can stop; it is omitted otherwise.
`host.Module.DebugInfo` returns the section.

## Exports

Modules built with a profile export, besides those of
[abi.md](abi.md#reactor-default):

| Export | Signature | Description |
|---|---|---|
| `starc_profile` | `() -> i32` | number of entries |
| `starc_profile_fuel` | `(i: i32) -> i64` | fuel used by entry `i` |
| `starc_profile_calls` | `(i: i32) -> i64` | calls of entry `i` |
| `starc_profile_reset` | `() -> ()` | clears all entries |

`starc_fuel_set` distinguishes two uses: during a call, lowering the
budget is a charge by a host function, which counts toward it; otherwise
it sets a new budget, which the profile does not count.

## Debug builds

Deterministic execution makes replay debugging possible: record the
inputs of a call in production, then run the same call again in a debug
build, which stops at statements of the program so that the host can
inspect the call stack and the variables. `Options.Debug` (`-debug`)
compiles such a build; it requires backtraces.

```go
wasm, err := starc.Compile("bot.star", src, &starc.Options{Fuel: true, Debug: true})
m, err := host.Load(ctx, wasm, &host.Config{Debug: &host.Debugger{
	Lines: []int{12}, // or Step: true for every statement
	Stop: func(ctx context.Context, s *host.Stop) {
		frames, _ := s.Stack(ctx) // outermost first
		for _, f := range frames {
			fmt.Println(f.Name, f.Kind, f.Pos)
			for _, v := range f.Locals {
				fmt.Println("   ", v.Name, v.Type, v.Value)
			}
		}
		globals, _ := s.Globals(ctx)
		_ = globals
		s.SetStep(ctx, true) // stop at the next statement as well
	},
}})
```

```
$ starc run -break 7 fib.star fib 2
stop at fib.star:7:9
  fib  fib.star:7:9
    n = 2
    a = 0
    b = 1
    _ = 0
  globals
    fib = <function fib>
...
```

The debugger stops at the statements that start on one of `Lines`, or
at every statement with `Step`; `Stop.SetStep` switches between the two
during a stop. A statement is a line of a function body or of the top
level, including the `return` that forms the body of a `lambda`; a `for`
statement stops once, its body once per iteration. Built-ins, including
those implemented in the prelude, have no statements of their own but
appear in the call stack (kind `builtin` or `prelude`), for example
`sorted` between a function and its key function.

`Stack` returns the frames outermost first, each with its position (the
call of the next frame, or the statement of the stop) and, for
functions of the program and the top level, its local variables.
Variables that closures share are read through their cells. The locals
of a frame are those at the start of its current statement: a caller
shows its variables before the statement that made the call, and a
comprehension variable is unbound until the statement after the
comprehension. `Globals` returns the global variables. Values are
formatted with `repr`; one that cannot be formatted, such as a value
nested beyond the call depth limit, reads as `<error: message>` without
affecting the program.

**A stop leaves no trace.** The hooks never charge fuel, and after a
stop the module restores its fuel, heap and error state, so a debug
build gives the same results and uses the same fuel as a release build,
however often it stops and whatever the host reads. A test runs every
conformance program both ways, stopping at every statement and reading
the whole stack, and compares output, errors and fuel. Without a
`Debugger`, a hook only tests a flag; with one, every statement stores
the local variables and looks up its breakpoint. A debug build is larger
and slower than a release build and is meant for replays, not for
production.

Debug builds import `starc_host.starc_dbg(pos)`, which package `host`
provides, and export functions starting with `starc_dbg_` that
`host.Stop` calls; their names and signatures are not part of the
stable ABI.
