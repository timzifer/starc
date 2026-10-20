package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

func sbytes(s Expr) Expr { return add(s, c32(offStrBytes)) }
func slen(s Expr) Expr   { return ld(s, offStrLen) }

func (r *RT) registerStr() {
	// str_new(n) -> string object with n uninitialized bytes
	r.define("str_new", p1, r1, func(f *Func) {
		s := f.Local(I32)
		f.Add(
			Set(s, r.Call("alloc", add(get(f.Param(0)), c32(offStrBytes)))),
			st(get(s), 0, c32(TString)),
			st(get(s), offStrLen, get(f.Param(0))),
			get(s),
		)
	})
	// str_from(ptr, n) -> string copied from memory
	r.define("str_from", p2, r1, func(f *Func) {
		s := f.Local(I32)
		f.Add(
			Set(s, r.Call("str_new", get(f.Param(1)))),
			MemoryCopy(sbytes(get(s)), get(f.Param(0)), get(f.Param(1))),
			get(s),
		)
	})
	r.define("str_concat", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		s := f.Local(I32)
		f.Add(
			If(eqz(slen(get(a))), Return(get(b))),
			If(eqz(slen(get(b))), Return(get(a))),
			Set(s, r.Call("str_new", add(slen(get(a)), slen(get(b))))),
			MemoryCopy(sbytes(get(s)), sbytes(get(a)), slen(get(a))),
			MemoryCopy(add(sbytes(get(s)), slen(get(a))), sbytes(get(b)), slen(get(b))),
			get(s),
		)
	})
	// mem_cmp(p, q, n) -> -1/0/1 bytewise unsigned
	r.define("mem_cmp", p3, r1, func(f *Func) {
		p, q, n := f.Param(0), f.Param(1), f.Param(2)
		i, x, y := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			forRange(i, c32(0), get(n),
				Set(x, I32Load8U(add(get(p), get(i)), 0)),
				Set(y, I32Load8U(add(get(q), get(i)), 0)),
				If(I32LtU(get(x), get(y)), Return(c32(-1))),
				If(I32GtU(get(x), get(y)), Return(c32(1))),
			),
			c32(0),
		)
	})
	r.define("str_eq", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		f.Add(
			If(eq(get(a), get(b)), Return(c32(1))),
			If(ne(slen(get(a)), slen(get(b))), Return(c32(0))),
			eqz(r.Call("mem_cmp", sbytes(get(a)), sbytes(get(b)), slen(get(a)))),
		)
	})
	r.define("str_cmp", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		la, lb, c := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(la, slen(get(a))), Set(lb, slen(get(b))),
			Set(c, r.Call("mem_cmp", sbytes(get(a)), sbytes(get(b)), Select(get(la), get(lb), lt(get(la), get(lb))))),
			If(get(c), Return(get(c))),
			IfV(I32, lt(get(la), get(lb)), c32(-1), IfV(I32, gt(get(la), get(lb)), c32(1), c32(0))),
		)
	})
	r.define("str_hash", p1, r1, func(f *Func) {
		s := f.Param(0)
		h, i := f.Local(I32), f.Local(I32)
		f.Add(
			Set(h, ld(get(s), offStrHash)),
			If(get(h), Return(get(h))),
			Set(h, U32c(2166136261)),
			forRange(i, c32(0), slen(get(s)),
				Set(h, mul(I32Xor(get(h), I32Load8U(add(sbytes(get(s)), get(i)), 0)), c32(16777619)))),
			If(eqz(get(h)), Set(h, c32(1))),
			st(get(s), offStrHash, get(h)),
			get(h),
		)
	})
	// str_sub(s, start, end) -> s[start:end] (byte indices, already clamped)
	r.define("str_sub", p3, r1, func(f *Func) {
		s, a, b := f.Param(0), f.Param(1), f.Param(2)
		f.Add(
			If(le(get(b), get(a)), Return(r.S(""))),
			If(and(eqz(get(a)), eq(get(b), slen(get(s)))), Return(get(s))),
			r.Call("str_from", add(sbytes(get(s)), get(a)), sub(get(b), get(a))),
		)
	})
	// str_find(s, sub, start, end) -> index or -1
	r.define("str_find", p4, r1, func(f *Func) {
		s, x, a, b := f.Param(0), f.Param(1), f.Param(2), f.Param(3)
		i, n := f.Local(I32), f.Local(I32)
		f.Add(
			Set(n, slen(get(x))),
			forRange(i, get(a), add(sub(get(b), get(n)), c32(1)),
				If(eqz(r.Call("mem_cmp", add(sbytes(get(s)), get(i)), sbytes(get(x)), get(n))), Return(get(i)))),
			c32(-1),
		)
	})
	// str_rfind(s, sub, start, end) -> index or -1
	r.define("str_rfind", p4, r1, func(f *Func) {
		s, x, a, b := f.Param(0), f.Param(1), f.Param(2), f.Param(3)
		i, n := f.Local(I32), f.Local(I32)
		brk, cont := NewLabel(), NewLabel()
		f.Add(
			Set(n, slen(get(x))),
			Set(i, sub(get(b), get(n))),
			Block(brk, Loop(cont,
				BrIf(brk, lt(get(i), get(a))),
				If(eqz(r.Call("mem_cmp", add(sbytes(get(s)), get(i)), sbytes(get(x)), get(n))), Return(get(i))),
				Set(i, sub(get(i), c32(1))),
				Br(cont),
			)),
			c32(-1),
		)
	})
	// str_join(sep, seq) -> string
	r.define("str_join", p2, r1, func(f *Func) {
		sep, seq := f.Param(0), f.Param(1)
		base, n, i, total, s, p, e := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(seq, r.Call("iter_seq", get(seq))),
			Set(n, ld(get(seq), 4)),
			Set(base, r.Call("seq_base", get(seq))),
			If(eqz(get(n)), Return(r.S(""))),
			forRange(i, c32(0), get(n),
				Set(e, ld(elem(get(base), 0, get(i)), 0)),
				If(not(isTag(get(e), TString)),
					r.Fail("join: in list, want string, got ", r.TypeName(get(e)))),
				Set(total, add(get(total), slen(get(e)))),
			),
			Set(total, add(get(total), mul(slen(get(sep)), sub(get(n), c32(1))))),
			Set(s, r.Call("str_new", get(total))),
			Set(p, sbytes(get(s))),
			forRange(i, c32(0), get(n),
				If(get(i), Seq{
					MemoryCopy(get(p), sbytes(get(sep)), slen(get(sep))),
					Set(p, add(get(p), slen(get(sep)))),
				}),
				Set(e, ld(elem(get(base), 0, get(i)), 0)),
				MemoryCopy(get(p), sbytes(get(e)), slen(get(e))),
				Set(p, add(get(p), slen(get(e)))),
			),
			get(s),
		)
	})
	// i64_to_str(x) -> decimal string
	r.define("i64_to_str", pi, r1, func(f *Func) {
		x := f.Param(0)
		u := f.Local(I64)
		buf, p, neg := f.Local(I32), f.Local(I32), f.Local(I32)
		brk, cont := NewLabel(), NewLabel()
		f.Add(
			Set(buf, r.Call("alloc", c32(24))),
			Set(p, add(get(buf), c32(24))),
			Set(neg, I64LtS(Get(x), I64c(0))),
			Set(u, IfV(I64, get(neg), I64Sub(I64c(0), Get(x)), Get(x))),
			Block(brk, Loop(cont,
				Set(p, sub(get(p), c32(1))),
				I32Store8(get(p), 0, add(I32WrapI64(I64RemU(get(u), I64c(10))), c32('0'))),
				Set(u, I64DivU(get(u), I64c(10))),
				BrIf(cont, I64Ne(get(u), I64c(0))),
			)),
			If(get(neg), Seq{Set(p, sub(get(p), c32(1))), I32Store8(get(p), 0, c32('-'))}),
			r.Call("str_from", get(p), sub(add(get(buf), c32(24)), get(p))),
		)
	})
	r.defineFloatToStr()

	// quote(s) -> Starlark string literal
	r.define("quote", p1, r1, func(f *Func) {
		s := f.Param(0)
		buf, p, i, c, out := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		esc := func(ch byte) Expr {
			return Seq{I32Store8(get(p), 0, c32('\\')), I32Store8(get(p), 1, c32(int(ch))), Set(p, add(get(p), c32(2)))}
		}
		const hex = "0123456789abcdef"
		hexTab := r.Str(hex)
		f.Add(
			Set(buf, r.Call("alloc", add(mul(slen(get(s)), c32(4)), c32(2)))),
			Set(p, get(buf)),
			I32Store8(get(p), 0, c32('"')), Set(p, add(get(p), c32(1))),
			forRange(i, c32(0), slen(get(s)),
				Set(c, I32Load8U(add(sbytes(get(s)), get(i)), 0)),
				IfElse(eq(get(c), c32('"')), []Expr{esc('"')}, []Expr{
					IfElse(eq(get(c), c32('\\')), []Expr{esc('\\')}, []Expr{
						IfElse(eq(get(c), c32('\n')), []Expr{esc('n')}, []Expr{
							IfElse(eq(get(c), c32('\t')), []Expr{esc('t')}, []Expr{
								IfElse(eq(get(c), c32('\r')), []Expr{esc('r')}, []Expr{
									IfElse(or(I32LtU(get(c), c32(0x20)), eq(get(c), c32(0x7f))), []Expr{
										I32Store8(get(p), 0, c32('\\')),
										I32Store8(get(p), 1, c32('x')),
										I32Store8(get(p), 2, I32Load8U(add(addr(hexTab+offStrBytes), I32ShrU(get(c), c32(4))), 0)),
										I32Store8(get(p), 3, I32Load8U(add(addr(hexTab+offStrBytes), and(get(c), c32(15))), 0)),
										Set(p, add(get(p), c32(4))),
									}, []Expr{
										I32Store8(get(p), 0, get(c)), Set(p, add(get(p), c32(1))),
									}),
								}),
							}),
						}),
					}),
				}),
			),
			I32Store8(get(p), 0, c32('"')), Set(p, add(get(p), c32(1))),
			Set(out, r.Call("str_from", get(buf), sub(get(p), get(buf)))),
			get(out),
		)
	})

	// str(v) -> string
	r.define("str", p1, r1, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(isTag(get(v), TString), Return(get(v))),
			r.Call("repr", get(v)),
		)
	})

	// repr(v) -> string
	r.define("repr", p1, r1, func(f *Func) {
		v := f.Param(0)
		t, parts, i, n, base, ent := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		push := func(x Expr) Expr { return r.Call("list_append_raw", get(parts), x) }
		f.Add(
			Set(t, tagOf(get(v))),
			If(eq(get(t), c32(TNone)), Return(r.S("None"))),
			If(eq(get(t), c32(TBool)), Return(IfV(I32, ld(get(v), offBool), r.S("True"), r.S("False")))),
			If(eq(get(t), c32(TInt)), Return(r.Call("i64_to_str", i64v(get(v))))),
			If(eq(get(t), c32(TFloat)), Return(r.Call("f64_to_str", f64v(get(v))))),
			If(eq(get(t), c32(TString)), Return(r.Call("quote", get(v)))),
			Set(parts, r.Call("list_new", c32(8))),
			If(or(eq(get(t), c32(TList)), eq(get(t), c32(TTuple))), Seq{
				push(IfV(I32, eq(get(t), c32(TList)), r.S("["), r.S("("))),
				Set(n, ld(get(v), 4)),
				Set(base, r.Call("seq_base", get(v))),
				forRange(i, c32(0), get(n),
					If(get(i), push(r.S(", "))),
					push(r.Call("repr", ld(elem(get(base), 0, get(i)), 0))),
				),
				If(and(eq(get(t), c32(TTuple)), eq(get(n), c32(1))), push(r.S(","))),
				push(IfV(I32, eq(get(t), c32(TList)), r.S("]"), r.S(")"))),
				Return(r.Call("str_join", r.S(""), get(parts))),
			}),
			If(eq(get(t), c32(TDict)), Seq{
				push(r.S("{")),
				forRange(i, c32(0), ld(get(v), offDictUsed),
					Set(ent, add(ld(get(v), offDictEntries), mul(get(i), c32(12)))),
					If(ld(get(ent), 4), Seq{
						If(get(n), push(r.S(", "))),
						Set(n, c32(1)),
						push(r.Call("repr", ld(get(ent), 4))),
						push(r.S(": ")),
						push(r.Call("repr", ld(get(ent), 8))),
					}),
				),
				push(r.S("}")),
				Return(r.Call("str_join", r.S(""), get(parts))),
			}),
			If(eq(get(t), c32(TFunction)),
				Return(r.Msg("<function ", ld(ld(get(v), offFnSpec), offSpecName), ">"))),
			If(eq(get(t), c32(TBuiltin)),
				Return(r.Msg("<built-in function ", ld(get(v), offBiName), ">"))),
			If(eq(get(t), c32(TBound)),
				Return(r.Msg("<built-in method ", r.Call("callable_name", ld(get(v), offBoundFn)), " of ",
					r.TypeName(ld(get(v), offBoundRecv)), " value>"))),
			If(eq(get(t), c32(TRange)), Seq{
				push(r.S("range(")),
				If(I64Ne(I64Load(get(v), offRangeStart), I64c(0)), Seq{
					push(r.Call("i64_to_str", I64Load(get(v), offRangeStart))),
					push(r.S(", ")),
				}),
				push(r.Call("i64_to_str", I64Load(get(v), offRangeStop))),
				If(I64Ne(I64Load(get(v), offRangeStep), I64c(1)), Seq{
					push(r.S(", ")),
					push(r.Call("i64_to_str", I64Load(get(v), offRangeStep))),
				}),
				push(r.S(")")),
				Return(r.Call("str_join", r.S(""), get(parts))),
			}),
			r.Msg("<", r.TypeName(get(v)), ">"),
		)
	})

	// str_upper / str_lower (ASCII)
	for _, c := range []struct {
		name   string
		lo, hi int
		delta  int
	}{{"str_upper", 'a', 'z', -32}, {"str_lower", 'A', 'Z', 32}} {
		r.define(c.name, p1, r1, func(f *Func) {
			s := f.Param(0)
			o, i, ch := f.Local(I32), f.Local(I32), f.Local(I32)
			f.Add(
				Set(o, r.Call("str_from", sbytes(get(s)), slen(get(s)))),
				forRange(i, c32(0), slen(get(s)),
					Set(ch, I32Load8U(add(sbytes(get(o)), get(i)), 0)),
					If(and(ge(get(ch), c32(c.lo)), le(get(ch), c32(c.hi))),
						I32Store8(add(sbytes(get(o)), get(i)), 0, add(get(ch), c32(c.delta)))),
				),
				get(o),
			)
		})
	}

	// parse_int(s, base) -> int object, or 0 if invalid. Accepts sign,
	// surrounding whitespace is not permitted (matching Starlark's int()).
	r.define("parse_int", p2, r1, func(f *Func) {
		s, base := f.Param(0), f.Param(1)
		i, n, c, d, neg := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		acc := f.Local(I64)
		f.Add(
			Set(n, slen(get(s))),
			If(eqz(get(n)), Return(c32(0))),
			Set(c, I32Load8U(sbytes(get(s)), 0)),
			If(or(eq(get(c), c32('-')), eq(get(c), c32('+'))), Seq{
				Set(neg, eq(get(c), c32('-'))),
				Set(i, c32(1)),
			}),
			// optional 0x/0o/0b prefix
			If(lt(add(get(i), c32(1)), get(n)), Seq{
				If(eq(I32Load8U(add(sbytes(get(s)), get(i)), 0), c32('0')), Seq{
					Set(c, I32Or(I32Load8U(add(sbytes(get(s)), get(i)), 1), c32(0x20))),
					If(and(eq(get(c), c32('x')), or(eq(get(base), c32(16)), eqz(get(base)))), Seq{Set(base, c32(16)), Set(i, add(get(i), c32(2)))}),
					If(and(eq(get(c), c32('o')), or(eq(get(base), c32(8)), eqz(get(base)))), Seq{Set(base, c32(8)), Set(i, add(get(i), c32(2)))}),
					If(and(eq(get(c), c32('b')), or(eq(get(base), c32(2)), eqz(get(base)))), Seq{Set(base, c32(2)), Set(i, add(get(i), c32(2)))}),
				}),
			}),
			If(eqz(get(base)), Set(base, c32(10))),
			If(ge(get(i), get(n)), Return(c32(0))),
			forRange(i, get(i), get(n),
				Set(c, I32Load8U(add(sbytes(get(s)), get(i)), 0)),
				Set(d, IfV(I32, and(ge(get(c), c32('0')), le(get(c), c32('9'))), sub(get(c), c32('0')),
					IfV(I32, and(ge(I32Or(get(c), c32(0x20)), c32('a')), le(I32Or(get(c), c32(0x20)), c32('z'))),
						add(sub(I32Or(get(c), c32(0x20)), c32('a')), c32(10)), c32(99)))),
				If(eq(get(c), c32('_')), Set(d, c32(-1))),
				If(ge(get(d), get(base)), Return(c32(0))),
				If(ge(get(d), c32(0)),
					Set(acc, r.Call("iadd", r.Call("imul", get(acc), I64ExtendI32S(get(base))), I64ExtendI32S(get(d))))),
			),
			r.Call("int", IfV(I64, get(neg), I64Sub(I64c(0), get(acc)), get(acc))),
		)
	})
}

// defineFloatToStr defines f64_to_str, Starlark's %g-style shortest
// formatting of floats. Digits are found by trying increasing precision
// until the value round-trips through float64 arithmetic.
func (r *RT) defineFloatToStr() {
	// scale(v, j) = v * 10^j
	r.define("f64_scale", []ValType{F64, I32}, rf, func(f *Func) {
		v, j := f.Param(0), f.Param(1)
		p, i := f.Local(F64), f.Local(I32)
		brk, cont := NewLabel(), NewLabel()
		f.Add(
			Block(brk, Loop(cont,
				BrIf(brk, le(get(j), c32(22))),
				Set(v, F64Mul(get(v), F64c(1e22))),
				Set(j, sub(get(j), c32(22))),
				Br(cont),
			)),
			Block(brk, Loop(cont,
				BrIf(brk, ge(get(j), c32(-22))),
				Set(v, F64Div(get(v), F64c(1e22))),
				Set(j, add(get(j), c32(22))),
				Br(cont),
			)),
			Set(p, F64c(1)),
			forRange(i, c32(0), Select(get(j), sub(c32(0), get(j)), ge(get(j), c32(0))),
				Set(p, F64Mul(get(p), F64c(10)))),
			IfV(F64, ge(get(j), c32(0)), F64Mul(get(v), get(p)), F64Div(get(v), get(p))),
		)
	})
	r.define("f64_to_str", pf, r1, func(f *Func) {
		x := f.Param(0)
		ax, y := f.Local(F64), f.Local(F64)
		e, p, k, nd, dp, i, buf, q, neg, exp, digits, ds := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32),
			f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		nn := f.Local(I64)
		brk, cont := NewLabel(), NewLabel()
		put := func(c Expr) Expr { return Seq{I32Store8(get(q), 0, c), Set(q, add(get(q), c32(1)))} }
		digit := func(i Expr) Expr { return I32Load8U(add(get(ds), i), 0) }
		f.Add(
			If(F64Ne(Get(x), Get(x)), Return(r.S("nan"))),
			If(F64Eq(Get(x), F64c(posInf)), Return(r.S("+inf"))),
			If(F64Eq(Get(x), F64c(negInf)), Return(r.S("-inf"))),
			If(F64Eq(Get(x), F64c(0)), Return(IfV(I32, I64LtS(I64ReinterpF64(Get(x)), I64c(0)), r.S("-0.0"), r.S("0.0")))),
			Set(neg, F64Lt(Get(x), F64c(0))),
			Set(ax, F64Abs(Get(x))),
			// estimate decimal exponent
			Set(y, get(ax)),
			Block(brk, Loop(cont, BrIf(brk, F64Lt(get(y), F64c(10))), Set(y, F64Div(get(y), F64c(10))), Set(e, add(get(e), c32(1))), Br(cont))),
			Block(brk, Loop(cont, BrIf(brk, F64Ge(get(y), F64c(1))), Set(y, F64Mul(get(y), F64c(10))), Set(e, sub(get(e), c32(1))), Br(cont))),
			// find the shortest precision that round-trips
			Set(p, c32(1)),
			Block(brk, Loop(cont,
				Set(k, add(sub(get(e), get(p)), c32(1))),
				Set(nn, I64TruncSatF64S(F64Nearest(r.Call("f64_scale", get(ax), sub(c32(0), get(k)))))),
				BrIf(brk, F64Eq(r.Call("f64_scale", F64ConvI64S(get(nn)), get(k)), get(ax))),
				BrIf(brk, ge(get(p), c32(17))),
				Set(p, add(get(p), c32(1))),
				Br(cont),
			)),
			// strip trailing zeros
			Block(brk, Loop(cont,
				BrIf(brk, or(I64Eqz(get(nn)), I64Ne(I64RemU(get(nn), I64c(10)), I64c(0)))),
				Set(nn, I64DivU(get(nn), I64c(10))),
				Set(k, add(get(k), c32(1))),
				Br(cont),
			)),
			Set(digits, r.Call("i64_to_str", get(nn))),
			Set(ds, sbytes(get(digits))),
			Set(nd, slen(get(digits))),
			Set(dp, add(get(nd), get(k))),
			Set(exp, sub(get(dp), c32(1))),
			Set(buf, r.Call("alloc", c32(400))),
			Set(q, get(buf)),
			If(get(neg), put(c32('-'))),
			IfElse(or(lt(get(exp), c32(-4)), ge(get(exp), c32(6))), []Expr{
				put(digit(c32(0))),
				If(gt(get(nd), c32(1)), Seq{
					put(c32('.')),
					forRange(i, c32(1), get(nd), put(digit(get(i)))),
				}),
				put(c32('e')),
				IfElse(lt(get(exp), c32(0)), []Expr{put(c32('-')), Set(exp, sub(c32(0), get(exp)))}, []Expr{put(c32('+'))}),
				If(lt(get(exp), c32(10)), put(c32('0'))),
				Set(digits, r.Call("i64_to_str", I64ExtendI32S(get(exp)))),
				MemoryCopy(get(q), sbytes(get(digits)), slen(get(digits))),
				Set(q, add(get(q), slen(get(digits)))),
			}, []Expr{
				IfElse(le(get(dp), c32(0)), []Expr{
					put(c32('0')), put(c32('.')),
					forRange(i, get(dp), c32(0), put(c32('0'))),
					forRange(i, c32(0), get(nd), put(digit(get(i)))),
				}, []Expr{
					IfElse(ge(get(dp), get(nd)), []Expr{
						forRange(i, c32(0), get(nd), put(digit(get(i)))),
						forRange(i, get(nd), get(dp), put(c32('0'))),
						put(c32('.')), put(c32('0')),
					}, []Expr{
						forRange(i, c32(0), get(dp), put(digit(get(i)))),
						put(c32('.')),
						forRange(i, get(dp), get(nd), put(digit(get(i)))),
					}),
				}),
			}),
			r.Call("str_from", get(buf), sub(get(q), get(buf))),
		)
	})
}

var (
	posInf = inf(1)
	negInf = inf(-1)
)
