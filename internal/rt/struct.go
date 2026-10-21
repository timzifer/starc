package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

// Structs, as in go.starlark.net's starlarkstruct: immutable records of
// named fields, kept sorted by name. A struct object points to an array
// of (name string, value) pairs.
const (
	offStructLen    = 4
	offStructFields = 8
	sizeStruct      = 16
)

// HasFields yields whether v has fields rather than methods: a module or
// a struct, whose callable attributes are called without a receiver.
func (r *RT) HasFields(v Expr) Expr { return r.Call("has_fields", v) }

func (r *RT) registerStruct() {
	r.define("has_fields", p1, r1, func(f *Func) {
		t := f.Local(I32)
		f.Add(Set(t, tagOf(get(f.Param(0)))), or(eq(get(t), c32(TModule)), eq(get(t), c32(TStruct))))
	})
	// struct_new(n) -> empty struct with room for n fields
	r.define("struct_new", p1, r1, func(f *Func) {
		s := f.Local(I32)
		f.Add(
			r.checkCount(get(f.Param(0))),
			Set(s, r.Call("alloc", c32(sizeStruct))),
			st(get(s), 0, c32(TStruct)),
			st(get(s), offStructFields, r.Call("alloc", I32Shl(get(f.Param(0)), c32(3)))),
			get(s),
		)
	})
	// struct_put(s, name, v) inserts a field at its sorted position.
	r.define("struct_put", p3, r0, func(f *Func) {
		s, name, v := f.Param(0), f.Param(1), f.Param(2)
		n, i, base, c := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		brk := NewLabel()
		f.Add(
			Set(n, ld(get(s), offStructLen)),
			Set(base, ld(get(s), offStructFields)),
			r.forRangeL(i, c32(0), get(n), brk,
				Set(c, r.Call("str_cmp", ld(add(get(base), I32Shl(get(i), c32(3))), 0), get(name))),
				If(eqz(get(c)), r.Fail("struct: duplicate field ", get(name))),
				BrIf(brk, gt(get(c), c32(0)))),
			r.memcopy(add(get(base), I32Shl(add(get(i), c32(1)), c32(3))), add(get(base), I32Shl(get(i), c32(3))),
				I32Shl(sub(get(n), get(i)), c32(3))),
			st(add(get(base), I32Shl(get(i), c32(3))), 0, get(name)),
			st(add(get(base), I32Shl(get(i), c32(3))), 4, get(v)),
			st(get(s), offStructLen, add(get(n), c32(1))),
		)
	})
	// struct_get(s, name) -> value of the field, or 0
	r.define("struct_get", p2, r1, func(f *Func) {
		s, name := f.Param(0), f.Param(1)
		i, e := f.Local(I32), f.Local(I32)
		f.Add(
			r.forRange(i, c32(0), ld(get(s), offStructLen),
				Set(e, add(ld(get(s), offStructFields), I32Shl(get(i), c32(3)))),
				If(r.Call("str_eq", ld(get(e), 0), get(name)), Return(ld(get(e), 4)))),
			c32(0),
		)
	})
	// struct_names(s) -> list of the field names
	r.define("struct_names", p1, r1, func(f *Func) {
		s := f.Param(0)
		l, i := f.Local(I32), f.Local(I32)
		f.Add(
			Set(l, r.Call("list_new", ld(get(s), offStructLen))),
			r.forRange(i, c32(0), ld(get(s), offStructLen),
				r.Call("list_append_raw", get(l), ld(add(ld(get(s), offStructFields), I32Shl(get(i), c32(3))), 0))),
			get(l),
		)
	})
	// no_field(x, name): error for a missing attribute
	r.define("no_field", p2, r0, func(f *Func) {
		x, name := f.Param(0), f.Param(1)
		f.Add(
			If(isTag(get(x), TStruct), r.Fail("struct has no .", get(name), " attribute")),
			r.Fail(r.TypeName(get(x)), " has no .", get(name), " field or method"),
		)
	})
	// struct(**kwargs)
	r.builtin("struct", func(f *Func, args, kw Local) {
		s, i, e := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			If(nargs(args), r.Fail("struct: unexpected positional arguments")),
			If(eqz(get(kw)), Return(r.Call("struct_new", c32(0)))),
			Set(s, r.Call("struct_new", ld(get(kw), offDictLen))),
			r.forRange(i, c32(0), ld(get(kw), offDictUsed),
				Set(e, add(ld(get(kw), offDictEntries), mul(get(i), c32(12)))),
				If(ld(get(e), 4), r.Call("struct_put", get(s), ld(get(e), 4), ld(get(e), 8)))),
			get(s),
		)
	})
}

// Opaque host values: the guest knows their type name, string form,
// truth and hashability, but cannot look into the payload, which it only
// compares, hashes and passes back to the host.
const (
	offOpqType    = 4
	offOpqRepr    = 8
	offOpqPayload = 12
	offOpqFlags   = 16
	sizeOpaque    = 24

	OpaqueFalsy      = 1 // the value is false in a boolean context
	OpaqueUnhashable = 2 // the value cannot be a dict key or set element
)

func (r *RT) registerOpaque() {
	// opaque_new(type, repr, payload, flags) -> opaque value
	r.define("opaque_new", p4, r1, func(f *Func) {
		o := f.Local(I32)
		f.Add(
			Set(o, r.Call("alloc", c32(sizeOpaque))),
			st(get(o), 0, c32(TOpaque|1<<8)),
			st(get(o), offOpqType, get(f.Param(0))),
			st(get(o), offOpqRepr, get(f.Param(1))),
			st(get(o), offOpqPayload, get(f.Param(2))),
			st(get(o), offOpqFlags, get(f.Param(3))),
			get(o),
		)
	})
}
