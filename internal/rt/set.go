package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

// Sets share the layout and the hash table functions of dicts: the
// elements are the keys, and every value is True (so that dict_get finds
// a present element). Set methods that are easier to express in
// Starlark live in the prelude (_set_*).

// setOp yields code that returns the set operation op (see set_binop)
// if both operands, with tags ta and tb, are sets.
func (r *RT) setOp(ta, tb, a, b Local, op int) Expr {
	return If(and(eq(get(ta), c32(TSet)), eq(get(tb), c32(TSet))), Return(r.Call("set_binop", get(a), get(b), c32(op))))
}

func (r *RT) registerSet() {
	r.define("set_new", p0, r1, func(f *Func) {
		s := f.Local(I32)
		f.Add(
			Set(s, r.Call("alloc", c32(sizeDict))),
			st(get(s), 0, c32(TSet)),
			get(s),
		)
	})
	// set_add(s, x)
	r.define("set_add", p2, r0, func(f *Func) {
		f.Add(r.Call("dict_set", get(f.Param(0)), get(f.Param(1)), r.TrueV()))
	})
	// set_has(s, x) -> i32
	r.define("set_has", p2, r1, func(f *Func) {
		f.Add(ne(r.Call("dict_get", get(f.Param(0)), get(f.Param(1))), c32(0)))
	})
	// set_copy(s) -> new set with the elements of s
	r.define("set_copy", p1, r1, func(f *Func) {
		s, i, e := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(s, r.Call("set_new")),
			r.forRange(i, c32(0), ld(get(f.Param(0)), offDictUsed),
				Set(e, add(ld(get(f.Param(0)), offDictEntries), mul(get(i), c32(12)))),
				If(ld(get(e), 4), r.Call("set_add", get(s), ld(get(e), 4)))),
			get(s),
		)
	})
	// set_subset(a, b) -> i32: every element of a is in b
	r.define("set_subset", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		i, e := f.Local(I32), f.Local(I32)
		f.Add(
			If(gt(ld(get(a), offDictLen), ld(get(b), offDictLen)), Return(c32(0))),
			r.forRange(i, c32(0), ld(get(a), offDictUsed),
				Set(e, add(ld(get(a), offDictEntries), mul(get(i), c32(12)))),
				If(and(ne(ld(get(e), 4), c32(0)), eqz(r.Call("set_has", get(b), ld(get(e), 4)))), Return(c32(0)))),
			c32(1),
		)
	})
	// set_binop(a, b, op) -> a | b (0), a & b (1), a - b (2), a ^ b (3).
	// The elements of b are visited in order, as in go.starlark.net.
	r.define("set_binop", p3, r1, func(f *Func) {
		a, b, op := f.Param(0), f.Param(1), f.Param(2)
		s, i, e, x := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(s, IfV(I32, eq(get(op), c32(1)), r.Call("set_new"), r.Call("set_copy", get(a)))),
			r.forRange(i, c32(0), ld(get(b), offDictUsed),
				Set(e, add(ld(get(b), offDictEntries), mul(get(i), c32(12)))),
				Set(x, ld(get(e), 4)),
				If(get(x), Seq{
					If(eqz(get(op)), r.Call("set_add", get(s), get(x))),
					If(eq(get(op), c32(1)), If(r.Call("set_has", get(a), get(x)), r.Call("set_add", get(s), get(x)))),
					If(eq(get(op), c32(2)), Drop(r.Call("dict_del", get(s), get(x)))),
					If(eq(get(op), c32(3)), If(eqz(r.Call("dict_del", get(s), get(x))), r.Call("set_add", get(s), get(x)))),
				})),
			get(s),
		)
	})
	// set_cmp(a, b, op) -> result of the subset comparison op, encoded
	// for the caller of cmp (see cmp).
	r.define("set_cmp", p3, r1, func(f *Func) {
		a, b, op := f.Param(0), f.Param(1), f.Param(2)
		la, lb := ld(get(a), offDictLen), ld(get(b), offDictLen)
		is := func(s string) Expr { return eq(get(op), addr(r.Str(s))) }
		f.Add(
			// a < b: proper subset; the caller tests cmp < 0.
			If(is("<"), Return(IfV(I32, and(lt(la, lb), r.Call("set_subset", get(a), get(b))), c32(-1), c32(0)))),
			If(is(">"), Return(IfV(I32, and(gt(la, lb), r.Call("set_subset", get(b), get(a))), c32(1), c32(0)))),
			If(is("<="), Return(IfV(I32, r.Call("set_subset", get(a), get(b)), c32(-1), c32(1)))),
			If(is(">="), Return(IfV(I32, r.Call("set_subset", get(b), get(a)), c32(1), c32(-1)))),
			r.Call("cmp_error", get(a), get(b), get(op)),
			Unreachable(),
		)
	})

	r.builtin("set", func(f *Func, args, kw Local) {
		s, seq, n, i := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			r.checkArgs("set", args, kw, 0, 1),
			Set(s, r.Call("set_new")),
			If(eqz(nargs(args)), Return(get(s))),
			Set(seq, r.Call("iter_begin", arg(args, 0))),
			Set(n, r.Call("seq_len", get(seq))),
			r.forRange(i, c32(0), get(n), r.Call("set_add", get(s), r.Call("seq_at", get(seq), get(i)))),
			r.Call("iter_end", arg(args, 0)),
			get(s),
		)
	})
	r.builtin("set.add", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("add", args, kw, 2, 2), r.Call("set_add", arg(args, 0), arg(args, 1)), r.NoneV())
	})
	r.builtin("set.discard", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("discard", args, kw, 2, 2), Drop(r.Call("dict_del", arg(args, 0), arg(args, 1))), r.NoneV())
	})
	r.builtin("set.remove", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("remove", args, kw, 2, 2),
			If(eqz(r.Call("dict_del", arg(args, 0), arg(args, 1))), r.Fail("remove: missing key")),
			r.NoneV())
	})
	r.builtin("set.clear", func(f *Func, args, kw Local) {
		f.Add(r.checkArgs("clear", args, kw, 1, 1), r.Call("dict_clear", arg(args, 0)), r.NoneV())
	})
	r.builtin("set.pop", func(f *Func, args, kw Local) {
		s, i, e := f.Local(I32), f.Local(I32), f.Local(I32)
		brk := NewLabel()
		f.Add(
			r.checkArgs("pop", args, kw, 1, 1),
			Set(s, arg(args, 0)),
			If(eqz(ld(get(s), offDictLen)), r.Fail("pop: empty set")),
			r.forRangeL(i, c32(0), ld(get(s), offDictUsed), brk,
				Set(e, add(ld(get(s), offDictEntries), mul(get(i), c32(12)))),
				BrIf(brk, ne(ld(get(e), 4), c32(0)))),
			Set(e, ld(get(e), 4)),
			Drop(r.Call("dict_del", get(s), get(e))),
			get(e),
		)
	})
}
