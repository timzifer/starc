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
	// call(fn, args, kwargs). call_cb is call for the prelude calling a
	// callable of the program, such as a key function: its frame is
	// shown in backtraces even if it is a built-in (see backtrace.go).
	for _, c := range []struct {
		name string
		cb   bool
	}{{"call", false}, {"call_cb", true}} {
		name, cb := c.name, c.cb
		r.define(name, p3, r1, func(f *Func) {
			fn, args, kw := f.Param(0), f.Param(1), f.Param(2)
			t := f.Local(I32)
			f.Add(
				Set(t, tagOf(get(fn))),
				If(or(eq(get(t), c32(TFunction)), eq(get(t), c32(TBuiltin))),
					r.callFrame(f, CallIndirect(CallType, ld(get(fn), offFnTable), get(args), get(kw), get(fn)), cb)),
				If(eq(get(t), c32(TBound)),
					Return(r.Call(name, ld(get(fn), offBoundFn),
						r.Call("tuple_prepend", ld(get(fn), offBoundRecv), get(args)), get(kw)))),
				r.Fail("invalid call of non-function (", r.TypeName(get(fn)), ")"),
			)
		})
	}
	r.define("tuple_prepend", p2, r1, func(f *Func) {
		x, t := f.Param(0), f.Param(1)
		o := f.Local(I32)
		f.Add(
			Set(o, r.Call("tuple_new", add(ld(get(t), offTupLen), c32(1)))),
			st(get(o), offTupElems, get(x)),
			r.memcopy(add(get(o), c32(offTupElems+4)), add(get(t), c32(offTupElems)), I32Shl(ld(get(t), offTupLen), c32(2))),
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
			r.dupParam(name()),
			Set(nslots, add(add(get(named), and(get(flags), c32(SpecVarargs))), I32ShrU(and(get(flags), c32(SpecKwargs)), c32(1)))),
			Set(slots, r.Call("alloc", I32Shl(add(get(nslots), c32(1)), c32(2)))),
			IfElse(gt(get(nargs), get(npos)), []Expr{
				If(eqz(and(get(flags), c32(SpecVarargs))),
					r.Fail("function ", name(), " accepts at most ", r.Call("i64_to_str", I64ExtendI32S(get(npos))),
						" positional arguments (", r.Call("i64_to_str", I64ExtendI32S(get(nargs))), " given)")),
				r.memcopy(get(slots), add(get(args), c32(offTupElems)), I32Shl(get(npos), c32(2))),
				Set(e, r.Call("tuple_new", sub(get(nargs), get(npos)))),
				r.memcopy(add(get(e), c32(offTupElems)), elem(get(args), offTupElems, get(npos)), I32Shl(sub(get(nargs), get(npos)), c32(2))),
				st(slot(get(named)), 0, get(e)),
			}, []Expr{
				r.memcopy(get(slots), add(get(args), c32(offTupElems)), I32Shl(get(nargs), c32(2))),
				If(and(get(flags), c32(SpecVarargs)), st(slot(get(named)), 0, r.Call("tuple_new", c32(0)))),
			}),
			If(and(get(flags), c32(SpecKwargs)), Seq{
				Set(kwd, r.Call("dict_new")),
				st(slot(sub(get(nslots), c32(1))), 0, get(kwd)),
			}),
			If(get(kw), r.forRange(i, c32(0), ld(get(kw), offDictUsed),
				Set(e, add(ld(get(kw), offDictEntries), mul(get(i), c32(12)))),
				Set(k, ld(get(e), 4)),
				If(get(k), Seq{
					Set(found, c32(-1)),
					r.forRangeL(j, c32(0), get(named), brk,
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
			r.forRange(i, c32(0), get(named),
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

	// method_lookup(recv, id) -> callable or 0. For a module, it is the
	// member with that name.
	r.define("method_lookup", p2, r1, func(f *Func) {
		f.Add(Raw(func() Expr {
			lookup := ld(add(addr(r.methodTable), I32Shl(add(mul(tagOf(get(f.Param(0))), c32(len(r.methodNames))), get(f.Param(1))), c32(2))), 0)
			lookup = IfV(I32, isTag(get(f.Param(0)), TStruct),
				r.Call("struct_get", get(f.Param(0)), r.Call("method_name", get(f.Param(1)))), lookup)
			if !r.modules {
				return lookup
			}
			return IfV(I32, isTag(get(f.Param(0)), TModule), r.Call("module_member", get(f.Param(0)), get(f.Param(1))), lookup)
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
	// method_resolve(recv, id) -> the method's callable; it is called
	// with the receiver as the first argument.
	r.define("method_resolve", p2, r1, func(f *Func) {
		recv, id := f.Param(0), f.Param(1)
		m := f.Local(I32)
		f.Add(
			Set(m, r.Call("method_lookup", get(recv), get(id))),
			If(eqz(get(m)), r.Call("no_field", get(recv), r.Call("method_name", get(id)))),
			get(m),
		)
	})
	// attr(x, id) -> bound method, or the member of a module
	r.define("attr", p2, r1, func(f *Func) {
		x, id := f.Param(0), f.Param(1)
		m, b := f.Local(I32), f.Local(I32)
		f.Add(
			Set(m, r.Call("method_lookup", get(x), get(id))),
			If(eqz(get(m)), r.Call("no_field", get(x), r.Call("method_name", get(id)))),
			If(r.HasFields(get(x)), Return(get(m))),
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
			r.forRange(i, c32(0), ld(get(x), offDictUsed),
				Set(e, add(ld(get(x), offDictEntries), mul(get(i), c32(12)))),
				If(ld(get(e), 4), Seq{
					If(not(isTag(ld(get(e), 4), TString)), r.Fail("keywords must be strings, not ", r.TypeName(ld(get(e), 4)))),
					r.mergeKeyword(get(d), ld(get(e), 4), ld(get(e), 8)),
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
	// inplace_or(a, b): a |= b (dicts are updated in place)
	r.define("inplace_or", p2, r1, func(f *Func) {
		a, b := f.Param(0), f.Param(1)
		f.Add(
			If(and(isTag(get(a), TDict), isTag(get(b), TDict)), Seq{
				r.Call("dict_check_mut", get(a)),
				r.Call("dict_merge", get(a), get(b)),
				Return(get(a)),
			}),
			r.Call("bitor", get(a), get(b)),
		)
	})
	// truth_bool(v) -> True/False object
	r.define("to_bool", p1, r1, func(f *Func) {
		f.Add(r.bool(r.Call("truth", get(f.Param(0)))))
	})
}

// dupParam yields code for bind that fails if a keyword was passed twice
// (see mergeKeyword).
func (r *RT) dupParam(name Expr) Expr {
	if !r.Cfg.Backtrace {
		return Nop()
	}
	return If(GGet(r.DupKw), r.Fail("function ", name, " got multiple values for parameter ", r.Call("take_dup_kw")))
}

// mergeKeyword yields code that adds the keyword argument k=v of a
// **kwargs operand to the keyword arguments d. If k is already there,
// the callee rejects it, as in go.starlark.net: the first value is kept,
// k is recorded in DupKw, and the callee fails within its own frame (see
// bind and callFrame). Without backtraces, where frames do not matter,
// it fails at once.
func (r *RT) mergeKeyword(d, k, v Expr) Expr {
	dup := r.Call("dict_get", d, k)
	if !r.Cfg.Backtrace {
		return Seq{If(dup, r.Fail("got multiple values for keyword argument ", k)), r.Call("dict_set", d, k, v)}
	}
	return IfElse(dup, []Expr{If(eqz(GGet(r.DupKw)), GSet(r.DupKw, k))}, []Expr{r.Call("dict_set", d, k, v)})
}
