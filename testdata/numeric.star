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
