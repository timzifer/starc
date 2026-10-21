# Sets, compared with go.starlark.net (FileOptions.Set).
s = set([3, 1, 2, 1])
print(s, len(s), type(s), bool(s), bool(set()), set(), str(set([])))
print(1 in s, 4 in s, 4 not in s, list(s), sorted(s), tuple(s))
print(set("abc".elems()), set({"a": 1, "b": 2}), set(range(3)), set((1, (2, 3))))
for x in s:
    print("elem", x)
print([x * 2 for x in s], {x: x for x in s}, max(s), min(s), any(s), all(s))
---
a = set([1, 2, 3, 4])
b = set([3, 4, 5])
print(a | b, a & b, a - b, a ^ b, b | a, b & a, b - a, b ^ a)
print(a == set([4, 3, 2, 1]), a != b, a == [1, 2, 3, 4], set() == set())
print(set([1, 2]) < a, a < a, a <= a, set([1, 2]) <= a, a > set([1]), a >= b, a > a)
print(sorted([set([1, 2]), set([1])]), [set([1])] < [set([1, 2])])
---
s = set([1, 2])
s.add(3)
s.add(1)
print(s, s.add(4), s)
s.discard(2)
s.discard(10)
print(s)
s.remove(1)
print(s, s.pop(), s)
s.clear()
print(s, len(s))
---
a = set([1, 2, 3])
print(a.union([3, 4], (5,)), a.union(), a.union(set([0])))
print(a.difference([2, 9]), a.intersection([3, 2, 7]), a.symmetric_difference([3, 4, 4]))
print(a.issubset([1, 2, 3, 4]), a.issubset([1, 2]), a.issubset([1, 1, 2, 3]), set().issubset([]))
print(a.issuperset([1, 2]), a.issuperset([1, 5]), a.issuperset([]))
a.update([7], [8, 9])
print(a, a.update(), a)
print(dir(a))
---
# Element order is insertion order; removal and re-insertion moves to the end.
s = set(["x", "y", "z"])
s.remove("x")
s.add("x")
print(s, list(s), s | set(["a"]), set(["a"]) | s)
---
FROZEN = set([1, 2])
def mutate():
    FROZEN.add(3)
mutate()
---
set([[1]])
---
{set([1]): 2}
---
set([1]).remove(2)
---
set().pop()
---
s = set([1, 2])
for x in s:
    s.add(3)
---
set([1]) | [2]
---
set([1]) < [2]
---
set(1)
---
set([1], [2])
---
set([1]).union(1)
