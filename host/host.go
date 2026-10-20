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
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"

	"github.com/timzifer/starc/abi"
)

// Config configures a module instance.
type Config struct {
	// Stdout and Stderr receive the module's output (print). Nil discards it.
	Stdout, Stderr io.Writer
	// Runtime, if set, is used instead of a private wazero runtime. It
	// must already have WASI instantiated.
	Runtime wazero.Runtime
}

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
}

// Error is a Starlark runtime error reported by the module.
type Error struct {
	Msg string
}

func (e *Error) Error() string { return e.Msg }

// ExitError reports that a command module exited with a non-zero status.
type ExitError struct {
	Code uint32
}

func (e *ExitError) Error() string { return fmt.Sprintf("module exited with status %d", e.Code) }

// Module is an instantiated starc module.
type Module struct {
	rt      wazero.Runtime
	ownRT   bool
	mod     api.Module
	meta    Meta
	alloc   api.Function
	errFn   api.Function
	exports map[string]api.Function
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
				return nil, fmt.Errorf("host: invalid starc.meta section: %v", err)
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
			m.rt.Close(ctx)
		}
		return nil, err
	}
	cm, err := m.rt.CompileModule(ctx, wasm)
	if err != nil {
		return fail(err)
	}
	meta, err := metaOf(cm)
	if err != nil {
		return fail(err)
	}
	m.meta = *meta
	mc := wazero.NewModuleConfig().WithName("")
	if cfg.Stdout != nil {
		mc = mc.WithStdout(cfg.Stdout)
	}
	if cfg.Stderr != nil {
		mc = mc.WithStderr(cfg.Stderr)
	}
	if meta.Model == "reactor" {
		mc = mc.WithStartFunctions() // _initialize is called below
	}
	mod, err := m.rt.InstantiateModule(ctx, cm, mc)
	if err != nil {
		var exit *sys.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 0 {
				return m, nil
			}
			return fail(&ExitError{exit.ExitCode()})
		}
		return fail(err)
	}
	m.mod = mod
	if meta.Model != "reactor" {
		return m, nil
	}
	m.alloc = mod.ExportedFunction("starc_alloc")
	m.errFn = mod.ExportedFunction("starc_error")
	for _, ex := range meta.Exports {
		m.exports[ex.Name] = mod.ExportedFunction(ex.Name)
	}
	if _, err := mod.ExportedFunction("_initialize").Call(ctx); err != nil {
		e := m.starlarkError(ctx, err)
		mod.Close(ctx)
		return fail(e)
	}
	return m, nil
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
func (m *Module) CallKw(ctx context.Context, name string, args []any, kwargs *abi.Dict) (any, error) {
	fn, ok := m.exports[name]
	if !ok || fn == nil {
		return nil, fmt.Errorf("host: module exports no function %q", name)
	}
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
	return &Error{Msg: string(msg)}
}

// Close releases the module (and its runtime, if private).
func (m *Module) Close(ctx context.Context) error {
	var err error
	if m.mod != nil {
		err = m.mod.Close(ctx)
	}
	if m.ownRT {
		if e := m.rt.Close(ctx); err == nil {
			err = e
		}
	}
	return err
}

