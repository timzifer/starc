package rt

import (
	"encoding/binary"

	. "github.com/timzifer/starc/internal/wasm"
)

// Host imports. Every host function is imported from HostModule under
// its dotted name with the signature (args ptr, args len) -> i32. The
// result is n >= 0 for an encoded result of n bytes, or ^n for an error
// message of n bytes; the guest then reserves n bytes and calls
// HostResult(dst) to have the host copy the pending bytes there.
const (
	HostModule = "starc_host"
	HostResult = "starc_result" // (dst i32) -> ()
	HostPrint  = "starc_print"  // (ptr i32, len i32) -> (), without WASI
)

// Member is a named member of a module object.
type Member struct {
	Name  string
	Value uint32 // address of a static object
}

// Module returns the address of a static, frozen module object with the
// given members. Members are looked up by attribute ID, so their names
// are interned as method names.
func (r *RT) Module(name string, members []Member) uint32 {
	r.modules = true
	tab := r.M.Static(8*len(members)+4, 4)
	for i, mb := range members {
		r.M.PutU32(tab+uint32(8*i), uint32(r.MethodID(mb.Name)))
		r.M.PutU32(tab+uint32(8*i+4), mb.Value)
	}
	a := r.M.Static(sizeMod, 8)
	r.M.PutU32(a, TModule|1<<8)
	r.M.PutU32(a+offModName, r.Str(name))
	r.M.PutU32(a+offModLen, uint32(len(members)))
	r.M.PutU32(a+offModMembers, tab)
	return a
}

// HasModules reports whether any module object has been created.
func (r *RT) HasModules() bool { return r.modules }

// HostBuiltin returns a static builtin object that calls the host
// function with the given dotted name.
func (r *RT) HostBuiltin(name string, cost int64) uint32 {
	im := r.M.AddImport(&Import{Module: HostModule, Name: name, Type: FuncType{Params: p2, Results: r1}})
	r.hostFuncs = append(r.hostFuncs, name)
	f := r.M.AddFunc(NewFunc("host."+name, CallType.Params, CallType.Results...))
	p := f.Local(I32)
	n := f.Local(I32)
	f.Add(
		r.ChargeN(FuelCall+cost),
		Set(p, r.Call("host_args", get(f.Param(0)), get(f.Param(1)))),
		Set(n, Call(im, get(p), sub(GGet(r.Cursor), get(p)))),
	)
	if r.Cfg.Fuel {
		// The host may have charged fuel during the call.
		f.Add(r.Charge(I64c(0)))
	}
	f.Add(r.Call("host_result", get(n)))
	display := name
	if i := lastDot(name); i >= 0 {
		display = name[i+1:]
	}
	a := r.Builtin(display, f)
	if r.Cfg.Profile {
		r.profEntries[binary.LittleEndian.Uint32(r.M.StaticSlice(a+offBiProf, 4))-1] = ProfileEntry{Name: name, Kind: ProfHost}
	}
	return a
}

// HostFuncs lists the host functions imported by the module.
func (r *RT) HostFuncs() []string { return r.hostFuncs }

func (r *RT) registerHost() {
	// host_args(args, kw) -> ptr of the encoded call arguments; the
	// cursor is left at their end.
	r.define("host_args", p2, r1, func(f *Func) {
		args, kw := f.Param(0), f.Param(1)
		n, p := f.Local(I32), f.Local(I32)
		f.Add(
			If(and(ne(get(kw), c32(0)), eqz(ld(get(kw), offDictLen))), Set(kw, c32(0))),
			Set(n, r.Call("enc_size", get(args))),
			If(get(kw), Set(n, r.Call("size_add", get(n), r.Call("enc_size", get(kw))))),
			Set(p, r.Call("alloc", get(n))),
			GSet(r.Cursor, get(p)),
			r.Call("encode", get(args)),
			If(get(kw), r.Call("encode", get(kw))),
			get(p),
		)
	})
	// host_result(n) -> value: fetches and decodes the pending result of
	// a host call, or raises the pending error.
	r.define("host_result", p1, r1, func(f *Func) {
		n := f.Param(0)
		p := f.Local(I32)
		f.Add(
			If(lt(get(n), c32(0)), Seq{
				Set(p, r.Call("str_new", I32Xor(get(n), c32(-1)))),
				Call(r.hostRes, add(get(p), c32(offStrBytes))),
				r.Call("fail", get(p)),
				Unreachable(),
			}),
			Set(p, r.Call("alloc", get(n))),
			Call(r.hostRes, get(p)),
			r.Call("decode_buf", get(p), get(n)),
		)
	})
	// call_method(fn, args, kw): a method call whose receiver args[0] may
	// be a module or a struct, whose members are called without the
	// receiver.
	r.define("call_method", p3, r1, func(f *Func) {
		fn, args, kw := f.Param(0), f.Param(1), f.Param(2)
		t := f.Local(I32)
		f.Add(
			Set(t, tagOf(ld(get(args), offTupElems))),
			If(or(eq(get(t), c32(TModule)), eq(get(t), c32(TStruct))), Seq{
				Set(t, r.Call("tuple_new", sub(ld(get(args), offTupLen), c32(1)))),
				r.memcopy(add(get(t), c32(offTupElems)), add(get(args), c32(offTupElems+4)),
					I32Shl(ld(get(t), offTupLen), c32(2))),
				Return(r.Call("call", get(fn), get(t), get(kw))),
			}),
			r.Call("call", get(fn), get(args), get(kw)),
		)
	})
	// module_member(m, id) -> member value or 0
	r.define("module_member", p2, r1, func(f *Func) {
		m, id := f.Param(0), f.Param(1)
		i, e := f.Local(I32), f.Local(I32)
		f.Add(
			r.forRange(i, c32(0), ld(get(m), offModLen),
				Set(e, add(ld(get(m), offModMembers), I32Shl(get(i), c32(3)))),
				If(eq(ld(get(e), 0), get(id)), Return(ld(get(e), 4))),
			),
			c32(0),
		)
	})
}
