# Unicode in strings, compared with go.starlark.net.
# repr escapes like syntax.Quote: short escapes for control characters,
# \u and \U for non-printable code points, \x for invalid UTF-8 bytes.
print(repr("\v\f\a\b"), repr("\u00a0x\u2028"), repr("\u3000\u200b\ufeff\u00ad"))
print(list("\u00e9".elems()), list("\u2713\U0001f600".elems()))
print(repr("\u00e9 \u2713 \U0001f600 \ufffd \u0378 \U000e0001 \U000e0100 \U0010ffff \U0001fbfa"))
print(["\x00\x1f\x7f\u0080\u009f", "\"\\'"], {"\u2029": "\U0001f600"}, ("\U00030000",))
print("%r" % "\t\u00a0", "{!r}".format("\u2028"), str(["\x0b"]))
s = "a\u00e9\u2713\U0001f600"
print([s[i:j] for i in range(len(s)) for j in range(i + 1, len(s) + 1)])
---
# hash works on code points (go.starlark.net's javaStringHash).
print(hash("\u00e9"), hash("h\u00e9llo"), hash("\u2713"), hash("\U0001f600"), hash("a\U0010ffffb"))
print(hash(""), hash("abc"), hash("\ufffd"), hash("\u00e9".elems()[0] + "x"), hash("\u2713\U0001f600"[1:]))
print([hash(c) for c in "a\u00e9\u2713\U0001f600".elems()])
---
# An empty substring matches at every code point (or invalid byte).
print("\u00e9".count(""), "\u00e9".replace("", "-"), "a\u00e9".count("", 1), "a\u00e9\U0001f600".count(""))
s = "x\u00e9\u2713\U0001f600"
print([s.replace("", "<>", n) for n in range(-1, 7)], "".replace("", "-"), "".replace("", "-", 0))
b = "\u00e9\u2713"[1:]
print(repr(b.replace("", "|")), b.count(""), b.count("", 1, 3), repr(b.replace("", "|", 2)))
print("ab".count("", 5), "ab".count("", 2, 1), "\u00e9".count("\u00e9", 1))
---
# With end < start, find and rfind search an empty slice at start.
s = "0000000000"
print(s.find("", -3, 5), s.rfind("", 7, 2), s.find("0", 7, 2), s.index("", 9, 1), s.rindex("", -1, 0))
print(s.find("", 20, 5), s.rfind("", -20, -30), "".find("", 1, 0))
---
# split and strip without separator use Unicode white space.
print("\u00a0x\u3000".split(), repr("\u00a0x\u3000".strip()), "a\u2028b\u0085c\u1680d".split())
ws = "\u0009\u000a\u000b\u000c\u000d \u0085\u00a0\u1680\u2000\u2005\u200a\u2028\u2029\u202f\u205f\u3000"
print(len((ws + "x" + ws).strip()), len(ws.lstrip()), len(("x" + ws).rstrip()), (ws + "a" + ws + "b" + ws).split())
print("\u200b\u180e\u00ad x".split(), "\u2000a b\u3000c ".split(None, 1), " a\u2000b\u3000c".rsplit(None, 1))
s = " a b  c  "
print([s.split(None, n) for n in range(-1, 5)], [s.rsplit(None, n) for n in range(-1, 5)])
b = "\u00e9\u00a0"[1:] + " x" + "\u00a0\u00e9"[:2]
print(b.split(), repr(b.strip()), b.rsplit(None, 0), b.split(None, 0))
---
# strip with a cutset works on code points.
print("\u00e9a\u00e8".strip("\u00e8"), repr("\u00e9".strip("\u00e8")), "x\u2713y\u2713".rstrip("\u2713\u2717"))
bad = "\u00e9"[:1]
print(repr((bad + "a" + bad).strip(bad)), repr((bad + "a").lstrip("\ufffd")), repr(("a" + bad).rstrip("b" + bad)))
print(repr("\ufffda\ufffd".strip(bad)), "aXbXa".strip("ab"), "  x  ".strip(" "), repr("\u00e9\u00e9".lstrip("\u00e9")))
