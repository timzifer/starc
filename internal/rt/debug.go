package rt

import (
	"math"

	. "github.com/timzifer/starc/internal/wasm"
)

// Debug builds. With Config.Debug, every statement of the program starts
// with a hook that, while debugging is on (DbgMode != 0), stores the
// function's local variables in the area of its frame and calls
// dbg_stmt with the statement's position id. dbg_stmt stops at the
// statement if the mode is DbgStep or a breakpoint is set on the position:
// it calls the host import starc_dbg, during which the host reads the
// stack, the local variables of every frame and the globals through the
// dbg_* exports.
//
// A stop leaves no trace: the fuel, the heap and the error state are
// restored afterwards, and the hooks never charge fuel, so a debug build
// uses the same fuel and gives the same results as a release build. The
// values that the host reads are formatted with repr, which may allocate;
// an error while formatting one (such as a too deeply nested value) fails
// only the read.
//
// The locals of a frame are stored at the start of its current
// statement, so the variables of a caller are those before the statement
// that made the call.

// Debug modes.
const (
	DbgOff     = 0
	DbgBreak   = 1  // stop at breakpoints
	DbgStep    = 2  // stop at every statement
	offSpecDbg = 32 // debug descriptor of a spec
)

func (r *RT) initDebug() {
	if !r.Cfg.Debug {
		return
	}
	r.DbgMode = r.M.AddGlobal(&Global{Name: "dbg_mode", Type: I32, Mutable: true})
	r.DbgPos = r.M.AddGlobal(&Global{Name: "dbg_pos", Type: I32, Mutable: true})
	r.DbgAreas = r.M.AddGlobal(&Global{Name: "dbg_areas", Type: I32, Mutable: true})
	r.DbgReading = r.M.AddGlobal(&Global{Name: "dbg_reading", Type: I32, Mutable: true})
	r.dbgImport = r.M.AddImport(&Import{Module: HostModule, Name: HostDbg, Type: FuncType{Params: p1}})
	r.registerDebugError()

	// dbg_stmt(pos): stops at the statement at pos if requested.
	r.define("dbg_stmt", p1, r0, func(f *Func) {
		pos := f.Param(0)
		fuel := f.Local(I64)
		heap, heapMax, depth, errObj, errCode, curPos := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32),
			f.Local(I32), f.Local(I32)
		save := []Expr{
			Set(heap, GGet(r.Heap)), Set(heapMax, GGet(r.HeapMax)), Set(depth, GGet(r.Depth)),
			Set(errObj, GGet(r.ErrObj)), Set(errCode, GGet(r.ErrCode)), Set(curPos, GGet(r.CurPos)),
			// Values are read with the full depth limit.
			GSet(r.Depth, c32(0)),
		}
		restore := []Expr{
			// Memory above the heap must be zero (see alloc).
			MemoryFill(get(heap), c32(0), sub(GGet(r.HeapMax), get(heap))),
			GSet(r.Heap, get(heap)), GSet(r.HeapMax, get(heapMax)), GSet(r.Depth, get(depth)),
			GSet(r.ErrObj, get(errObj)), GSet(r.ErrCode, get(errCode)), GSet(r.CurPos, get(curPos)),
			GSet(r.DbgReading, c32(0)),
		}
		if r.Cfg.Fuel {
			// Reading values must not run out of fuel.
			save = append(save, Set(fuel, GGet(r.Fuel)), GSet(r.Fuel, I64c(math.MaxInt64)))
			restore = append(restore, GSet(r.Fuel, get(fuel)))
		}
		f.Add(
			If(and(ne(GGet(r.DbgMode), c32(DbgStep)), eqz(I32Load8U(r.dbgBreak(get(pos)), 0))), Return(nil)),
			Seq(save),
			GSet(r.DbgPos, get(pos)),
			Call(r.dbgImport, get(pos)),
			Seq(restore),
		)
	})
	// dbg_area() -> the locals area of the current frame
	r.define("dbg_area", p0, r1, func(f *Func) {
		f.Add(r.dbgAreaOf(I32ShrU(sub(sub(GGet(r.SP), c32(8)), GGet(r.StackBase)), c32(3))))
	})
	// dbg_set_mode(mode) and dbg_set_break(pos, on)
	r.define("dbg_set_mode", p1, r0, func(f *Func) {
		f.Add(GSet(r.DbgMode, get(f.Param(0))))
	})
	r.define("dbg_set_break", p2, r0, func(f *Func) {
		pos := f.Param(0)
		f.Add(If(I32LtU(get(pos), Raw(func() Expr { return c32(len(r.posList) + 1) })),
			I32Store8(r.dbgBreak(get(pos)), 0, ne(get(f.Param(1)), c32(0)))))
	})

	// dbg_frames() -> number of frames, outermost first
	r.define("dbg_frames", p0, r1, func(f *Func) {
		f.Add(I32ShrU(sub(GGet(r.SP), GGet(r.StackBase)), c32(3)))
	})
	// dbg_frame_fn(i) -> the callable of frame i (0 for the top level),
	// or -1 if there is no such frame
	r.define("dbg_frame_fn", p1, r1, func(f *Func) {
		i := f.Param(0)
		f.Add(
			If(I32GeU(get(i), r.Call("dbg_frames")), Return(c32(-1))),
			and(ld(add(GGet(r.StackBase), I32Shl(get(i), c32(3))), 0), c32(^btCallback)),
		)
	})
	// dbg_frame_name(i) -> packed name of frame i
	r.define("dbg_frame_name", p1, ri, func(f *Func) {
		fn, s := f.Local(I32), f.Local(I32)
		f.Add(
			Set(fn, r.Call("dbg_frame_fn", get(f.Param(0)))),
			If(or(eq(get(fn), c32(-1)), ne(and(get(fn), c32(btLoad)), c32(0))), Return(I64c(0))),
			Set(s, IfV(I32, eqz(get(fn)), r.S("<toplevel>"), r.Call("callable_name", get(fn)))),
			// A prelude function appears as the built-in it implements.
			If(eq(r.Call("dbg_frame_kind", get(f.Param(0))), c32(2)), Set(s, ld(ld(get(fn), offFnSpec), offSpecShow))),
			r.packStr(get(s)),
		)
	})
	// dbg_frame_kind(i) -> 0 for a function of the program or the top
	// level, 1 for a built-in, 2 for a prelude function that implements a
	// built-in, 3 for an internal prelude helper; -1 if none
	r.define("dbg_frame_kind", p1, r1, func(f *Func) {
		fn := f.Local(I32)
		f.Add(
			Set(fn, r.Call("dbg_frame_fn", get(f.Param(0)))),
			If(and(get(fn), c32(btLoad)), Return(c32(3))),
			If(le(get(fn), c32(0)), Return(get(fn))),
			If(isTag(get(fn), TBuiltin), Return(c32(1))),
			If(eqz(and(ld(ld(get(fn), offFnSpec), offSpecFlags), c32(SpecPrelude))), Return(c32(0))),
			IfV(I32, ld(ld(get(fn), offFnSpec), offSpecShow), c32(2), c32(3)),
		)
	})
	// dbg_frame_pos(i) -> packed (line | col<<32) where frame i is: the
	// call of frame i+1, or the statement of the stop for the innermost
	r.define("dbg_frame_pos", p1, ri, func(f *Func) {
		i, id := f.Param(0), f.Local(I32)
		f.Add(
			If(I32GeU(get(i), r.Call("dbg_frames")), Return(I64c(0))),
			Set(id, IfV(I32, I32LtU(add(get(i), c32(1)), r.Call("dbg_frames")),
				ld(add(GGet(r.StackBase), I32Shl(add(get(i), c32(1)), c32(3))), 4), GGet(r.DbgPos))),
			I64Load(r.posEntry(get(id)), 0),
		)
	})
	// dbg_desc(i) -> debug descriptor of frame i, or 0
	r.define("dbg_desc", p1, r1, func(f *Func) {
		fn, prev := f.Local(I32), f.Local(I32)
		f.Add(
			Set(fn, r.Call("dbg_frame_fn", get(f.Param(0)))),
			// The top level of a loaded module follows the frame of its load
			// statement, which holds its descriptor.
			If(eqz(get(fn)), Seq{
				If(and(ne(get(f.Param(0)), c32(0)), ne(and(Tee(prev, r.Call("dbg_frame_fn", sub(get(f.Param(0)), c32(1)))), c32(btLoad)), c32(0))),
					Return(and(get(prev), c32(-8)))),
				Return(Raw(func() Expr { return addr(r.dbgTop) })),
			}),
			If(ne(and(get(fn), c32(btLoad)), c32(0)), Return(c32(0))),
			If(or(eq(get(fn), c32(-1)), not(isTag(get(fn), TFunction))), Return(c32(0))),
			ld(ld(get(fn), offFnSpec), offSpecDbg),
		)
	})
	// dbg_locals(i) -> number of local variables of frame i
	r.define("dbg_locals", p1, r1, func(f *Func) {
		d := f.Local(I32)
		f.Add(Set(d, r.Call("dbg_desc", get(f.Param(0)))), IfV(I32, get(d), ld(get(d), 0), c32(0)))
	})
	// dbg_local_name(i, j) -> packed name of local j of frame i
	r.define("dbg_local_name", p2, ri, func(f *Func) {
		d := f.Local(I32)
		f.Add(
			If(I32GeU(get(f.Param(1)), r.Call("dbg_locals", get(f.Param(0)))), Return(I64c(0))),
			Set(d, r.Call("dbg_desc", get(f.Param(0)))),
			r.packStr(ld(elem(get(d), 4, I32Shl(get(f.Param(1)), c32(1))), 0)),
		)
	})
	// dbg_local(i, j) -> value of local j of frame i, 0 if unbound
	r.define("dbg_local", p2, r1, func(f *Func) {
		i, j := f.Param(0), f.Param(1)
		d, v := f.Local(I32), f.Local(I32)
		f.Add(
			If(I32GeU(get(j), r.Call("dbg_locals", get(i))), Return(c32(0))),
			Set(d, r.Call("dbg_desc", get(i))),
			Set(v, ld(elem(r.dbgAreaOf(get(i)), 0, get(j)), 0)),
			// A cell holds the value of a variable that closures share.
			If(and(ne(get(v), c32(0)), ne(ld(elem(get(d), 8, I32Shl(get(j), c32(1))), 0), c32(0))),
				Set(v, r.CellGet(get(v)))),
			get(v),
		)
	})
	// dbg_value(v) -> packed repr of v, 0 for 0 (unbound)
	r.define("dbg_value", p1, ri, func(f *Func) {
		v := f.Param(0)
		f.Add(
			If(eqz(get(v)), Return(I64c(0))),
			GSet(r.DbgReading, c32(1)),
			r.packStr(r.Call("repr", get(v))),
		)
	})
	// dbg_type(v) -> packed type name of v, 0 for 0
	r.define("dbg_type", p1, ri, func(f *Func) {
		v := f.Param(0)
		f.Add(If(eqz(get(v)), Return(I64c(0))), r.packStr(r.TypeName(get(v))))
	})
}

// packStr yields a string as a packed (ptr | len<<32) pair.
func (r *RT) packStr(s Expr) Expr {
	t := s
	return Raw(func() Expr {
		return I64Or(I64ExtendI32U(sbytes(t)), I64Shl(I64ExtendI32U(slen(t)), I64c(32)))
	})
}

// dbgAreaOf yields the locals area of frame i.
func (r *RT) dbgAreaOf(i Expr) Expr {
	return Raw(func() Expr { return add(GGet(r.DbgAreas), mul(i, c32(4*r.dbgMaxLocals))) })
}

// dbgBreak yields the address of the breakpoint flag of position id pos.
func (r *RT) dbgBreak(pos Expr) Expr {
	return Raw(func() Expr { return add(addr(r.dbgBreaks), pos) })
}

// DebugDescriptor creates the static descriptor of a function's local
// variables: their number, then a (name, cell) pair per variable.
func (r *RT) DebugDescriptor(names []string, cells []bool) uint32 {
	d := r.M.Static(4+8*len(names), 8) // 8-aligned: see LoadEnter
	r.M.PutU32(d, uint32(len(names)))
	for i, n := range names {
		r.M.PutU32(d+4+uint32(8*i), r.Str(n))
		if cells[i] {
			r.M.PutU32(d+8+uint32(8*i), 1)
		}
	}
	r.dbgMaxLocals = max(r.dbgMaxLocals, len(names))
	return d
}

// DebugFunction sets the descriptor of the function with the given spec.
func (r *RT) DebugFunction(spec, desc uint32) {
	if r.Cfg.Debug {
		r.M.PutU32(spec+offSpecDbg, desc)
	}
}

// DebugToplevel sets the descriptor of the program's top level.
func (r *RT) DebugToplevel(desc uint32) { r.dbgTop = desc }

// DebugStatement yields the hook at the start of a statement at position
// id pos, in a function whose local variables are locals; area is a
// scratch local of the function.
func (r *RT) DebugStatement(pos uint32, area Local, locals []Expr) Expr {
	if !r.Cfg.Debug {
		return Nop()
	}
	r.dbgStmts = append(r.dbgStmts, pos)
	spill := Seq{}
	if len(locals) > 0 {
		spill = append(spill, Set(area, r.Call("dbg_area")))
		for i, l := range locals {
			spill = append(spill, st(get(area), uint32(4*i), l))
		}
	}
	return If(GGet(r.DbgMode), Seq{spill, r.Call("dbg_stmt", U32c(pos))})
}

// DebugStatements lists the position ids of the statements.
func (r *RT) DebugStatements() []uint32 { return r.dbgStmts }

// layoutDebug emits the table of breakpoints.
func (r *RT) layoutDebug() {
	if !r.Cfg.Debug {
		return
	}
	r.dbgBreaks = r.M.Static(len(r.posList)+1, 1)
	if r.dbgTop == 0 {
		r.dbgTop = r.DebugDescriptor(nil, nil)
	}
}

// initDebugAreas yields the code that allocates the locals areas.
func (r *RT) initDebugAreas() Expr {
	if !r.Cfg.Debug {
		return Nop()
	}
	return GSet(r.DbgAreas, r.Call("alloc_free", Raw(func() Expr {
		return c32(4 * max(r.dbgMaxLocals, 1) * r.frameSlots())
	})))
}

// StrPacked returns the packed (ptr | len<<32) pair of the static string s.
func (r *RT) StrPacked(s string) int64 {
	return int64(r.Str(s)+offStrBytes) | int64(len(s))<<32
}

func (r *RT) registerDebugError() {
	// dbg_error() -> packed message of the error that failed the last
	// read of a value, 0 if none
	r.define("dbg_error", p0, ri, func(f *Func) {
		f.Add(If(eqz(GGet(r.ErrObj)), Return(I64c(0))), r.packStr(GGet(r.ErrObj)))
	})
}
