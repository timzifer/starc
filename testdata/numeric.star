# Float modulo is exact (no rounding of the quotient). Results are
# compared rather than printed where float formatting differs.
print(1e20 % 3.0, 1.0 % 0.1 == 0.09999999999999995, -1.0 % 0.1, 1.0 % -0.1, 7.5 % 2, -7.5 % 2, 5 % 2.5)
print(1e308 % 1e-308 == 3.498445546245627e-309, 5e-324 % 3e-324, 1e-310 % 3e-320, 3.0 % float("inf"), -3.0 % float("inf"))
print(123456789.0 % 0.001 == 0.0009999974300393183, 1152921504606846976.0 % 7.0, -1e20 % 3.0, 1e20 % -3.0)
---
# Mixed int/float comparisons are exact beyond 2^53.
print(9007199254740993 == 9007199254740992.0, 9007199254740993 > 9007199254740992.0)
print(9007199254740992.0 < 9007199254740993, 9223372036854775807 < 9223372036854775807.0)
m = -9223372036854775807 - 1
print(m == -9223372036854775808.0, m < -9223372036854775807.0, -9223372036854775807 > -9223372036854775808.0)
print(1 < 1.5, 2 > 1.5, -1 < -0.5, -1 > -1.5, 3 == 3.0, 3 != 3.000001)
print(sorted([9007199254740993, 9007199254740992.0, 9007199254740991]))
print(1 < float("inf"), 1 > float("-inf"), 5 < float("nan"), float("nan") > 5)
---
# Equal int and float keys hash alike over the whole int64 range.
print({9223372036854774784: "ok"}.get(9223372036854774784.0, "missing"))
print({-9223372036854775807 - 1: "ok"}.get(-9223372036854775808.0, "missing"))
print({1.0: "one"}[1], {2: "two"}[2.0], 9007199254740993 in {9007199254740992.0: 1})
---
# Slices with extreme steps and range slicing.
print([1, 2, 3][1::2147483647], [1, 2, 3][::-2147483648], [1, 2, 3][-1::-2147483647])
print("abcdef"[1::2147483647], (1, 2, 3)[2::-2], [1, 2, 3, 4, 5][4:0:-3], "abc"[::-1])
print(range(5)[1:3], range(10)[::3], range(10)[::-2], range(1, 20, 4)[1:-1], range(5)[3:1])
print(list(range(10, 0, -3)[1:]), range(0, 10, 2)[::-1], len(range(5)[::2]))
---
# range lengths do not overflow.
print(len(range(0, 9223372036854775807, 9223372036854775807)))
print(len(range(9223372036854775807, 0, -9223372036854775807)), len(range(5, 5)), len(range(0, -3, -1)))
---
# int() parsing.
print(int("-9223372036854775808"), int("9223372036854775807"), int("-0x8000000000000000", 16))
print(int("0x1f", 0), int("0o17", 0), int("0b101", 0), int("000", 0), int("-0", 0), int("z", 36))
print(int("0x10", 16), int("10", 16), int("+7"), int("0B11", 0), int("12", base = 0))
---
# Keyword-only parameters, and right-hand side before target operands.
def f(a, *, b = 10):
    return a + b

print(f(1), f(1, b = 2))
log = []

def t():
    log.append("target")
    return {}

def i():
    log.append("index")
    return "k"

def v():
    log.append("rhs")
    return 1

t()[i()] = v()
print(log)
---
# repr of cyclic containers.
a = []
a.append(a)
d = {}
d["self"] = d
d["l"] = a
b = [1, (a, 2)]
print(a, d, b, [a, a], str(a))
a.append(3)
print(a, repr(a))
---
# Floats are parsed and formatted with correct rounding.
print(float("0.12345678901234567") == 0.12345678901234567, 0.09999999999999995, float("0.09999999999999995"))
print(1.4593585424475087e-142, float("1.4593585424475087e-142"), 5e-324, float("4.9406564584124654e-324"))
print(float("2.2250738585072011e-308"), float("1.7976931348623157e308"), float("-0.0"), float("1e-400"))
print(float("0.1e1"), float("123456789012345678901234567890"), 1e21, 1e-5, 123456.0, 1234567.0, 0.0001)
---
# int() binds x and base like any function.
print(int(x = "12"), int(x = "ff", base = 16), int("10", base = 2), int(-9223372036854775808.0), int(-2.5))
---
# Ranges keep 64-bit lengths; slicing and indexing stay lazy.
r = range(4294967296)
print(len(r), list(r[:2]), r[-1], r[4294967293], r[4294967290:], len(r[::2]), bool(r))
print(range(0, 10, 3) == range(0, 12, 3), range(1, 1) == range(5, 2), r == range(4294967296), range(3) == range(1, 3))
print(len(range(-4294967296, 4294967296, 3)), 4294967295 in r, 4294967296 in r)
---
# Bound methods compare (and hash) by identity, as in Starlark.
x = [1]
m = x.append
d = {m: "m"}
print(d[m], m == m, x.append == x.append, x.append in d)
---
# Comparisons nested 9 deep are fine.
a = [1]
b = [1]
for i in range(8):
    a = [a]
    b = (b,)
    b = [b[0]]
print(a == b, a < [[2]], {"k": a} == {"k": b})
---
# NaN from different operations behaves the same.
inf = float("inf")
n = inf - inf
print(n, -n, n == n, n != n, n < 1.0, n > 1.0, n >= inf, [n] == [n], (n,) == (0.0 * inf,))
print(sorted([3.0, n, 1.0, -inf, 0.0 * inf]))
print(str(float("nan")), repr(0.0 * inf), inf / inf, abs(n), float("-nan"))
print(min([n, 1.0]), max([n, 1.0]), min([1.0, n]), max([1.0, n]))
d = {n: 1}
print(d[n], n in d, len(d))
print(int(1.0) if n != n else "never", type(n), bool(n))
