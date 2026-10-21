package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

// Exact decimal arithmetic for float conversion, after the "decimal"
// slow path of Go's strconv package. A decimal holds a big-endian digit
// string d[0:nd] (digit values 0-9, not ASCII) with the decimal point at
// dp, representing 0.d[0]d[1]...d[nd-1] * 10^dp. Shifting by powers of
// two is exact (up to decCap digits; trunc records discarded nonzero
// digits), which makes both directions of float conversion correctly
// rounded.
//
// Conversions never nest, so the decimals live in static memory.
const (
	decNd     = 0
	decDp     = 4
	decTrunc  = 8
	decDigits = 12
	decCap    = 800 // digits kept, as in strconv
	// A left shift writes up to decCap+19 digits before truncating.
	sizeDec = decDigits + decCap + 24

	decMaxShift = 60 // largest shift handled in one pass (64-bit accumulator)

	float64MantBits = 52
	float64Bias     = -1023
)

// decBuf returns the address of static decimal number i (0-2).
func (r *RT) decBuf(i int) uint32 {
	if r.decs[i] == 0 {
		r.decs[i] = r.M.Static(sizeDec, 8)
	}
	return r.decs[i]
}

// DecV yields the address of static decimal number i.
func (r *RT) decV(i int) Expr { return addr(r.decBuf(i)) }

func dnd(d Expr) Expr          { return ld(d, decNd) }
func ddp(d Expr) Expr          { return ld(d, decDp) }
func ddig(d, i Expr) Expr      { return I32Load8U(add(d, i), decDigits) }
func dset(d, i, v Expr) Expr   { return I32Store8(add(d, i), decDigits, v) }
func dsetNd(d, v Expr) Expr    { return st(d, decNd, v) }
func dsetDp(d, v Expr) Expr    { return st(d, decDp, v) }
func dsetTrunc(d, v Expr) Expr { return st(d, decTrunc, v) }
func i64(v Expr) Expr          { return I64ExtendI32U(v) }
func (r *RT) whileLoop(cond Expr, body ...Expr) Expr {
	brk, cont := NewLabel(), NewLabel()
	return Block(brk, r.loop(cont, BrIf(brk, not(cond)), Seq(body), Br(cont)))
}

func (r *RT) registerDecimal() {
	// dec_trim(d): drops trailing zeros.
	r.define("dec_trim", p1, r0, func(f *Func) {
		d := f.Param(0)
		f.Add(
			r.whileLoop(and(gt(dnd(get(d)), c32(0)), eqz(ddig(get(d), sub(dnd(get(d)), c32(1))))),
				dsetNd(get(d), sub(dnd(get(d)), c32(1)))),
			If(eqz(dnd(get(d))), dsetDp(get(d), c32(0))),
		)
	})
	// dec_assign(d, v): d = v (unsigned).
	r.define("dec_assign", []ValType{I32, I64}, r0, func(f *Func) {
		d, v := f.Param(0), f.Param(1)
		t := f.Local(I64)
		n, i := f.Local(I32), f.Local(I32)
		f.Add(
			Set(t, Get(v)),
			r.whileLoop(I64Ne(get(t), I64c(0)), Set(t, I64DivU(get(t), I64c(10))), Set(n, add(get(n), c32(1)))),
			Set(i, get(n)),
			r.whileLoop(gt(get(i), c32(0)),
				Set(i, sub(get(i), c32(1))),
				dset(get(d), get(i), I32WrapI64(I64RemU(Get(v), I64c(10)))),
				Set(v, I64DivU(Get(v), I64c(10))),
			),
			dsetNd(get(d), get(n)),
			dsetDp(get(d), get(n)),
			dsetTrunc(get(d), c32(0)),
			r.Call("dec_trim", get(d)),
		)
	})
	// dec_rshift(d, k): d /= 2^k for 0 < k <= decMaxShift.
	r.define("dec_rshift", p2, r0, func(f *Func) {
		d, k := f.Param(0), f.Param(1)
		rd, w, dig := f.Local(I32), f.Local(I32), f.Local(I32)
		n, kk, mask := f.Local(I64), f.Local(I64), f.Local(I64)
		brk, cont := NewLabel(), NewLabel()
		shifted := func() Expr { return I64ShrU(get(n), get(kk)) }
		f.Add(
			Set(kk, i64(get(k))),
			// Read digits until the value is at least 2^k.
			Block(brk, r.loop(cont,
				BrIf(brk, I64Ne(shifted(), I64c(0))),
				If(ge(get(rd), dnd(get(d))), Seq{
					If(I64Eqz(get(n)), Seq{dsetNd(get(d), c32(0)), Return(nil)}),
					r.whileLoop(I64Eqz(shifted()),
						Set(n, I64Mul(get(n), I64c(10))),
						Set(rd, add(get(rd), c32(1))),
					),
					Br(brk),
				}),
				Set(n, I64Add(I64Mul(get(n), I64c(10)), i64(ddig(get(d), get(rd))))),
				Set(rd, add(get(rd), c32(1))),
				Br(cont),
			)),
			dsetDp(get(d), sub(ddp(get(d)), sub(get(rd), c32(1)))),
			Set(mask, I64Sub(I64Shl(I64c(1), get(kk)), I64c(1))),
			// Pick up a digit, put down a digit.
			r.whileLoop(lt(get(rd), dnd(get(d))),
				Set(dig, I32WrapI64(shifted())),
				Set(n, I64And(get(n), get(mask))),
				dset(get(d), get(w), get(dig)),
				Set(w, add(get(w), c32(1))),
				Set(n, I64Add(I64Mul(get(n), I64c(10)), i64(ddig(get(d), get(rd))))),
				Set(rd, add(get(rd), c32(1))),
			),
			// Put down extra digits.
			r.whileLoop(I64Ne(get(n), I64c(0)),
				Set(dig, I32WrapI64(shifted())),
				Set(n, I64And(get(n), get(mask))),
				IfElse(lt(get(w), c32(decCap)), []Expr{
					dset(get(d), get(w), get(dig)),
					Set(w, add(get(w), c32(1))),
				}, []Expr{
					If(get(dig), dsetTrunc(get(d), c32(1))),
				}),
				Set(n, I64Mul(get(n), I64c(10))),
			),
			dsetNd(get(d), get(w)),
			r.Call("dec_trim", get(d)),
		)
	})
	// dec_lshift(d, k): d *= 2^k for 0 < k <= decMaxShift.
	r.define("dec_lshift", p2, r0, func(f *Func) {
		d, k := f.Param(0), f.Param(1)
		rd, w, delta, i := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		n, quo := f.Local(I64), f.Local(I64)
		put := func() Expr {
			return Seq{
				Set(quo, I64DivU(get(n), I64c(10))),
				Set(w, sub(get(w), c32(1))),
				dset(get(d), get(w), I32WrapI64(I64Sub(get(n), I64Mul(get(quo), I64c(10))))),
				Set(n, get(quo)),
			}
		}
		f.Add(
			// An upper bound of the number of new digits (k*log10(2)+1);
			// the digits are written right-aligned and moved down after.
			Set(delta, add(I32DivU(mul(get(k), c32(30103)), c32(100000)), c32(1))),
			Set(rd, dnd(get(d))),
			Set(w, add(dnd(get(d)), get(delta))),
			r.whileLoop(gt(get(rd), c32(0)),
				Set(rd, sub(get(rd), c32(1))),
				Set(n, I64Add(get(n), I64Shl(i64(ddig(get(d), get(rd))), i64(get(k))))),
				put(),
			),
			r.whileLoop(I64Ne(get(n), I64c(0)), put()),
			// Digits now occupy [w, nd+delta).
			Set(delta, sub(get(delta), get(w))),
			If(get(w), r.memcopy(add(get(d), c32(decDigits)), add(add(get(d), c32(decDigits)), get(w)),
				add(dnd(get(d)), get(delta)))),
			dsetNd(get(d), add(dnd(get(d)), get(delta))),
			dsetDp(get(d), add(ddp(get(d)), get(delta))),
			If(gt(dnd(get(d)), c32(decCap)), Seq{
				r.forRange(i, c32(decCap), dnd(get(d)), If(ddig(get(d), get(i)), dsetTrunc(get(d), c32(1)))),
				dsetNd(get(d), c32(decCap)),
			}),
			r.Call("dec_trim", get(d)),
		)
	})
	// dec_shift(d, k): d *= 2^k (k may be negative).
	r.define("dec_shift", p2, r0, func(f *Func) {
		d, k := f.Param(0), f.Param(1)
		f.Add(
			If(eqz(dnd(get(d))), Return(nil)),
			If(gt(get(k), c32(0)), Seq{
				r.whileLoop(gt(get(k), c32(decMaxShift)),
					r.Call("dec_lshift", get(d), c32(decMaxShift)),
					Set(k, sub(get(k), c32(decMaxShift)))),
				r.Call("dec_lshift", get(d), get(k)),
			}),
			If(lt(get(k), c32(0)), Seq{
				r.whileLoop(lt(get(k), c32(-decMaxShift)),
					r.Call("dec_rshift", get(d), c32(decMaxShift)),
					Set(k, add(get(k), c32(decMaxShift)))),
				r.Call("dec_rshift", get(d), sub(c32(0), get(k))),
			}),
		)
	})
	// dec_should_round_up(d, nd): whether rounding to nd digits rounds up
	// (half to even, unless digits were truncated).
	r.define("dec_should_round_up", p2, r1, func(f *Func) {
		d, nd := f.Param(0), f.Param(1)
		f.Add(
			If(or(lt(get(nd), c32(0)), ge(get(nd), dnd(get(d)))), Return(c32(0))),
			If(and(eq(ddig(get(d), get(nd)), c32(5)), eq(add(get(nd), c32(1)), dnd(get(d)))), Seq{
				If(ld(get(d), decTrunc), Return(c32(1))),
				Return(and(gt(get(nd), c32(0)), and(ddig(get(d), sub(get(nd), c32(1))), c32(1)))),
			}),
			ge(ddig(get(d), get(nd)), c32(5)),
		)
	})
	r.define("dec_round_down", p2, r0, func(f *Func) {
		d, nd := f.Param(0), f.Param(1)
		f.Add(
			If(or(lt(get(nd), c32(0)), ge(get(nd), dnd(get(d)))), Return(nil)),
			dsetNd(get(d), get(nd)),
			r.Call("dec_trim", get(d)),
		)
	})
	r.define("dec_round_up", p2, r0, func(f *Func) {
		d, nd := f.Param(0), f.Param(1)
		i := f.Local(I32)
		f.Add(
			If(or(lt(get(nd), c32(0)), ge(get(nd), dnd(get(d)))), Return(nil)),
			Set(i, get(nd)),
			r.whileLoop(gt(get(i), c32(0)),
				Set(i, sub(get(i), c32(1))),
				If(lt(ddig(get(d), get(i)), c32(9)), Seq{
					dset(get(d), get(i), add(ddig(get(d), get(i)), c32(1))),
					dsetNd(get(d), add(get(i), c32(1))),
					Return(nil),
				}),
			),
			// all nines: 0.99...9 rounds up to 1.0
			dset(get(d), c32(0), c32(1)),
			dsetNd(get(d), c32(1)),
			dsetDp(get(d), add(ddp(get(d)), c32(1))),
		)
	})
	r.define("dec_round", p2, r0, func(f *Func) {
		d, nd := f.Param(0), f.Param(1)
		f.Add(
			If(or(lt(get(nd), c32(0)), ge(get(nd), dnd(get(d)))), Return(nil)),
			IfElse(r.Call("dec_should_round_up", get(d), get(nd)),
				[]Expr{r.Call("dec_round_up", get(d), get(nd))},
				[]Expr{r.Call("dec_round_down", get(d), get(nd))}),
		)
	})
	// dec_rounded_int(d) -> d rounded to an unsigned integer
	r.define("dec_rounded_int", p1, ri, func(f *Func) {
		d := f.Param(0)
		i := f.Local(I32)
		n := f.Local(I64)
		f.Add(
			If(gt(ddp(get(d)), c32(20)), Return(I64c(-1))),
			r.whileLoop(and(lt(get(i), ddp(get(d))), lt(get(i), dnd(get(d)))),
				Set(n, I64Add(I64Mul(get(n), I64c(10)), i64(ddig(get(d), get(i))))),
				Set(i, add(get(i), c32(1))),
			),
			r.whileLoop(lt(get(i), ddp(get(d))),
				Set(n, I64Mul(get(n), I64c(10))),
				Set(i, add(get(i), c32(1))),
			),
			If(r.Call("dec_should_round_up", get(d), ddp(get(d))), Set(n, I64Add(get(n), I64c(1)))),
			get(n),
		)
	})

	// dec_to_f64(d, neg) -> the float64 nearest to d (±inf on overflow).
	r.define("dec_to_f64", p2, rf, func(f *Func) {
		d, neg := f.Param(0), f.Param(1)
		exp, n, out := f.Local(I32), f.Local(I32), f.Local(I32)
		mant, bits := f.Local(I64), f.Local(I64)
		// powtab[i]: a binary shift that keeps a number with i integral
		// decimal digits representable while bringing it toward [0.5, 1).
		pow := func(i Expr) Expr {
			x := c32(27)
			for k, v := range []int{26, 23, 19, 16, 13, 9, 6, 3, 1} {
				x = IfV(I32, eq(i, c32(8-k)), c32(v), x)
			}
			return x
		}
		overflow := Return(F64ReinterpI64(I64Or(I64c(0x7ff<<float64MantBits), I64Shl(i64(get(neg)), I64c(63)))))
		zero := Return(F64ReinterpI64(I64Shl(i64(get(neg)), I64c(63))))
		maxExp := c32((1 << 11) - 1)
		f.Add(
			If(eqz(dnd(get(d))), zero),
			If(gt(ddp(get(d)), c32(310)), overflow),
			If(lt(ddp(get(d)), c32(-330)), zero),
			// Scale by powers of two until the value is in [0.5, 1).
			r.whileLoop(gt(ddp(get(d)), c32(0)),
				Set(n, pow(ddp(get(d)))),
				r.Call("dec_shift", get(d), sub(c32(0), get(n))),
				Set(exp, add(get(exp), get(n))),
			),
			r.whileLoop(or(lt(ddp(get(d)), c32(0)), and(eqz(ddp(get(d))), lt(ddig(get(d), c32(0)), c32(5)))),
				Set(n, pow(sub(c32(0), ddp(get(d))))),
				r.Call("dec_shift", get(d), get(n)),
				Set(exp, sub(get(exp), get(n))),
			),
			// Now in [0.5, 1); the float exponent is for [1, 2).
			Set(exp, sub(get(exp), c32(1))),
			// Denormals have the minimum exponent.
			If(lt(get(exp), c32(float64Bias+1)), Seq{
				Set(n, sub(c32(float64Bias+1), get(exp))),
				r.Call("dec_shift", get(d), sub(c32(0), get(n))),
				Set(exp, add(get(exp), get(n))),
			}),
			If(ge(sub(get(exp), c32(float64Bias)), maxExp), overflow),
			// Extract 1+mantbits bits.
			r.Call("dec_shift", get(d), c32(1+float64MantBits)),
			Set(mant, r.Call("dec_rounded_int", get(d))),
			// Rounding might have added a bit; shift down.
			If(I64Eq(get(mant), I64c(2<<float64MantBits)), Seq{
				Set(mant, I64ShrU(get(mant), I64c(1))),
				Set(exp, add(get(exp), c32(1))),
				If(ge(sub(get(exp), c32(float64Bias)), maxExp), overflow),
			}),
			// Denormalized?
			If(I64Eqz(I64And(get(mant), I64c(1<<float64MantBits))), Set(exp, c32(float64Bias))),
			Set(out, sub(get(exp), c32(float64Bias))),
			Set(bits, I64Or(I64And(get(mant), I64c(1<<float64MantBits-1)),
				I64Shl(i64(and(get(out), maxExp)), I64c(float64MantBits)))),
			F64ReinterpI64(I64Or(get(bits), I64Shl(i64(get(neg)), I64c(63)))),
		)
	})

	// dec_shortest(x): sets decimal 0 to the shortest decimal that
	// rounds to the finite, positive float x.
	r.define("dec_shortest", pf, r0, func(f *Func) {
		x := f.Param(0)
		d, upper, lower := r.decV(0), r.decV(1), r.decV(2)
		exp, explo, ui, mi, li, l, m, u, okdown, okup, updelta, inclusive :=
			f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32),
			f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		bits, mant, mantlo := f.Local(I64), f.Local(I64), f.Local(I64)
		brk, cont := NewLabel(), NewLabel()
		minexp := c32(float64Bias + 1)
		f.Add(
			Set(bits, I64ReinterpF64(Get(x))),
			Set(exp, I32WrapI64(I64And(I64ShrU(get(bits), I64c(float64MantBits)), I64c(0x7ff)))),
			Set(mant, I64And(get(bits), I64c(1<<float64MantBits-1))),
			IfElse(eqz(get(exp)), []Expr{Set(exp, c32(1))}, []Expr{Set(mant, I64Or(get(mant), I64c(1<<float64MantBits)))}),
			Set(exp, add(get(exp), c32(float64Bias))),
			// The exact value mant * 2^(exp-mantbits).
			r.Call("dec_assign", d, get(mant)),
			r.Call("dec_shift", d, sub(get(exp), c32(float64MantBits))),
			// If the exact decimal is already short (integers), done.
			If(and(gt(get(exp), minexp), ge(mul(c32(332), sub(ddp(d), dnd(d))), mul(c32(100), sub(get(exp), c32(float64MantBits))))),
				Return(nil)),
			// The halfway points to the neighboring floats.
			r.Call("dec_assign", upper, I64Add(I64Mul(get(mant), I64c(2)), I64c(1))),
			r.Call("dec_shift", upper, sub(get(exp), c32(float64MantBits+1))),
			IfElse(or(I64GtU(get(mant), I64c(1<<float64MantBits)), eq(get(exp), minexp)), []Expr{
				Set(mantlo, I64Sub(get(mant), I64c(1))),
				Set(explo, get(exp)),
			}, []Expr{
				Set(mantlo, I64Sub(I64Mul(get(mant), I64c(2)), I64c(1))),
				Set(explo, sub(get(exp), c32(1))),
			}),
			r.Call("dec_assign", lower, I64Add(I64Mul(get(mantlo), I64c(2)), I64c(1))),
			r.Call("dec_shift", lower, sub(get(explo), c32(float64MantBits+1))),
			// With an even mantissa the halfway points round to x.
			Set(inclusive, I64Eqz(I64And(get(mant), I64c(1)))),
			// Find the shortest prefix within (lower, upper).
			Block(brk, r.loop(cont,
				Set(mi, add(sub(get(ui), ddp(upper)), ddp(d))),
				BrIf(brk, ge(get(mi), dnd(d))),
				Set(li, add(sub(get(ui), ddp(upper)), ddp(lower))),
				Set(l, IfV(I32, and(ge(get(li), c32(0)), lt(get(li), dnd(lower))), ddig(lower, get(li)), c32(0))),
				Set(m, IfV(I32, ge(get(mi), c32(0)), ddig(d, get(mi)), c32(0))),
				Set(u, IfV(I32, lt(get(ui), dnd(upper)), ddig(upper, get(ui)), c32(0))),
				// Truncating at this digit stays above lower?
				Set(okdown, or(ne(get(l), get(m)), and(get(inclusive), eq(add(get(li), c32(1)), dnd(lower))))),
				IfElse(and(eqz(get(updelta)), lt(add(get(m), c32(1)), get(u))), []Expr{Set(updelta, c32(2))}, []Expr{
					IfElse(and(eqz(get(updelta)), ne(get(m), get(u))), []Expr{Set(updelta, c32(1))}, []Expr{
						If(and(eq(get(updelta), c32(1)), or(ne(get(m), c32(9)), ne(get(u), c32(0)))), Set(updelta, c32(2))),
					}),
				}),
				// Rounding up at this digit stays below upper?
				Set(okup, and(ne(get(updelta), c32(0)),
					or(or(get(inclusive), gt(get(updelta), c32(1))), lt(add(get(ui), c32(1)), dnd(upper))))),
				If(and(get(okdown), get(okup)), Seq{r.Call("dec_round", d, add(get(mi), c32(1))), Return(nil)}),
				If(get(okdown), Seq{r.Call("dec_round_down", d, add(get(mi), c32(1))), Return(nil)}),
				If(get(okup), Seq{r.Call("dec_round_up", d, add(get(mi), c32(1))), Return(nil)}),
				Set(ui, add(get(ui), c32(1))),
				Br(cont),
			)),
		)
	})
}
