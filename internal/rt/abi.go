package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

// ABI value tags. See docs/abi.md.
const (
	ABINone  = 0
	ABIFalse = 1
	ABITrue  = 2
	ABIInt   = 3
	ABIFloat = 4
	ABIStr   = 5
	ABIList  = 6
	ABITuple = 7
	ABIDict  = 8
)

func (r *RT) registerABI() {
	// abi_alloc(n): resets the per-call arena and allocates n bytes for
	// the host to write call arguments into.
	r.define("abi_alloc", p1, r1, func(f *Func) {
		f.Add(r.Call("arena_reset"), r.Call("alloc", get(f.Param(0))))
	})
	// abi_error() -> packed (ptr | len<<32) of the last error message
	r.define("abi_error", p0, ri, func(f *Func) {
		e := f.Local(I32)
		f.Add(
			Set(e, GGet(r.ErrObj)),
			If(eqz(get(e)), Return(I64c(0))),
			I64Or(I64ExtendI32U(sbytes(get(e))), I64Shl(I64ExtendI32U(slen(get(e))), I64c(32))),
		)
	})
	// decode() -> value read at the cursor. The bytes up to CurEnd are
	// untrusted: a value that does not fit is an error, never a read
	// beyond the buffer.
	r.define("decode", p0, r1, func(f *Func) {
		t, n, i, o, k := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		cur := func() Expr { return GGet(r.Cursor) }
		adv := func(n Expr) Expr { return GSet(r.Cursor, add(cur(), n)) }
		// need fails unless n more bytes are available (unsigned, so that
		// lengths above 2^31 are rejected as well).
		need := func(n Expr) Expr {
			return If(I32GtU(n, sub(GGet(r.CurEnd), cur())), r.Fail("abi: truncated value"))
		}
		f.Add(
			need(c32(1)),
			Set(t, I32Load8U(cur(), 0)),
			adv(c32(1)),
			If(eq(get(t), c32(ABINone)), Return(r.NoneV())),
			If(eq(get(t), c32(ABIFalse)), Return(r.FalseV())),
			If(eq(get(t), c32(ABITrue)), Return(r.TrueV())),
			If(eq(get(t), c32(ABIInt)), Seq{need(c32(8)), Set(o, r.Call("int", I64Load(cur(), 0))), adv(c32(8)), Return(get(o))}),
			If(eq(get(t), c32(ABIFloat)), Seq{need(c32(8)), Set(o, r.Call("float", F64Load(cur(), 0))), adv(c32(8)), Return(get(o))}),
			If(gt(get(t), c32(ABIDict)), r.Fail("abi: invalid value tag ", r.Call("i64_to_str", I64ExtendI32U(get(t))))),
			need(c32(4)),
			Set(n, ld(cur(), 0)),
			adv(c32(4)),
			If(eq(get(t), c32(ABIStr)), Seq{need(get(n)), Set(o, r.Call("str_from", cur(), get(n))), adv(get(n)), Return(get(o))}),
			If(or(eq(get(t), c32(ABIList)), eq(get(t), c32(ABITuple))), Seq{
				// Each element takes at least one byte.
				need(get(n)),
				Set(o, r.Call("tuple_new", get(n))),
				forRange(i, c32(0), get(n), st(elem(get(o), offTupElems, get(i)), 0, r.Call("decode"))),
				Return(IfV(I32, eq(get(t), c32(ABIList)), r.Call("list_of", get(o)), get(o))),
			}),
			// dict: each entry takes at least two bytes.
			If(I32GtU(get(n), I32ShrU(sub(GGet(r.CurEnd), cur()), c32(1))), r.Fail("abi: truncated value")),
			Set(o, r.Call("dict_new")),
			forRange(i, c32(0), get(n),
				Set(k, r.Call("decode")),
				r.Call("dict_set", get(o), get(k), r.Call("decode"))),
			get(o),
		)
	})
	// decode_buf(ptr, n) -> the single value encoded in [ptr, ptr+n)
	r.define("decode_buf", p2, r1, func(f *Func) {
		ptr, n := f.Param(0), f.Param(1)
		v := f.Local(I32)
		f.Add(
			r.Call("decode_begin", get(ptr), get(n)),
			Set(v, r.Call("decode")),
			r.Call("decode_end"),
			get(v),
		)
	})
	// decode_begin(ptr, n) starts decoding the buffer [ptr, ptr+n).
	r.define("decode_begin", p2, r0, func(f *Func) {
		ptr, n := f.Param(0), f.Param(1)
		f.Add(
			If(or(I32GtU(get(ptr), I32Shl(MemorySize(), c32(16))), I32GtU(get(n), sub(I32Shl(MemorySize(), c32(16)), get(ptr)))),
				r.Fail("abi: buffer out of range")),
			GSet(r.Cursor, get(ptr)),
			GSet(r.CurEnd, add(get(ptr), get(n))),
		)
	})
	// decode_end() fails unless the whole buffer has been decoded.
	r.define("decode_end", p0, r0, func(f *Func) {
		f.Add(If(ne(GGet(r.Cursor), GGet(r.CurEnd)), r.Fail("abi: trailing bytes after value")))
	})
	// enc_size(v) -> encoded size in bytes
	r.define("enc_size", p1, r1, func(f *Func) {
		v := f.Param(0)
		t, n, i, s, e := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(t, tagOf(get(v))),
			If(or(eq(get(t), c32(TNone)), eq(get(t), c32(TBool))), Return(c32(1))),
			If(or(eq(get(t), c32(TInt)), eq(get(t), c32(TFloat))), Return(c32(9))),
			If(eq(get(t), c32(TString)), Return(add(c32(5), slen(get(v))))),
			If(or(eq(get(t), c32(TList)), eq(get(t), c32(TTuple))), Seq{
				Set(s, c32(5)),
				Set(n, ld(get(v), 4)),
				forRange(i, c32(0), get(n),
					Set(s, add(get(s), r.Call("enc_size", ld(elem(r.Call("seq_base", get(v)), 0, get(i)), 0))))),
				Return(get(s)),
			}),
			If(eq(get(t), c32(TDict)), Seq{
				Set(s, c32(5)),
				forRange(i, c32(0), ld(get(v), offDictUsed),
					Set(e, add(ld(get(v), offDictEntries), mul(get(i), c32(12)))),
					If(ld(get(e), 4), Set(s, add(get(s), add(r.Call("enc_size", ld(get(e), 4)), r.Call("enc_size", ld(get(e), 8))))))),
				Return(get(s)),
			}),
			r.Fail("cannot pass value of type ", r.TypeName(get(v)), " to the host"),
		)
	})
	// encode(v): writes v at the cursor
	r.define("encode", p1, r0, func(f *Func) {
		v := f.Param(0)
		t, n, i, e := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		cur := func() Expr { return GGet(r.Cursor) }
		adv := func(n Expr) Expr { return GSet(r.Cursor, add(cur(), n)) }
		putTag := func(x Expr) Expr { return Seq{I32Store8(cur(), 0, x), adv(c32(1))} }
		f.Add(
			Set(t, tagOf(get(v))),
			If(eq(get(t), c32(TNone)), Seq{putTag(c32(ABINone)), Return(nil)}),
			If(eq(get(t), c32(TBool)), Seq{putTag(add(c32(ABIFalse), ld(get(v), offBool))), Return(nil)}),
			If(eq(get(t), c32(TInt)), Seq{putTag(c32(ABIInt)), I64Store(cur(), 0, i64v(get(v))), adv(c32(8)), Return(nil)}),
			If(eq(get(t), c32(TFloat)), Seq{putTag(c32(ABIFloat)), F64Store(cur(), 0, f64v(get(v))), adv(c32(8)), Return(nil)}),
			If(eq(get(t), c32(TString)), Seq{
				putTag(c32(ABIStr)), st(cur(), 0, slen(get(v))), adv(c32(4)),
				MemoryCopy(cur(), sbytes(get(v)), slen(get(v))), adv(slen(get(v))),
				Return(nil),
			}),
			If(or(eq(get(t), c32(TList)), eq(get(t), c32(TTuple))), Seq{
				putTag(IfV(I32, eq(get(t), c32(TList)), c32(ABIList), c32(ABITuple))),
				Set(n, ld(get(v), 4)),
				st(cur(), 0, get(n)), adv(c32(4)),
				forRange(i, c32(0), get(n), r.Call("encode", ld(elem(r.Call("seq_base", get(v)), 0, get(i)), 0))),
				Return(nil),
			}),
			putTag(c32(ABIDict)),
			st(cur(), 0, ld(get(v), offDictLen)), adv(c32(4)),
			forRange(i, c32(0), ld(get(v), offDictUsed),
				Set(e, add(ld(get(v), offDictEntries), mul(get(i), c32(12)))),
				If(ld(get(e), 4), Seq{r.Call("encode", ld(get(e), 4)), r.Call("encode", ld(get(e), 8))})),
		)
	})
	// abi_result(v) -> packed (ptr | len<<32) of the encoded value
	r.define("abi_result", p1, ri, func(f *Func) {
		n, p := f.Local(I32), f.Local(I32)
		f.Add(
			Set(n, r.Call("enc_size", get(f.Param(0)))),
			Set(p, r.Call("alloc", get(n))),
			GSet(r.Cursor, get(p)),
			r.Call("encode", get(f.Param(0))),
			I64Or(I64ExtendI32U(get(p)), I64Shl(I64ExtendI32U(get(n)), I64c(32))),
		)
	})
	// abi_args(ptr, len, out) -> args tuple; stores kwargs dict (or 0) at out
	r.define("abi_args", p3, r1, func(f *Func) {
		ptr, n, out := f.Param(0), f.Param(1), f.Param(2)
		a, k := f.Local(I32), f.Local(I32)
		f.Add(
			st(get(out), 0, c32(0)),
			If(eqz(get(n)), Return(r.Call("tuple_new", c32(0)))),
			r.Call("decode_begin", get(ptr), get(n)),
			Set(a, r.Call("decode")),
			If(not(isTag(get(a), TTuple)), Set(a, r.Call("tuple_from", get(a)))),
			If(I32LtU(GGet(r.Cursor), GGet(r.CurEnd)), Seq{
				Set(k, r.Call("decode")),
				If(not(isTag(get(k), TDict)), r.Fail("abi: keyword arguments must be a dict")),
				st(get(out), 0, get(k)),
			}),
			r.Call("decode_end"),
			get(a),
		)
	})
}
