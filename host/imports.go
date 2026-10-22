package host

import (
	"context"
	"fmt"
	"io"
	"math"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/timzifer/starc/abi"
)

// checkImports verifies that the module imports only what starc modules
// may import, nothing from WASI if it was built without WASI, and only
// host functions that funcs provides.
func checkImports(cm wazero.CompiledModule, meta *Meta, funcs map[string]Func) error {
	for _, d := range cm.ImportedFunctions() {
		mod, name, _ := d.Import()
		switch mod {
		case wasiModule:
			if meta.NoWASI {
				return fmt.Errorf("host: module built without WASI imports %s.%s", mod, name)
			}
		case hostModule:
			if name == hostResult || name == hostPrint || name == hostDbg {
				continue
			}
			if f, ok := funcs[name]; !ok || f == nil {
				return fmt.Errorf("host: module imports host function %q, which Config.Funcs does not provide", name)
			}
		default:
			return fmt.Errorf("host: module imports %s.%s, which is not part of the starc ABI", mod, name)
		}
	}
	return nil
}

type callerKey struct{}

// ChargeFuel charges n units of fuel to the module that called the host
// function whose context is ctx, for example for work proportional to
// the size of a query. The module fails with "fuel exhausted" when the
// call returns if its budget is used up. ChargeFuel does nothing for
// modules built without fuel metering, for n <= 0, or outside a host
// function call. It must be called before the host function returns, on
// the goroutine that runs it.
func ChargeFuel(ctx context.Context, n int64) {
	mod, _ := ctx.Value(callerKey{}).(api.Module)
	if mod == nil || n <= 0 {
		return
	}
	get, set := mod.ExportedFunction("starc_fuel"), mod.ExportedFunction("starc_fuel_set")
	if get == nil || set == nil {
		return
	}
	res, err := get.Call(ctx)
	if err != nil {
		panic(err)
	}
	rest := int64(res[0])
	if rest < math.MinInt64+n {
		rest = math.MinInt64 // repeated charges within one call
	} else {
		rest -= n
	}
	if _, err := set.Call(ctx, api.EncodeI64(rest)); err != nil {
		panic(err)
	}
}

// hostState holds the result of the last host call until the guest
// fetches it with starc_result.
type hostState struct {
	pending []byte
	valid   bool
}

// guestError is a violation of the host call protocol by the guest. It
// aborts the guest call; the host itself is unaffected.
type guestError struct{ msg string }

func (e *guestError) Error() string { return "host: " + e.msg }

func protocolError(format string, args ...any) {
	panic(&guestError{fmt.Sprintf(format, args...)})
}

// instantiateHost instantiates an anonymous starc_host module with the
// functions that cm imports, or returns nil if it imports none.
func instantiateHost(ctx context.Context, r wazero.Runtime, cm wazero.CompiledModule, cfg *Config, dbg *DebugInfo) (api.Module, error) {
	st := &hostState{}
	b := r.NewHostModuleBuilder(hostModule)
	found := false
	i32 := api.ValueTypeI32
	for _, d := range cm.ImportedFunctions() {
		mod, name, _ := d.Import()
		if mod != hostModule {
			continue
		}
		found = true
		fb := b.NewFunctionBuilder()
		switch name {
		case hostResult:
			fb = fb.WithGoModuleFunction(api.GoModuleFunc(st.result), []api.ValueType{i32}, nil)
		case hostDbg:
			fb = fb.WithGoModuleFunction(stopFunc(cfg.Debug, dbg), []api.ValueType{i32}, nil)
		case hostPrint:
			out := cfg.Stdout
			if out == nil {
				out = io.Discard
			}
			fb = fb.WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
				ptr, n := api.DecodeU32(stack[0]), api.DecodeU32(stack[1])
				msg, ok := mod.Memory().Read(ptr, n)
				if !ok {
					protocolError("print: buffer [%d, +%d) out of range", ptr, n)
				}
				_, _ = out.Write(msg)
			}), []api.ValueType{i32, i32}, nil)
		default:
			fb = fb.WithGoModuleFunction(st.call(name, cfg.Funcs[name]), []api.ValueType{i32, i32}, []api.ValueType{i32})
		}
		fb.Export(name)
	}
	if !found {
		return nil, nil
	}
	hcm, err := b.Compile(ctx)
	if err != nil {
		return nil, err
	}
	// Instances outlive their compiled module.
	defer hcm.Close(ctx)
	return r.InstantiateModule(ctx, hcm, wazero.NewModuleConfig().WithName(""))
}

// call returns the import implementing the host function fn.
func (st *hostState) call(name string, fn Func) api.GoModuleFunc {
	return func(ctx context.Context, mod api.Module, stack []uint64) {
		st.pending, st.valid = nil, false
		ptr, n := api.DecodeU32(stack[0]), api.DecodeU32(stack[1])
		in, ok := mod.Memory().Read(ptr, n)
		if !ok {
			protocolError("%s: argument buffer [%d, +%d) out of range", name, ptr, n)
		}
		args, kwargs, err := abi.DecodeCall(in)
		if err != nil {
			protocolError("%s: %v", name, err)
		}
		res, err := fn(context.WithValue(ctx, callerKey{}, mod), args, kwargs)
		var out []byte
		if err == nil {
			if out, err = abi.Encode(res); err != nil {
				err = fmt.Errorf("%s: invalid result: %w", name, err)
			}
		}
		if err == nil && len(out) > math.MaxInt32 {
			err = fmt.Errorf("%s: result too large", name)
		}
		if err != nil {
			out = []byte(err.Error())
			if len(out) > 1<<16 {
				out = out[:1<<16]
			}
			st.pending, st.valid = out, true
			stack[0] = api.EncodeI32(^int32(len(out)))
			return
		}
		st.pending, st.valid = out, true
		stack[0] = api.EncodeI32(int32(len(out)))
	}
}

// result implements starc_result(dst): it copies the pending result of
// the last host call to dst.
func (st *hostState) result(_ context.Context, mod api.Module, stack []uint64) {
	dst := api.DecodeU32(stack[0])
	if !st.valid {
		protocolError("%s called without a pending host result", hostResult)
	}
	out := st.pending
	st.pending, st.valid = nil, false
	if !mod.Memory().Write(dst, out) {
		protocolError("%s: buffer [%d, +%d) out of range", hostResult, dst, len(out))
	}
}
