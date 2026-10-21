package rt

import (
	. "github.com/timzifer/starc/internal/wasm"
)

// Hard limits. They make a module fail in the same, defined way on every
// platform instead of running into the limits of the WebAssembly engine:
// a Starlark error with a code (starc_error_code) rather than a trap.

// Defaults of the limits.
const (
	// DefaultMaxCallDepth is small enough that the deepest permitted call
	// chain fits into the call stack of every common engine, including
	// wazero's interpreter (2000 frames).
	DefaultMaxCallDepth = 500
	// MaxMemoryPages is the largest memory a module may declare: one page
	// short of 4 GiB, so that byte sizes of the memory fit into an i32.
	MaxMemoryPages = 65535
)

// Error codes of the limits, continuing ErrStarlark and ErrFuel.
const (
	ErrMemory = 3 // out of memory
	ErrDepth  = 4 // maximum call depth exceeded
	ErrSize   = 5 // argument or result too large
)

// Limits describes the limits for starc.meta.
type Limits struct {
	MemoryPages int `json:"memory_pages"`
	CallDepth   int `json:"call_depth"`
	InputBytes  int `json:"input_bytes,omitempty"`
	ResultBytes int `json:"result_bytes,omitempty"`
}

func (r *RT) initLimits() {
	r.Depth = r.M.AddGlobal(&Global{Name: "depth", Type: I32, Mutable: true})

	// fail_code(msg, code): raises an error with the given code.
	r.define("fail_code", p2, r0, func(f *Func) {
		f.Add(GSet(r.ErrCode, get(f.Param(1))), r.Call("fail", get(f.Param(0))), Unreachable())
	})
	// depth_out(): the call depth limit is exceeded.
	r.define("depth_out", p0, r0, func(f *Func) {
		f.Add(r.Call("fail_code", r.S("maximum call depth exceeded"), c32(ErrDepth)), Unreachable())
	})
	r.define("out_of_memory", p0, r0, func(f *Func) {
		f.Add(r.Call("fail_code", r.S("out of memory"), c32(ErrMemory)), Unreachable())
	})
}

// Enter yields code that enters a call: it fails if the maximum call
// depth is exceeded. Every Enter is paired with a Leave on return; an
// error resets the depth.
func (r *RT) Enter() Expr {
	return Seq{
		GSet(r.Depth, add(GGet(r.Depth), c32(1))),
		If(I32GtU(GGet(r.Depth), c32(r.Cfg.MaxCallDepth)), r.Call("depth_out")),
	}
}

// LeaveCall yields code that leaves a call entered with Enter.
func (r *RT) LeaveCall() Expr { return GSet(r.Depth, sub(GGet(r.Depth), c32(1))) }

// maxCount bounds the number of elements of a container, so that their
// byte sizes cannot wrap around in i32 arithmetic. Such a container
// would not fit into memory anyway.
const maxCount = 1 << 28

// checkCount yields code that fails with "out of memory" if the element
// count n (treated as unsigned) exceeds maxCount.
func (r *RT) checkCount(n Expr) Expr {
	return If(I32GtU(n, c32(maxCount)), r.Call("out_of_memory"))
}

// checkSize yields code that fails with msg (code ErrSize) if the size n
// exceeds limit; it is empty for limit 0.
func (r *RT) checkSize(n Expr, limit uint32, msg string) Expr {
	if limit == 0 {
		return Nop()
	}
	return If(I32GtU(n, U32c(limit)), r.Call("fail_code", r.Msg(msg+": ", r.Call("i64_to_str", I64ExtendI32U(n)),
		" bytes, limit "+itoa(int(limit))), c32(ErrSize)))
}

// defineDeep defines a recursive runtime function whose every level
// counts toward the call depth, so that deeply nested values fail like
// deep calls instead of exhausting the engine's stack.
func (r *RT) defineDeep(name string, params, results []ValType, build func(f *Func)) {
	r.define(name+".body", params, results, build)
	r.define(name, params, results, func(f *Func) {
		args := make([]Expr, len(params))
		for i := range params {
			args[i] = get(f.Param(i))
		}
		f.Add(r.Enter())
		if len(results) == 0 {
			f.Add(r.Call(name+".body", args...), r.LeaveCall())
			return
		}
		v := f.Local(results[0])
		f.Add(Set(v, r.Call(name+".body", args...)), r.LeaveCall(), get(v))
	})
}
