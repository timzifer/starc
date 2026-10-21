package rt

import (
	"sort"

	. "github.com/timzifer/starc/internal/wasm"
)

// builtin defines a Starlark-callable primitive named "bi."+name.
func (r *RT) builtin(name string, build func(f *Func, args, kw Local)) {
	r.define("bi."+name, CallType.Params, CallType.Results, func(f *Func) {
		build(f, f.Param(0), f.Param(1))
	})
}

// BuiltinObj returns the static builtin object for the named primitive.
func (r *RT) BuiltinObj(name string) uint32 {
	display := name
	if i := lastDot(name); i >= 0 {
		display = name[i+1:]
	}
	return r.Builtin(display, r.Fn("bi."+name))
}

// HasBuiltin reports whether a primitive builtin with the given name exists.
func (r *RT) HasBuiltin(name string) bool {
	_, ok := r.defs["bi."+name]
	return ok
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

func nargs(args Local) Expr      { return ld(get(args), offTupLen) }
func arg(args Local, i int) Expr { return ld(get(args), uint32(offTupElems+4*i)) }

// optArg yields argument i or 0 if absent or None.
func optArg(args Local, i int) Expr {
	return IfV(I32, gt(nargs(args), c32(i)),
		IfV(I32, isTag(arg(args, i), TNone), c32(0), arg(args, i)), c32(0))
}

// checkArgs validates the positional argument count and rejects keywords.
func (r *RT) checkArgs(name string, args, kw Local, min, max int) Expr {
	s := Seq{
		If(and(ne(get(kw), c32(0)), ne(ld(get(kw), offDictLen), c32(0))),
			r.Fail(name+": unexpected keyword arguments")),
	}
	if min == max {
		s = append(s, If(ne(nargs(args), c32(min)),
			r.Fail(name+": got ", r.Call("i64_to_str", I64ExtendI32S(nargs(args))), " arguments, want "+itoa(min))))
	} else {
		s = append(s, If(or(lt(nargs(args), c32(min)), gt(nargs(args), c32(max))),
			r.Fail(name+": got ", r.Call("i64_to_str", I64ExtendI32S(nargs(args))),
				" arguments, want "+itoa(min)+".."+itoa(max))))
	}
	return s
}

func itoa(i int) string {
	if i < 0 {
		return "-" + itoa(-i)
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// MethodSpec describes a primitive method.
type MethodSpec struct {
	Tag     int
	Name    string // method name
	Builtin string // primitive builtin name
}

// PrimitiveMethods lists the methods implemented by the runtime itself.
func (r *RT) PrimitiveMethods() []MethodSpec {
	var ms []MethodSpec
	for name := range r.defs {
		if len(name) < 3 || name[:3] != "bi." {
			continue
		}
		rest := name[3:]
		for prefix, tag := range map[string]int{"list.": TList, "dict.": TDict, "str.": TString} {
			if len(rest) > len(prefix) && rest[:len(prefix)] == prefix {
				ms = append(ms, MethodSpec{tag, rest[len(prefix):], rest})
			}
		}
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Builtin < ms[j].Builtin })
	return ms
}

func (r *RT) registerBuiltins() {
	r.builtin("print", func(f *Func, args, kw Local) {
		parts, i, sep, s := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(sep, r.S(" ")),
			If(get(kw), Seq{
				Set(sep, r.Call("dict_get", get(kw), r.S("sep"))),
				If(gt(ld(get(kw), offDictLen), Select(c32(1), c32(0), get(sep))),
					r.Fail("print: unexpected keyword argument")),
				If(eqz(get(sep)), Set(sep, r.S(" "))),
				If(not(isTag(get(sep), TString)), r.Fail("print: sep must be a string")),
			}),
			Set(parts, r.Call("list_new", nargs(args))),
			r.forRange(i, c32(0), nargs(args),
				r.Call("list_append_raw", get(parts), r.Call("str", ld(elem(get(args), offTupElems, get(i)), 0)))),
			Set(s, r.Call("str_concat", r.Call("str_join", get(sep), get(parts)), r.S("\n"))),
			Drop(r.Call("write", c32(1), get(s))),
			r.NoneV(),
		)
	})
	r.builtin("fail", func(f *Func, args, kw Local) {
		parts, i, sep := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(sep, r.S(" ")),
			If(get(kw), Seq{
				Set(sep, r.Call("dict_get", get(kw), r.S("sep"))),
				If(eqz(get(sep)), Set(sep, r.S(" "))),
			}),
			Set(parts, r.Call("list_new", nargs(args))),
			r.forRange(i, c32(0), nargs(args),
				r.Call("list_append_raw", get(parts), r.Call("str", ld(elem(get(args), offTupElems, get(i)), 0)))),
			r.Call("fail", r.Call("str_concat", r.S("fail: "), r.Call("str_join", get(sep), get(parts)))),
			Unreachable(),
		)
	})
	r.builtin("_error", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("_error", args, kw, 1, 1), r.Call("fail", r.Call("str", arg(args, 0))), Unreachable())
	})
	r.builtin("len", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("len", args, kw, 1, 1),
			If(isTag(arg(args, 0), TRange), Return(r.Call("int", r.Call("range_len", arg(args, 0))))),
			r.Call("int", I64ExtendI32S(r.Call("len", arg(args, 0)))))
	})
	r.builtin("type", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("type", args, kw, 1, 1), r.TypeName(arg(args, 0)))
	})
	r.builtin("str", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("str", args, kw, 1, 1), r.Call("str", arg(args, 0)))
	})
	r.builtin("repr", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("repr", args, kw, 1, 1), r.Call("repr", arg(args, 0)))
	})
	r.builtin("bool", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("bool", args, kw, 0, 1),
			IfV(I32, eqz(nargs(args)), r.FalseV(), r.bool(r.Call("truth", arg(args, 0)))))
	})
	r.builtin("int", func(f *Func, args, kw Local) {
		x, base, hasBase, res, i, e, k := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32),
			f.Local(I32), f.Local(I32), f.Local(I32)
		fl := f.Local(F64)
		f.Add(
			// Bind the parameters x and base?.
			If(gt(nargs(args), c32(2)), r.Fail("int: got ", r.Call("i64_to_str", I64ExtendI32S(nargs(args))),
				" arguments, want at most 2")),
			If(gt(nargs(args), c32(0)), Set(x, arg(args, 0))),
			If(eq(nargs(args), c32(2)), Set(res, arg(args, 1))),
			If(get(kw), r.forRange(i, c32(0), ld(get(kw), offDictUsed),
				Set(e, add(ld(get(kw), offDictEntries), mul(get(i), c32(12)))),
				Set(k, ld(get(e), 4)),
				If(get(k), Seq{
					IfElse(r.Call("str_eq", get(k), r.S("x")), []Expr{
						If(get(x), r.Fail("int: got multiple values for keyword argument \"x\"")),
						Set(x, ld(get(e), 8)),
					}, []Expr{
						IfElse(r.Call("str_eq", get(k), r.S("base")), []Expr{
							If(get(res), r.Fail("int: got multiple values for keyword argument \"base\"")),
							Set(res, ld(get(e), 8)),
						}, []Expr{
							r.Fail("int: unexpected keyword argument ", r.Call("quote", get(k))),
						}),
					}),
				}),
			)),
			If(eqz(get(x)), r.Fail("int: missing argument for x")),
			Set(base, c32(10)),
			If(get(res), Seq{Set(base, r.Call("as_i32", get(res), r.S("int: base"))), Set(hasBase, c32(1))}),
			If(isTag(get(x), TString), Seq{
				If(and(ne(get(base), c32(0)), or(lt(get(base), c32(2)), gt(get(base), c32(36)))),
					r.Fail("int: base must be an integer >= 2 && <= 36")),
				Set(res, r.Call("parse_int", get(x), get(base))),
				If(eqz(get(res)), r.Fail("int: invalid literal with base ",
					r.Call("i64_to_str", I64ExtendI32S(get(base))), ": ", r.Call("quote", get(x)))),
				Return(get(res)),
			}),
			If(get(hasBase), r.Fail("int: can't convert non-string with explicit base")),
			If(isTag(get(x), TInt), Return(get(x))),
			If(isTag(get(x), TBool), Return(r.Call("int", I64ExtendI32U(ld(get(x), offBool))))),
			If(isTag(get(x), TFloat), Seq{
				Set(fl, f64v(get(x))),
				If(F64Ne(get(fl), get(fl)), r.Fail("int: cannot convert float NaN to integer")),
				If(or(F64Lt(get(fl), F64c(-9223372036854775808.0)), F64Ge(get(fl), F64c(9223372036854775808.0))),
					r.Fail("int: cannot convert float to integer: out of range")),
				Return(r.Call("int", I64TruncSatF64S(get(fl)))),
			}),
			r.Fail("int: unsupported type ", r.TypeName(get(x))),
		)
	})
	r.builtin("float", func(f *Func, args, kw Local) {
		x, res := f.Local(I32), f.Local(I32)
		f.Add(
			r.checkArgs("float", args, kw, 0, 1),
			If(eqz(nargs(args)), Return(addr(r.Float(0)))),
			Set(x, arg(args, 0)),
			If(isTag(get(x), TFloat), Return(get(x))),
			If(isTag(get(x), TInt), Return(r.Call("float", F64ConvI64S(i64v(get(x)))))),
			If(isTag(get(x), TBool), Return(r.Call("float", F64ConvI32S(ld(get(x), offBool))))),
			If(isTag(get(x), TString), Seq{
				Set(res, r.Call("parse_float", get(x))),
				If(eqz(get(res)), r.Fail("float: invalid float literal: ", get(x))),
				Return(get(res)),
			}),
			r.Fail("float: got ", r.TypeName(get(x)), ", want number or string"),
		)
	})
	r.builtin("list", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("list", args, kw, 0, 1),
			IfV(I32, eqz(nargs(args)), r.Call("list_new", c32(0)), r.Call("list_from", arg(args, 0))))
	})
	r.builtin("tuple", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("tuple", args, kw, 0, 1),
			IfV(I32, eqz(nargs(args)), r.Call("tuple_new", c32(0)), r.Call("tuple_from", arg(args, 0))))
	})
	r.builtin("dict", func(f *Func, args, kw Local) {
		d, x, s, n, i, p := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			If(gt(nargs(args), c32(1)), r.Fail("dict: got more than 1 positional argument")),
			Set(d, r.Call("dict_new")),
			If(eq(nargs(args), c32(1)), Seq{
				Set(x, arg(args, 0)),
				IfElse(isTag(get(x), TDict), []Expr{r.Call("dict_merge", get(d), get(x))}, []Expr{
					Set(s, r.Call("iter_seq", get(x))),
					Set(n, ld(get(s), 4)),
					r.forRange(i, c32(0), get(n),
						Set(p, r.Call("unpack", ld(elem(r.Call("seq_base", get(s)), 0, get(i)), 0), c32(2))),
						r.Call("dict_set", get(d), ld(r.Call("seq_base", get(p)), 0), ld(r.Call("seq_base", get(p)), 4)),
					),
				}),
			}),
			If(get(kw), r.Call("dict_merge", get(d), get(kw))),
			get(d),
		)
	})
	r.builtin("range", func(f *Func, args, kw Local) {
		a, b, s := f.Local(I64), f.Local(I64), f.Local(I64)
		n := f.Local(I32)
		f.Add(
			r.checkArgs("range", args, kw, 1, 3),
			Set(n, nargs(args)),
			Set(s, I64c(1)),
			IfElse(eq(get(n), c32(1)), []Expr{
				Set(b, r.Call("as_int", arg(args, 0), r.S("range"))),
			}, []Expr{
				Set(a, r.Call("as_int", arg(args, 0), r.S("range"))),
				Set(b, r.Call("as_int", arg(args, 1), r.S("range"))),
				If(eq(get(n), c32(3)), Set(s, r.Call("as_int", arg(args, 2), r.S("range")))),
			}),
			r.Call("range_new", get(a), get(b), get(s)),
		)
	})
	r.builtin("hash", func(f *Func, args, kw Local) {
		s, i, h := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			r.checkArgs("hash", args, kw, 1, 1),
			Set(s, arg(args, 0)),
			If(not(isTag(get(s), TString)), r.Fail("hash: got ", r.TypeName(get(s)), ", want string")),
			r.forRange(i, c32(0), slen(get(s)),
				Set(h, add(mul(get(h), c32(31)), I32Load8U(add(sbytes(get(s)), get(i)), 0)))),
			r.Call("int", I64ExtendI32S(get(h))),
		)
	})
	r.builtin("abs", func(f *Func, args, kw Local) {
		x := f.Local(I32)
		f.Add(
			r.checkArgs("abs", args, kw, 1, 1),
			Set(x, arg(args, 0)),
			If(isTag(get(x), TFloat), Return(r.Call("float", F64Abs(f64v(get(x)))))),
			If(isTag(get(x), TInt), Return(IfV(I32, I64LtS(i64v(get(x)), I64c(0)), r.Call("neg", get(x)), get(x)))),
			r.Fail("abs: got ", r.TypeName(get(x)), ", want int or float"),
		)
	})
	r.builtin("getattr", func(f *Func, args, kw Local) {
		id := f.Local(I32)
		f.Add(
			r.checkArgs("getattr", args, kw, 2, 3),
			If(not(isTag(arg(args, 1), TString)), r.Fail("getattr: attribute name must be a string")),
			Set(id, r.Call("method_id", arg(args, 1))),
			If(and(ge(get(id), c32(0)), ne(r.Call("method_lookup", arg(args, 0), get(id)), c32(0))),
				Return(r.Call("attr", arg(args, 0), get(id)))),
			If(eq(nargs(args), c32(3)), Return(arg(args, 2))),
			r.Fail(r.TypeName(arg(args, 0)), " has no .", arg(args, 1), " field or method"),
		)
	})
	r.builtin("hasattr", func(f *Func, args, kw Local) {
		id := f.Local(I32)
		f.Add(
			r.checkArgs("hasattr", args, kw, 2, 2),
			If(not(isTag(arg(args, 1), TString)), r.Fail("hasattr: attribute name must be a string")),
			Set(id, r.Call("method_id", arg(args, 1))),
			r.bool(and(ge(get(id), c32(0)), ne(r.Call("method_lookup", arg(args, 0), get(id)), c32(0)))),
		)
	})
	r.builtin("_dir", func(f *Func, args, kw Local) {
		l, i := f.Local(I32), f.Local(I32)
		f.Add(
			r.checkArgs("dir", args, kw, 1, 1),
			Set(l, r.Call("list_new", c32(0))),
			Raw(func() Expr {
				return r.forRange(i, c32(0), c32(len(r.methodNames)),
					If(r.Call("method_lookup", arg(args, 0), get(i)),
						r.Call("list_append_raw", get(l), r.Call("method_name", get(i)))))
			}),
			get(l),
		)
	})
	r.builtin("chr", func(f *Func, args, kw Local) {
		c := f.Local(I64)
		s := f.Local(I32)
		cp := func() Expr { return I32WrapI64(get(c)) }
		f.Add(
			r.checkArgs("chr", args, kw, 1, 1),
			Set(c, r.Call("as_int", arg(args, 0), r.S("chr"))),
			If(or(I64LtS(get(c), I64c(0)), I64GtS(get(c), I64c(0x10ffff))), r.Fail("chr: Unicode code point out of range")),
			If(I64LtS(get(c), I64c(0x80)), Seq{
				Set(s, r.Call("str_new", c32(1))),
				I32Store8(sbytes(get(s)), 0, cp()),
				Return(get(s)),
			}),
			If(I64LtS(get(c), I64c(0x800)), Seq{
				Set(s, r.Call("str_new", c32(2))),
				I32Store8(sbytes(get(s)), 0, or(c32(0xc0), I32ShrU(cp(), c32(6)))),
				I32Store8(sbytes(get(s)), 1, or(c32(0x80), and(cp(), c32(0x3f)))),
				Return(get(s)),
			}),
			If(I64LtS(get(c), I64c(0x10000)), Seq{
				Set(s, r.Call("str_new", c32(3))),
				I32Store8(sbytes(get(s)), 0, or(c32(0xe0), I32ShrU(cp(), c32(12)))),
				I32Store8(sbytes(get(s)), 1, or(c32(0x80), and(I32ShrU(cp(), c32(6)), c32(0x3f)))),
				I32Store8(sbytes(get(s)), 2, or(c32(0x80), and(cp(), c32(0x3f)))),
				Return(get(s)),
			}),
			Set(s, r.Call("str_new", c32(4))),
			I32Store8(sbytes(get(s)), 0, or(c32(0xf0), I32ShrU(cp(), c32(18)))),
			I32Store8(sbytes(get(s)), 1, or(c32(0x80), and(I32ShrU(cp(), c32(12)), c32(0x3f)))),
			I32Store8(sbytes(get(s)), 2, or(c32(0x80), and(I32ShrU(cp(), c32(6)), c32(0x3f)))),
			I32Store8(sbytes(get(s)), 3, or(c32(0x80), and(cp(), c32(0x3f)))),
			get(s),
		)
	})
	r.builtin("ord", func(f *Func, args, kw Local) {
		s, n, b0, cp, i := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		byteAt := func(i Expr) Expr { return I32Load8U(add(sbytes(get(s)), i), 0) }
		f.Add(
			r.checkArgs("ord", args, kw, 1, 1),
			Set(s, arg(args, 0)),
			If(not(isTag(get(s), TString)), r.Fail("ord: got ", r.TypeName(get(s)), ", want string")),
			Set(n, slen(get(s))),
			If(eqz(get(n)), r.Fail("ord: string encodes 0 Unicode code points, want 1")),
			Set(b0, byteAt(c32(0))),
			If(lt(get(b0), c32(0x80)), Seq{
				If(ne(get(n), c32(1)), r.Fail("ord: string encodes more than one Unicode code point")),
				Return(r.Call("int", I64ExtendI32U(get(b0)))),
			}),
			// multi-byte sequence: length from the leading byte
			Set(i, IfV(I32, eq(and(get(b0), c32(0xe0)), c32(0xc0)), c32(2),
				IfV(I32, eq(and(get(b0), c32(0xf0)), c32(0xe0)), c32(3), c32(4)))),
			If(ne(get(i), get(n)), r.Fail("ord: string encodes more than one Unicode code point")),
			Set(cp, and(get(b0), I32ShrU(c32(0x7f), get(i)))),
			r.forRange(b0, c32(1), get(n), Set(cp, or(I32Shl(get(cp), c32(6)), and(byteAt(get(b0)), c32(0x3f))))),
			r.Call("int", I64ExtendI32U(get(cp))),
		)
	})

	r.builtin("_byte", func(f *Func, args, kw Local) {
		s := f.Local(I32)
		f.Add(
			r.checkArgs("_byte", args, kw, 2, 2),
			Set(s, arg(args, 0)),
			r.Call("int", I64ExtendI32U(I32Load8U(add(sbytes(get(s)),
				r.Call("norm_index", r.Call("as_int", arg(args, 1), r.S("_byte")), slen(get(s)))), 0))),
		)
	})

	// ---- list methods ----
	r.builtin("list.append", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("append", args, kw, 2, 2), r.Call("list_append", arg(args, 0), arg(args, 1)), r.NoneV())
	})
	r.builtin("list.clear", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("clear", args, kw, 1, 1), r.Call("list_check_mut", arg(args, 0)),
			st(arg(args, 0), offListLen, c32(0)), r.NoneV())
	})
	r.builtin("list.extend", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("extend", args, kw, 2, 2), r.Call("list_extend", arg(args, 0), arg(args, 1)), r.NoneV())
	})
	r.builtin("list.insert", func(f *Func, args, kw Local) {
		l, n, i := f.Local(I32), f.Local(I32), f.Local(I32)
		x := f.Local(I64)
		f.Add(
			r.checkArgs("insert", args, kw, 3, 3),
			Set(l, arg(args, 0)),
			r.Call("list_check_mut", get(l)),
			Set(n, ld(get(l), offListLen)),
			Set(x, r.Call("as_int", arg(args, 1), r.S("insert: index"))),
			If(I64LtS(get(x), I64c(0)), Set(x, I64Add(get(x), I64ExtendI32S(get(n))))),
			If(I64LtS(get(x), I64c(0)), Set(x, I64c(0))),
			If(I64GtS(get(x), I64ExtendI32S(get(n))), Set(x, I64ExtendI32S(get(n)))),
			Set(i, I32WrapI64(get(x))),
			r.Call("list_append_raw", get(l), r.NoneV()),
			r.memcopy(elem(ld(get(l), offListData), 0, add(get(i), c32(1))), elem(ld(get(l), offListData), 0, get(i)),
				I32Shl(sub(get(n), get(i)), c32(2))),
			st(elem(ld(get(l), offListData), 0, get(i)), 0, arg(args, 2)),
			r.NoneV(),
		)
	})
	r.builtin("list.pop", func(f *Func, args, kw Local) {
		l, n, i, v := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			r.checkArgs("pop", args, kw, 1, 2),
			Set(l, arg(args, 0)),
			r.Call("list_check_mut", get(l)),
			Set(n, ld(get(l), offListLen)),
			Set(i, sub(get(n), c32(1))),
			If(eq(nargs(args), c32(2)), Set(i, r.Call("norm_index", r.Call("as_int", arg(args, 1), r.S("pop: index")), get(n)))),
			If(eqz(get(n)), r.Fail("pop: list is empty")),
			Set(v, ld(elem(ld(get(l), offListData), 0, get(i)), 0)),
			r.memcopy(elem(ld(get(l), offListData), 0, get(i)), elem(ld(get(l), offListData), 0, add(get(i), c32(1))),
				I32Shl(sub(sub(get(n), get(i)), c32(1)), c32(2))),
			st(get(l), offListLen, sub(get(n), c32(1))),
			get(v),
		)
	})

	// ---- dict methods ----
	r.builtin("dict.get", func(f *Func, args, kw Local) {
		v := f.Local(I32)
		f.Add(
			r.checkArgs("get", args, kw, 2, 3),
			Set(v, r.Call("dict_get", arg(args, 0), arg(args, 1))),
			If(get(v), Return(get(v))),
			IfV(I32, eq(nargs(args), c32(3)), arg(args, 2), r.NoneV()),
		)
	})
	r.builtin("dict.pop", func(f *Func, args, kw Local) {
		v := f.Local(I32)
		f.Add(
			r.checkArgs("pop", args, kw, 2, 3),
			Set(v, r.Call("dict_del", arg(args, 0), arg(args, 1))),
			If(get(v), Return(get(v))),
			If(eq(nargs(args), c32(3)), Return(arg(args, 2))),
			r.Fail("pop: missing key ", r.Call("repr", arg(args, 1))),
		)
	})
	for name, mode := range map[string]int{"keys": 0, "values": 1, "items": 2} {
		r.builtin("dict."+name, func(f *Func, args, kw Local) {
			f.Add(r.checkArgs(name, args, kw, 1, 1), r.Call("dict_view", arg(args, 0), c32(mode)))
		})
	}
	r.builtin("dict.clear", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("clear", args, kw, 1, 1), r.Call("dict_clear", arg(args, 0)), r.NoneV())
	})

	// ---- string methods ----
	for _, name := range []string{"find", "rfind"} {
		r.builtin("str."+name, func(f *Func, args, kw Local) {
			s, a, b, x := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
			f.Add(
				r.checkArgs(name, args, kw, 2, 4),
				Set(s, arg(args, 0)),
				Set(x, arg(args, 1)),
				If(not(isTag(get(x), TString)), r.Fail(name+": got ", r.TypeName(get(x)), ", want string")),
				Set(b, slen(get(s))),
				If(optArg(args, 2), Set(a, I32WrapI64(r.Call("clamp", r.Call("as_int", arg(args, 2), r.S(name)), I64ExtendI32S(slen(get(s))), c32(0))))),
				If(optArg(args, 3), Set(b, I32WrapI64(r.Call("clamp", r.Call("as_int", arg(args, 3), r.S(name)), I64ExtendI32S(slen(get(s))), c32(0))))),
				r.Call("int", I64ExtendI32S(r.Call("str_"+name, get(s), get(x), get(a), get(b)))),
			)
		})
	}
	r.builtin("str.join", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("join", args, kw, 2, 2), r.Call("str_join", arg(args, 0), arg(args, 1)))
	})
	r.builtin("str.upper", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("upper", args, kw, 1, 1), r.Call("str_upper", arg(args, 0)))
	})
	r.builtin("str.lower", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("lower", args, kw, 1, 1), r.Call("str_lower", arg(args, 0)))
	})

	// parse_float(s) -> float object or 0. The digits are read into an
	// exact decimal, so the result is correctly rounded.
	r.define("parse_float", p1, r1, func(f *Func) {
		s := f.Param(0)
		i, n, c, neg, eneg, ev, dot, seen, sig := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32),
			f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		v := f.Local(F64)
		d := r.decV(0)
		brk, cont := NewLabel(), NewLabel()
		at := func() Expr { return I32Load8U(add(sbytes(get(s)), get(i)), 0) }
		isDigit := func(x Expr) Expr { return and(ge(x, c32('0')), le(x, c32('9'))) }
		lower := r.Call("str_lower", get(s))
		f.Add(
			Set(n, slen(get(s))),
			If(eqz(get(n)), Return(c32(0))),
			Set(c, I32Load8U(sbytes(get(s)), 0)),
			If(or(eq(get(c), c32('-')), eq(get(c), c32('+'))), Seq{Set(neg, eq(get(c), c32('-'))), Set(i, c32(1))}),
			// inf / nan
			Set(c, r.Call("str_sub", lower, get(i), get(n))),
			If(or(r.Call("str_eq", get(c), r.S("inf")), r.Call("str_eq", get(c), r.S("infinity"))),
				Return(r.Call("float", IfV(F64, get(neg), F64c(negInf), F64c(posInf))))),
			If(r.Call("str_eq", get(c), r.S("nan")), Return(r.Call("float", F64Div(F64c(0), F64c(0))))),
			dsetNd(d, c32(0)), dsetDp(d, c32(0)), dsetTrunc(d, c32(0)),
			Block(brk, r.loop(cont,
				BrIf(brk, ge(get(i), get(n))),
				Set(c, at()),
				IfElse(isDigit(get(c)), []Expr{
					Set(seen, c32(1)),
					IfElse(and(eq(get(c), c32('0')), eqz(dnd(d))), []Expr{
						// leading zeros only move the decimal point
						dsetDp(d, sub(ddp(d), c32(1))),
					}, []Expr{
						// sig counts all significant digits, including those
						// dropped beyond decCap, for the decimal point.
						Set(sig, add(get(sig), c32(1))),
						IfElse(lt(dnd(d), c32(decCap)), []Expr{
							dset(d, dnd(d), sub(get(c), c32('0'))),
							dsetNd(d, add(dnd(d), c32(1))),
						}, []Expr{
							If(ne(get(c), c32('0')), dsetTrunc(d, c32(1))),
						}),
					}),
				}, []Expr{
					IfElse(and(eq(get(c), c32('.')), eqz(get(dot))), []Expr{
						Set(dot, c32(1)),
						dsetDp(d, get(sig)),
					}, []Expr{Br(brk)}),
				}),
				Set(i, add(get(i), c32(1))),
				Br(cont),
			)),
			If(eqz(get(seen)), Return(c32(0))),
			If(eqz(get(dot)), dsetDp(d, get(sig))),
			If(lt(get(i), get(n)), Seq{
				If(ne(I32Or(at(), c32(0x20)), c32('e')), Return(c32(0))),
				Set(i, add(get(i), c32(1))),
				If(ge(get(i), get(n)), Return(c32(0))),
				If(or(eq(at(), c32('-')), eq(at(), c32('+'))), Seq{Set(eneg, eq(at(), c32('-'))), Set(i, add(get(i), c32(1)))}),
				If(ge(get(i), get(n)), Return(c32(0))),
				r.forRange(i, get(i), get(n),
					If(not(isDigit(at())), Return(c32(0))),
					If(lt(get(ev), c32(10000)), Set(ev, add(mul(get(ev), c32(10)), sub(at(), c32('0'))))),
				),
				dsetDp(d, IfV(I32, get(eneg), sub(ddp(d), get(ev)), add(ddp(d), get(ev)))),
			}),
			Set(v, r.Call("dec_to_f64", d, get(neg))),
			If(F64Eq(F64Abs(get(v)), F64c(posInf)), r.Fail("floating-point number too large")),
			r.Call("float", get(v)),
		)
	})
}
