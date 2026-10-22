# Supported language

starc implements the [Starlark language](https://github.com/bazelbuild/starlark/blob/master/spec.md)
as defined by the reference implementation
[go.starlark.net](https://github.com/google/starlark-go), with the
restrictions and deviations listed below. The test suite in `testdata/`
runs every program with both implementations and compares the results.

## Supported

**Types:** `NoneType`, `bool`, `int` (64-bit), `float`, `string`, `list`,
`tuple`, `dict`, `set` (with `Dialect.Set`), `range`, `function`,
`builtin_function_or_method`, `struct`, `module` (predeclared host
modules) and opaque host values; see below.

**Expressions:** literals (string, int, float, list, tuple, dict),
arithmetic `+ - * / // %`, bitwise `& | ^ ~ << >>`, comparisons, `in` /
`not in`, `and` / `or` / `not`, conditional expressions, indexing and
slicing (including negative steps), attribute access to methods, calls
with positional, keyword, `*args` and `**kwargs` arguments, `lambda`,
list and dict comprehensions, `%` string formatting, dict union `|`.

**Statements:** assignment (including tuple/list unpacking and indexed
targets), augmented assignment (`+=` extends lists and `|=` updates dicts in
place), `if` / `elif` / `else`, `for`, `break`, `continue`, `pass`,
`return`, `def` (default values, keyword-only parameters, `*args`,
`**kwargs`) and nested functions with closures.

**Semantics:** values are frozen after module initialization; mutating a
frozen value, or a list or dict while it is being iterated, is an error.
Recursion is rejected at run time unless the `Recursion` option is set.
Calls nest at most 500 deep by default (`MaxCallDepth`), and memory is
limited to `MaxMemoryPages`; see [abi.md](abi.md#limits).

**Dialect options** (`starc.Dialect` / CLI flags): `While` (`while`
loops), `TopLevelControl` (`if`/`for`/`while` at top level),
`GlobalReassign`, `Recursion`, `Set` (the `set` type and builtin, as
`FileOptions.Set` in go.starlark.net).

**Host functions** (`starc.Options.Host` / `-host`) are predeclared
builtins implemented by the embedder. A dotted name such as `ctx.entity`
predeclares the module `ctx`, which behaves like a `starlarkstruct.Module`
in go.starlark.net: `type(ctx)` is `"module"`, `str(ctx)` is
`<module "ctx">`, members are accessible with `.`, `getattr`, `hasattr`
and `dir`, and modules are unhashable. See
[abi.md](abi.md#host-functions).

**Structs** behave like go.starlark.net's `starlarkstruct.Struct`: named,
immutable fields sorted by name, accessed with `.`, `getattr`, `hasattr`
and `dir`; `str(s)` is `struct(a = 1, b = "x")`; structs compare equal
field by field and are hashable if their fields are. A field that holds
a function is called without a receiver (`s.f(x)`). The host can pass
structs in and receive them (`abi.Struct`); the builtin `struct(**kwargs)`
is available with `Options.Struct` (`-struct`), since embedders of
go.starlark.net usually predeclare `starlarkstruct.Make` as `struct`.

**Opaque host values** (`abi.Opaque`) model custom value types of an
embedder, such as actions or wake-ups that a program constructs through
host functions and returns. The host chooses the type name (`type(v)`),
the string form (`str(v)`, `repr(v)`), whether the value is false in a
boolean context and whether it is hashable. The program cannot look into
the payload: two opaque values are equal if their types and payloads
are equal, and the value returns to the host unchanged. They have no
attributes and do not support ordering.

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
* **set:** `add`, `clear`, `difference`, `discard`, `intersection`,
  `issubset`, `issuperset`, `pop`, `remove`, `symmetric_difference`,
  `union`, `update`; operators `|`, `&`, `-`, `^` and the subset
  comparisons `<`, `<=`, `>`, `>=`

## Not supported (compile-time errors)

* `load` statements
* bytes literals and the `bytes` type
* integer literals that do not fit in 64 bits

## Floats and NaN

The bit pattern of a NaN produced by WebAssembly arithmetic differs
between platforms (and between operations), and it is observable: it
feeds the hash of dict keys and appears in the host encoding. starc
therefore canonicalizes NaN where it is created. Every float value is
boxed by a single runtime function, and that function replaces any NaN
(from arithmetic, `float()`, conversions or the host) with the positive
quiet NaN `0x7ff8000000000000`. This costs one comparison per float value
created. As a result, every NaN hashes, compares and encodes identically
on every platform, and NaN dict keys from different sources find each
other (NaN equals itself in Starlark).

With `Options.FiniteFloats` (`-finite-floats`), creating NaN or an
infinity, including `float("nan")`, `float("inf")`, overflowing
arithmetic and such values passed in by the host, fails with
`non-finite float value` instead.

## Deviations from go.starlark.net

* **Integers are 64-bit.** Arithmetic that overflows is a run-time error
  (`int overflow`) instead of producing a big integer. Hosts that use
  unsigned 64-bit IDs (`starlark.MakeUint64` in go.starlark.net) must keep
  them below 2^63, since `abi.Encode` rejects larger integers, or pass
  them as strings or opaque values.
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
* Case conversion and character classification (`upper`, `isalpha`,
  `isspace`, …) handle ASCII only. Splitting and stripping white space
  (`split()`, `strip()`, …) recognize Unicode white space, as in
  go.starlark.net.
* Error messages are similar but not always identical to the reference.
* Accessing a member that a predeclared host module lacks, such as
  `ctx.nope`, is a compile-time error rather than a run-time error.
* There is no garbage collector: memory allocated during initialization
  or during one exported call is reclaimed only when the next call starts.
  Very long-running calls can therefore use a lot of memory, up to the
  memory limit.
* Call depth is limited (500 by default), counting nested values that
  are printed, hashed or passed to the host as well; go.starlark.net
  limits only the Go stack.
