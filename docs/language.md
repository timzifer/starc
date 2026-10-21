# Supported language

starc implements the [Starlark language](https://github.com/bazelbuild/starlark/blob/master/spec.md)
as defined by the reference implementation
[go.starlark.net](https://github.com/google/starlark-go), with the
restrictions and deviations listed below. The test suite in `testdata/`
runs every program with both implementations and compares the results.

## Supported

**Types:** `NoneType`, `bool`, `int` (64-bit), `float`, `string`, `list`,
`tuple`, `dict`, `range`, `function`, `builtin_function_or_method`,
`module` (predeclared host modules, see below).

**Expressions:** literals (string, int, float, list, tuple, dict),
arithmetic `+ - * / // %`, bitwise `& | ^ ~ << >>`, comparisons, `in` /
`not in`, `and` / `or` / `not`, conditional expressions, indexing and
slicing (including negative steps), attribute access to methods, calls
with positional, keyword, `*args` and `**kwargs` arguments, `lambda`,
list and dict comprehensions, `%` string formatting, dict union `|`.

**Statements:** assignment (including tuple/list unpacking and indexed
targets), augmented assignment (`+=` extends lists in place), `if` /
`elif` / `else`, `for`, `break`, `continue`, `pass`, `return`, `def`
(default values, keyword-only parameters, `*args`, `**kwargs`) and nested
functions with closures.

**Semantics:** values are frozen after module initialization; mutating a
frozen value, or a list or dict while it is being iterated, is an error.
Recursion is rejected at run time unless the `Recursion` option is set.

**Dialect options** (`starc.Dialect` / CLI flags): `While` (`while`
loops), `TopLevelControl` (`if`/`for`/`while` at top level),
`GlobalReassign`, `Recursion`.

**Host functions** (`starc.Options.Host` / `-host`) are predeclared
builtins implemented by the embedder. A dotted name such as `ctx.entity`
predeclares the module `ctx`, which behaves like a `starlarkstruct.Module`
in go.starlark.net: `type(ctx)` is `"module"`, `str(ctx)` is
`<module "ctx">`, members are accessible with `.`, `getattr`, `hasattr`
and `dir`, and modules are unhashable. See
[abi.md](abi.md#host-functions).

### Built-in functions

`abs`, `all`, `any`, `bool`, `chr`, `dict`, `dir`, `enumerate`, `fail`,
`float`, `getattr`, `hasattr`, `hash`, `int`, `len`, `list`, `max`, `min`,
`ord`, `print`, `range`, `repr`, `reversed`, `sorted`, `str`, `tuple`,
`type`, `zip`.

### Methods

* **string:** `capitalize`, `codepoint_ords`, `codepoints`, `count`,
  `elem_ords`, `elems`, `endswith`, `find`, `format`, `index`, `isalnum`,
  `isalpha`, `isdigit`, `islower`, `isspace`, `istitle`, `isupper`, `join`,
  `lower`, `lstrip`, `partition`, `removeprefix`, `removesuffix`,
  `replace`, `rfind`, `rindex`, `rpartition`, `rsplit`, `rstrip`, `split`,
  `splitlines`, `startswith`, `strip`, `title`, `upper`
* **list:** `append`, `clear`, `extend`, `index`, `insert`, `pop`, `remove`
* **dict:** `clear`, `get`, `items`, `keys`, `pop`, `popitem`,
  `setdefault`, `update`, `values`

## Not supported (compile-time errors)

* `load` statements
* `set` and the `set` type
* bytes literals and the `bytes` type
* integer literals that do not fit in 64 bits

## Deviations from go.starlark.net

* **Integers are 64-bit.** Arithmetic that overflows is a run-time error
  (`int overflow`) instead of producing a big integer.
* `str(float)` and `float(string)` match the reference exactly (shortest
  round-trip formatting, correctly rounded parsing). `%e`/`%f` formatting
  supports magnitudes below about 9.2e18.
* `len`, indexing and slicing of a `range` support 64-bit lengths, but a
  range with more than 2^31-1 elements cannot be iterated or converted to
  a list; doing so is a run-time error.
* `%` formatting supports the conversions `s r d i x X o c e E f F g G`
  and `%%`, without width, precision or flags; `str.format` supports
  `{}`, `{0}`, `{name}`, `!s` and `!r` without format specifications.
* `elems()`, `codepoints()`, `elem_ords()` and `codepoint_ords()` return
  lists rather than iterable views.
* Case conversion and character classification (`upper`, `isalpha`, …)
  handle ASCII only.
* Error messages are similar but not always identical to the reference.
* Accessing a member that a predeclared host module lacks, such as
  `ctx.nope`, is a compile-time error rather than a run-time error.
* There is no garbage collector: memory allocated during initialization
  or during one exported call is reclaimed only when the next call starts.
  Very long-running calls can therefore use a lot of memory.
