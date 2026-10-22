package rt

import (
	"math"

	. "github.com/timzifer/starc/internal/wasm"
)

// Fuel metering. With Config.Fuel, the global Fuel holds the remaining
// budget; every unit of work subtracts its cost, and the program fails
// with "fuel exhausted" (error code ErrFuel) when the budget drops below
// zero. Costs follow a fixed table (FuelTable), so consumption depends
// only on the program and its inputs, never on the platform.

// FuelVersion identifies the cost table. It changes whenever a change
// of the table, the compiler or the runtime changes fuel consumption.
const FuelVersion = 1

// Costs of the fuel table.
const (
	FuelStatement = 1              // per Starlark statement
	FuelCall      = 1              // per call of a Starlark function
	FuelIteration = 1              // per iteration of a Starlark loop or comprehension clause
	FuelLoop      = 1              // per iteration of a loop in the runtime library
	FuelBytes     = 1 << fuelShift // allocating or copying memory costs 1 per started FuelBytes bytes

	fuelShift = 4
)

// Error codes reported by starc_error_code.
const (
	ErrNone     = 0
	ErrStarlark = 1
	ErrFuel     = 2
)

// FuelTable describes the cost table for starc.meta.
type FuelTable struct {
	Version   int              `json:"version"`
	Statement int              `json:"statement"`
	Call      int              `json:"call"`
	Iteration int              `json:"iteration"`
	Loop      int              `json:"runtime_iteration"`
	Bytes     int              `json:"bytes_per_unit"`
	Host      map[string]int64 `json:"host,omitempty"`
}

// Table returns the cost table; host lists the declared costs of host
// functions.
func Table(host map[string]int64) FuelTable {
	return FuelTable{FuelVersion, FuelStatement, FuelCall, FuelIteration, FuelLoop, FuelBytes, host}
}

func (r *RT) initFuel() {
	if r.Cfg.Fuel {
		r.Fuel = r.M.AddGlobal(&Global{Name: "fuel", Type: I64, Mutable: true, Init: math.MaxInt64})
	}
	r.ErrCode = r.M.AddGlobal(&Global{Name: "err_code", Type: I32, Mutable: true})

	// fuel_out(): the budget is exhausted.
	r.define("fuel_out", p0, r0, func(f *Func) {
		f.Add(
			GSet(r.Fuel, I64c(0)),
			GSet(r.ErrCode, c32(ErrFuel)),
			r.Call("fail", r.S("fuel exhausted")),
			Unreachable(),
		)
	})
	// fuel_set(n): sets the remaining budget.
	r.define("fuel_set", pi, r0, func(f *Func) {
		if r.Cfg.Profile {
			// Lowering the budget during a call is a charge by a host
			// function, which the profile attributes to it; otherwise
			// the host sets a new budget.
			f.Add(If(or(eqz(GGet(r.ProfCur)), I64GtS(get(f.Param(0)), GGet(r.Fuel))), Seq{
				r.Call("prof_flush"),
				GSet(r.ProfLast, get(f.Param(0))),
			}))
		}
		f.Add(GSet(r.Fuel, get(f.Param(0))))
	})
	// fuel_get() -> remaining budget
	r.define("fuel_get", p0, ri, func(f *Func) {
		f.Add(GGet(r.Fuel))
	})
	// error_code() -> code of the last error
	r.define("error_code", p0, r1, func(f *Func) {
		f.Add(GGet(r.ErrCode))
	})
}

// Charge yields code that charges cost (an i64 expression) and fails if
// the budget is exhausted. It is empty without metering.
func (r *RT) Charge(cost Expr) Expr {
	if !r.Cfg.Fuel {
		return Nop()
	}
	return Seq{
		GSet(r.Fuel, I64Sub(GGet(r.Fuel), cost)),
		If(I64LtS(GGet(r.Fuel), I64c(0)), r.Call("fuel_out")),
	}
}

// ChargeN charges a constant cost.
func (r *RT) ChargeN(n int64) Expr {
	if n == 0 {
		return Nop()
	}
	return r.Charge(I64c(n))
}

// chargeBytes charges for touching n bytes (an i32 expression, treated
// as unsigned).
func (r *RT) chargeBytes(n Expr) Expr {
	if !r.Cfg.Fuel {
		return Nop()
	}
	return r.Charge(I64ShrU(I64Add(I64ExtendI32U(n), I64c(FuelBytes-1)), I64c(fuelShift)))
}

// loop is a WebAssembly loop whose every iteration costs FuelLoop.
func (r *RT) loop(l *Label, body ...Expr) Expr {
	if !r.Cfg.Fuel {
		return Loop(l, body...)
	}
	return Loop(l, append([]Expr{r.ChargeN(FuelLoop)}, body...)...)
}

// memcopy is memory.copy, charged by size. n must be free of side effects.
func (r *RT) memcopy(dst, src, n Expr) Expr {
	if !r.Cfg.Fuel {
		return MemoryCopy(dst, src, n)
	}
	return Seq{r.chargeBytes(n), MemoryCopy(dst, src, n)}
}
