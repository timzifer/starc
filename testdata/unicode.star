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
