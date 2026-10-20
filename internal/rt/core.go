// Package rt generates the runtime support code that compiled Starlark
// programs link against: memory allocation, the value representation,
// operators, strings, collections, calls and the host ABI.
//
// The runtime is not an interpreter; it is a library of WebAssembly
// functions, written here with the expression builder of package wasm,
// that the compiled program calls. Only functions that are actually
// reachable from the program end up in the final binary.
package rt

import (
	"fmt"
	"math"

	. "github.com/timzifer/starc/internal/wasm" //nolint:revive // DSL
)

// Value type tags (byte 0 of every heap object).
const (
	TNone     = 0
	TBool     = 1
	TInt      = 2
	TFloat    = 3
	TString   = 4
	TTuple    = 5
	TList     = 6
	TDict     = 7
	TFunction = 8
	TBuiltin  = 9
	TCell     = 10
	TRange    = 11
	TBound    = 12
	NumTags   = 13
)

// Object layouts (byte offsets).
const (
	offFlags = 1 // u8: bit 0 = frozen

	offBool  = 4
	offInt   = 8
	offFloat = 8

	offStrLen   = 4
	offStrHash  = 8
	offStrBytes = 12

	offTupLen   = 4
	offTupElems = 8

	offListLen   = 4
	offListCap   = 8
	offListData  = 12
	offListIters = 16
	sizeList     = 20

	offDictLen     = 4
	offDictUsed    = 8
	offDictCap     = 12
	offDictEntries = 16
	offDictIndex   = 20
	offDictICap    = 24
	offDictIters   = 28
	sizeDict       = 32

	offFnTable    = 4
	offFnSpec     = 8
	offFnDefaults = 12
	offFnFree     = 16
	sizeFn        = 20

	offBiTable = 4
	offBiName  = 8
	sizeBi     = 12

	offBoundFn   = 4
	offBoundRecv = 8

	offCellVal = 4

	offRangeStart = 8
	offRangeStop  = 16
	offRangeStep  = 24
	sizeRange     = 32

	// function spec (static)
	offSpecName   = 0
	offSpecNamed  = 4
	offSpecKwonly = 8
	offSpecFlags  = 12
	offSpecNames  = 16
	offSpecActive = 20
	sizeSpec      = 24
)

// Spec flags.
const (
	SpecVarargs   = 1
	SpecKwargs    = 2
	SpecRecursive = 4 // recursion permitted
)

// CallType is the signature of every Starlark-callable function:
// (args tuple, kwargs dict or 0, self object) -> result.
var CallType = FuncType{Params: []ValType{I32, I32, I32}, Results: []ValType{I32}}

var (
	p0  = []ValType{}
	p1  = []ValType{I32}
	p2  = []ValType{I32, I32}
	p3  = []ValType{I32, I32, I32}
	p4  = []ValType{I32, I32, I32, I32}
	r1  = []ValType{I32}
	r0  = []ValType{}
	pi  = []ValType{I64}
	pf  = []ValType{F64}
	ri  = []ValType{I64}
	rf  = []ValType{F64}
	pii = []ValType{I64, I64}
)

type def struct {
	params, results []ValType
	build           func(f *Func)
}

// Config controls runtime generation.
type Config struct {
	// Command selects the WASI command model: errors are written to
	// stderr and terminate the process via proc_exit.
	Command bool
}

// RT is the runtime library for one module.
type RT struct {
	M   *Module
	Cfg Config

	defs  map[string]def
	funcs map[string]*Func

	Heap     *Global // next free address
	HeapMark *Global // arena reset point
	ErrObj   *Global // string object with the last error message
	Cursor   *Global // ABI encoder/decoder cursor
	IntCache *Global // table of preallocated small ints, or 0

	None, False, True uint32

	strs   map[string]uint32
	ints   map[int64]uint32
	floats map[uint64]uint32

	builtins map[string]uint32
	fixups   []Expr // stores executed by init_static

	scratch uint32 // 16 bytes of static scratch memory

	// Methods maps "type.method" to the global slot holding the callable.
	methodNames   []string
	methodIDs     map[string]int
	methodTable   uint32 // static table [NumTags][len(methodNames)]i32
	methodNameTab uint32 // static table of method name strings
	methodInit    []Expr

	// Hooks supplied by the compiler: Starlark-level helpers from the prelude.
	Hooks map[string]func() Expr

	fdWrite  *Import
	procExit *Import
}

// New returns a runtime that adds its functions to m.
func New(m *Module, cfg Config) *RT {
	r := &RT{
		M:         m,
		Cfg:       cfg,
		defs:      map[string]def{},
		funcs:     map[string]*Func{},
		strs:      map[string]uint32{},
		ints:      map[int64]uint32{},
		floats:    map[uint64]uint32{},
		builtins:  map[string]uint32{},
		methodIDs: map[string]int{},
		Hooks:     map[string]func() Expr{},
	}
	r.Heap = m.AddGlobal(&Global{Name: "heap", Type: I32, Mutable: true})
	r.HeapMark = m.AddGlobal(&Global{Name: "heap_mark", Type: I32, Mutable: true})
	r.ErrObj = m.AddGlobal(&Global{Name: "err", Type: I32, Mutable: true})
	r.Cursor = m.AddGlobal(&Global{Name: "cursor", Type: I32, Mutable: true})
	r.IntCache = m.AddGlobal(&Global{Name: "int_cache", Type: I32, Mutable: true})

	r.None = m.Static(8, 8)
	m.PutU32(r.None, TNone|1<<8)
	r.False = m.Static(8, 8)
	m.PutU32(r.False, TBool|1<<8)
	r.True = m.Static(8, 8)
	m.PutU32(r.True, TBool|1<<8)
	m.PutU32(r.True+offBool, 1)
	r.scratch = m.Static(16, 8)

	r.fdWrite = m.AddImport(&Import{Module: "wasi_snapshot_preview1", Name: "fd_write",
		Type: FuncType{Params: p4, Results: r1}})
	r.procExit = m.AddImport(&Import{Module: "wasi_snapshot_preview1", Name: "proc_exit",
		Type: FuncType{Params: p1}})

	r.registerCore()
	r.registerNum()
	r.registerStr()
	r.registerColl()
	r.registerCall()
	r.registerCallExtras()
	r.registerBuiltins()
	r.registerABI()
	return r
}

func (r *RT) define(name string, params, results []ValType, build func(f *Func)) {
	if _, dup := r.defs[name]; dup {
		panic("rt: duplicate definition " + name)
	}
	r.defs[name] = def{params, results, build}
}

// Fn returns the runtime function with the given name, generating it on
// first use.
func (r *RT) Fn(name string) *Func {
	if f, ok := r.funcs[name]; ok {
		return f
	}
	d, ok := r.defs[name]
	if !ok {
		panic("rt: unknown runtime function " + name)
	}
	f := r.M.AddFunc(NewFunc("rt."+name, d.params, d.results...))
	r.funcs[name] = f
	d.build(f)
	return f
}

// Call calls the named runtime function.
func (r *RT) Call(name string, args ...Expr) Expr { return Call(r.Fn(name), args...) }

// Finish must be called after code generation: it finalizes static data
// and returns the function that initializes static objects.
func (r *RT) Finish() *Func {
	f := r.M.AddFunc(NewFunc("rt.init_static", nil))
	f.Add(r.fixups...)
	if _, ok := r.funcs["int"]; ok {
		t, i, o := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			Set(t, r.Call("alloc", c32(4*intCacheSize))),
			forRange(i, c32(0), c32(intCacheSize),
				Set(o, r.Call("alloc", c32(16))),
				st(get(o), 0, c32(TInt|1<<8)),
				I64Store(get(o), offInt, I64ExtendI32S(sub(get(i), c32(intCacheMin)))),
				st(elem(get(t), 0, get(i)), 0, get(o)),
			),
			GSet(r.IntCache, get(t)),
		)
	}
	f.Add(r.methodInit...)
	end := (r.M.DataEnd() + 7) &^ 7
	r.Heap.Init = int64(end)
	r.HeapMark.Init = int64(end)
	return f
}

// ---- static objects ----

func addr(a uint32) Expr { return U32c(a) }

// NoneV etc. return expressions yielding the singletons.
func (r *RT) NoneV() Expr  { return addr(r.None) }
func (r *RT) FalseV() Expr { return addr(r.False) }
func (r *RT) TrueV() Expr  { return addr(r.True) }

// Str returns the address of a static, frozen string object.
func (r *RT) Str(s string) uint32 {
	if a, ok := r.strs[s]; ok {
		return a
	}
	a := r.M.Static(offStrBytes+len(s), 8)
	r.M.PutU32(a, TString|1<<8)
	r.M.PutU32(a+offStrLen, uint32(len(s)))
	copy(r.M.StaticSlice(a+offStrBytes, len(s)), s)
	r.strs[s] = a
	return a
}

// S yields a static string object.
func (r *RT) S(s string) Expr { return addr(r.Str(s)) }

// Int returns the address of a static int object.
func (r *RT) Int(v int64) uint32 {
	if a, ok := r.ints[v]; ok {
		return a
	}
	a := r.M.Static(16, 8)
	r.M.PutU32(a, TInt|1<<8)
	r.M.PutU64(a+offInt, uint64(v))
	r.ints[v] = a
	return a
}

// Float returns the address of a static float object.
func (r *RT) Float(v float64) uint32 {
	k := math.Float64bits(v)
	if a, ok := r.floats[k]; ok {
		return a
	}
	a := r.M.Static(16, 8)
	r.M.PutU32(a, TFloat|1<<8)
	r.M.PutF64(a+offFloat, v)
	r.floats[k] = a
	return a
}

// Builtin returns a static builtin object wrapping the CallType function f.
func (r *RT) Builtin(name string, f *Func) uint32 {
	key := name + "\x00" + f.Name
	if a, ok := r.builtins[key]; ok {
		return a
	}
	a := r.M.Static(sizeBi, 8)
	r.M.PutU32(a, TBuiltin|1<<8)
	r.M.PutU32(a+offBiName, r.Str(name))
	r.fixups = append(r.fixups, I32Store(addr(a), offBiTable, TableIndex(f)))
	r.builtins[key] = a
	return a
}

// Spec allocates a static function spec.
func (r *RT) Spec(name string, params []string, nkwonly int, flags int) uint32 {
	names := r.M.Static(4*len(params)+4, 4)
	for i, p := range params {
		r.M.PutU32(names+uint32(4*i), r.Str(p))
	}
	s := r.M.Static(sizeSpec, 8)
	r.M.PutU32(s+offSpecName, r.Str(name))
	r.M.PutU32(s+offSpecNamed, uint32(len(params)))
	r.M.PutU32(s+offSpecKwonly, uint32(nkwonly))
	r.M.PutU32(s+offSpecFlags, uint32(flags))
	r.M.PutU32(s+offSpecNames, names)
	return s
}

// ---- method table ----

// MethodID interns a method name.
func (r *RT) MethodID(name string) int {
	if id, ok := r.methodIDs[name]; ok {
		return id
	}
	id := len(r.methodNames)
	r.methodNames = append(r.methodNames, name)
	r.methodIDs[name] = id
	return id
}

// MethodNames returns all interned method names.
func (r *RT) MethodNames() []string { return r.methodNames }

// SetMethod records that init must store the callable yielded by v in
// the method table entry for (tag, name). It must be called before the
// method table is laid out by Layout.
func (r *RT) SetMethod(tag int, name string, v Expr) {
	id := r.MethodID(name)
	r.methodInit = append(r.methodInit, Raw(func() Expr {
		return I32Store(addr(r.methodTable), uint32(4*(tag*len(r.methodNames)+id)), v)
	}))
}

// StoreMethod yields code that stores the callable v in the method table
// entry for (tag, name). The name must have been interned before
// LayoutMethods is called.
func (r *RT) StoreMethod(tag int, name string, v Expr) Expr {
	id := r.MethodID(name)
	return Raw(func() Expr {
		return I32Store(addr(r.methodTable), uint32(4*(tag*len(r.methodNames)+id)), v)
	})
}

// LayoutMethods allocates the method tables; call once all names are interned.
func (r *RT) LayoutMethods() {
	n := len(r.methodNames)
	r.methodTable = r.M.Static(4*NumTags*n+4, 4)
	r.methodNameTab = r.M.Static(4*n+4, 4)
	for i, name := range r.methodNames {
		r.M.PutU32(r.methodNameTab+uint32(4*i), r.Str(name))
	}
	r.Fn("str_eq")
}

// ---- helpers for writing runtime code ----

func tagOf(v Expr) Expr                  { return I32Load8U(v, 0) }
func isTag(v Expr, t int) Expr           { return I32Eq(tagOf(v), I32c(int32(t))) }
func ld(p Expr, off uint32) Expr         { return I32Load(p, off) }
func st(p Expr, off uint32, v Expr) Expr { return I32Store(p, off, v) }
func c32(v int) Expr                     { return I32c(int32(v)) }
func add(a, b Expr) Expr                 { return I32Add(a, b) }
func sub(a, b Expr) Expr                 { return I32Sub(a, b) }
func mul(a, b Expr) Expr                 { return I32Mul(a, b) }
func eq(a, b Expr) Expr                  { return I32Eq(a, b) }
func ne(a, b Expr) Expr                  { return I32Ne(a, b) }
func lt(a, b Expr) Expr                  { return I32LtS(a, b) }
func ge(a, b Expr) Expr                  { return I32GeS(a, b) }
func gt(a, b Expr) Expr                  { return I32GtS(a, b) }
func le(a, b Expr) Expr                  { return I32LeS(a, b) }
func and(a, b Expr) Expr                 { return I32And(a, b) }
func or(a, b Expr) Expr                  { return I32Or(a, b) }
func not(a Expr) Expr                    { return I32Eqz(a) }
func get(l Local) Expr                   { return Get(l) }

// elem yields the address of the i'th 4-byte element after base+off.
func elem(base Expr, off uint32, i Expr) Expr {
	return add(add(base, I32Shl(i, c32(2))), c32(int(off)))
}

// forRange emits: for i = from; i < to; i++ { body }.
func forRange(i Local, from, to Expr, body ...Expr) Expr {
	brk, cont := NewLabel(), NewLabel()
	return Seq{
		Set(i, from),
		Block(brk, Loop(cont,
			BrIf(brk, ge(get(i), to)),
			Seq(body),
			Set(i, add(get(i), c32(1))),
			Br(cont),
		)),
	}
}

// forRangeL is forRange with an explicit break label available to body.
func forRangeL(i Local, from, to Expr, brk *Label, body ...Expr) Expr {
	cont := NewLabel()
	return Seq{
		Set(i, from),
		Block(brk, Loop(cont,
			BrIf(brk, ge(get(i), to)),
			Seq(body),
			Set(i, add(get(i), c32(1))),
			Br(cont),
		)),
	}
}

// Fail yields code that raises a Starlark error whose message is the
// concatenation of parts (strings or Exprs yielding string objects).
func (r *RT) Fail(parts ...any) Expr {
	return Seq{r.Call("fail", r.Msg(parts...)), Unreachable()}
}

// Msg concatenates message parts into a string object.
func (r *RT) Msg(parts ...any) Expr {
	var acc Expr
	for _, p := range parts {
		var x Expr
		switch p := p.(type) {
		case string:
			x = r.S(p)
		case Expr:
			x = p
		default:
			panic(fmt.Sprintf("rt: bad message part %T", p))
		}
		if acc == nil {
			acc = x
		} else {
			acc = r.Call("str_concat", acc, x)
		}
	}
	if acc == nil {
		return r.S("")
	}
	return acc
}

// TypeName yields the type name string of v.
func (r *RT) TypeName(v Expr) Expr { return r.Call("type_name", v) }

func (r *RT) bool(cond Expr) Expr { return Select(r.TrueV(), r.FalseV(), cond) }

// ---- core functions ----

func (r *RT) registerCore() {
	// alloc(n) -> zeroed, 8-aligned memory
	r.define("alloc", p1, r1, func(f *Func) {
		n := f.Param(0)
		p, end := f.Local(I32), f.Local(I32)
		f.Add(
			Set(p, GGet(r.Heap)),
			Set(end, add(get(p), and(add(get(n), c32(7)), c32(-8)))),
			If(I32GtU(get(end), I32Shl(MemorySize(), c32(16))),
				If(eq(MemoryGrow(I32ShrU(add(sub(get(end), I32Shl(MemorySize(), c32(16))), c32(0xffff)), c32(16))), c32(-1)),
					r.Fail("out of memory")),
			),
			GSet(r.Heap, get(end)),
			MemoryFill(get(p), c32(0), get(n)),
			get(p),
		)
	})

	// fail(msg string): records the error and traps.
	r.define("fail", p1, r0, func(f *Func) {
		msg := f.Param(0)
		f.Add(GSet(r.ErrObj, get(msg)))
		if r.Cfg.Command {
			f.Add(
				Drop(r.Call("write", c32(2), r.S("Error: "))),
				Drop(r.Call("write", c32(2), get(msg))),
				Drop(r.Call("write", c32(2), r.S("\n"))),
				Call(r.procExit, c32(1)),
			)
		}
		f.Add(Unreachable())
	})

	// write(fd, str) -> errno
	r.define("write", p2, r1, func(f *Func) {
		fd, s := f.Param(0), f.Param(1)
		iov := r.scratch
		f.Add(
			st(addr(iov), 0, add(get(s), c32(offStrBytes))),
			st(addr(iov), 4, ld(get(s), offStrLen)),
			Call(r.fdWrite, get(fd), addr(iov), c32(1), addr(iov+8)),
		)
	})

	// type_name(v) -> str
	r.define("type_name", p1, r1, func(f *Func) {
		v := f.Param(0)
		names := []string{"NoneType", "bool", "int", "float", "string", "tuple", "list", "dict",
			"function", "builtin_function_or_method", "cell", "range", "builtin_function_or_method"}
		var body Expr = r.S("unknown")
		for t := len(names) - 1; t >= 0; t-- {
			body = IfV(I32, isTag(get(v), t), r.S(names[t]), body)
		}
		f.Add(body)
	})

	// truth(v) -> i32
	r.define("truth", p1, r1, func(f *Func) {
		v := f.Param(0)
		t := f.Local(I32)
		f.Add(
			Set(t, tagOf(get(v))),
			If(eq(get(t), c32(TNone)), Return(c32(0))),
			If(eq(get(t), c32(TBool)), Return(ld(get(v), offBool))),
			If(eq(get(t), c32(TInt)), Return(I64Ne(I64Load(get(v), offInt), I64c(0)))),
			If(eq(get(t), c32(TFloat)), Return(F64Ne(F64Load(get(v), offFloat), F64c(0)))),
			If(or(or(eq(get(t), c32(TString)), eq(get(t), c32(TTuple))), or(eq(get(t), c32(TList)), eq(get(t), c32(TDict)))),
				Return(ne(ld(get(v), 4), c32(0)))),
			If(eq(get(t), c32(TRange)), Return(I64Ne(r.Call("range_len", get(v)), I64c(0)))),
			c32(1),
		)
	})

	// cell(v) -> new cell
	r.define("cell", p1, r1, func(f *Func) {
		c := f.Local(I32)
		f.Add(
			Set(c, r.Call("alloc", c32(8))),
			st(get(c), 0, c32(TCell)),
			st(get(c), offCellVal, get(f.Param(0))),
			get(c),
		)
	})

	// unbound(name): error for use of an unassigned variable.
	r.define("unbound", p1, r0, func(f *Func) {
		f.Add(r.Fail("variable ", get(f.Param(0)), " referenced before assignment"))
	})

	// frozen_check(v, what)
	r.define("check_mutable", p1, r0, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(and(I32Load8U(get(v), offFlags), c32(1)),
				r.Fail("cannot modify frozen ", r.TypeName(get(v)))),
		)
	})

	// freeze(v): recursively marks v frozen.
	r.define("freeze", p1, r0, func(f *Func) {
		v := f.Param(0)
		t, i, n := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			If(eqz(get(v)), Return(nil)),
			If(and(I32Load8U(get(v), offFlags), c32(1)), Return(nil)),
			I32Store8(get(v), offFlags, or(I32Load8U(get(v), offFlags), c32(1))),
			Set(t, tagOf(get(v))),
			If(eq(get(t), c32(TTuple)),
				forRange(i, c32(0), ld(get(v), offTupLen),
					r.Call("freeze", ld(elem(get(v), offTupElems, get(i)), 0)))),
			If(eq(get(t), c32(TList)),
				forRange(i, c32(0), ld(get(v), offListLen),
					r.Call("freeze", ld(elem(ld(get(v), offListData), 0, get(i)), 0)))),
			If(eq(get(t), c32(TDict)), Seq{
				Set(n, ld(get(v), offDictUsed)),
				forRange(i, c32(0), get(n),
					r.Call("freeze", ld(add(ld(get(v), offDictEntries), mul(get(i), c32(12))), 4)),
					r.Call("freeze", ld(add(ld(get(v), offDictEntries), mul(get(i), c32(12))), 8)),
				),
			}),
			If(eq(get(t), c32(TFunction)), Seq{
				r.Call("freeze", ld(get(v), offFnDefaults)),
				r.Call("freeze", ld(get(v), offFnFree)),
			}),
			If(eq(get(t), c32(TCell)), r.Call("freeze", ld(get(v), offCellVal))),
			If(eq(get(t), c32(TBound)), r.Call("freeze", ld(get(v), offBoundRecv))),
		)
	})
}

func eqz(a Expr) Expr { return I32Eqz(a) }

// ---- helpers for the compiler ----

// CellGet yields the value stored in a cell (0 if unbound).
func (r *RT) CellGet(cell Expr) Expr { return ld(cell, offCellVal) }

// CellSet stores v in a cell.
func (r *RT) CellSet(cell, v Expr) Expr { return st(cell, offCellVal, v) }

// FreeVar yields the cell of the i'th free variable of function object self.
func (r *RT) FreeVar(self Expr, i int) Expr { return ld(ld(self, offFnFree), uint32(offTupElems+4*i)) }

// TupleSet stores v as element i of a freshly allocated tuple t.
func (r *RT) TupleSet(t Expr, i int, v Expr) Expr { return st(t, uint32(offTupElems+4*i), v) }

// Slot yields parameter slot i of a bind result.
func (r *RT) Slot(slots Expr, i int) Expr { return ld(slots, uint32(4*i)) }

// Leave clears the recursion guard of a function spec.
func (r *RT) Leave(spec uint32) Expr { return st(addr(spec), offSpecActive, c32(0)) }
