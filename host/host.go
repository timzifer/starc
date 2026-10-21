// Package host runs modules produced by starc using the wazero
// WebAssembly runtime, and converts between Go and Starlark values.
//
//	m, err := host.Load(ctx, wasm, nil)
//	if err != nil { ... }
//	defer m.Close(ctx)
//	res, err := m.Call(ctx, "fib", 20)
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"

	"github.com/timzifer/starc/abi"
)

// Config configures a module instance.
type Config struct {
	// Stdout and Stderr receive the module's output (print). Nil discards it.
	Stdout, Stderr io.Writer
	// Runtime, if set, is used instead of a private wazero runtime. It
	// must already have WASI instantiated, unless the module was built
	// without WASI.
	Runtime wazero.Runtime
	// Funcs implements the host functions that the module imports (see
	// starc.Options.Host), keyed by their dotted names. Load fails if the
	// module imports a function that is missing here.
	Funcs map[string]Func
	// InitFuel is the fuel budget for running the top-level code of a
	// module built with fuel metering. 0 means unlimited.
	InitFuel int64
}

// Func implements a host function. It receives the positional and
// keyword arguments of the call (kwargs is nil if there are none) and
// returns a value that package abi can encode. A non-nil error becomes a
// Starlark error in the calling program, with the error text as message.
type Func func(ctx context.Context, args []any, kwargs *abi.Dict) (any, error)

// Export describes an exported Starlark function.
type Export struct {
	Name   string   `json:"name"`
	Params []string `json:"params"`
}

// Meta is the content of the starc.meta custom section.
type Meta struct {
	Version int      `json:"version"`
	Model   string   `json:"model"` // "reactor" or "command"
	Exports []Export `json:"exports"`
	// Host lists the host functions that the module imports.
	Host []string `json:"host,omitempty"`
	// NoWASI reports a module built without WASI imports.
	NoWASI bool `json:"no_wasi,omitempty"`
	// Fuel is the cost table of a module built with fuel metering.
	Fuel *FuelTable `json:"fuel,omitempty"`
	// Limits are the hard limits compiled into the module. They are zero
	// for modules built before limits were recorded.
	Limits Limits `json:"limits"`
	// FiniteFloats reports a module in which NaN and infinities are errors.
	FiniteFloats bool `json:"finite_floats,omitempty"`
}

// Limits are the hard limits of a module (see starc.Options).
type Limits struct {
	MemoryPages int `json:"memory_pages"`
	CallDepth   int `json:"call_depth"`
	InputBytes  int `json:"input_bytes,omitempty"`  // 0: no limit
	ResultBytes int `json:"result_bytes,omitempty"` // 0: no limit
}

// FuelTable is the cost table of fuel metering (see docs/fuel.md).
type FuelTable struct {
	Version          int              `json:"version"`
	Statement        int              `json:"statement"`
	Call             int              `json:"call"`
	Iteration        int              `json:"iteration"`
	RuntimeIteration int              `json:"runtime_iteration"`
	BytesPerUnit     int              `json:"bytes_per_unit"`
	Host             map[string]int64 `json:"host,omitempty"`
}

// Names of the host import module and of its protocol functions.
const (
	hostModule = "starc_host"
	hostResult = "starc_result"
	hostPrint  = "starc_print"
	wasiModule = "wasi_snapshot_preview1"
)

// Error is a Starlark runtime error reported by the module.
type Error struct {
	Msg  string
	Code ErrorCode
}

func (e *Error) Error() string { return e.Msg }

// Is reports whether target is the sentinel error of e's code, such as
// ErrFuelExhausted for CodeFuel.
func (e *Error) Is(target error) bool {
	s, ok := codeErrors[e.Code]
	return ok && target == s
}

// ErrorCode classifies a Starlark error (see starc_error_code).
type ErrorCode int

// Error codes.
const (
	CodeStarlark ErrorCode = 1 // an ordinary Starlark error
	CodeFuel     ErrorCode = 2 // the fuel budget was exhausted
	CodeMemory   ErrorCode = 3 // the memory limit was reached
	CodeDepth    ErrorCode = 4 // the call depth limit was exceeded
	CodeSize     ErrorCode = 5 // arguments or result exceed their size limit
)

// Sentinel errors that match (with errors.Is) an *Error with the
// corresponding code.
var (
	ErrFuelExhausted = errors.New("fuel exhausted")
	ErrOutOfMemory   = errors.New("out of memory")
	ErrCallDepth     = errors.New("maximum call depth exceeded")
	ErrTooLarge      = errors.New("value too large")
)

var codeErrors = map[ErrorCode]error{
	CodeFuel: ErrFuelExhausted, CodeMemory: ErrOutOfMemory, CodeDepth: ErrCallDepth, CodeSize: ErrTooLarge,
}

// ExitError reports that a command module exited with a non-zero status.
type ExitError struct {
	Code uint32
	// Err is the Starlark error that ended a starc command module: the
	// exit status of such a module is the error code (see ErrorCode). It
	// is nil for status 1 (an ordinary error, whose message went to stderr)
	// and for other modules.
	Err *Error
}

func (e *ExitError) Error() string { return fmt.Sprintf("module exited with status %d", e.Code) }

// Unwrap returns the Starlark error, so that errors.Is(err,
// ErrFuelExhausted) holds for a command module that ran out of fuel.
func (e *ExitError) Unwrap() error {
	if e.Err == nil {
		return nil
	}
	return e.Err
}

// Module is an instantiated starc module.
type Module struct {
	rt      wazero.Runtime
	ownRT   bool
	mod     api.Module
	hostMod api.Module
	meta    Meta
	alloc   api.Function
	errFn   api.Function
	codeFn  api.Function
	fuelSet api.Function
	fuelGet api.Function
	exports map[string]api.Function
	busy    bool // a call is in progress
}

// ReadMeta extracts the starc.meta custom section from a module binary.
func ReadMeta(ctx context.Context, wasm []byte) (*Meta, error) {
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCustomSections(true))
	defer r.Close(ctx)
	cm, err := r.CompileModule(ctx, wasm)
	if err != nil {
		return nil, err
	}
	return metaOf(cm)
}

func metaOf(cm wazero.CompiledModule) (*Meta, error) {
	for _, cs := range cm.CustomSections() {
		if cs.Name() == "starc.meta" {
			var m Meta
			if err := json.Unmarshal(cs.Data(), &m); err != nil {
				return nil, fmt.Errorf("host: invalid starc.meta section: %w", err)
			}
			return &m, nil
		}
	}
	return nil, errors.New("host: module has no starc.meta section (not built by starc?)")
}

// Load instantiates a starc module and runs its top-level code. For a
// command module, the program runs to completion during Load.
func Load(ctx context.Context, wasm []byte, cfg *Config) (*Module, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	m := &Module{rt: cfg.Runtime, exports: map[string]api.Function{}}
	if m.rt == nil {
		m.rt = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCustomSections(true))
		m.ownRT = true
		wasi_snapshot_preview1.MustInstantiate(ctx, m.rt)
	}
	fail := func(err error) (*Module, error) {
		if m.ownRT {
			_ = m.rt.Close(ctx)
		}
		return nil, err
	}
	cm, err := m.rt.CompileModule(ctx, wasm)
	if err != nil {
		return fail(err)
	}
	// The instance outlives the compiled module.
	defer cm.Close(ctx)
	meta, err := metaOf(cm)
	if err != nil {
		return fail(err)
	}
	if err := checkExports(cm, meta); err != nil {
		return fail(err)
	}
	if err := checkImports(cm, meta, cfg.Funcs); err != nil {
		return fail(err)
	}
	m.meta = *meta
	if m.hostMod, err = instantiateHost(ctx, m.rt, cm, cfg); err != nil {
		return fail(err)
	}
	if m.hostMod != nil {
		hostMod := m.hostMod
		ctx = experimental.WithImportResolver(ctx, func(name string) api.Module {
			if name == hostModule {
				return hostMod
			}
			return nil
		})
		fail0 := fail
		fail = func(err error) (*Module, error) {
			_ = hostMod.Close(ctx)
			return fail0(err)
		}
	}
	mc := wazero.NewModuleConfig().WithName("")
	if cfg.Stdout != nil {
		mc = mc.WithStdout(cfg.Stdout)
	}
	if cfg.Stderr != nil {
		mc = mc.WithStderr(cfg.Stderr)
	}
	// The start function is called below, after the fuel budget is set.
	mc = mc.WithStartFunctions()
	mod, err := m.rt.InstantiateModule(ctx, cm, mc)
	if err != nil {
		return fail(err)
	}
	m.mod = mod
	m.fuelSet = mod.ExportedFunction("starc_fuel_set")
	m.fuelGet = mod.ExportedFunction("starc_fuel")
	if cfg.InitFuel != 0 {
		if err := m.SetFuel(ctx, cfg.InitFuel); err != nil {
			_ = mod.Close(ctx)
			return fail(err)
		}
	}
	if meta.Model != "reactor" {
		if _, err := mod.ExportedFunction("_start").Call(ctx); err != nil {
			var exit *sys.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 0 {
				return m, nil
			}
			_ = mod.Close(ctx)
			if errors.As(err, &exit) {
				e := &ExitError{Code: exit.ExitCode()}
				if s, ok := codeErrors[ErrorCode(e.Code)]; ok {
					e.Err = &Error{Msg: s.Error(), Code: ErrorCode(e.Code)}
				}
				return fail(e)
			}
			return fail(err)
		}
		return m, nil
	}
	m.alloc = mod.ExportedFunction("starc_alloc")
	m.errFn = mod.ExportedFunction("starc_error")
	m.codeFn = mod.ExportedFunction("starc_error_code")
	for _, ex := range meta.Exports {
		m.exports[ex.Name] = mod.ExportedFunction(ex.Name)
	}
	if _, err := mod.ExportedFunction("_initialize").Call(ctx); err != nil {
		e := m.starlarkError(ctx, err)
		_ = mod.Close(ctx)
		return fail(e)
	}
	return m, nil
}

// SetFuel sets the remaining fuel budget of a module built with fuel
// metering. Each call consumes fuel from it; set it before each call to
// give every call its own budget.
func (m *Module) SetFuel(ctx context.Context, n int64) error {
	if m.fuelSet == nil {
		return errors.New("host: module was built without fuel metering")
	}
	if n < 0 {
		return fmt.Errorf("host: negative fuel budget %d", n)
	}
	_, err := m.fuelSet.Call(ctx, api.EncodeI64(n))
	return err
}

// Fuel returns the remaining fuel budget. It is math.MaxInt64 until a
// budget is set, and 0 after the budget was exhausted.
func (m *Module) Fuel(ctx context.Context) (int64, error) {
	if m.fuelGet == nil {
		return 0, errors.New("host: module was built without fuel metering")
	}
	res, err := m.fuelGet.Call(ctx)
	if err != nil {
		return 0, err
	}
	return int64(res[0]), nil
}

// checkExports verifies that the module's execution model is known and
// that a reactor module exports the ABI functions with the expected
// signatures, so that malformed modules are rejected before they run.
func checkExports(cm wazero.CompiledModule, meta *Meta) error {
	defs := cm.ExportedFunctions()
	check := func(name string, params, results []api.ValueType) error {
		d, ok := defs[name]
		if !ok {
			return fmt.Errorf("host: module does not export required function %q", name)
		}
		if !equalTypes(d.ParamTypes(), params) || !equalTypes(d.ResultTypes(), results) {
			return fmt.Errorf("host: exported function %q has signature %v -> %v, want %v -> %v",
				name, d.ParamTypes(), d.ResultTypes(), params, results)
		}
		return nil
	}
	i32, i64 := api.ValueTypeI32, api.ValueTypeI64
	if meta.Fuel != nil {
		if err := check("starc_fuel_set", []api.ValueType{i64}, nil); err != nil {
			return err
		}
		if err := check("starc_fuel", nil, []api.ValueType{i64}); err != nil {
			return err
		}
	}
	switch meta.Model {
	case "command":
		return check("_start", nil, nil)
	case "reactor":
	default:
		return fmt.Errorf("host: unknown execution model %q in starc.meta", meta.Model)
	}
	if len(cm.ExportedMemories()) == 0 {
		return errors.New("host: module does not export its memory")
	}
	// Modules built before starc_error_code existed lack it.
	if _, ok := defs["starc_error_code"]; ok {
		if err := check("starc_error_code", nil, []api.ValueType{i32}); err != nil {
			return err
		}
	}
	if err := check("_initialize", nil, nil); err != nil {
		return err
	}
	if err := check("starc_alloc", []api.ValueType{i32}, []api.ValueType{i32}); err != nil {
		return err
	}
	if err := check("starc_error", nil, []api.ValueType{i64}); err != nil {
		return err
	}
	for _, ex := range meta.Exports {
		if err := check(ex.Name, []api.ValueType{i32, i32}, []api.ValueType{i64}); err != nil {
			return err
		}
	}
	return nil
}

func equalTypes(a, b []api.ValueType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Meta returns the module's metadata.
func (m *Module) Meta() Meta { return m.meta }

// Exports lists the exported Starlark functions.
func (m *Module) Exports() []Export { return m.meta.Exports }

// Call calls an exported function with positional arguments.
func (m *Module) Call(ctx context.Context, name string, args ...any) (any, error) {
	return m.CallKw(ctx, name, args, nil)
}

// CallKw calls an exported function with positional and keyword arguments.
// A host function must not call back into the module that called it:
// the call would reset the arena that the suspended call still uses.
func (m *Module) CallKw(ctx context.Context, name string, args []any, kwargs *abi.Dict) (any, error) {
	fn, ok := m.exports[name]
	if !ok || fn == nil {
		return nil, fmt.Errorf("host: module exports no function %q", name)
	}
	if m.busy {
		return nil, errors.New("host: module called from one of its own host functions")
	}
	m.busy = true
	defer func() { m.busy = false }()
	payload, err := abi.EncodeCall(args, kwargs)
	if err != nil {
		return nil, err
	}
	res, err := m.alloc.Call(ctx, uint64(len(payload)))
	if err != nil {
		return nil, m.starlarkError(ctx, err)
	}
	ptr := uint32(res[0])
	if !m.mod.Memory().Write(ptr, payload) {
		return nil, errors.New("host: argument buffer out of range")
	}
	res, err = fn.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		return nil, m.starlarkError(ctx, err)
	}
	out, ok := m.read(res[0])
	if !ok {
		return nil, errors.New("host: result buffer out of range")
	}
	return abi.Decode(out)
}

// read returns the bytes of a packed (ptr | len<<32) buffer.
func (m *Module) read(packed uint64) ([]byte, bool) {
	ptr, n := uint32(packed), uint32(packed>>32)
	b, ok := m.mod.Memory().Read(ptr, n)
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

// starlarkError converts a trap into an *Error if the module recorded one.
func (m *Module) starlarkError(ctx context.Context, err error) error {
	if m.errFn == nil {
		return err
	}
	res, e2 := m.errFn.Call(ctx)
	if e2 != nil || res[0] == 0 {
		return err
	}
	msg, ok := m.read(res[0])
	if !ok {
		return err
	}
	code := CodeStarlark
	if m.codeFn != nil {
		if res, e2 := m.codeFn.Call(ctx); e2 == nil && res[0] != 0 {
			code = ErrorCode(api.DecodeI32(res[0]))
		}
	}
	return &Error{Msg: string(msg), Code: code}
}

// Close releases the module (and its runtime, if private).
func (m *Module) Close(ctx context.Context) error {
	var err error
	if m.mod != nil {
		err = m.mod.Close(ctx)
	}
	if m.hostMod != nil {
		if e := m.hostMod.Close(ctx); err == nil {
			err = e
		}
	}
	if m.ownRT {
		if e := m.rt.Close(ctx); err == nil {
			err = e
		}
	}
	return err
}
