package rt

import (
	"math"

	. "github.com/timzifer/starc/internal/wasm"
)

// Binary operator names, used in error messages and by the compiler.
const (
	OpAdd      = "+"
	OpSub      = "-"
	OpMul      = "*"
	OpDiv      = "/"
	OpFloorDiv = "//"
	OpMod      = "%"
	OpAnd      = "&"
	OpOr       = "|"
	OpXor      = "^"
	OpShl      = "<<"
	OpShr      = ">>"
)

// BinOps maps operator spellings to runtime function names.
var BinOps = map[string]string{
	OpAdd: "add", OpSub: "sub", OpMul: "mul", OpDiv: "div", OpFloorDiv: "floordiv",
	OpMod: "mod", OpAnd: "bitand", OpOr: "bitor", OpXor: "bitxor", OpShl: "shl", OpShr: "shr",
}

// compareLimit is Starlark's depth limit on recursive comparisons.
const compareLimit = 10

// Small ints in [-intCacheMin, intCacheSize-intCacheMin) are preallocated.
const (
	intCacheMin  = 16
	intCacheSize = 1040
)

func i64v(v Expr) Expr { return I64Load(v, offInt) }
func f64v(v Expr) Expr { return F64Load(v, offFloat) }

func (r *RT) registerNum() {
	r.define("int", pi, r1, func(f *Func) {
		o := f.Local(I32)
		f.Add(
			If(and(ne(GGet(r.IntCache), c32(0)), I64LtU(I64Add(Get(f.Param(0)), I64c(intCacheMin)), I64c(intCacheSize))),
				Return(ld(add(GGet(r.IntCache), I32Shl(I32WrapI64(I64Add(Get(f.Param(0)), I64c(intCacheMin))), c32(2))), 0))),
			Set(o, r.Call("alloc", c32(16))),
			st(get(o), 0, c32(TInt)),
			I64Store(get(o), offInt, Get(f.Param(0))),
			get(o),
		)
	})
	r.define("float", pf, r1, func(f *Func) {
		o := f.Local(I32)
		f.Add(
			Set(o, r.Call("alloc", c32(16))),
			st(get(o), 0, c32(TFloat)),
			F64Store(get(o), offFloat, Get(f.Param(0))),
			get(o),
		)
	})
	// as_int(v, what) -> i64: requires an int.
	r.define("as_int", p2, ri, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(not(isTag(get(v), TInt)),
				r.Fail(get(f.Param(1)), ": got ", r.TypeName(get(v)), ", want int")),
			i64v(get(v)),
		)
	})
	// as_i32(v, what) -> i32: int in int32 range.
	r.define("as_i32", p2, r1, func(f *Func) {
		x := f.Local(I64)
		f.Add(
			Set(x, r.Call("as_int", get(f.Param(0)), get(f.Param(1)))),
			If(or(I64LtS(get(x), I64c(math.MinInt32)), I64GtS(get(x), I64c(math.MaxInt32))),
				r.Fail(get(f.Param(1)), ": int out of range")),
			I32WrapI64(get(x)),
		)
	})
	// is_num(v)
	r.define("is_num", p1, r1, func(f *Func) {
		v := f.Param(0)
		f.Add(or(isTag(get(v), TInt), isTag(get(v), TFloat)))
	})
	// to_f64(v) -> f64 for an int or float
	r.define("to_f64", p1, rf, func(f *Func) {
		v := f.Param(0)
		f.Add(IfV(F64, isTag(get(v), TInt), F64ConvI64S(i64v(get(v))), f64v(get(v))))
	})

	r.defineArith("add", OpAdd)
	r.defineArith("sub", OpSub)
	r.defineArith("mul", OpMul)
	r.defineArith("div", OpDiv)
	r.defineArith("floordiv", OpFloorDiv)
	r.defineArith("mod", OpMod)
	r.defineArith("bitand", OpAnd)
	r.defineArith("bitor", OpOr)
	r.defineArith("bitxor", OpXor)
	r.defineArith("shl", OpShl)
	r.defineArith("shr", OpShr)

	r.define("binop_error", p3, r0, func(f *Func) {
		f.Add(r.Fail("unknown binary op: ", r.TypeName(get(f.Param(0))), " ", get(f.Param(2)), " ",
			r.TypeName(get(f.Param(1)))))
	})

	r.define("overflow", p0, r0, func(f *Func) { f.Add(r.Fail("int overflow")) })

	// unary operators
	r.define("neg", p1, r1, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(isTag(get(v), TInt), Seq{
				If(I64Eq(i64v(get(v)), I64c(math.MinInt64)), r.Call("overflow")),
				Return(r.Call("int", I64Sub(I64c(0), i64v(get(v))))),
			}),
			If(isTag(get(v), TFloat), Return(r.Call("float", F64Neg(f64v(get(v)))))),
			r.Fail("unknown unary op: -", r.TypeName(get(v))),
		)
	})
	r.define("pos", p1, r1, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(r.Call("is_num", get(v)), Return(get(v))),
			r.Fail("unknown unary op: +", r.TypeName(get(v))),
		)
	})
	r.define("invert", p1, r1, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(isTag(get(v), TInt), Return(r.Call("int", I64Xor(i64v(get(v)), I64c(-1))))),
			r.Fail("unknown unary op: ~", r.TypeName(get(v))),
		)
	})
	r.define("not", p1, r1, func(f *Func) {
		f.Add(r.bool(not(r.Call("truth", get(f.Param(0))))))
	})

	// i64 helpers with overflow checks
	r.define("iadd", pii, ri, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		res := f.Local(I64)
		f.Add(
			Set(res, I64Add(Get(a), Get(b))),
			If(I64LtS(I64And(I64Xor(Get(a), Get(res)), I64Xor(Get(b), Get(res))), I64c(0)), r.Call("overflow")),
			Get(res),
		)
	})
	r.define("isub", pii, ri, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		res := f.Local(I64)
		f.Add(
			Set(res, I64Sub(Get(a), Get(b))),
			If(I64LtS(I64And(I64Xor(Get(a), Get(b)), I64Xor(Get(a), Get(res))), I64c(0)), r.Call("overflow")),
			Get(res),
		)
	})
	r.define("imul", pii, ri, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		res := f.Local(I64)
		f.Add(
			If(or(I64Eqz(Get(a)), I64Eqz(Get(b))), Return(I64c(0))),
			If(or(and(I64Eq(Get(a), I64c(-1)), I64Eq(Get(b), I64c(math.MinInt64))),
				and(I64Eq(Get(b), I64c(-1)), I64Eq(Get(a), I64c(math.MinInt64)))), r.Call("overflow")),
			Set(res, I64Mul(Get(a), Get(b))),
			If(I64Ne(I64DivS(Get(res), Get(b)), Get(a)), r.Call("overflow")),
			Get(res),
		)
	})
	r.define("ifloordiv", pii, ri, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		q := f.Local(I64)
		f.Add(
			If(I64Eqz(Get(b)), r.Fail("floored division by zero")),
			If(and(I64Eq(Get(a), I64c(math.MinInt64)), I64Eq(Get(b), I64c(-1))), r.Call("overflow")),
			Set(q, I64DivS(Get(a), Get(b))),
			If(and(I64Ne(I64RemS(Get(a), Get(b)), I64c(0)),
				ne(I64LtS(Get(a), I64c(0)), I64LtS(Get(b), I64c(0)))),
				Set(q, I64Sub(Get(q), I64c(1)))),
			Get(q),
		)
	})
	r.define("imod", pii, ri, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		m := f.Local(I64)
		f.Add(
			If(I64Eqz(Get(b)), r.Fail("integer modulo by zero")),
			If(I64Eq(Get(b), I64c(-1)), Return(I64c(0))),
			Set(m, I64RemS(Get(a), Get(b))),
			If(and(I64Ne(Get(m), I64c(0)), ne(I64LtS(Get(m), I64c(0)), I64LtS(Get(b), I64c(0)))),
				Set(m, I64Add(Get(m), Get(b)))),
			Get(m),
		)
	})
	r.define("fmod", []ValType{F64, F64}, rf, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		m := f.Local(F64)
		f.Add(
			If(F64Eq(Get(b), F64c(0)), r.Fail("floating-point modulo by zero")),
			Set(m, r.Call("fmod_exact", Get(a), Get(b))),
			If(and(F64Ne(Get(m), F64c(0)), ne(F64Lt(Get(m), F64c(0)), F64Lt(Get(b), F64c(0)))),
				Set(m, F64Add(Get(m), Get(b)))),
			Get(m),
		)
	})

	// fmod_exact(x, y) -> the exact remainder of x/y with the sign of x,
	// like C's fmod. The quotient is never rounded: the remainder is found
	// by binary long division on the significands.
	r.define("fmod_exact", []ValType{F64, F64}, rf, func(f *Func) {
		x, y := f.Param(0), f.Param(1)
		ux, uy, d, sx := f.Local(I64), f.Local(I64), f.Local(I64), f.Local(I64)
		ex, ey := f.Local(I64), f.Local(I64)
		brk, cont := NewLabel(), NewLabel()
		const mant = int64(1)<<52 - 1
		// normalize(u, e): u becomes the significand with an explicit
		// leading bit at position 52; e the matching biased exponent.
		normalize := func(u, e Local) Expr {
			brk, cont := NewLabel(), NewLabel()
			return IfElse(I64Eqz(Get(e)), []Expr{
				// subnormal: shift the leading one up to bit 52
				Set(u, I64And(Get(u), I64c(mant))),
				Set(e, I64c(1)),
				Block(brk, Loop(cont,
					BrIf(brk, I64Ne(I64And(Get(u), I64c(1<<52)), I64c(0))),
					Set(u, I64Shl(Get(u), I64c(1))),
					Set(e, I64Sub(Get(e), I64c(1))),
					Br(cont),
				)),
			}, []Expr{
				Set(u, I64Or(I64And(Get(u), I64c(mant)), I64c(1<<52))),
			})
		}
		f.Add(
			Set(ux, I64ReinterpF64(Get(x))),
			Set(uy, I64ReinterpF64(Get(y))),
			Set(sx, I64And(Get(ux), I64c(math.MinInt64))),
			Set(ex, I64And(I64ShrU(Get(ux), I64c(52)), I64c(0x7ff))),
			Set(ey, I64And(I64ShrU(Get(uy), I64c(52)), I64c(0x7ff))),
			// NaN or infinite x, NaN y, or zero y: NaN
			If(or(or(I64Eq(Get(ex), I64c(0x7ff)), F64Ne(Get(y), Get(y))), F64Eq(Get(y), F64c(0))),
				Return(F64Div(F64Mul(Get(x), Get(y)), F64Mul(Get(x), Get(y))))),
			// |x| < |y| (including infinite y): x; |x| == |y|: signed zero
			If(I64LtU(I64Shl(Get(ux), I64c(1)), I64Shl(Get(uy), I64c(1))), Return(Get(x))),
			If(I64Eq(I64Shl(Get(ux), I64c(1)), I64Shl(Get(uy), I64c(1))), Return(F64Mul(F64c(0), Get(x)))),
			normalize(ux, ex),
			normalize(uy, ey),
			// long division: reduce ux modulo uy one exponent step at a time
			Block(brk, Loop(cont,
				BrIf(brk, I64LeS(Get(ex), Get(ey))),
				Set(d, I64Sub(Get(ux), Get(uy))),
				If(I64GeS(Get(d), I64c(0)), Seq{
					If(I64Eqz(Get(d)), Return(F64Mul(F64c(0), Get(x)))),
					Set(ux, Get(d)),
				}),
				Set(ux, I64Shl(Get(ux), I64c(1))),
				Set(ex, I64Sub(Get(ex), I64c(1))),
				Br(cont),
			)),
			Set(d, I64Sub(Get(ux), Get(uy))),
			If(I64GeS(Get(d), I64c(0)), Seq{
				If(I64Eqz(Get(d)), Return(F64Mul(F64c(0), Get(x)))),
				Set(ux, Get(d)),
			}),
			// renormalize so the leading bit is at position 52
			Block(brk, Loop(cont,
				BrIf(brk, I64Ne(I64And(Get(ux), I64c(1<<52)), I64c(0))),
				Set(ux, I64Shl(Get(ux), I64c(1))),
				Set(ex, I64Sub(Get(ex), I64c(1))),
				Br(cont),
			)),
			IfElse(I64GtS(Get(ex), I64c(0)), []Expr{
				Set(ux, I64Or(I64And(Get(ux), I64c(mant)), I64Shl(Get(ex), I64c(52)))),
			}, []Expr{
				Set(ux, I64ShrU(Get(ux), I64Sub(I64c(1), Get(ex)))),
			}),
			F64ReinterpI64(I64Or(Get(ux), Get(sx))),
		)
	})

	// cmp_int_float(i, x) -> -1, 0, 1 comparing an int with a non-NaN
	// float exactly, without rounding the int to float64.
	r.define("cmp_int_float", []ValType{I64, F64}, r1, func(f *Func) {
		i, x := f.Param(0), f.Param(1)
		t := f.Local(F64)
		ti := f.Local(I64)
		f.Add(
			If(F64Ge(Get(x), F64c(9223372036854775808.0)), Return(c32(-1))),
			If(F64Lt(Get(x), F64c(-9223372036854775808.0)), Return(c32(1))),
			// x is in int64 range, so its integral part converts exactly.
			Set(t, F64Trunc(Get(x))),
			Set(ti, I64TruncSatF64S(get(t))),
			If(I64LtS(Get(i), get(ti)), Return(c32(-1))),
			If(I64GtS(Get(i), get(ti)), Return(c32(1))),
			IfV(I32, F64Gt(Get(x), get(t)), c32(-1), IfV(I32, F64Lt(Get(x), get(t)), c32(1), c32(0))),
		)
	})

	// cmp(a, b, op) -> -1, 0, 1 (ordered comparison)
	r.define("cmp", p3, r1, func(f *Func) {
		a, b, opn := f.Param(0), f.Param(1), f.Param(2)
		ta, tb, i, n, c := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		x, y := f.Local(F64), f.Local(F64)
		la, lb := f.Local(I32), f.Local(I32)
		pa, pb := f.Local(I32), f.Local(I32)
		brk := NewLabel()
		cmp3 := func(lt, gt Expr) Expr {
			return IfV(I32, lt, c32(-1), IfV(I32, gt, c32(1), c32(0)))
		}
		f.Add(
			Set(ta, tagOf(get(a))), Set(tb, tagOf(get(b))),
			If(and(eq(get(ta), c32(TInt)), eq(get(tb), c32(TInt))),
				Return(cmp3(I64LtS(i64v(get(a)), i64v(get(b))), I64GtS(i64v(get(a)), i64v(get(b)))))),
			If(and(r.Call("is_num", get(a)), r.Call("is_num", get(b))), Seq{
				Set(x, r.Call("to_f64", get(a))), Set(y, r.Call("to_f64", get(b))),
				// NaN sorts above all other values and equals itself.
				If(F64Ne(get(x), get(x)), Return(IfV(I32, F64Ne(get(y), get(y)), c32(0), c32(1)))),
				If(F64Ne(get(y), get(y)), Return(c32(-1))),
				If(eq(get(ta), c32(TInt)), Return(r.Call("cmp_int_float", i64v(get(a)), get(y)))),
				If(eq(get(tb), c32(TInt)), Return(sub(c32(0), r.Call("cmp_int_float", i64v(get(b)), get(x))))),
				Return(cmp3(F64Lt(get(x), get(y)), F64Gt(get(x), get(y)))),
			}),
			If(ne(get(ta), get(tb)), r.Call("cmp_error", get(a), get(b), get(opn))),
			If(eq(get(ta), c32(TString)), Return(r.Call("str_cmp", get(a), get(b)))),
			If(eq(get(ta), c32(TBool)),
				Return(sub(ld(get(a), offBool), ld(get(b), offBool)))),
			If(or(eq(get(ta), c32(TTuple)), eq(get(ta), c32(TList))), Seq{
				Set(la, ld(get(a), 4)), Set(lb, ld(get(b), 4)),
				Set(pa, r.Call("seq_base", get(a))), Set(pb, r.Call("seq_base", get(b))),
				Set(n, Select(get(la), get(lb), lt(get(la), get(lb)))),
				If(get(n), r.Call("cmp_enter")),
				forRangeL(i, c32(0), get(n), brk,
					If(not(r.Call("eq", ld(elem(get(pa), 0, get(i)), 0), ld(elem(get(pb), 0, get(i)), 0))), Seq{
						Set(c, r.Call("cmp", ld(elem(get(pa), 0, get(i)), 0), ld(elem(get(pb), 0, get(i)), 0), get(opn))),
						r.Call("cmp_leave"),
						Return(get(c)),
					}),
				),
				If(get(n), r.Call("cmp_leave")),
				Return(cmp3(lt(get(la), get(lb)), gt(get(la), get(lb)))),
			}),
			r.Call("cmp_error", get(a), get(b), get(opn)),
			Unreachable(),
		)
	})
	// cmp_enter/cmp_leave bracket the comparison of container elements.
	// Like Starlark, comparisons nested compareLimit deep fail; this also
	// bounds the recursion on cyclic values. fail resets the depth.
	r.define("cmp_enter", p0, r0, func(f *Func) {
		f.Add(
			GSet(r.CmpDepth, add(GGet(r.CmpDepth), c32(1))),
			If(ge(GGet(r.CmpDepth), c32(compareLimit)), r.Fail("comparison exceeded maximum recursion depth")),
		)
	})
	r.define("cmp_leave", p0, r0, func(f *Func) {
		f.Add(GSet(r.CmpDepth, sub(GGet(r.CmpDepth), c32(1))))
	})
	r.define("cmp_error", p3, r0, func(f *Func) {
		f.Add(r.Fail(r.TypeName(get(f.Param(0))), " ", get(f.Param(2)), " ", r.TypeName(get(f.Param(1))),
			" not implemented"))
	})

	// eq(a, b) -> i32
	r.define("eq", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		ta, tb, i, n, ent, v := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		pa, pb := f.Local(I32), f.Local(I32)
		la64 := f.Local(I64)
		f.Add(
			Set(ta, tagOf(get(a))), Set(tb, tagOf(get(b))),
			If(and(eq(get(ta), c32(TInt)), eq(get(tb), c32(TInt))),
				Return(I64Eq(i64v(get(a)), i64v(get(b))))),
			If(and(r.Call("is_num", get(a)), r.Call("is_num", get(b))),
				Return(eqz(r.Call("cmp", get(a), get(b), r.S("=="))))),
			If(ne(get(ta), get(tb)), Return(c32(0))),
			// Containers are compared element-wise even if identical, so that
			// cyclic values hit the depth limit as in Starlark.
			If(and(eq(get(a), get(b)), not(or(or(eq(get(ta), c32(TList)), eq(get(ta), c32(TTuple))), eq(get(ta), c32(TDict))))),
				Return(c32(1))),
			If(eq(get(ta), c32(TString)), Return(r.Call("str_eq", get(a), get(b)))),
			If(eq(get(ta), c32(TBool)), Return(eq(ld(get(a), offBool), ld(get(b), offBool)))),
			If(or(eq(get(ta), c32(TTuple)), eq(get(ta), c32(TList))), Seq{
				Set(n, ld(get(a), 4)),
				If(ne(get(n), ld(get(b), 4)), Return(c32(0))),
				If(eqz(get(n)), Return(c32(1))),
				Set(pa, r.Call("seq_base", get(a))), Set(pb, r.Call("seq_base", get(b))),
				r.Call("cmp_enter"),
				forRange(i, c32(0), get(n),
					If(not(r.Call("eq", ld(elem(get(pa), 0, get(i)), 0), ld(elem(get(pb), 0, get(i)), 0))),
						Seq{r.Call("cmp_leave"), Return(c32(0))})),
				r.Call("cmp_leave"),
				Return(c32(1)),
			}),
			If(eq(get(ta), c32(TDict)), Seq{
				If(ne(ld(get(a), offDictLen), ld(get(b), offDictLen)), Return(c32(0))),
				If(eqz(ld(get(a), offDictLen)), Return(c32(1))),
				forRange(i, c32(0), ld(get(a), offDictUsed),
					Set(ent, add(ld(get(a), offDictEntries), mul(get(i), c32(12)))),
					If(ld(get(ent), 4), Seq{
						// Key lookup is a fresh comparison, as in Starlark.
						Set(n, GGet(r.CmpDepth)),
						GSet(r.CmpDepth, c32(0)),
						Set(v, r.Call("dict_get", get(b), ld(get(ent), 4))),
						GSet(r.CmpDepth, get(n)),
						If(eqz(get(v)), Return(c32(0))),
						r.Call("cmp_enter"),
						Set(v, r.Call("eq", get(v), ld(get(ent), 8))),
						r.Call("cmp_leave"),
						If(eqz(get(v)), Return(c32(0))),
					}),
				),
				Return(c32(1)),
			}),
			// Ranges are equal if they denote the same sequence.
			If(eq(get(ta), c32(TRange)), Seq{
				Set(la64, r.Call("range_len", get(a))),
				If(I64Ne(get(la64), r.Call("range_len", get(b))), Return(c32(0))),
				If(I64Eqz(get(la64)), Return(c32(1))),
				If(I64Ne(I64Load(get(a), offRangeStart), I64Load(get(b), offRangeStart)), Return(c32(0))),
				Return(or(I64Eq(get(la64), I64c(1)), I64Eq(I64Load(get(a), offRangeStep), I64Load(get(b), offRangeStep)))),
			}),
			c32(0),
		)
	})

	// hash(v) -> i32
	r.define("hash", p1, r1, func(f *Func) {
		v := f.Param(0)
		t, h, i := f.Local(I32), f.Local(I32), f.Local(I32)
		x := f.Local(I64)
		fl := f.Local(F64)
		mix := func(x Expr) Expr {
			return I32WrapI64(I64Xor(x, I64ShrU(x, I64c(32))))
		}
		f.Add(
			Set(t, tagOf(get(v))),
			If(eq(get(t), c32(TString)), Return(r.Call("str_hash", get(v)))),
			If(eq(get(t), c32(TInt)), Return(mix(I64Mul(i64v(get(v)), I64c(-7046029254386353131))))),
			If(eq(get(t), c32(TFloat)), Seq{
				Set(fl, f64v(get(v))),
				If(and(F64Eq(F64Trunc(get(fl)), get(fl)), and(F64Ge(get(fl), F64c(-9223372036854775808.0)), F64Lt(get(fl), F64c(9223372036854775808.0)))), Seq{
					Set(x, I64TruncSatF64S(get(fl))),
					Return(mix(I64Mul(get(x), I64c(-7046029254386353131)))),
				}),
				Return(mix(I64Mul(I64ReinterpF64(get(fl)), I64c(-7046029254386353131)))),
			}),
			If(eq(get(t), c32(TNone)), Return(c32(0x1f2e3d4c))),
			If(eq(get(t), c32(TBool)), Return(add(ld(get(v), offBool), c32(0x5a5a)))),
			If(eq(get(t), c32(TTuple)), Seq{
				Set(h, c32(0x345678)),
				forRange(i, c32(0), ld(get(v), offTupLen),
					Set(h, add(mul(I32Xor(get(h), r.Call("hash", ld(elem(get(v), offTupElems, get(i)), 0))), c32(1000003)), get(i)))),
				Return(get(h)),
			}),
			If(or(or(eq(get(t), c32(TList)), eq(get(t), c32(TDict))), or(eq(get(t), c32(TRange)), eq(get(t), c32(TModule)))),
				r.Fail("unhashable type: ", r.TypeName(get(v)))),
			mul(get(v), c32(-1640531535)),
		)
	})
}

// defineArith defines a dynamically dispatched binary operator.
func (r *RT) defineArith(name, opname string) {
	r.define(name, p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		ta, tb := f.Local(I32), f.Local(I32)
		x, y := f.Local(F64), f.Local(F64)
		f.Add(Set(ta, tagOf(get(a))), Set(tb, tagOf(get(b))))
		bothInt := and(eq(get(ta), c32(TInt)), eq(get(tb), c32(TInt)))
		bothNum := and(r.Call("is_num", get(a)), r.Call("is_num", get(b)))
		ia, ib := i64v(get(a)), i64v(get(b))
		intRes := func(e Expr) Expr { return If(bothInt, Return(r.Call("int", e))) }
		floatRes := func(e Expr) Expr {
			return If(bothNum, Seq{
				Set(x, r.Call("to_f64", get(a))), Set(y, r.Call("to_f64", get(b))),
				Return(r.Call("float", e)),
			})
		}
		switch opname {
		case OpAdd:
			f.Add(
				intRes(r.Call("iadd", ia, ib)),
				floatRes(F64Add(get(x), get(y))),
				If(and(eq(get(ta), c32(TString)), eq(get(tb), c32(TString))), Return(r.Call("str_concat", get(a), get(b)))),
				If(and(eq(get(ta), c32(TList)), eq(get(tb), c32(TList))), Return(r.Call("list_concat", get(a), get(b)))),
				If(and(eq(get(ta), c32(TTuple)), eq(get(tb), c32(TTuple))), Return(r.Call("tuple_concat", get(a), get(b)))),
			)
		case OpSub:
			f.Add(intRes(r.Call("isub", ia, ib)), floatRes(F64Sub(get(x), get(y))))
		case OpMul:
			f.Add(
				intRes(r.Call("imul", ia, ib)),
				floatRes(F64Mul(get(x), get(y))),
				If(eq(get(tb), c32(TInt)), Return(r.Call("repeat", get(a), get(b), get(a), get(b)))),
				If(eq(get(ta), c32(TInt)), Return(r.Call("repeat", get(b), get(a), get(a), get(b)))),
			)
		case OpDiv:
			f.Add(
				If(bothNum, Seq{
					Set(x, r.Call("to_f64", get(a))), Set(y, r.Call("to_f64", get(b))),
					If(F64Eq(get(y), F64c(0)), r.Fail("floating-point division by zero")),
					Return(r.Call("float", F64Div(get(x), get(y)))),
				}),
			)
		case OpFloorDiv:
			f.Add(
				intRes(r.Call("ifloordiv", ia, ib)),
				If(bothNum, Seq{
					Set(x, r.Call("to_f64", get(a))), Set(y, r.Call("to_f64", get(b))),
					If(F64Eq(get(y), F64c(0)), r.Fail("floored division by zero")),
					Return(r.Call("float", F64Floor(F64Div(get(x), get(y))))),
				}),
			)
		case OpMod:
			f.Add(
				intRes(r.Call("imod", ia, ib)),
				floatRes(r.Call("fmod", get(x), get(y))),
				If(eq(get(ta), c32(TString)), Return(r.hook("percent_format", get(a), get(b)))),
			)
		case OpAnd:
			f.Add(intRes(I64And(ia, ib)))
		case OpOr:
			f.Add(
				intRes(I64Or(ia, ib)),
				If(and(eq(get(ta), c32(TDict)), eq(get(tb), c32(TDict))), Return(r.Call("dict_union", get(a), get(b)))),
			)
		case OpXor:
			f.Add(intRes(I64Xor(ia, ib)))
		case OpShl, OpShr:
			s := f.Local(I64)
			res := f.Local(I64)
			body := Seq{
				Set(s, ib),
				If(I64LtS(get(s), I64c(0)), r.Fail("negative shift count")),
			}
			if opname == OpShl {
				body = append(body,
					If(I64Eqz(ia), Return(get(a))),
					If(I64GeS(get(s), I64c(64)), r.Call("overflow")),
					Set(res, I64Shl(ia, get(s))),
					If(I64Ne(I64ShrS(get(res), get(s)), ia), r.Call("overflow")),
					Return(r.Call("int", get(res))),
				)
			} else {
				body = append(body,
					If(I64GeS(get(s), I64c(64)), Set(s, I64c(63))),
					Return(r.Call("int", I64ShrS(ia, get(s)))),
				)
			}
			f.Add(If(bothInt, body))
		}
		f.Add(r.Call("binop_error", get(a), get(b), r.S(opname)), Unreachable())
	})
}

// hook calls a compiler-provided Starlark helper with two arguments.
func (r *RT) hook(name string, args ...Expr) Expr {
	h, ok := r.Hooks[name]
	if !ok {
		panic("rt: missing runtime hook " + name)
	}
	return r.Call("call", h(), r.Call("tuple"+string(rune('0'+len(args))), args...), c32(0))
}

func inf(sign int) float64 { return math.Inf(sign) }
