package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

func (r *RT) registerCall() {
	// func_new(table index, spec, defaults tuple, freevars tuple)
	r.define("func_new", p4, r1, func(f *Func) {
		o := f.Local(I32)
		f.Add(
			Set(o, r.Call("alloc", c32(sizeFn))),
			st(get(o), 0, c32(TFunction)),
			st(get(o), offFnTable, get(f.Param(0))),
			st(get(o), offFnSpec, get(f.Param(1))),
			st(get(o), offFnDefaults, get(f.Param(2))),
			st(get(o), offFnFree, get(f.Param(3))),
			get(o),
		)
	})
	// call(fn, args, kwargs)
	r.define("call", p3, r1, func(f *Func) {
		fn, args, kw := f.Param(0), f.Param(1), f.Param(2)
		t := f.Local(I32)
		f.Add(
			Set(t, tagOf(get(fn))),
			If(or(eq(get(t), c32(TFunction)), eq(get(t), c32(TBuiltin))),
				Return(CallIndirect(CallType, ld(get(fn), offFnTable), get(args), get(kw), get(fn)))),
			If(eq(get(t), c32(TBound)),
				Return(r.Call("call", ld(get(fn), offBoundFn),
					r.Call("tuple_prepend", ld(get(fn), offBoundRecv), get(args)), get(kw)))),
			r.Fail("invalid call of non-function (", r.TypeName(get(fn)), ")"),
		)
	})
	r.define("tuple_prepend", p2, r1, func(f *Func) {
		x, t := f.Param(0), f.Param(1)
		o := f.Local(I32)
		f.Add(
			Set(o, r.Call("tuple_new", add(ld(get(t), offTupLen), c32(1)))),
			st(get(o), offTupElems, get(x)),
			MemoryCopy(add(get(o), c32(offTupElems+4)), add(get(t), c32(offTupElems)), I32Shl(ld(get(t), offTupLen), c32(2))),
			get(o),
		)
	})
	r.define("callable_name", p1, r1, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(isTag(get(v), TFunction), Return(ld(ld(get(v), offFnSpec), offSpecName))),
			If(isTag(get(v), TBuiltin), Return(ld(get(v), offBiName))),
			r.TypeName(get(v)),
		)
	})
	// enter(spec): recursion check
	r.define("enter", p1, r0, func(f *Func) {
		s := f.Param(0)
		f.Add(
			If(ld(get(s), offSpecActive),
				r.Fail("function ", ld(get(s), offSpecName), " called recursively")),
			st(get(s), offSpecActive, c32(1)),
		)
	})

	// bind(fn, args, kwargs) -> address of the parameter slots
	r.define("bind", p3, r1, func(f *Func) {
		fn, args, kw := f.Param(0), f.Param(1), f.Param(2)
		spec, named, npos, flags, nslots, slots, nargs, i, j, e, k, kwd, defs, found, names :=
			f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32),
			f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		name := func() Expr { return ld(get(spec), offSpecName) }
		brk := NewLabel()
		slot := func(i Expr) Expr { return elem(get(slots), 0, i) }
		f.Add(
			Set(spec, ld(get(fn), offFnSpec)),
			Set(named, ld(get(spec), offSpecNamed)),
			Set(flags, ld(get(spec), offSpecFlags)),
			Set(npos, sub(get(named), ld(get(spec), offSpecKwonly))),
			Set(nargs, ld(get(args), offTupLen)),
			Set(names, ld(get(spec), offSpecNames)),
			// fast path: exact positional call of a function without
			// keyword-only parameters
			If(and(and(eqz(get(kw)), and(eq(get(nargs), get(named)), eq(get(npos), get(named)))),
				eqz(and(get(flags), c32(SpecVarargs|SpecKwargs)))),
				Return(add(get(args), c32(offTupElems)))),
			Set(nslots, add(add(get(named), and(get(flags), c32(SpecVarargs))), I32ShrU(and(get(flags), c32(SpecKwargs)), c32(1)))),
			Set(slots, r.Call("alloc", I32Shl(add(get(nslots), c32(1)), c32(2)))),
			IfElse(gt(get(nargs), get(npos)), []Expr{
				If(eqz(and(get(flags), c32(SpecVarargs))),
					r.Fail("function ", name(), " accepts at most ", r.Call("i64_to_str", I64ExtendI32S(get(npos))),
						" positional arguments (", r.Call("i64_to_str", I64ExtendI32S(get(nargs))), " given)")),
				MemoryCopy(get(slots), add(get(args), c32(offTupElems)), I32Shl(get(npos), c32(2))),
				Set(e, r.Call("tuple_new", sub(get(nargs), get(npos)))),
				MemoryCopy(add(get(e), c32(offTupElems)), elem(get(args), offTupElems, get(npos)), I32Shl(sub(get(nargs), get(npos)), c32(2))),
				st(slot(get(named)), 0, get(e)),
			}, []Expr{
				MemoryCopy(get(slots), add(get(args), c32(offTupElems)), I32Shl(get(nargs), c32(2))),
				If(and(get(flags), c32(SpecVarargs)), st(slot(get(named)), 0, r.Call("tuple_new", c32(0)))),
			}),
			If(and(get(flags), c32(SpecKwargs)), Seq{
				Set(kwd, r.Call("dict_new")),
				st(slot(sub(get(nslots), c32(1))), 0, get(kwd)),
			}),
			If(get(kw), forRange(i, c32(0), ld(get(kw), offDictUsed),
				Set(e, add(ld(get(kw), offDictEntries), mul(get(i), c32(12)))),
				Set(k, ld(get(e), 4)),
				If(get(k), Seq{
					Set(found, c32(-1)),
					forRangeL(j, c32(0), get(named), brk,
						If(r.Call("str_eq", ld(elem(get(names), 0, get(j)), 0), get(k)), Seq{Set(found, get(j)), Br(brk)})),
					IfElse(ge(get(found), c32(0)), []Expr{
						If(ld(slot(get(found)), 0),
							r.Fail("function ", name(), " got multiple values for parameter ", get(k))),
						st(slot(get(found)), 0, ld(get(e), 8)),
					}, []Expr{
						If(eqz(get(kwd)),
							r.Fail("function ", name(), " got an unexpected keyword argument ", get(k))),
						r.Call("dict_set", get(kwd), get(k), ld(get(e), 8)),
					}),
				}),
			)),
			Set(defs, ld(get(fn), offFnDefaults)),
			forRange(i, c32(0), get(named),
				If(eqz(ld(slot(get(i)), 0)), Seq{
					Set(e, IfV(I32, get(defs), ld(elem(get(defs), offTupElems, get(i)), 0), c32(0))),
					If(eqz(get(e)),
						r.Fail("function ", name(), " missing argument for ", ld(elem(get(names), 0, get(i)), 0))),
					st(slot(get(i)), 0, get(e)),
				}),
			),
			get(slots),
		)
	})

	// method_lookup(recv, id) -> callable or 0
	r.define("method_lookup", p2, r1, func(f *Func) {
		f.Add(Raw(func() Expr {
			return ld(add(addr(r.methodTable), I32Shl(add(mul(tagOf(get(f.Param(0))), c32(len(r.methodNames))), get(f.Param(1))), c32(2))), 0)
		}))
	})
	// method_name(id) -> string
	r.define("method_name", p1, r1, func(f *Func) {
		f.Add(Raw(func() Expr {
			return ld(add(addr(r.methodNameTab), I32Shl(get(f.Param(0)), c32(2))), 0)
		}))
	})
	// method_id(name string) -> id or -1
	r.define("method_id", p1, r1, func(f *Func) {
		f.Add(Raw(func() Expr {
			body := c32(-1)
			for id := len(r.methodNames) - 1; id >= 0; id-- {
				body = IfV(I32, r.Call("str_eq", get(f.Param(0)), addr(r.strs[r.methodNames[id]])), c32(id), body)
			}
			return body
		}))
	})
	// call_method(id, args, kwargs): args[0] is the receiver
	r.define("call_method", p3, r1, func(f *Func) {
		id, args, kw := f.Param(0), f.Param(1), f.Param(2)
		m, recv := f.Local(I32), f.Local(I32)
		f.Add(
			Set(recv, ld(get(args), offTupElems)),
			Set(m, r.Call("method_lookup", get(recv), get(id))),
			If(eqz(get(m)), r.Fail(r.TypeName(get(recv)), " has no .", r.Call("method_name", get(id)), " field or method")),
			r.Call("call", get(m), get(args), get(kw)),
		)
	})
	// attr(x, id) -> bound method
	r.define("attr", p2, r1, func(f *Func) {
		x, id := f.Param(0), f.Param(1)
		m, b := f.Local(I32), f.Local(I32)
		f.Add(
			Set(m, r.Call("method_lookup", get(x), get(id))),
			If(eqz(get(m)), r.Fail(r.TypeName(get(x)), " has no .", r.Call("method_name", get(id)), " field or method")),
			Set(b, r.Call("alloc", c32(12))),
			st(get(b), 0, c32(TBound)),
			st(get(b), offBoundFn, get(m)),
			st(get(b), offBoundRecv, get(x)),
			get(b),
		)
	})
}

func (r *RT) registerCallExtras() {
	// kwargs_merge(d, x): adds the entries of the **kwargs operand x to d
	r.define("kwargs_merge", p2, r0, func(f *Func) {
		d, x := f.Param(0), f.Param(1)
		i, e := f.Local(I32), f.Local(I32)
		f.Add(
			If(not(isTag(get(x), TDict)), r.Fail("argument after ** must be a mapping, not ", r.TypeName(get(x)))),
			forRange(i, c32(0), ld(get(x), offDictUsed),
				Set(e, add(ld(get(x), offDictEntries), mul(get(i), c32(12)))),
				If(ld(get(e), 4), Seq{
					If(not(isTag(ld(get(e), 4), TString)), r.Fail("keywords must be strings, not ", r.TypeName(ld(get(e), 4)))),
					If(r.Call("dict_get", get(d), ld(get(e), 4)),
						r.Fail("got multiple values for keyword argument ", ld(get(e), 4))),
					r.Call("dict_set", get(d), ld(get(e), 4), ld(get(e), 8)),
				}),
			),
		)
	})
	// inplace_add(a, b): a += b (lists are extended in place)
	r.define("inplace_add", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		f.Add(
			If(isTag(get(a), TList), Seq{r.Call("list_extend", get(a), get(b)), Return(get(a))}),
			r.Call("add", get(a), get(b)),
		)
	})
	// truth_bool(v) -> True/False object
	r.define("to_bool", p1, r1, func(f *Func) {
		f.Add(r.bool(r.Call("truth", get(f.Param(0)))))
	})
}
