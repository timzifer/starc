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

A module built with a profile contains the custom section `starc.debug`
(UTF-8 JSON), which maps the module back to the source:

```json
{
  "version": 1,
  "file": "bot.star",
  "functions": [
    {"name": "<toplevel>", "kind": "toplevel"},
    {"name": "sorted", "kind": "builtin"},
    {"name": "fib", "kind": "function", "line": 1, "col": 1, "wasm": "main.fib"}
  ],
  "positions": [[0, 0], [3, 14], [4, 9]]
}
```

`functions` lists the entries of the profile in the order of their
indices. For a function of the program, `line` and `col` locate its
`def` and `wasm` names the WebAssembly function that implements it in
the name section. `positions` maps the position ids that the module
records before each operation that can fail (see
[backtraces](abi.md#backtraces)) to `[line, col]`; id 0 is unknown.
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
