# The prelude implements the parts of the Starlark standard library that
# are most naturally expressed in Starlark itself. It is compiled
# together with every program; functions named _<type>_<name> become
# methods of the given type, other public functions are universal
# built-ins. Primitive operations are provided by the runtime.

_WS = " \t\n\r\v\f"

def sorted(iterable, *, key = None, reverse = False):
    xs = list(iterable)
    n = len(xs)
    if key == None:
        keys = xs
    else:
        keys = [key(x) for x in xs]
    idx = list(range(n))
    buf = list(range(n))
    width = 1
    while width < n:
        lo = 0
        while lo < n:
            mid = lo + width
            if mid > n:
                mid = n
            hi = mid + width
            if hi > n:
                hi = n
            i = lo
            j = mid
            k = lo
            while i < mid and j < hi:
                a = keys[idx[i]]
                b = keys[idx[j]]
                if reverse:
                    take_right = a < b
                else:
                    take_right = b < a
                if take_right:
                    buf[k] = idx[j]
                    j += 1
                else:
                    buf[k] = idx[i]
                    i += 1
                k += 1
            while i < mid:
                buf[k] = idx[i]
                i += 1
                k += 1
            while j < hi:
                buf[k] = idx[j]
                j += 1
                k += 1
            lo = hi
        idx, buf = buf, idx
        width *= 2
    return [xs[i] for i in idx]

def _extremum(name, args, key, want_max):
    if len(args) == 0:
        _error(name + ": got 0 arguments, want at least 1")
    if len(args) == 1:
        xs = list(args[0])
    else:
        xs = list(args)
    if len(xs) == 0:
        _error(name + ": argument is an empty sequence")
    best = xs[0]
    if key == None:
        best_key = best
    else:
        best_key = key(best)
    for x in xs[1:]:
        if key == None:
            k = x
        else:
            k = key(x)
        if want_max:
            better = best_key < k
        else:
            better = k < best_key
        if better:
            best = x
            best_key = k
    return best

def min(*args, key = None):
    return _extremum("min", args, key, False)

def max(*args, key = None):
    return _extremum("max", args, key, True)

def any(x):
    for e in x:
        if e:
            return True
    return False

def all(x):
    for e in x:
        if not e:
            return False
    return True

def enumerate(x, start = 0):
    res = []
    i = start
    for e in x:
        res.append((i, e))
        i += 1
    return res

def zip(*args):
    if len(args) == 0:
        return []
    seqs = [list(a) for a in args]
    n = len(seqs[0])
    for s in seqs:
        if len(s) < n:
            n = len(s)
    return [tuple([s[i] for s in seqs]) for i in range(n)]

def reversed(x):
    xs = list(x)
    return [xs[i] for i in range(len(xs) - 1, -1, -1)]

def dir(x):
    return sorted(_dir(x))

# ---- string methods ----

def _str_count(self, sub, start = None, end = None):
    s = self[start:end]
    if sub == "":
        return len(s) + 1
    n = 0
    i = s.find(sub)
    while i >= 0:
        n += 1
        i = s.find(sub, i + len(sub))
    return n

def _affix_args(name, self, x, start, end):
    s = self[start:end]
    if type(x) == "string":
        return s, (x,)
    if type(x) != "tuple":
        _error(name + ": got " + type(x) + ", want string or tuple")
    for p in x:
        if type(p) != "string":
            _error(name + ": got " + type(p) + ", want string")
    return s, x

def _str_startswith(self, prefix, start = None, end = None):
    s, xs = _affix_args("startswith", self, prefix, start, end)
    for p in xs:
        if s[:len(p)] == p:
            return True
    return False

def _str_endswith(self, suffix, start = None, end = None):
    s, xs = _affix_args("endswith", self, suffix, start, end)
    for p in xs:
        if len(p) <= len(s) and s[len(s) - len(p):] == p:
            return True
    return False

def _str_index(self, sub, start = None, end = None):
    i = self.find(sub, start, end)
    if i < 0:
        _error("index: substring not found")
    return i

def _str_rindex(self, sub, start = None, end = None):
    i = self.rfind(sub, start, end)
    if i < 0:
        _error("rindex: substring not found")
    return i

def _str_lstrip(self, chars = None):
    if chars == None:
        chars = _WS
    i = 0
    n = len(self)
    while i < n and self[i] in chars:
        i += 1
    return self[i:]

def _str_rstrip(self, chars = None):
    if chars == None:
        chars = _WS
    j = len(self)
    while j > 0 and self[j - 1] in chars:
        j -= 1
    return self[:j]

def _str_strip(self, chars = None):
    return self.lstrip(chars).rstrip(chars)

def _str_removeprefix(self, prefix):
    if self.startswith(prefix):
        return self[len(prefix):]
    return self

def _str_removesuffix(self, suffix):
    if suffix != "" and self.endswith(suffix):
        return self[:len(self) - len(suffix)]
    return self

def _str_split(self, sep = None, maxsplit = -1):
    res = []
    n = len(self)
    if sep == None:
        i = 0
        while i < n:
            while i < n and self[i] in _WS:
                i += 1
            if i >= n:
                break
            if maxsplit >= 0 and len(res) == maxsplit:
                res.append(self[i:])
                break
            j = i
            while j < n and not (self[j] in _WS):
                j += 1
            res.append(self[i:j])
            i = j
        return res
    if sep == "":
        _error("split: empty separator")
    i = 0
    while maxsplit < 0 or len(res) < maxsplit:
        j = self.find(sep, i)
        if j < 0:
            break
        res.append(self[i:j])
        i = j + len(sep)
    res.append(self[i:])
    return res

def _str_rsplit(self, sep = None, maxsplit = -1):
    if maxsplit < 0:
        return self.split(sep)
    res = []
    if sep == None:
        j = len(self)
        while j > 0:
            while j > 0 and self[j - 1] in _WS:
                j -= 1
            if j <= 0:
                break
            if len(res) == maxsplit:
                res.append(self[:j])
                break
            i = j
            while i > 0 and not (self[i - 1] in _WS):
                i -= 1
            res.append(self[i:j])
            j = i
        return reversed(res)
    if sep == "":
        _error("rsplit: empty separator")
    j = len(self)
    while len(res) < maxsplit:
        i = self.rfind(sep, 0, j)
        if i < 0:
            break
        res.append(self[i + len(sep):j])
        j = i
    res.append(self[:j])
    return reversed(res)

def _str_splitlines(self, keepends = False):
    res = []
    i = 0
    n = len(self)
    while i < n:
        j = self.find("\n", i)
        if j < 0:
            res.append(self[i:])
            break
        if keepends:
            res.append(self[i:j + 1])
        else:
            res.append(self[i:j])
        i = j + 1
    return res

def _str_replace(self, old, new, count = -1):
    res = []
    i = 0
    n = 0
    if old == "":
        for c in self.elems():
            if count >= 0 and n >= count:
                res.append(self[i:])
                return "".join(res)
            res.append(new)
            res.append(c)
            i += 1
            n += 1
        if count < 0 or n < count:
            res.append(new)
        return "".join(res)
    while count < 0 or n < count:
        j = self.find(old, i)
        if j < 0:
            break
        res.append(self[i:j])
        res.append(new)
        i = j + len(old)
        n += 1
    res.append(self[i:])
    return "".join(res)

def _str_partition(self, sep):
    if sep == "":
        _error("partition: empty separator")
    i = self.find(sep)
    if i < 0:
        return (self, "", "")
    return (self[:i], sep, self[i + len(sep):])

def _str_rpartition(self, sep):
    if sep == "":
        _error("rpartition: empty separator")
    i = self.rfind(sep)
    if i < 0:
        return ("", "", self)
    return (self[:i], sep, self[i + len(sep):])

def _str_elems(self):
    return [self[i] for i in range(len(self))]

def _str_codepoints(self):
    res = []
    i = 0
    n = len(self)
    while i < n:
        b = _byte(self, i)
        k = 1
        if b >= 0xf0:
            k = 4
        elif b >= 0xe0:
            k = 3
        elif b >= 0xc0:
            k = 2
        res.append(self[i:i + k])
        i += k
    return res

def _str_codepoint_ords(self):
    return [ord(c) for c in self.codepoints()]

def _str_elem_ords(self):
    return [_byte(self, i) for i in range(len(self))]

def _classify(s, pred):
    if s == "":
        return False
    for i in range(len(s)):
        if not pred(_byte(s, i)):
            return False
    return True

def _is_digit(b):
    return b >= 48 and b <= 57

def _is_upper(b):
    return b >= 65 and b <= 90

def _is_lower(b):
    return b >= 97 and b <= 122

def _is_alpha(b):
    return _is_upper(b) or _is_lower(b)

def _str_isdigit(self):
    return _classify(self, _is_digit)

def _str_isalpha(self):
    return _classify(self, _is_alpha)

def _str_isalnum(self):
    return _classify(self, lambda b: _is_alpha(b) or _is_digit(b))

def _str_isspace(self):
    return _classify(self, lambda b: b == 32 or (b >= 9 and b <= 13))

def _str_islower(self):
    return self.lower() == self and self.upper() != self

def _str_isupper(self):
    return self.upper() == self and self.lower() != self

def _str_title(self):
    res = []
    prev_cased = False
    for c in self.elems():
        b = _byte(c, 0)
        if _is_alpha(b):
            if prev_cased:
                res.append(c.lower())
            else:
                res.append(c.upper())
            prev_cased = True
        else:
            res.append(c)
            prev_cased = False
    return "".join(res)

def _str_istitle(self):
    return self.title() == self and self.lower() != self

def _str_capitalize(self):
    if self == "":
        return self
    return self[0].upper() + self[1:].lower()

def _str_format(self, *args, **kwargs):
    res = []
    i = 0
    n = len(self)
    auto = 0
    while i < n:
        c = self[i]
        if c == "}":
            if i + 1 < n and self[i + 1] == "}":
                res.append("}")
                i += 2
                continue
            _error("format: single '}' in format")
        if c != "{":
            j = i
            while j < n and self[j] != "{" and self[j] != "}":
                j += 1
            res.append(self[i:j])
            i = j
            continue
        if i + 1 < n and self[i + 1] == "{":
            res.append("{")
            i += 2
            continue
        j = self.find("}", i)
        if j < 0:
            _error("format: unmatched '{' in format")
        field = self[i + 1:j]
        conv = "s"
        bang = field.find("!")
        if bang >= 0:
            conv = field[bang + 1:]
            field = field[:bang]
            if conv != "s" and conv != "r":
                _error("format: unknown conversion")
        if field.find(":") >= 0:
            _error("format: format spec features not supported in replacement fields")
        if field == "":
            if auto < 0:
                _error("format: cannot switch from manual field specification to automatic field numbering")
            if auto >= len(args):
                _error("format: tuple index out of range")
            v = args[auto]
            auto += 1
        elif field.isdigit():
            if auto > 0:
                _error("format: cannot switch from automatic field numbering to manual field specification")
            auto = -1
            k = int(field)
            if k >= len(args):
                _error("format: tuple index out of range")
            v = args[k]
        else:
            if field not in kwargs:
                _error("format: keyword " + field + " not found")
            v = kwargs[field]
        if conv == "r":
            res.append(repr(v))
        else:
            res.append(str(v))
        i = j + 1
    return "".join(res)

# ---- list methods ----

def _list_index(self, x, start = None, end = None):
    n = len(self)
    lo = 0
    hi = n
    if start != None:
        lo = start
        if lo < 0:
            lo += n
        if lo < 0:
            lo = 0
    if end != None:
        hi = end
        if hi < 0:
            hi += n
        if hi > n:
            hi = n
    for i in range(lo, hi):
        if self[i] == x:
            return i
    _error("index: value not in list")

def _list_remove(self, x):
    for i in range(len(self)):
        if self[i] == x:
            self.pop(i)
            return None
    _error("remove: element not found")

# ---- dict methods ----

def _dict_setdefault(self, key, default = None):
    if key in self:
        return self[key]
    self[key] = default
    return default

def _dict_update(self, *args, **kwargs):
    if len(args) > 1:
        _error("update: got " + str(len(args)) + " arguments, want at most 1")
    if len(args) == 1:
        x = args[0]
        if type(x) == "dict":
            for k, v in x.items():
                self[k] = v
        else:
            for pair in x:
                k, v = pair
                self[k] = v
    for k, v in kwargs.items():
        self[k] = v
    return None

def _dict_popitem(self):
    if len(self) == 0:
        _error("popitem: empty dict")
    k = self.keys()[0]
    return (k, self.pop(k))

# ---- string formatting ----

_DIGITS = "0123456789abcdef"

def _int_to_base(x, base, upper):
    if x == 0:
        return "0"
    neg = x < 0
    if neg:
        x = -x
    res = []
    for _ in range(64):
        if x == 0:
            break
        res.append(_DIGITS[x % base])
        x //= base
    s = "".join(reversed(res))
    if upper:
        s = s.upper()
    if neg:
        s = "-" + s
    return s

def _pad_frac(q, prec):
    s = str(q)
    return "0" * (prec - len(s)) + s

def _fmt_fixed(x, prec):
    if x != x:
        return "nan"
    if x == float("inf"):
        return "+inf"
    if x == float("-inf"):
        return "-inf"
    neg = x < 0
    ax = -x if neg else x
    scale = 1
    for _ in range(prec):
        scale *= 10
    ip = int(ax)
    q = int((ax - ip) * scale + 0.5)
    if q >= scale:
        ip += 1
        q -= scale
    s = str(ip)
    if prec > 0:
        s += "." + _pad_frac(q, prec)
    if neg:
        s = "-" + s
    return s

def _fmt_exp(x, prec, upper):
    if x != x or x == float("inf") or x == float("-inf"):
        return _fmt_fixed(x, prec)
    neg = x < 0
    ax = -x if neg else x
    e = 0
    if ax != 0:
        while ax >= 10.0:
            ax /= 10.0
            e += 1
        while ax < 1.0:
            ax *= 10.0
            e -= 1
    m = _fmt_fixed(ax, prec)
    if m.startswith("10"):
        e += 1
        m = _fmt_fixed(ax / 10.0, prec)
    es = str(-e if e < 0 else e)
    if len(es) < 2:
        es = "0" + es
    s = m + ("E" if upper else "e") + ("-" if e < 0 else "+") + es
    if neg:
        s = "-" + s
    return s

def _format_one(c, v):
    if c == "s":
        return str(v)
    if c == "r":
        return repr(v)
    if c == "d" or c == "i":
        if type(v) == "float":
            v = int(v)
        if type(v) != "int":
            _error("%" + c + " format requires integer: " + type(v))
        return str(v)
    if c == "x" or c == "X" or c == "o":
        if type(v) != "int":
            _error("%" + c + " format requires integer: " + type(v))
        return _int_to_base(v, 8 if c == "o" else 16, c == "X")
    if c == "c":
        if type(v) == "int":
            return chr(v)
        if type(v) == "string" and len(v.codepoints()) == 1:
            return v
        _error("%c format requires a single-character string")
    if c in "eEfFgG":
        if type(v) != "float" and type(v) != "int":
            _error("%" + c + " format requires float: " + type(v))
        f = float(v)
        if c == "f" or c == "F":
            return _fmt_fixed(f, 6)
        if c == "e" or c == "E":
            return _fmt_exp(f, 6, c == "E")
        s = str(f)
        if c == "G":
            s = s.upper()
        return s
    _error("unknown conversion %" + c)

def _percent_format(format, args):
    is_dict = type(args) == "dict"
    if type(args) == "tuple":
        items = args
    else:
        items = (args,)
    res = []
    i = 0
    n = len(format)
    ai = 0
    used_key = False
    while i < n:
        j = format.find("%", i)
        if j < 0:
            res.append(format[i:])
            break
        res.append(format[i:j])
        j += 1
        if j >= n:
            _error("incomplete format")
        c = format[j]
        if c == "%":
            res.append("%")
            i = j + 1
            continue
        if c == "(":
            k = format.find(")", j)
            if k < 0:
                _error("incomplete format key")
            if not is_dict:
                _error("format requires a mapping")
            v = args[format[j + 1:k]]
            used_key = True
            j = k + 1
            if j >= n:
                _error("incomplete format")
            c = format[j]
        else:
            if ai >= len(items):
                _error("not enough arguments for format string")
            v = items[ai]
            ai += 1
        res.append(_format_one(c, v))
        i = j + 1
    if not used_key and ai < len(items):
        _error("too many arguments for format string")
    return "".join(res)
