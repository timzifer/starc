# Backtraces of run-time errors, compared with go.starlark.net: the frames,
# their positions and the failing built-in.
def f(x):
    fail("boom", x)

def g():
    f(1)

g()
---
x = [1, 2]
print(x[5])
---
d = {"a": 1}
def get(k):
    return d[k]
get("b")
---
def add(a, b):
    return a + b
add(1,
    "x")
---
def outer():
    def inner(y):
        return -y
    return inner("s")
outer()
---
x = 1
x.foo
---
"abc".nope()
---
def key(v):
    return v + "!"
sorted([3, 1], key = key)
---
def key(v):
    return {}[v]
max([1, 2], key = key)
---
print("abc".index("z"))
---
def h(s):
    return s.split(1)
h("a b")
---
[1, 2].remove(3)
---
{}.pop("k")
---
def f():
    return [y * 2 for y in [1, "a", 3] if y > 0]
f()
---
def f():
    return {k: v for k, v in [(1, 2), ([], 3)]}
f()
---
a, b, c = (1, 2)
---
def f():
    for x, y in [(1, 2), (3,)]:
        pass
f()
---
x = [1, 2, 3]
print(x[1:"a"])
---
s = "abc"
print(s[::0])
---
def f(**kw):
    return kw
f(**[1])
---
def f(*args):
    return args
f(*1)
---
def f(a, b):
    pass
f(1)
---
def f(a):
    return a
f(1, c = 2)
---
l = lambda x: x // 0
l(1)
---
def f(x):
    x += "a"
    return x
f(1)
---
d = {}
d[[1]] = 2
---
d = {"a": [1]}
d["a"] += 1
---
def f():
    return int("x")
f()
---
def f(n):
    if n == 0:
        return 1 // n
    return f(n - 1)
f(3)
---
def f():
    for i in 5:
        pass
f()
---
x = {[1]: 2}
---
def f():
    return undefined_local
    undefined_local = 1
f()
---
def f():
    return len(1)
def g():
    return [f() for _ in range(2)]
g()
---
print(not None + 1)
---
print("%d" % "x")
---
def f(n):
    return "{}".format(n) + n
f(1)
---
def f(x):
    return x.get("a")
f(1)
---
r = range(5)
print(r[10])
---
def cb(x):
    fail("in callback", x)
def run():
    return sorted([2, 1], key = cb)
run()
---
x = struct(a = 1)
print(x.b)
---
def f():
    return set([1]) | [2]
f()
---
print(enumerate(1))
---
print(zip([1], 2))
---
def f(a, b = 1, *, c):
    pass
f(1)
---
# go.starlark.net adds adjacent literals at compile time.
print(([] + [])[::0])
---
x = "a"
print(("b" + "c" + x)[::0])
---
# The slice operation fails at the position of the last instruction
# before it in go.starlark.net's code layout.
print(("{}".format(0) if False else "")[::0])
---
x = [1]
print((x and "")[::0])
---
x = []
print((x or "ab")[::0])
---
print([y for y in [1, 2] if y > 1][::0])
---
def f(a):
    """Doc."""
    return a
f()
---
def f(a):
    return struct(a = a)
f()
---
def f(a):
    return [] + [a]
f()
---
def f(a, b = [][0]):
    pass
f(1)
---
# Callables of the program called by built-ins have frames of their own.
sorted(["1", "a"], key = int)
---
sorted([3, 1], key = "".join)
---
max(["1", "a"], key = int)
---
sorted([[1], ["a", 2]], key = sorted)
---
# The callee rejects a keyword passed twice.
def f(a):
    pass
f(a = 1, **{"a": 2})
---
print(sep = "", **{"sep": ""})
---
# *args and **kwargs are evaluated after the other arguments and
# unpacked by the call.
def f(*args, **kwargs):
    pass
def g():
    return 1 // 0
f(*1, **g())
---
def f(*args, **kwargs):
    pass
def g():
    return 1 // 0
f(x = g(), *1)
---
def f(*args, **kwargs):
    pass
f(1, *[2], **{1: 2})
