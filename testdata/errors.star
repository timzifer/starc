# Each chunk must fail in both implementations, after identical output.
print("before")
x = 1 + "a"
---
fail("custom", 42)
---
l = [1, 2]
for x in l:
    l.append(x)
---
d = {}
d[[1]] = 2
---
print(1 // 0)
---
x = [1, 2, 3][5]
---
def f():
    return undefined_later

f()
undefined_later = 1
---
def r(n):
    return r(n - 1) if n else 0

r(3)
---
frozen = [1]
def mutate():
    frozen.append(2)
mutate()
print(frozen)
---
"abc".nosuchmethod()
---
def two(a, b):
    pass
two(1)
---
def two(a, b):
    pass
two(1, 2, 3)
---
def two(a, b):
    pass
two(1, c = 2)
---
a, b = [1, 2, 3]
---
{"a": 1}["b"]
---
int("12x")
---
sorted([1, "a"])
---
"%d" % "x"
---
print([1, 2][::4294967296])
---
print([1, 2][2147483648:])
---
def f(a, *, b):
    return a + b

print(f(1, 2))
---
def f(a, *, b = 1):
    return a + b

print(f(1, 2))
---
print(int("_1"))
---
print(int("1_"))
---
print(int("__"))
---
print(int("1__2"))
---
print(int("1_000"))
---
print(int("9", base = 37))
---
print(int("1", 1))
---
print(int("0755", 0))
---
print(int("0x", 0))
---
print("x", invalid = True)
---
print("x", sep = "-", end = "")
---
print({range(3): "value"})
---
print(int())
---
print(int(base = 2))
---
print(int("10", 10, base = 2))
---
print(int("10", ignored = True))
---
print(int(float("inf")))
---
def side():
    print("side effect")
    return 1

print("before")
[].missing(side())
---
a = []
a.append(a)
print(a == a)
---
a = []
a.append(a)
print(a in [a])
---
d = {}
d["k"] = d
print(d == d)
---
a = [1]
b = [1]
for i in range(9):
    a = [a]
    b = [b]
print(a == b)
---
a = [1]
b = [1]
for i in range(9):
    a = [a]
    b = [b]
print(a < b)
---
print(float("1e400"))
