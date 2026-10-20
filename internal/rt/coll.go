package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

func (r *RT) registerColl() {
	// ---- tuples ----
	r.define("tuple_new", p1, r1, func(f *Func) {
		t := f.Local(I32)
		f.Add(
			Set(t, r.Call("alloc", add(c32(offTupElems), I32Shl(get(f.Param(0)), c32(2))))),
			st(get(t), 0, c32(TTuple)),
			st(get(t), offTupLen, get(f.Param(0))),
			get(t),
		)
	})
	for n := 0; n <= 3; n++ {
		params := make([]ValType, n)
		for i := range params {
			params[i] = I32
		}
		r.define("tuple"+string(rune('0'+n)), params, r1, func(f *Func) {
			t := f.Local(I32)
			f.Add(Set(t, r.Call("tuple_new", c32(n))))
			for i := 0; i < n; i++ {
				f.Add(st(get(t), uint32(offTupElems+4*i), get(f.Param(i))))
			}
			f.Add(get(t))
		})
	}
	// seq_base(v) -> address of element 0 of a tuple or list
	r.define("seq_base", p1, r1, func(f *Func) {
		v := f.Param(0)
		f.Add(IfV(I32, isTag(get(v), TList), ld(get(v), offListData), add(get(v), c32(offTupElems))))
	})
	r.define("tuple_concat", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		t := f.Local(I32)
		f.Add(
			Set(t, r.Call("tuple_new", add(ld(get(a), 4), ld(get(b), 4)))),
			MemoryCopy(add(get(t), c32(offTupElems)), add(get(a), c32(offTupElems)), I32Shl(ld(get(a), 4), c32(2))),
			MemoryCopy(elem(get(t), offTupElems, ld(get(a), 4)), add(get(b), c32(offTupElems)), I32Shl(ld(get(b), 4), c32(2))),
			get(t),
		)
	})
	// tuple_from(iterable)
	r.define("tuple_from", p1, r1, func(f *Func) {
		v := f.Param(0)
		s, t := f.Local(I32), f.Local(I32)
		f.Add(
			If(isTag(get(v), TTuple), Return(get(v))),
			Set(s, r.Call("iter_seq", get(v))),
			Set(t, r.Call("tuple_new", ld(get(s), 4))),
			MemoryCopy(add(get(t), c32(offTupElems)), r.Call("seq_base", get(s)), I32Shl(ld(get(s), 4), c32(2))),
			get(t),
		)
	})

	// ---- lists ----
	r.define("list_new", p1, r1, func(f *Func) {
		l, c := f.Local(I32), f.Local(I32)
		f.Add(
			Set(c, Select(get(f.Param(0)), c32(4), gt(get(f.Param(0)), c32(4)))),
			Set(l, r.Call("alloc", c32(sizeList))),
			st(get(l), 0, c32(TList)),
			st(get(l), offListCap, get(c)),
			st(get(l), offListData, r.Call("alloc", I32Shl(get(c), c32(2)))),
			get(l),
		)
	})
	// list_of(tuple) -> list with the tuple's elements
	r.define("list_of", p1, r1, func(f *Func) {
		t := f.Param(0)
		l := f.Local(I32)
		f.Add(
			Set(l, r.Call("list_new", ld(get(t), offTupLen))),
			MemoryCopy(ld(get(l), offListData), add(get(t), c32(offTupElems)), I32Shl(ld(get(t), offTupLen), c32(2))),
			st(get(l), offListLen, ld(get(t), offTupLen)),
			get(l),
		)
	})
	r.define("list_reserve", p2, r0, func(f *Func) {
		l, n := f.Param(0), f.Param(1)
		c, d := f.Local(I32), f.Local(I32)
		f.Add(
			If(le(get(n), ld(get(l), offListCap)), Return(nil)),
			Set(c, I32Shl(ld(get(l), offListCap), c32(1))),
			If(lt(get(c), get(n)), Set(c, get(n))),
			Set(d, r.Call("alloc", I32Shl(get(c), c32(2)))),
			MemoryCopy(get(d), ld(get(l), offListData), I32Shl(ld(get(l), offListLen), c32(2))),
			st(get(l), offListData, get(d)),
			st(get(l), offListCap, get(c)),
		)
	})
	r.define("list_append_raw", p2, r0, func(f *Func) {
		l, v := f.Param(0), f.Param(1)
		n := f.Local(I32)
		f.Add(
			Set(n, ld(get(l), offListLen)),
			r.Call("list_reserve", get(l), add(get(n), c32(1))),
			st(elem(ld(get(l), offListData), 0, get(n)), 0, get(v)),
			st(get(l), offListLen, add(get(n), c32(1))),
		)
	})
	r.define("list_check_mut", p1, r0, func(f *Func) {
		l := f.Param(0)
		f.Add(
			r.Call("check_mutable", get(l)),
			If(ld(get(l), offListIters), r.Fail("cannot modify list during iteration")),
		)
	})
	r.define("list_append", p2, r0, func(f *Func) {
		f.Add(r.Call("list_check_mut", get(f.Param(0))), r.Call("list_append_raw", get(f.Param(0)), get(f.Param(1))))
	})
	r.define("list_concat", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		l := f.Local(I32)
		f.Add(
			Set(l, r.Call("list_new", add(ld(get(a), 4), ld(get(b), 4)))),
			MemoryCopy(ld(get(l), offListData), ld(get(a), offListData), I32Shl(ld(get(a), 4), c32(2))),
			MemoryCopy(elem(ld(get(l), offListData), 0, ld(get(a), 4)), ld(get(b), offListData), I32Shl(ld(get(b), 4), c32(2))),
			st(get(l), offListLen, add(ld(get(a), 4), ld(get(b), 4))),
			get(l),
		)
	})
	// list_extend(l, iterable)
	r.define("list_extend", p2, r0, func(f *Func) {
		l, v := f.Param(0), f.Param(1)
		s, n, i := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			r.Call("list_check_mut", get(l)),
			Set(s, r.Call("iter_begin", get(v))),
			Set(n, r.Call("seq_len", get(s))),
			forRange(i, c32(0), get(n), r.Call("list_append_raw", get(l), r.Call("seq_at", get(s), get(i)))),
			r.Call("iter_end", get(v)),
		)
	})
	// list_from(iterable) -> new list
	r.define("list_from", p1, r1, func(f *Func) {
		v := f.Param(0)
		l := f.Local(I32)
		f.Add(
			Set(l, r.Call("list_new", c32(0))),
			r.Call("list_extend", get(l), get(v)),
			get(l),
		)
	})
	// iter_seq(v) -> list or tuple holding v's elements (v itself if possible)
	r.define("iter_seq", p1, r1, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(or(isTag(get(v), TList), isTag(get(v), TTuple)), Return(get(v))),
			r.Call("list_from", get(v)),
		)
	})
	// repeat(seq, n, a, b): seq * n
	r.define("repeat", p4, r1, func(f *Func) {
		s, nv := f.Param(0), f.Param(1)
		n, i, res, sz, t, base := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		x := f.Local(I64)
		f.Add(
			Set(t, tagOf(get(s))),
			If(not(or(eq(get(t), c32(TString)), or(eq(get(t), c32(TList)), eq(get(t), c32(TTuple))))),
				Seq{r.Call("binop_error", get(f.Param(2)), get(f.Param(3)), r.S("*")), Unreachable()}),
			Set(x, i64v(get(nv))),
			If(I64LtS(get(x), I64c(0)), Set(x, I64c(0))),
			Set(sz, ld(get(s), 4)),
			If(and(ne(get(sz), c32(0)), I64GtS(get(x), I64DivS(I64c(1<<28), I64ExtendI32U(get(sz))))),
				r.Fail("excessive repeat")),
			Set(n, I32WrapI64(get(x))),
			If(eq(get(t), c32(TString)), Seq{
				Set(res, r.Call("str_new", mul(get(sz), get(n)))),
				forRange(i, c32(0), get(n),
					MemoryCopy(add(sbytes(get(res)), mul(get(i), get(sz))), sbytes(get(s)), get(sz))),
				Return(get(res)),
			}),
			Set(res, r.Call("tuple_new", mul(get(sz), get(n)))),
			Set(base, r.Call("seq_base", get(s))),
			forRange(i, c32(0), get(n),
				MemoryCopy(elem(get(res), offTupElems, mul(get(i), get(sz))), get(base), I32Shl(get(sz), c32(2)))),
			If(eq(get(t), c32(TList)), Return(r.Call("list_of", get(res)))),
			get(res),
		)
	})

	// ---- dicts ----
	r.define("dict_new", p0, r1, func(f *Func) {
		d := f.Local(I32)
		f.Add(
			Set(d, r.Call("alloc", c32(sizeDict))),
			st(get(d), 0, c32(TDict)),
			get(d),
		)
	})
	// dict_lookup(d, k, h) -> entry address or 0
	r.define("dict_lookup", p3, r1, func(f *Func) {
		d, k, h := f.Param(0), f.Param(1), f.Param(2)
		mask, i, s, e := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		brk, cont := NewLabel(), NewLabel()
		f.Add(
			If(eqz(ld(get(d), offDictICap)), Return(c32(0))),
			Set(mask, sub(ld(get(d), offDictICap), c32(1))),
			Set(i, and(get(h), get(mask))),
			Block(brk, Loop(cont,
				Set(s, ld(elem(ld(get(d), offDictIndex), 0, get(i)), 0)),
				BrIf(brk, eqz(get(s))),
				Set(e, add(ld(get(d), offDictEntries), mul(sub(get(s), c32(1)), c32(12)))),
				If(and(ne(ld(get(e), 4), c32(0)), eq(ld(get(e), 0), get(h))),
					If(r.Call("eq", ld(get(e), 4), get(k)), Return(get(e)))),
				Set(i, and(add(get(i), c32(1)), get(mask))),
				Br(cont),
			)),
			c32(0),
		)
	})
	// dict_index_insert(d, slot, h): records entry number slot (1-based)
	r.define("dict_index_insert", p3, r0, func(f *Func) {
		d, s, h := f.Param(0), f.Param(1), f.Param(2)
		mask, i, idx := f.Local(I32), f.Local(I32), f.Local(I32)
		brk, cont := NewLabel(), NewLabel()
		f.Add(
			Set(idx, ld(get(d), offDictIndex)),
			Set(mask, sub(ld(get(d), offDictICap), c32(1))),
			Set(i, and(get(h), get(mask))),
			Block(brk, Loop(cont,
				BrIf(brk, eqz(ld(elem(get(idx), 0, get(i)), 0))),
				Set(i, and(add(get(i), c32(1)), get(mask))),
				Br(cont),
			)),
			st(elem(get(idx), 0, get(i)), 0, get(s)),
		)
	})
	// dict_rehash(d, cap): compacts entries into a table of the given capacity
	r.define("dict_rehash", p2, r0, func(f *Func) {
		d, c := f.Param(0), f.Param(1)
		old, oldUsed, ents, i, n, e, ne2 := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(old, ld(get(d), offDictEntries)),
			Set(oldUsed, ld(get(d), offDictUsed)),
			Set(ents, r.Call("alloc", mul(get(c), c32(12)))),
			st(get(d), offDictEntries, get(ents)),
			st(get(d), offDictCap, get(c)),
			st(get(d), offDictICap, I32Shl(get(c), c32(1))),
			st(get(d), offDictIndex, r.Call("alloc", I32Shl(get(c), c32(3)))),
			forRange(i, c32(0), get(oldUsed),
				Set(e, add(get(old), mul(get(i), c32(12)))),
				If(ld(get(e), 4), Seq{
					Set(ne2, add(get(ents), mul(get(n), c32(12)))),
					MemoryCopy(get(ne2), get(e), c32(12)),
					Set(n, add(get(n), c32(1))),
					r.Call("dict_index_insert", get(d), get(n), ld(get(e), 0)),
				}),
			),
			st(get(d), offDictUsed, get(n)),
		)
	})
	r.define("dict_check_mut", p1, r0, func(f *Func) {
		d := f.Param(0)
		f.Add(
			r.Call("check_mutable", get(d)),
			If(ld(get(d), offDictIters), r.Fail("cannot modify dict during iteration")),
		)
	})
	// dict_set(d, k, v)
	r.define("dict_set", p3, r0, func(f *Func) {
		d, k, v := f.Param(0), f.Param(1), f.Param(2)
		h, e, n, c := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			r.Call("dict_check_mut", get(d)),
			Set(h, r.Call("hash", get(k))),
			Set(e, r.Call("dict_lookup", get(d), get(k), get(h))),
			If(get(e), Seq{st(get(e), 8, get(v)), Return(nil)}),
			If(ge(ld(get(d), offDictUsed), ld(get(d), offDictCap)), Seq{
				// capacity: the next power of two >= 2*(len+1), at least 8
				Set(c, I32Shl(c32(1), sub(c32(32), I32Clz(I32Shl(add(ld(get(d), offDictLen), c32(1)), c32(1)))))),
				If(lt(get(c), c32(8)), Set(c, c32(8))),
				r.Call("dict_rehash", get(d), get(c)),
			}),
			Set(n, ld(get(d), offDictUsed)),
			Set(e, add(ld(get(d), offDictEntries), mul(get(n), c32(12)))),
			st(get(e), 0, get(h)),
			st(get(e), 4, get(k)),
			st(get(e), 8, get(v)),
			st(get(d), offDictUsed, add(get(n), c32(1))),
			st(get(d), offDictLen, add(ld(get(d), offDictLen), c32(1))),
			r.Call("dict_index_insert", get(d), add(get(n), c32(1)), get(h)),
		)
	})
	// dict_lit_set(d, k, v): insertion for dict literals, rejecting duplicates
	r.define("dict_lit_set", p3, r0, func(f *Func) {
		d, k, v := f.Param(0), f.Param(1), f.Param(2)
		f.Add(
			If(r.Call("dict_get", get(d), get(k)), r.Fail("duplicate key: ", r.Call("repr", get(k)))),
			r.Call("dict_set", get(d), get(k), get(v)),
		)
	})
	// dict_get(d, k) -> value or 0
	r.define("dict_get", p2, r1, func(f *Func) {
		d, k := f.Param(0), f.Param(1)
		e := f.Local(I32)
		f.Add(
			Set(e, r.Call("dict_lookup", get(d), get(k), r.Call("hash", get(k)))),
			IfV(I32, get(e), ld(get(e), 8), c32(0)),
		)
	})
	// dict_del(d, k) -> removed value or 0
	r.define("dict_del", p2, r1, func(f *Func) {
		d, k := f.Param(0), f.Param(1)
		e, v := f.Local(I32), f.Local(I32)
		f.Add(
			r.Call("dict_check_mut", get(d)),
			Set(e, r.Call("dict_lookup", get(d), get(k), r.Call("hash", get(k)))),
			If(eqz(get(e)), Return(c32(0))),
			Set(v, ld(get(e), 8)),
			st(get(e), 4, c32(0)),
			st(get(e), 8, c32(0)),
			st(get(d), offDictLen, sub(ld(get(d), offDictLen), c32(1))),
			get(v),
		)
	})
	r.define("dict_clear", p1, r0, func(f *Func) {
		d := f.Param(0)
		f.Add(
			r.Call("dict_check_mut", get(d)),
			st(get(d), offDictLen, c32(0)),
			st(get(d), offDictUsed, c32(0)),
			st(get(d), offDictCap, c32(0)),
			st(get(d), offDictICap, c32(0)),
		)
	})
	// dict_view(d, mode) -> list of keys (0), values (1) or (k, v) tuples (2)
	r.define("dict_view", p2, r1, func(f *Func) {
		d, mode := f.Param(0), f.Param(1)
		l, i, e := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(l, r.Call("list_new", ld(get(d), offDictLen))),
			forRange(i, c32(0), ld(get(d), offDictUsed),
				Set(e, add(ld(get(d), offDictEntries), mul(get(i), c32(12)))),
				If(ld(get(e), 4),
					r.Call("list_append_raw", get(l),
						IfV(I32, eqz(get(mode)), ld(get(e), 4),
							IfV(I32, eq(get(mode), c32(1)), ld(get(e), 8),
								r.Call("tuple2", ld(get(e), 4), ld(get(e), 8)))))),
			),
			get(l),
		)
	})
	// dict_update(d, other dict)
	r.define("dict_merge", p2, r0, func(f *Func) {
		d, o := f.Param(0), f.Param(1)
		i, e := f.Local(I32), f.Local(I32)
		f.Add(
			forRange(i, c32(0), ld(get(o), offDictUsed),
				Set(e, add(ld(get(o), offDictEntries), mul(get(i), c32(12)))),
				If(ld(get(e), 4), r.Call("dict_set", get(d), ld(get(e), 4), ld(get(e), 8))),
			),
		)
	})
	r.define("dict_union", p2, r1, func(f *Func) {
		d := f.Local(I32)
		f.Add(
			Set(d, r.Call("dict_new")),
			r.Call("dict_merge", get(d), get(f.Param(0))),
			r.Call("dict_merge", get(d), get(f.Param(1))),
			get(d),
		)
	})

	// ---- ranges ----
	r.define("range_new", []ValType{I64, I64, I64}, r1, func(f *Func) {
		o := f.Local(I32)
		f.Add(
			If(I64Eqz(Get(f.Param(2))), r.Fail("range: step argument must not be zero")),
			Set(o, r.Call("alloc", c32(sizeRange))),
			st(get(o), 0, c32(TRange|1<<8)),
			I64Store(get(o), offRangeStart, Get(f.Param(0))),
			I64Store(get(o), offRangeStop, Get(f.Param(1))),
			I64Store(get(o), offRangeStep, Get(f.Param(2))),
			get(o),
		)
	})
	r.define("range_len", p1, ri, func(f *Func) {
		o := f.Param(0)
		a, b, s := f.Local(I64), f.Local(I64), f.Local(I64)
		f.Add(
			Set(a, I64Load(get(o), offRangeStart)),
			Set(b, I64Load(get(o), offRangeStop)),
			Set(s, I64Load(get(o), offRangeStep)),
			If(I64GtS(get(s), I64c(0)), Return(IfV(I64, I64LtS(get(a), get(b)),
				I64DivS(I64Add(I64Sub(get(b), get(a)), I64Sub(get(s), I64c(1))), get(s)), I64c(0)))),
			IfV(I64, I64GtS(get(a), get(b)),
				I64DivS(I64Sub(I64Sub(get(a), get(b)), I64Add(get(s), I64c(1))), I64Sub(I64c(0), get(s))), I64c(0)),
		)
	})

	// ---- generic sequence operations ----
	r.define("len", p1, r1, func(f *Func) {
		v := f.Param(0)
		t := f.Local(I32)
		f.Add(
			Set(t, tagOf(get(v))),
			If(or(or(eq(get(t), c32(TString)), eq(get(t), c32(TTuple))), or(eq(get(t), c32(TList)), eq(get(t), c32(TDict)))),
				Return(ld(get(v), 4))),
			If(eq(get(t), c32(TRange)), Return(I32WrapI64(r.Call("range_len", get(v))))),
			r.Fail("len: value of type ", r.TypeName(get(v)), " has no len"),
		)
	})
	// iter_begin(v) -> sequence to iterate (list, tuple or range)
	r.define("iter_begin", p1, r1, func(f *Func) {
		v := f.Param(0)
		t := f.Local(I32)
		f.Add(
			Set(t, tagOf(get(v))),
			If(eq(get(t), c32(TList)), Seq{
				st(get(v), offListIters, add(ld(get(v), offListIters), c32(1))),
				Return(get(v)),
			}),
			If(or(eq(get(t), c32(TTuple)), eq(get(t), c32(TRange))), Return(get(v))),
			If(eq(get(t), c32(TDict)), Seq{
				st(get(v), offDictIters, add(ld(get(v), offDictIters), c32(1))),
				Return(r.Call("dict_view", get(v), c32(0))),
			}),
			r.Fail(r.TypeName(get(v)), " value is not iterable"),
		)
	})
	r.define("iter_end", p1, r0, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(isTag(get(v), TList), st(get(v), offListIters, sub(ld(get(v), offListIters), c32(1)))),
			If(isTag(get(v), TDict), st(get(v), offDictIters, sub(ld(get(v), offDictIters), c32(1)))),
		)
	})
	r.define("seq_len", p1, r1, func(f *Func) {
		s := f.Param(0)
		f.Add(IfV(I32, isTag(get(s), TRange), I32WrapI64(r.Call("range_len", get(s))), ld(get(s), 4)))
	})
	r.define("seq_at", p2, r1, func(f *Func) {
		s, i := f.Param(0), f.Param(1)
		f.Add(
			If(isTag(get(s), TRange), Return(r.Call("int",
				I64Add(I64Load(get(s), offRangeStart), I64Mul(I64ExtendI32S(get(i)), I64Load(get(s), offRangeStep)))))),
			ld(elem(r.Call("seq_base", get(s)), 0, get(i)), 0),
		)
	})
	// unpack(v, n) -> list or tuple of exactly n elements
	r.define("unpack", p2, r1, func(f *Func) {
		v, n := f.Param(0), f.Param(1)
		s := f.Local(I32)
		f.Add(
			If(isTag(get(v), TString), r.Fail("got string in sequence assignment, want iterable")),
			Set(s, r.Call("iter_seq", get(v))),
			If(gt(ld(get(s), 4), get(n)),
				r.Fail("too many values to unpack (got ", r.Call("i64_to_str", I64ExtendI32S(ld(get(s), 4))),
					", want ", r.Call("i64_to_str", I64ExtendI32S(get(n))), ")")),
			If(lt(ld(get(s), 4), get(n)),
				r.Fail("too few values to unpack (got ", r.Call("i64_to_str", I64ExtendI32S(ld(get(s), 4))),
					", want ", r.Call("i64_to_str", I64ExtendI32S(get(n))), ")")),
			get(s),
		)
	})
	// norm_index(i, n) -> i32 in [0, n)
	r.define("norm_index", []ValType{I64, I32}, r1, func(f *Func) {
		i, n := f.Param(0), f.Param(1)
		j := f.Local(I64)
		f.Add(
			Set(j, Get(i)),
			If(I64LtS(get(j), I64c(0)), Set(j, I64Add(get(j), I64ExtendI32S(get(n))))),
			If(or(I64LtS(get(j), I64c(0)), I64GeS(get(j), I64ExtendI32S(get(n)))),
				r.Fail("index ", r.Call("i64_to_str", Get(i)), " out of range [0:",
					r.Call("i64_to_str", I64ExtendI32S(get(n))), "]")),
			I32WrapI64(get(j)),
		)
	})
	// index(x, y) -> x[y]
	r.define("index", p2, r1, func(f *Func) {
		x, y := f.Param(0), f.Param(1)
		t, i, v := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(t, tagOf(get(x))),
			If(eq(get(t), c32(TDict)), Seq{
				Set(v, r.Call("dict_get", get(x), get(y))),
				If(eqz(get(v)), r.Fail("key ", r.Call("repr", get(y)), " not in dict")),
				Return(get(v)),
			}),
			If(or(or(eq(get(t), c32(TList)), eq(get(t), c32(TTuple))), or(eq(get(t), c32(TString)), eq(get(t), c32(TRange)))), Seq{
				If(not(isTag(get(y), TInt)), r.Fail(r.TypeName(get(x)), " index: got ", r.TypeName(get(y)), ", want int")),
				Set(i, r.Call("norm_index", i64v(get(y)), r.Call("len", get(x)))),
				If(eq(get(t), c32(TString)), Return(r.Call("str_from", add(sbytes(get(x)), get(i)), c32(1)))),
				Return(r.Call("seq_at", get(x), get(i))),
			}),
			r.Fail("unhandled index operation ", r.TypeName(get(x)), "[", r.TypeName(get(y)), "]"),
		)
	})
	// setindex(x, y, v): x[y] = v
	r.define("setindex", p3, r0, func(f *Func) {
		x, y, v := f.Param(0), f.Param(1), f.Param(2)
		i := f.Local(I32)
		f.Add(
			If(isTag(get(x), TDict), Seq{r.Call("dict_set", get(x), get(y), get(v)), Return(nil)}),
			If(isTag(get(x), TList), Seq{
				r.Call("list_check_mut", get(x)),
				Set(i, r.Call("norm_index", r.Call("as_int", get(y), r.S("list index")), ld(get(x), offListLen))),
				st(elem(ld(get(x), offListData), 0, get(i)), 0, get(v)),
				Return(nil),
			}),
			r.Fail(r.TypeName(get(x)), " value does not support item assignment"),
		)
	})
	// slice(x, lo, hi, step) with 0 for absent operands
	r.define("slice", p4, r1, func(f *Func) {
		x, lov, hiv, stv := f.Param(0), f.Param(1), f.Param(2), f.Param(3)
		n, t, i, res, base, cnt := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		lo, hi, step := f.Local(I64), f.Local(I64), f.Local(I64)
		absent := func(v Local) Expr { return or(eqz(get(v)), isTag(get(v), TNone)) }
		brk, cont := NewLabel(), NewLabel()
		cond := IfV(I32, I64GtS(get(step), I64c(0)), I64LtS(I64ExtendI32S(get(i)), get(hi)), I64GtS(I64ExtendI32S(get(i)), get(hi)))
		f.Add(
			Set(t, tagOf(get(x))),
			If(not(or(eq(get(t), c32(TString)), or(eq(get(t), c32(TList)), eq(get(t), c32(TTuple))))),
				r.Fail("invalid slice operand ", r.TypeName(get(x)))),
			Set(n, ld(get(x), 4)),
			Set(step, IfV(I64, absent(stv), I64c(1), r.Call("as_int", get(stv), r.S("slice step")))),
			If(I64Eqz(get(step)), r.Fail("zero is not a valid slice step")),
			IfElse(I64GtS(get(step), I64c(0)), []Expr{
				Set(lo, IfV(I64, absent(lov), I64c(0), r.Call("clamp", r.Call("as_int", get(lov), r.S("slice index")), get(n), c32(0)))),
				Set(hi, IfV(I64, absent(hiv), I64ExtendI32S(get(n)), r.Call("clamp", r.Call("as_int", get(hiv), r.S("slice index")), get(n), c32(0)))),
			}, []Expr{
				Set(lo, IfV(I64, absent(lov), I64ExtendI32S(sub(get(n), c32(1))), r.Call("clamp", r.Call("as_int", get(lov), r.S("slice index")), get(n), c32(1)))),
				Set(hi, IfV(I64, absent(hiv), I64c(-1), r.Call("clamp", r.Call("as_int", get(hiv), r.S("slice index")), get(n), c32(1)))),
			}),
			If(and(eq(get(t), c32(TString)), I64Eq(get(step), I64c(1))),
				Return(r.Call("str_sub", get(x), I32WrapI64(get(lo)), I32WrapI64(get(hi))))),
			// count elements
			Set(i, I32WrapI64(get(lo))),
			Block(brk, Loop(cont,
				BrIf(brk, not(cond)),
				Set(cnt, add(get(cnt), c32(1))),
				Set(i, add(get(i), I32WrapI64(get(step)))),
				Br(cont),
			)),
			IfElse(eq(get(t), c32(TString)), []Expr{
				Set(res, r.Call("str_new", get(cnt))),
				Set(base, sbytes(get(res))),
			}, []Expr{
				Set(res, r.Call("tuple_new", get(cnt))),
				Set(base, add(get(res), c32(offTupElems))),
			}),
			Set(i, I32WrapI64(get(lo))),
			Set(cnt, c32(0)),
			Block(brk, Loop(cont,
				BrIf(brk, not(cond)),
				IfElse(eq(get(t), c32(TString)), []Expr{
					I32Store8(add(get(base), get(cnt)), 0, I32Load8U(add(sbytes(get(x)), get(i)), 0)),
				}, []Expr{
					st(elem(get(base), 0, get(cnt)), 0, ld(elem(r.Call("seq_base", get(x)), 0, get(i)), 0)),
				}),
				Set(cnt, add(get(cnt), c32(1))),
				Set(i, add(get(i), I32WrapI64(get(step)))),
				Br(cont),
			)),
			If(eq(get(t), c32(TList)), Return(r.Call("list_of", get(res)))),
			get(res),
		)
	})
	// clamp(i, n, neg) -> slice bound. For positive steps the result is in
	// [0, n]; for negative steps in [-1, n-1].
	r.define("clamp", []ValType{I64, I32, I32}, ri, func(f *Func) {
		i, n, neg := f.Param(0), f.Param(1), f.Param(2)
		nn := f.Local(I64)
		f.Add(
			Set(nn, I64ExtendI32S(get(n))),
			If(I64LtS(Get(i), I64c(0)), Set(i, I64Add(Get(i), get(nn)))),
			IfElse(get(neg), []Expr{
				If(I64LtS(Get(i), I64c(0)), Return(I64c(-1))),
				If(I64GeS(Get(i), get(nn)), Return(I64Sub(get(nn), I64c(1)))),
			}, []Expr{
				If(I64LtS(Get(i), I64c(0)), Return(I64c(0))),
				If(I64GtS(Get(i), get(nn)), Return(get(nn))),
			}),
			Get(i),
		)
	})
	// contains(x, coll) -> i32 for "x in coll"
	r.define("contains", p2, r1, func(f *Func) {
		x, c := f.Param(0), f.Param(1)
		t, i, n, base := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		v, a, b, s := f.Local(I64), f.Local(I64), f.Local(I64), f.Local(I64)
		f.Add(
			Set(t, tagOf(get(c))),
			If(eq(get(t), c32(TDict)), Return(ne(r.Call("dict_get", get(c), get(x)), c32(0)))),
			If(or(eq(get(t), c32(TList)), eq(get(t), c32(TTuple))), Seq{
				Set(n, ld(get(c), 4)),
				Set(base, r.Call("seq_base", get(c))),
				forRange(i, c32(0), get(n),
					If(r.Call("eq", ld(elem(get(base), 0, get(i)), 0), get(x)), Return(c32(1)))),
				Return(c32(0)),
			}),
			If(eq(get(t), c32(TString)), Seq{
				If(not(isTag(get(x), TString)),
					r.Fail("'in <string>' requires string as left operand, not ", r.TypeName(get(x)))),
				Return(ge(r.Call("str_find", get(c), get(x), c32(0), slen(get(c))), c32(0))),
			}),
			If(eq(get(t), c32(TRange)), Seq{
				If(not(isTag(get(x), TInt)), Return(c32(0))),
				Set(v, i64v(get(x))),
				Set(a, I64Load(get(c), offRangeStart)),
				Set(b, I64Load(get(c), offRangeStop)),
				Set(s, I64Load(get(c), offRangeStep)),
				IfElse(I64GtS(get(s), I64c(0)), []Expr{
					If(or(I64LtS(get(v), get(a)), I64GeS(get(v), get(b))), Return(c32(0))),
				}, []Expr{
					If(or(I64GtS(get(v), get(a)), I64LeS(get(v), get(b))), Return(c32(0))),
				}),
				Return(I64Eqz(I64RemS(I64Sub(get(v), get(a)), get(s)))),
			}),
			r.Fail("unknown binary op: ", r.TypeName(get(x)), " in ", r.TypeName(get(c))),
		)
	})
}
