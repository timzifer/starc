package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

// Fuel profile. With Config.Profile, the fuel that a call consumes is
// attributed to the function that consumes it: a static table holds, for
// every profiled function, the fuel used by its own code (not by the
// functions it calls) and the number of its calls.
//
// Each function spec and built-in object refers to its entry. When a call
// switches the function, the fuel consumed since the last switch is added
// to the current entry (flush). The entries follow the frames of
// backtraces: an internal prelude helper and what the prelude calls for
// itself count toward the prelude function, so that a built-in like
// sorted appears as one entry; a callable of the program that the prelude
// calls (a key function) has its own.
//
// Profiling never charges fuel, so a profiled module uses exactly the
// same fuel as one without profile.

const (
	offSpecProf = 28 // 1-based profile entry of a spec, 0 if not profiled
	offBiProf   = 12 // 1-based profile entry of a built-in
	sizeProf    = 16 // entry: i64 fuel, i64 calls
)

// ProfileKind classifies a profile entry.
type ProfileKind string

const (
	ProfToplevel ProfileKind = "toplevel" // the top-level code of the program
	ProfFunction ProfileKind = "function" // a function of the program
	ProfBuiltin  ProfileKind = "builtin"  // a built-in, including those of the prelude
	ProfHost     ProfileKind = "host"     // a host function
)

// ProfileEntry describes a profiled function.
type ProfileEntry struct {
	Name string
	Kind ProfileKind
	// File, Line and Col locate the def of a function of the program.
	File      string
	Line, Col int
	// Wasm is the name of the WebAssembly function that implements a
	// function of the program, as in the name section.
	Wasm string
}

func (r *RT) initProfile() {
	if !r.Cfg.Profile {
		return
	}
	r.ProfCur = r.M.AddGlobal(&Global{Name: "prof_cur", Type: I32, Mutable: true})
	r.ProfLast = r.M.AddGlobal(&Global{Name: "prof_last", Type: I64, Mutable: true})
	r.ProfPrelude = r.M.AddGlobal(&Global{Name: "prof_prelude", Type: I32, Mutable: true})
	r.profEntries = []ProfileEntry{{Name: "<toplevel>", Kind: ProfToplevel}}

	// prof_flush(): adds the fuel used since the last flush to the
	// current entry. Between calls (entry 0), the fuel is not attributed.
	r.define("prof_flush", p0, r0, func(f *Func) {
		e := f.Local(I32)
		f.Add(
			If(GGet(r.ProfCur), Seq{
				Set(e, r.profEntry(GGet(r.ProfCur))),
				I64Store(get(e), 0, I64Add(I64Load(get(e), 0), I64Sub(GGet(r.ProfLast), GGet(r.Fuel)))),
			}),
			GSet(r.ProfLast, GGet(r.Fuel)),
		)
	})
	// prof_start(entry): starts attributing fuel to entry, for the top
	// level or an exported call.
	r.define("prof_start", p1, r0, func(f *Func) {
		f.Add(
			r.Call("prof_flush"),
			GSet(r.ProfCur, get(f.Param(0))),
			GSet(r.ProfPrelude, c32(0)),
		)
	})
	// prof_end(): ends a call (or the top level), also when it fails.
	r.define("prof_end", p0, r0, func(f *Func) {
		f.Add(r.Call("prof_flush"), GSet(r.ProfCur, c32(0)))
	})
	// prof_count() -> number of entries
	r.define("prof_count", p0, r1, func(f *Func) {
		f.Add(c32(len(r.profEntries)))
	})
	// prof_fuel(i) and prof_calls(i) -> the counters of entry i (0-based)
	for k, name := range []string{"prof_fuel", "prof_calls"} {
		off := uint32(8 * k)
		r.define(name, p1, ri, func(f *Func) {
			i := f.Param(0)
			f.Add(IfV(I64, I32LtU(get(i), c32(len(r.profEntries))),
				I64Load(r.profEntry(add(get(i), c32(1))), off), I64c(0)))
		})
	}
	// prof_reset(): clears all entries
	r.define("prof_reset", p0, r0, func(f *Func) {
		f.Add(
			r.Call("prof_flush"),
			MemoryFill(r.profEntry(c32(1)), c32(0), c32(sizeProf*len(r.profEntries))),
		)
	})
}

// profEntry yields the address of the 1-based entry e.
func (r *RT) profEntry(e Expr) Expr {
	return Raw(func() Expr { return add(addr(r.profTab), mul(sub(e, c32(1)), c32(sizeProf))) })
}

// profAdd registers a profile entry and returns its 1-based index.
func (r *RT) profAdd(e ProfileEntry) uint32 {
	r.profEntries = append(r.profEntries, e)
	return uint32(len(r.profEntries))
}

// ProfileFunction profiles calls of the function with the given spec
// under e.
func (r *RT) ProfileFunction(spec uint32, e ProfileEntry) {
	if r.Cfg.Profile {
		r.M.PutU32(spec+offSpecProf, r.profAdd(e))
	}
}

// ProfileEntries lists the entries of the profile in table order.
func (r *RT) ProfileEntries() []ProfileEntry { return r.profEntries }

// profileBuiltin profiles calls of the built-in object a.
func (r *RT) profileBuiltin(a uint32, name string, kind ProfileKind) {
	if r.Cfg.Profile {
		r.M.PutU32(a+offBiProf, r.profAdd(ProfileEntry{Name: name, Kind: kind}))
	}
}

// layoutProfile emits the table of entries.
func (r *RT) layoutProfile() {
	if r.Cfg.Profile {
		r.profTab = r.M.Static(sizeProf*len(r.profEntries), 8)
	}
}

// ProfileStart yields code that attributes the following fuel to the
// entry of fn (the top level if fn is 0).
func (r *RT) ProfileStart(fn Expr) Expr {
	if !r.Cfg.Profile {
		return Nop()
	}
	return r.Call("prof_start", IfV(I32, fn, r.profIndex(fn), c32(1)))
}

// ProfileEnd yields code that attributes the fuel used so far and ends
// the call.
func (r *RT) ProfileEnd() Expr {
	if !r.Cfg.Profile {
		return Nop()
	}
	return r.Call("prof_end")
}

// profIndex yields the entry of the function or built-in fn, 0 if none.
// fn must be free of side effects.
func (r *RT) profIndex(fn Expr) Expr {
	return IfV(I32, isTag(fn, TFunction), ld(ld(fn, offFnSpec), offSpecProf),
		IfV(I32, isTag(fn, TBuiltin), ld(fn, offBiProf), c32(0)))
}

// profCall wraps call, a call of fn with its frame pushed (see callFrame),
// so that the fuel of the callee is attributed to it.
func (r *RT) profCall(f *Func, call Expr, cb bool) Expr {
	if !r.Cfg.Profile {
		return call
	}
	fn := f.Param(0)
	idx, prelude, sw, saved, res := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
	inPrelude := GGet(r.ProfPrelude)
	if cb {
		inPrelude = c32(0)
	}
	return Seq{
		// What the prelude calls for itself counts toward the prelude.
		Set(idx, IfV(I32, inPrelude, c32(0), r.profIndex(get(fn)))),
		Set(prelude, IfV(I32, isTag(get(fn), TFunction),
			ne(and(ld(ld(get(fn), offFnSpec), offSpecFlags), c32(SpecPrelude)), c32(0)), c32(0))),
		// A call switches the entry, or enters the prelude from the
		// program: an internal helper, such as % formatting, counts
		// toward its caller together with what it calls.
		Set(sw, or(ne(get(idx), c32(0)), and(get(prelude), eqz(inPrelude)))),
		If(get(sw), Seq{
			r.Call("prof_flush"),
			Set(saved, or(GGet(r.ProfCur), I32Shl(GGet(r.ProfPrelude), c32(31)))),
			GSet(r.ProfPrelude, get(prelude)),
		}),
		If(get(idx), Seq{
			GSet(r.ProfCur, get(idx)),
			I64Store(r.profEntry(get(idx)), 8, I64Add(I64Load(r.profEntry(get(idx)), 8), I64c(1))),
		}),
		Set(res, call),
		If(get(sw), Seq{
			r.Call("prof_flush"),
			GSet(r.ProfCur, and(get(saved), c32(1<<31-1))),
			GSet(r.ProfPrelude, I32ShrU(get(saved), c32(31))),
		}),
		get(res),
	}
}
