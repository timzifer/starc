# Arithmetic and numbers.
print(1 + 2, 7 - 10, 6 * 7, 7 / 2, 7 // 2, -7 // 2, 7 % 3, -7 % 3, 7 % -3)
print(1.5 + 2, 3 * 0.5, 1 / 4, 2.0 // 0.3, -5.5 % 2)
print(0.1, 1.0, 1e100, 123456.0, 1234567.0, 1e-5, 0.0001, -0.0, 3.14159, 2.0/3)
print(5 & 3, 5 | 3, 5 ^ 3, ~5, 1 << 10, -16 >> 2)
print(9223372036854775807, -9223372036854775807 - 1)
print(int("42"), int("-0x1f", 16), int("0o17", 0), int(3.9), int(-3.9), int(True))
print(float("1.5"), float("-2e3"), float(7), float(".5"))
print(abs(-3), abs(2.5), 1 < 2, 2.0 >= 2, 1 == 1.0, "a" < "b", (1, 2) < (1, 3), [1] == [1])
---
# Strings.
s = "Hello, World"
print(len(s), s[0], s[-1], s[1:5], s[::2], s[::-1], s[7:], s[:-7])
print(s.upper(), s.lower(), s.find("o"), s.rfind("o"), s.find("z"), "lo" in s)
print("a,b,,c".split(","), " a  b c ".split(), "a b c".split(" ", 1), "a.b.c".rsplit(".", 1))
print("-".join(["x", "y", "z"]), "  pad  ".strip(), "xxhixx".strip("x"), "  l".lstrip(), "r  ".rstrip())
print("abc".startswith("ab"), "abc".endswith(("x", "c")), "aaa".count("a"), "banana".replace("a", "o", 2))
print("hello world".title(), "hello".capitalize(), "abc".isalpha(), "123".isdigit(), " ".isspace())
print("a\nb\r\nc".splitlines(), "k=v".partition("="), "a/b/c".rpartition("/"))
print(repr("quote\"d\n\ttab\\"), str(1), repr([1, "a"]), "x" * 3, 2 * "ab")
print("{} and {}".format(1, "two"), "{1}{0}".format("a", "b"), "{x}!{y!r}".format(x = 1, y = "z"))
print("%d items, %s, %r, %5%" if False else "%d items, %s, %r, %%" % (3, "ok", "q"))
print("%x %X %o %c %f %e %g" % (255, 255, 8, 65, 3.14159, 12345.678, 0.5))
print("%(a)s-%(b)d" % {"a": "A", "b": 2})
print(ord("A"), chr(97), chr(0x4e16), ord("世"), hash("abc"))
print("x".join(list("abc".elems())), list("héllo".codepoints()), "a\nb\n".splitlines(True))
---
# Lists, tuples, dicts.
l = [3, 1, 2]
l.append(4)
l.extend([5, 6])
l.insert(0, 0)
print(l, len(l), l.pop(), l.pop(0), l)
l.remove(2)
print(l, l.index(4), 3 in l, 9 not in l, l + [7], l * 2)
l[0] = 99
print(l, sorted(l), sorted(l, reverse = True), sorted(["bb", "a", "ccc"], key = len))
t = (1, "two", 3.0)
print(t, t[1], t[-1], t[1:], len(t), (1,), ())
a, b, c = t
print(a, b, c)
d = {"one": 1, "two": 2}
d["three"] = 3
print(d, d["two"], d.get("four"), d.get("four", 4), len(d), "one" in d)
print(d.keys(), d.values(), d.items())
print(d.pop("one"), d, d.setdefault("z", 26), d)
d.update({"a": 1}, b = 2)
print(d, {"x": 1} == {"x": 1}, dict([("k", "v")]), dict(a = 1))
del_target = {1: "a", 2: "b"}
print(del_target.popitem(), del_target)
print(list(range(5)), list(range(2, 10, 3)), list(range(5, 0, -2)), len(range(10)), 3 in range(5))
print(min(3, 1, 2), max([1, 5, 3]), min("abc"), max([1, 2], key = lambda x: -x))
print(any([0, 1]), all([1, 0]), enumerate(["a", "b"]), zip([1, 2], "ab".elems()), reversed([1, 2, 3]))
print(type(1), type(1.0), type(""), type([]), type(()), type({}), type(None), type(True), type(len))
print(tuple([1, 2]), list((3, 4)), bool([]), bool([0]), str(None))
---
# Control flow and functions.
def fact(n):
    r = 1
    for i in range(1, n + 1):
        r *= i
    return r

def collatz(n):
    steps = 0
    while n != 1:
        if n % 2 == 0:
            n //= 2
        else:
            n = 3 * n + 1
        steps += 1
    return steps

def kw(a, b = 2, *args, c, d = 4, **kwargs):
    return (a, b, args, c, d, kwargs)

print(fact(10), collatz(27))
print(kw(1, c = 3), kw(1, 2, 3, 4, c = 5, e = 6), kw(*[1, 2], **{"c": 3}))

def make_counter():
    count = [0]
    def inc(by = 1):
        count[0] += by
        return count[0]
    return inc

c = make_counter()
c()
c(5)
print(c())

def adder(x):
    return lambda y: x + y

print(adder(3)(4), [f(1) for f in [adder(1), adder(2)]])

total = 0
for i in range(10):
    if i == 7:
        break
    if i % 2:
        continue
    total += i
print(total)
print([x * x for x in range(6) if x % 2 == 0], {k: v for k, v in [("a", 1), ("b", 2)]})
print([(x, y) for x in range(3) for y in range(x)])
print(1 if True else 2, 0 or "default", 1 and 2, not None)
x = [1, 2]
x += [3]
print(x)

def outer():
    v = 1
    def mid():
        def inner():
            return v
        return inner()
    return mid()

print(outer())
