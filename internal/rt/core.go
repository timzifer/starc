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

	. "github.com/timzifer/starc/internal/wasm"
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
	TModule   = 13
	TSet      = 14
	TStruct   = 15
	TOpaque   = 16
	NumTags   = 17
)

// Object layouts (byte offsets).
const (
	offFlags = 1 // u8: bit 0 = frozen, bit 1 = being printed by repr

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
	sizeBi     = 16 // offBiProf at 12

	offBoundFn   = 4
	offBoundRecv = 8

	offCellVal = 4

	offModName    = 4
	offModLen     = 8
	offModMembers = 12
	sizeMod       = 16

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
	offSpecShow   = 24 // name in backtraces of a prelude function, 0 if hidden
	sizeSpec      = 40 // offSpecProf at 28, offSpecDbg at 32
)

// flagRepr marks a list or dict whose repr is in progress, to detect cycles.
const flagRepr = 2

// Spec flags.
const (
	SpecVarargs   = 1
	SpecKwargs    = 2
	SpecRecursive = 4 // recursion permitted
	SpecPrelude   = 8 // defined by the prelude: a built-in in backtraces
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
	// NoWASI removes all WASI imports: print calls the host import
	// starc_host.starc_print instead of fd_write. It excludes Command.
	NoWASI bool
	// Fuel enables fuel metering (see fuel.go).
	Fuel bool
	// FiniteFloats makes the creation of NaN or an infinity an error.
	FiniteFloats bool
	// MaxCallDepth is the maximum depth of calls and of recursion into
	// nested values (see limits.go).
	MaxCallDepth int
	// MaxInputBytes and MaxResultBytes limit the encoded arguments and
	// result of an exported call; 0 means no limit.
	MaxInputBytes, MaxResultBytes uint32
	// Backtrace records source positions and a shadow call stack for
	// the backtraces of errors (see backtrace.go).
	Backtrace bool
	// Filename is the name of the source file in backtraces.
	Filename string
	// Profile attributes fuel to functions (see profile.go). It requires
	// Fuel and Backtrace.
	Profile bool
	// Debug compiles statement hooks for a debugger (see debug.go). It
	// requires Backtrace.
	Debug bool
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
	CurEnd   *Global // end of the buffer being decoded
	IntCache *Global // table of preallocated small ints, or 0
	HeapMax  *Global // high-water mark of the heap
	ReprBase *Global // stack of the values that repr marks (see reprMark)
	ReprTop  *Global
	CmpDepth *Global // nesting depth of the current comparison
	Fuel     *Global // remaining fuel (i64), with Config.Fuel
	ErrCode  *Global // code of the last error (ErrStarlark, ErrFuel, ...)
	Depth    *Global // current call depth

	// Backtraces (see backtrace.go).
	CurPos     *Global // position id of the current operation
	SP         *Global // top of the shadow stack
	StackBase  *Global // bottom of the shadow stack
	StackLimit *Global // end of the shadow stack
	ErrFrames  *Global // number of frames of the last error
	DupKw      *Global // keyword passed twice to the pending call, or 0
	posIDs     map[[3]int]uint32
	posList    [][3]int
	files      []string // source files of positions (see Files)
	fileTab    uint32   // static table of the file name strings
	posTab     uint32   // static table of (line, col) by position id
	digits     uint32   // static string for writing numbers

	// Fuel profile (see profile.go).
	ProfCur     *Global // 1-based entry that the fuel is attributed to
	ProfLast    *Global // fuel at the last flush (i64)
	ProfPrelude *Global // whether the current entry is a prelude function
	profEntries []ProfileEntry
	profTab     uint32

	// Debug builds (see debug.go).
	DbgMode      *Global // DbgOff, DbgBreak or DbgStep
	DbgPos       *Global // position id of the current stop
	DbgAreas     *Global // locals areas, one per frame slot
	DbgReading   *Global // whether the host is reading a value
	dbgImport    *Import
	dbgBreaks    uint32 // static breakpoint flags by position id
	dbgTop       uint32 // descriptor of the top level
	dbgMaxLocals int
	dbgStmts     []uint32

	None, False, True uint32

	strs   map[string]uint32
	ints   map[int64]uint32
	floats map[uint64]uint32

	builtins map[string]uint32
	fixups   []Expr // stores executed by init_static

	scratch uint32    // 16 bytes of static scratch memory
	decs    [3]uint32 // static decimals for float conversion (decimal.go)

	// Methods maps "type.method" to the global slot holding the callable.
	methodNames   []string
	methodIDs     map[string]int
	methodTable   uint32 // static table [NumTags][len(methodNames)]i32
	methodNameTab uint32 // static table of method name strings
	methodInit    []Expr

	// Hooks supplied by the compiler: Starlark-level helpers from the prelude.
	Hooks map[string]func() Expr

	fdWrite   *Import
	procExit  *Import
	hostPrint *Import
	hostRes   *Import
	hostFuncs []string // host functions in creation order
	modules   bool     // whether any module object exists
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
	r.CurEnd = m.AddGlobal(&Global{Name: "cursor_end", Type: I32, Mutable: true})
	r.IntCache = m.AddGlobal(&Global{Name: "int_cache", Type: I32, Mutable: true})
	r.HeapMax = m.AddGlobal(&Global{Name: "heap_max", Type: I32, Mutable: true})
	r.ReprBase = m.AddGlobal(&Global{Name: "repr_marks", Type: I32, Mutable: true})
	r.ReprTop = m.AddGlobal(&Global{Name: "repr_marks_top", Type: I32, Mutable: true})
	r.CmpDepth = m.AddGlobal(&Global{Name: "cmp_depth", Type: I32, Mutable: true})

	r.None = m.Static(8, 8)
	m.PutU32(r.None, TNone|1<<8)
	r.False = m.Static(8, 8)
	m.PutU32(r.False, TBool|1<<8)
	r.True = m.Static(8, 8)
	m.PutU32(r.True, TBool|1<<8)
	m.PutU32(r.True+offBool, 1)
	r.scratch = m.Static(16, 8)

	if cfg.NoWASI {
		r.hostPrint = m.AddImport(&Import{Module: HostModule, Name: HostPrint, Type: FuncType{Params: p2}})
	} else {
		r.fdWrite = m.AddImport(&Import{Module: "wasi_snapshot_preview1", Name: "fd_write",
			Type: FuncType{Params: p4, Results: r1}})
		r.procExit = m.AddImport(&Import{Module: "wasi_snapshot_preview1", Name: "proc_exit",
			Type: FuncType{Params: p1}})
	}
	r.hostRes = m.AddImport(&Import{Module: HostModule, Name: HostResult, Type: FuncType{Params: p1}})

	if cfg.MaxCallDepth == 0 {
		r.Cfg.MaxCallDepth = DefaultMaxCallDepth
	}
	r.initFuel()
	r.initLimits()
	r.initBacktrace()
	r.initProfile()
	r.initDebug()
	r.registerCore()
	r.registerNum()
	r.registerStr()
	r.registerUnicode()
	r.registerDecimal()
	r.registerColl()
	r.registerCall()
	r.registerCallExtras()
	r.registerBuiltins()
	r.registerABI()
	r.registerHost()
	r.registerSet()
	r.registerStruct()
	r.registerOpaque()
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
		// The cache is the runtime's own: building it costs no fuel, so
		// that fuel does not depend on which runtime functions a module
		// contains (a debug build contains more).
		t, i, o := f.Local(I32), f.Local(I32), f.Local(I32)
		brk, cont := NewLabel(), NewLabel()
		f.Add(
			Set(t, r.Call("alloc_free", c32(4*intCacheSize))),
			Block(brk, Loop(cont,
				BrIf(brk, ge(get(i), c32(intCacheSize))),
				Set(o, r.Call("alloc_free", c32(16))),
				st(get(o), 0, c32(TInt|1<<8)),
				I64Store(get(o), offInt, I64ExtendI32S(sub(get(i), c32(intCacheMin)))),
				st(elem(get(t), 0, get(i)), 0, get(o)),
				Set(i, add(get(i), c32(1))),
				Br(cont),
			)),
			GSet(r.IntCache, get(t)),
		)
	}
	f.Add(r.methodInit...)
	f.Add(r.initFrames())
	f.Add(r.initReprMarks())
	r.layoutPositions()
	r.layoutProfile()
	r.layoutDebug()
	end := (r.M.DataEnd() + 7) &^ 7
	r.Heap.Init = int64(end)
	r.HeapMark.Init = int64(end)
	r.HeapMax.Init = int64(end)
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

// CanonicalNaN is the bit pattern of every NaN value: the positive quiet
// NaN with an empty payload (a canonical NaN in WebAssembly terms).
const CanonicalNaN = 0x7ff8000000000000

// Float returns the address of a static float object.
func (r *RT) Float(v float64) uint32 {
	if v != v {
		v = math.Float64frombits(CanonicalNaN)
	}
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
	r.profileBuiltin(a, name, ProfBuiltin)
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

// SpecShow sets the name under which a prelude function appears in
// backtraces; without it, the function is hidden.
func (r *RT) SpecShow(spec uint32, name string) { r.M.PutU32(spec+offSpecShow, r.Str(name)) }

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
func (r *RT) forRange(i Local, from, to Expr, body ...Expr) Expr {
	brk, cont := NewLabel(), NewLabel()
	return Seq{
		Set(i, from),
		Block(brk, r.loop(cont,
			BrIf(brk, ge(get(i), to)),
			Seq(body),
			Set(i, add(get(i), c32(1))),
			Br(cont),
		)),
	}
}

// forRangeL is forRange with an explicit break label available to body.
func (r *RT) forRangeL(i Local, from, to Expr, brk *Label, body ...Expr) Expr {
	cont := NewLabel()
	return Seq{
		Set(i, from),
		Block(brk, r.loop(cont,
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
	// alloc(n) -> zeroed, 8-aligned memory. Memory above the high-water
	// mark HeapMax has never been used and is zero; arena_reset clears
	// the reused region in bulk, so alloc itself never clears memory.
	// alloc_free is alloc without charging fuel, for the runtime's own
	// data structures.
	alloc := func(charge bool) func(f *Func) {
		return func(f *Func) {
			n := f.Param(0)
			if charge {
				f.Add(r.chargeBytes(get(n)))
			}
			r.allocBody(f, n)
		}
	}
	r.define("alloc", p1, r1, alloc(true))
	r.define("alloc_free", p1, r1, alloc(false))

	// arena_reset(): discards everything allocated since HeapMark.
	r.define("arena_reset", p0, r0, func(f *Func) {
		f.Add(
			MemoryFill(GGet(r.HeapMark), c32(0), sub(GGet(r.HeapMax), GGet(r.HeapMark))),
			GSet(r.Heap, GGet(r.HeapMark)),
			GSet(r.HeapMax, GGet(r.HeapMark)),
		)
	})

	// fail(msg string): records the error and traps.
	r.define("fail", p1, r0, func(f *Func) {
		msg := f.Param(0)
		if r.Cfg.Debug {
			// An error while the host reads a value during a stop fails
			// only the read, leaving the stopped program as it is.
			f.Add(If(GGet(r.DbgReading), Seq{GSet(r.ErrObj, get(msg)), r.reprUnwind(), Unreachable()}))
		}
		f.Add(GSet(r.ErrObj, get(msg)))
		// The fuel up to the error counts toward the failing function.
		f.Add(r.ProfileEnd())
		if r.Cfg.Backtrace {
			f.Add(r.Call("bt_collect"), GSet(r.DupKw, c32(0)))
		}
		f.Add(
			GSet(r.CmpDepth, c32(0)),
			GSet(r.Depth, c32(0)),
			r.reprUnwind(),
			If(eqz(GGet(r.ErrCode)), GSet(r.ErrCode, c32(ErrStarlark))),
		)
		if r.Cfg.Command {
			if r.Cfg.Backtrace {
				f.Add(r.Call("write_backtrace", c32(2)))
			} else {
				f.Add(Drop(r.Call("write", c32(2), r.S("Error: "))))
			}
			f.Add(
				Drop(r.Call("write", c32(2), get(msg))),
				Drop(r.Call("write", c32(2), r.S("\n"))),
				// The exit status is the error code.
				Call(r.procExit, GGet(r.ErrCode)),
			)
		}
		f.Add(Unreachable())
	})

	// write(fd, str) -> errno. Without WASI, only print writes (fd 1),
	// and the text goes to the host.
	r.define("write", p2, r1, func(f *Func) {
		fd, s := f.Param(0), f.Param(1)
		if r.Cfg.NoWASI {
			f.Add(Call(r.hostPrint, add(get(s), c32(offStrBytes)), ld(get(s), offStrLen)), c32(0))
			return
		}
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
			"function", "builtin_function_or_method", "cell", "range", "builtin_function_or_method", "module", "set", "struct"}
		// An opaque value names its own type.
		body := IfV(I32, isTag(get(v), TOpaque), ld(get(v), offOpqType), r.S("unknown"))
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
			If(or(or(or(eq(get(t), c32(TString)), eq(get(t), c32(TTuple))), or(eq(get(t), c32(TList)), eq(get(t), c32(TDict)))), eq(get(t), c32(TSet))),
				Return(ne(ld(get(v), 4), c32(0)))),
			If(eq(get(t), c32(TRange)), Return(I64Ne(r.Call("range_len", get(v)), I64c(0)))),
			If(eq(get(t), c32(TOpaque)), Return(eqz(and(ld(get(v), offOpqFlags), c32(OpaqueFalsy))))),
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
	r.defineDeep("freeze", p1, r0, func(f *Func) {
		v := f.Param(0)
		t, i, n := f.Local(I32), f.Local(I32), f.Local(I32)
		f.Add(
			If(eqz(get(v)), Return(nil)),
			If(and(I32Load8U(get(v), offFlags), c32(1)), Return(nil)),
			I32Store8(get(v), offFlags, or(I32Load8U(get(v), offFlags), c32(1))),
			Set(t, tagOf(get(v))),
			If(eq(get(t), c32(TTuple)),
				r.forRange(i, c32(0), ld(get(v), offTupLen),
					r.Call("freeze", ld(elem(get(v), offTupElems, get(i)), 0)))),
			If(eq(get(t), c32(TList)),
				r.forRange(i, c32(0), ld(get(v), offListLen),
					r.Call("freeze", ld(elem(ld(get(v), offListData), 0, get(i)), 0)))),
			If(or(eq(get(t), c32(TDict)), eq(get(t), c32(TSet))), Seq{
				Set(n, ld(get(v), offDictUsed)),
				r.forRange(i, c32(0), get(n),
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
			If(eq(get(t), c32(TStruct)),
				r.forRange(i, c32(0), ld(get(v), offStructLen),
					r.Call("freeze", ld(add(ld(get(v), offStructFields), I32Shl(get(i), c32(3))), 4)))),
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

// allocBody adds the code of alloc(n) to f.
func (r *RT) allocBody(f *Func, n Local) {
	p, end, need, pages := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
	f.Add(
		Set(p, GGet(r.Heap)),
		Set(end, add(get(p), and(add(get(n), c32(7)), c32(-8)))),
		If(I32LtU(get(end), get(p)), r.Call("out_of_memory")),
		If(I32GtU(get(end), I32Shl(MemorySize(), c32(16))), Seq{
			// Grow geometrically, by at least 4 MiB, to keep the number
			// of (expensive) grow operations small.
			Set(need, I32ShrU(add(sub(get(end), I32Shl(MemorySize(), c32(16))), c32(0xffff)), c32(16))),
			Set(pages, MemorySize()),
			If(I32LtU(get(pages), c32(64)), Set(pages, c32(64))),
			If(I32GtU(get(pages), c32(4096)), Set(pages, c32(4096))),
			If(I32LtU(get(pages), get(need)), Set(pages, get(need))),
			If(eq(MemoryGrow(get(pages)), c32(-1)),
				If(eq(MemoryGrow(get(need)), c32(-1)), r.Call("out_of_memory"))),
		}),
		GSet(r.Heap, get(end)),
		If(I32GtU(get(end), GGet(r.HeapMax)), GSet(r.HeapMax, get(end))),
		get(p),
	)
}
