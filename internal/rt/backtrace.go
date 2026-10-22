package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

// Source positions and backtraces of run-time errors.
//
// The compiled code stores the position of every operation that can fail
// in the global CurPos before performing it: an index into a static table
// of (line, col) pairs. A call pushes the callee and the caller's CurPos
// onto a shadow stack and restores CurPos on return. When an error
// occurs, fail turns the shadow stack in place into the frames that
// go.starlark.net would report, outermost first: (name string, position)
// pairs, where the position of a built-in has the bit btBuiltin set.
//
// The frames are hidden as in go.starlark.net, where the prelude is
// written in Go: a prelude function appears as one built-in frame (or not
// at all if it is an internal helper), and what it calls is hidden unless
// it is a Starlark function of the program, such as a key function.
//
// The code in this file runs while an error is raised. It must neither
// allocate nor charge fuel, which may have failed in the first place.

// btBuiltin marks the position of a built-in frame.
const btBuiltin = 1 << 31

// btCallback marks the frame of a callable that the prelude calls on
// behalf of the program (bit 0 of the callable, whose objects are
// 8-aligned).
const btCallback = 1

// Frame kinds while collecting.
const (
	btUser    = 0 // a function of the program, or its top level
	btNative  = 1 // a built-in implemented by the runtime
	btPrelude = 2 // a built-in implemented in the prelude
	btHidden  = 3 // an internal prelude helper
)

// frameSlots is the size of the shadow stack in frames: every Starlark
// call counts toward the call depth, and at most one built-in frame lies
// between two of them.
func (r *RT) frameSlots() int { return 2*r.Cfg.MaxCallDepth + 16 }

func (r *RT) initBacktrace() {
	r.CurPos = r.M.AddGlobal(&Global{Name: "cur_pos", Type: I32, Mutable: true})
	if !r.Cfg.Backtrace {
		return
	}
	r.SP = r.M.AddGlobal(&Global{Name: "frames_top", Type: I32, Mutable: true})
	r.StackBase = r.M.AddGlobal(&Global{Name: "frames_base", Type: I32, Mutable: true})
	r.StackLimit = r.M.AddGlobal(&Global{Name: "frames_limit", Type: I32, Mutable: true})
	r.ErrFrames = r.M.AddGlobal(&Global{Name: "err_frames", Type: I32, Mutable: true})
	r.DupKw = r.M.AddGlobal(&Global{Name: "dup_kw", Type: I32, Mutable: true})
	// take_dup_kw() -> the keyword that kwargs_merge found twice; it is
	// cleared.
	r.define("take_dup_kw", p0, r1, func(f *Func) {
		k := f.Local(I32)
		f.Add(Set(k, GGet(r.DupKw)), GSet(r.DupKw, c32(0)), get(k))
	})
	r.posIDs = map[[2]int]uint32{}
	r.digits = r.M.Static(offStrBytes+16, 8)
	r.M.PutU32(r.digits, TString|1<<8)

	// bt_collect(): converts the shadow stack into the error's frames.
	r.define("bt_collect", p0, r0, func(f *Func) {
		p, out, fn, kind, prev, name, spec, cb := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32),
			f.Local(I32), f.Local(I32), f.Local(I32)
		brk, top := NewLabel(), NewLabel()
		f.Add(
			Set(p, GGet(r.StackBase)),
			Set(out, GGet(r.StackBase)),
			Set(prev, c32(btUser)),
			Block(brk, Loop(top,
				BrIf(brk, I32GeU(get(p), GGet(r.SP))),
				Set(fn, ld(get(p), 0)),
				Set(cb, and(get(fn), c32(btCallback))),
				Set(fn, and(get(fn), c32(^btCallback))),
				Set(kind, c32(btUser)),
				Set(name, r.S("<toplevel>")),
				If(get(fn), Seq{
					If(isTag(get(fn), TBuiltin), Seq{Set(kind, c32(btNative)), Set(name, ld(get(fn), offBiName))}),
					If(isTag(get(fn), TFunction), Seq{
						Set(spec, ld(get(fn), offFnSpec)),
						Set(name, ld(get(spec), offSpecName)),
						If(and(ld(get(spec), offSpecFlags), c32(SpecPrelude)), Seq{
							Set(name, ld(get(spec), offSpecShow)),
							Set(kind, IfV(I32, get(name), c32(btPrelude), c32(btHidden))),
						}),
					}),
				}),
				// A frame is shown unless it is an internal helper or a
				// built-in called by the prelude for itself.
				If(and(ne(get(kind), c32(btHidden)),
					or(or(eq(get(kind), c32(btUser)), lt(get(prev), c32(btPrelude))), get(cb))), Seq{
					st(get(out), 0, get(name)),
					// The position of a frame is the call site of the next
					// frame, or the current position for the innermost one.
					st(get(out), 4, IfV(I32, eq(get(kind), c32(btUser)),
						IfV(I32, I32LtU(add(get(p), c32(8)), GGet(r.SP)), ld(get(p), 12), GGet(r.CurPos)),
						c32(btBuiltin))),
					Set(out, add(get(out), c32(8))),
				}),
				Set(prev, get(kind)),
				Set(p, add(get(p), c32(8))),
				Br(top),
			)),
			GSet(r.ErrFrames, I32ShrU(sub(get(out), GGet(r.StackBase)), c32(3))),
			GSet(r.SP, GGet(r.StackBase)),
		)
	})
	// frame_at(i) -> address of the error's frame i, or 0
	r.define("frame_at", p1, r1, func(f *Func) {
		f.Add(IfV(I32, I32LtU(get(f.Param(0)), GGet(r.ErrFrames)),
			add(GGet(r.StackBase), I32Shl(get(f.Param(0)), c32(3))), c32(0)))
	})
	// frame_name(i) -> packed (ptr | len<<32) name of frame i
	r.define("frame_name", p1, ri, func(f *Func) {
		e, s := f.Local(I32), f.Local(I32)
		f.Add(
			Set(e, r.Call("frame_at", get(f.Param(0)))),
			If(eqz(get(e)), Return(I64c(0))),
			Set(s, ld(get(e), 0)),
			I64Or(I64ExtendI32U(sbytes(get(s))), I64Shl(I64ExtendI32U(slen(get(s))), I64c(32))),
		)
	})
	// frame_pos(i) -> packed (line | col<<32) of frame i, -1 for a
	// built-in (0 for a position that is not known)
	r.define("frame_pos", p1, ri, func(f *Func) {
		e, id := f.Local(I32), f.Local(I32)
		f.Add(
			Set(e, r.Call("frame_at", get(f.Param(0)))),
			If(eqz(get(e)), Return(I64c(0))),
			Set(id, ld(get(e), 4)),
			If(and(get(id), c32(btBuiltin)), Return(I64c(-1))),
			I64Load(r.posEntry(get(id)), 0),
		)
	})
	if !r.Cfg.Command {
		return
	}
	// write_u32(fd, n): writes n in decimal.
	r.define("write_u32", p2, r0, func(f *Func) {
		n, i := f.Local(I32), f.Local(I32)
		brk, top := NewLabel(), NewLabel()
		buf := addr(r.digits + offStrBytes)
		f.Add(
			Set(n, get(f.Param(1))),
			Set(i, c32(16)),
			Block(brk, Loop(top,
				Set(i, sub(get(i), c32(1))),
				I32Store8(add(buf, get(i)), 0, add(I32RemU(get(n), c32(10)), c32('0'))),
				Set(n, I32DivU(get(n), c32(10))),
				BrIf(top, get(n)),
			)),
			MemoryCopy(buf, add(buf, get(i)), sub(c32(16), get(i))),
			st(addr(r.digits), offStrLen, sub(c32(16), get(i))),
			Drop(r.Call("write", get(f.Param(0)), addr(r.digits))),
		)
	})
	// write_pos(fd, id): writes a position as go.starlark.net does.
	r.define("write_pos", p2, r0, func(f *Func) {
		fd, id := f.Param(0), f.Param(1)
		e := f.Local(I32)
		f.Add(
			If(and(get(id), c32(btBuiltin)), Seq{Drop(r.Call("write", get(fd), r.S("<builtin>"))), Return(nil)}),
			Drop(r.Call("write", get(fd), r.S(r.Cfg.Filename))),
			Set(e, r.posEntry(get(id))),
			If(eqz(ld(get(e), 0)), Return(nil)),
			Drop(r.Call("write", get(fd), r.S(":"))),
			r.Call("write_u32", get(fd), ld(get(e), 0)),
			If(eqz(ld(get(e), 4)), Return(nil)),
			Drop(r.Call("write", get(fd), r.S(":"))),
			r.Call("write_u32", get(fd), ld(get(e), 4)),
		)
	})
	// write_backtrace(fd): writes the frames of the error and "Error: "
	// or "Error in f: ", as EvalError.Backtrace does.
	r.define("write_backtrace", p1, r0, func(f *Func) {
		fd := f.Param(0)
		n, last, i, e := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		brk, top := NewLabel(), NewLabel()
		f.Add(
			Set(n, GGet(r.ErrFrames)),
			Set(last, r.Call("frame_at", sub(get(n), c32(1)))),
			// A built-in innermost frame becomes part of the last line.
			If(and(ne(get(last), c32(0)), ne(and(ld(get(last), 4), c32(btBuiltin)), c32(0))), Set(n, sub(get(n), c32(1)))),
			If(get(n), Drop(r.Call("write", get(fd), r.S("Traceback (most recent call last):\n")))),
			Set(i, c32(0)),
			Block(brk, Loop(top,
				BrIf(brk, I32GeU(get(i), get(n))),
				Set(e, r.Call("frame_at", get(i))),
				Drop(r.Call("write", get(fd), r.S("  "))),
				r.Call("write_pos", get(fd), ld(get(e), 4)),
				Drop(r.Call("write", get(fd), r.S(": in "))),
				Drop(r.Call("write", get(fd), ld(get(e), 0))),
				Drop(r.Call("write", get(fd), r.S("\n"))),
				Set(i, add(get(i), c32(1))),
				Br(top),
			)),
			Drop(r.Call("write", get(fd), r.S("Error"))),
			If(ne(get(n), GGet(r.ErrFrames)), Seq{
				Drop(r.Call("write", get(fd), r.S(" in "))),
				Drop(r.Call("write", get(fd), ld(get(last), 0))),
			}),
			Drop(r.Call("write", get(fd), r.S(": "))),
		)
	})
}

// Pos returns the position id of a source location; it is 0 without
// backtraces.
func (r *RT) Pos(line, col int) uint32 {
	if !r.Cfg.Backtrace {
		return 0
	}
	k := [2]int{line, col}
	if id, ok := r.posIDs[k]; ok {
		return id
	}
	r.posList = append(r.posList, k)
	id := uint32(len(r.posList))
	r.posIDs[k] = id
	return id
}

// posEntry yields the address of the (line, col) entry of position id.
func (r *RT) posEntry(id Expr) Expr {
	return Raw(func() Expr { return add(addr(r.posTab), I32Shl(id, c32(3))) })
}

// layoutPositions emits the position table; entry 0 is unknown.
func (r *RT) layoutPositions() {
	if !r.Cfg.Backtrace {
		return
	}
	r.posTab = r.M.Static(8*(len(r.posList)+1), 8)
	for i, p := range r.posList {
		r.M.PutU32(r.posTab+uint32(8*(i+1)), uint32(p[0]))
		r.M.PutU32(r.posTab+uint32(8*(i+1))+4, uint32(p[1]))
	}
}

// initFrames yields the code that allocates the shadow stack.
func (r *RT) initFrames() Expr {
	if !r.Cfg.Backtrace {
		return Nop()
	}
	return Seq{
		GSet(r.StackBase, r.Call("alloc_free", c32(8*r.frameSlots()))),
		GSet(r.SP, GGet(r.StackBase)),
		GSet(r.StackLimit, add(GGet(r.StackBase), c32(8*r.frameSlots()))),
	}
}

// SetPos yields code that records the position id of the next operation
// that can fail.
func (r *RT) SetPos(id uint32) Expr {
	if !r.Cfg.Backtrace {
		return Nop()
	}
	return GSet(r.CurPos, U32c(id))
}

// PushToplevel and PopFrame yield code that enters and leaves the frame
// of the program's top level.
func (r *RT) PushToplevel() Expr {
	if !r.Cfg.Backtrace {
		return Nop()
	}
	return Seq{st(GGet(r.SP), 0, c32(0)), st(GGet(r.SP), 4, GGet(r.CurPos)), GSet(r.SP, add(GGet(r.SP), c32(8)))}
}

func (r *RT) PopFrame() Expr {
	if !r.Cfg.Backtrace {
		return Nop()
	}
	return GSet(r.SP, sub(GGet(r.SP), c32(8)))
}

// ResetFrames yields code that discards the frames of the last call and
// of its error.
func (r *RT) ResetFrames() Expr {
	if !r.Cfg.Backtrace {
		return Nop()
	}
	return Seq{GSet(r.SP, GGet(r.StackBase)), GSet(r.ErrFrames, c32(0)), GSet(r.CurPos, c32(0)), GSet(r.DupKw, c32(0))}
}

// callFrame yields code that calls fn with a frame on the shadow stack;
// the frame of a callback (see call_cb) is marked by bit 0.
func (r *RT) callFrame(f *Func, call Expr, cb bool) Expr {
	fn, kw := f.Param(0), f.Param(2)
	if !r.Cfg.Backtrace {
		return Return(call)
	}
	sp, res := f.Local(I32), f.Local(I32)
	slot := get(fn)
	if cb {
		slot = or(slot, c32(btCallback))
	}
	return Seq{
		Set(sp, GGet(r.SP)),
		If(I32GeU(get(sp), GGet(r.StackLimit)), r.Call("depth_out")),
		st(get(sp), 0, slot),
		st(get(sp), 4, GGet(r.CurPos)),
		GSet(r.SP, add(get(sp), c32(8))),
		// A built-in rejects a keyword passed twice, as found by
		// kwargs_merge, within its own frame (functions do so in bind).
		If(and(ne(get(kw), c32(0)), ne(GGet(r.DupKw), c32(0))), If(isTag(get(fn), TBuiltin),
			r.Fail(ld(get(fn), offBiName), ": got multiple values for keyword argument ", r.Call("take_dup_kw")))),
		Set(res, r.profCall(f, call, cb)),
		GSet(r.SP, get(sp)),
		GSet(r.CurPos, ld(get(sp), 4)),
		Return(get(res)),
	}
}

// Positions returns the source positions (line, col) by position id,
// starting with id 1.
func (r *RT) Positions() [][2]int { return r.posList }
