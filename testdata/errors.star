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
