# Structs, compared with go.starlark.net's starlarkstruct (struct is
# predeclared in both implementations).
s = struct(name = "probe", pos = (1.5, -2), tags = ["a"], count = 3)
print(s, type(s), bool(struct()), struct())
print(s.name, s.pos, s.count, s.tags[0], str(s), repr(s))
print(dir(s), hasattr(s, "name"), hasattr(s, "nope"), getattr(s, "count"), getattr(s, "nope", "dflt"))
print(s == struct(count = 3, tags = ["a"], pos = (1.5, -2), name = "probe"), s == struct(name = "probe"), s != s)
print([s.name for s in [struct(name = "x"), struct(name = "y")]], struct(a = struct(b = 1)).a.b)
---
def double(x):
    return 2 * x

t = struct(f = double, g = lambda: "g", n = None)
print(t.f(21), t.g(), t.n, getattr(t, "f")(1))
u = t
print(u.f(4))
---
d = {struct(k = 1): "one", struct(k = (2, 3)): "two"}
print(d[struct(k = 1)], d[struct(k = (2, 3))], struct(k = 1) in d, struct(k = 9) in d)
print(sorted([struct(a = 2).a, struct(a = 1).a]), len(dir(struct(z = 1, y = 2, x = 3))))
---
FROZEN = struct(l = [1])
def mutate():
    FROZEN.l.append(2)
mutate()
---
struct(a = 1).b
---
struct(1)
---
{struct(l = [1]): 1}
---
struct(a = 1) < struct(a = 2)
---
len(struct(a = 1))
---
for x in struct(a = 1):
    pass
---
getattr(struct(a = 1), "b")
---
# A struct inside a cycle is compared element-wise, like a list.
l = []
s = struct(l = l)
l.append(s)
print(s == s)
