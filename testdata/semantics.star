# Slicing.
l = list(range(10))
print(l[::-1], l[8:2:-2], l[-3:], l[:-8:-3], l[100:], l[-100:2], "hello"[::-2], (1, 2, 3)[::-1])
print(l[2:8:3], "abcdef"[1:-1], [][:], ""[1:])
---
# Float formatting.
for x in [0.5, 2.5, 100.0, 1e21, 1e22, 123456789.0, 0.000123, 1.0/3, 2.0, 1e-7, 5e-324, -1.5e-10, 12345.678, 0.3, 0.1 + 0.2, 100000.0, 999999.0, 1000000.0]:
    print(x, repr(x), str(-x))
print(float("inf"), -float("inf"), float("nan") != float("nan"), int(1e15))
---
# Comparisons and equality.
print(1 == 1.0, 1 < 1.5, [1, 2] < [1, 2, 3], (2,) > (1, 9), "abc" < "abd", "" < "a", None == None, None != False)
print({"a": [1]} == {"a": [1]}, [1, [2]] == [1, [2]], (1, 2) == [1, 2], 2 in (1, 2), "b" in {"b": 1})
print(sorted([(2, "b"), (1, "z"), (2, "a")]), sorted([3.5, 1, 2]), sorted("bca".elems()), max("b", "a", "c"))
print(sorted([5, 3, 1, 4], key = lambda x: x % 2), sorted([1, 2, 3, 4], key = lambda x: x % 2, reverse = True))
---
# Dict ordering, deletion, re-insertion.
d = {}
for i in range(20):
    d[i] = i * i
for i in range(0, 20, 3):
    d.pop(i)
d[0] = "zero"
print(d, len(d), list(d)[:3])
e = {"x": 1, "y": 2}
e["x"] = 10
print(e, e.items()[0], {(1, 2): "t"}[(1, 2)])
---
print({1: 1, 1.0: 2})
---
# Closures over loop variables and nested scopes.
fns = []
for i in range(3):
    fns.append(lambda: i)
print([f() for f in fns])

def make(n):
    return [lambda k=k: k * n for k in range(n)]

print([f() for f in make(3)])

def counter():
    state = {"n": 0}
    def step():
        state["n"] += 1
        return state["n"]
    return step

s = counter()
print(s(), s(), s())
x = 10
def read_global():
    return x
print(read_global())
---
# Unpacking and augmented assignment targets.
a, (b, c) = 1, [2, 3]
[d, e] = "xy".elems()
print(a, b, c, d, e)
m = {"k": [1]}
m["k"] += [2]
m["k"][0] -= 5
print(m)
t = (1, 2)
t += (3,)
s = "ab"
s *= 2
print(t, s)
for k, v in {"p": 1, "q": 2}.items():
    print(k, v)
---
# String formatting corner cases.
print("%s|%r|%d|%i" % ("s", "r", 12, -3), "%5.2f" if False else "%f" % 2.5, "%e" % 0.000123)
print("%s" % (1,), "%s" % [1, 2], "%%d", "a%sb" % "-", "{}{{}}{}".format(1, 2), "{!r}".format("x"))
print(str(True), repr(None), repr([None, True, 1.5, "s", (1,), {}]))
print("x".join([]), ",".join(["1"]), "abc".replace("", "-"), "aXbXc".split("X", 1), "".split(","))
print("  a b  ".rsplit(None, 1), "a,b,c".rsplit(",", 5), "a\tb".split())
---
# Integer edge cases.
print(-9223372036854775807 - 1, 8, 7 // -2, -7 // -2, 7 % -2, 0 // 5, -1 >> 63, 1 << 62)
print(int("0x10", 16), int("-42"), int("  7".strip()), int("101", 2), int("z", 36), int("0b11", 0))
print(abs(-9223372036854775807), 3 * -4, -(5), +5, ~-1)
---
# Builtins.
print(len("héllo"), len([1]), len({}), len(range(0, 10, 4)), list(range(10, 0, -3)), range(3), range(1, 5, 2))
print(type(range(3)), str(range(2)), list(reversed("ab".elems())), enumerate([1], 5), zip())
print(min([4, 2, 8]), max(4, 2, 8, key = lambda v: -v), any([]), all([]))
print(hasattr("", "split"), hasattr([], "nope"), getattr([1], "append") != None, getattr({}, "zzz", "dflt"))
print(dir({})[:4], bool(0.0), bool("x"), int(False), float(True), str(1.0), repr("\x01"))
print(dict(x = 1, y = 2), dict({"a": 1}, b = 2), list({"k": 1}), tuple("ab".elems()))
---
# Dict union and misc operators.
a = {"x": 1, "y": 2}
b = {"y": 3, "z": 4}
print(a | b, b | a, a)
print([1, 2] + [3], (1,) + (2, 3), "ab" + "cd", [0] * 3, 3 * (1, 2), -2 * [1])
print("" * 3, [] * 2, 5 * (), "" * -1, [] * 9223372036854775807)
print(1 if [] else 2, [] or {} or "last", "first" and 0)
---
# |= updates a dict in place, like += extends a list.
a = {"x": 1}
b = a
b |= {"y": 2}
c = {"z": 0}
c |= c
b["w"] = 3
print(a, b, c)
d = {"k": {"p": 1}}
inner = d["k"]
d["k"] |= {"q": 2}
print(d, inner)
s = set([1])
t = s
t |= set([2])
n = 6
n |= 1
print(s, t, n)
l = [1]
m = l
m += (2, 3)
print(l, m)
---
# |= on a dict being iterated fails.
d = {"a": 1}
for k in d:
    d |= {}
