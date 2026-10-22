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
