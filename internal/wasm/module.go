// Package wasm provides a small WebAssembly module builder and binary
// encoder. Function bodies are expressed as trees of [Expr] nodes, which
// keeps hand-written code readable and lets the encoder resolve function
// and table indices late, after unreachable functions have been dropped.
package wasm

import (
	"fmt"
	"math"
	"strings"
)

// ValType is a WebAssembly value type.
type ValType byte

// Value types.
const (
	I32 ValType = 0x7f
	I64 ValType = 0x7e
	F32 ValType = 0x7d
	F64 ValType = 0x7c
)

func (t ValType) String() string {
	switch t {
	case I32:
		return "i32"
	case I64:
		return "i64"
	case F32:
		return "f32"
	case F64:
		return "f64"
	}
	return fmt.Sprintf("valtype(%#x)", byte(t))
}

// FuncType is a function signature.
type FuncType struct {
	Params  []ValType
	Results []ValType
}

func (t FuncType) key() string {
	var sb strings.Builder
	for _, p := range t.Params {
		sb.WriteByte(byte(p))
	}
	sb.WriteByte(0)
	for _, r := range t.Results {
		sb.WriteByte(byte(r))
	}
	return sb.String()
}

// Callee is a function that can be the target of a call: a *Func or an *Import.
type Callee interface {
	signature() FuncType
	name() string
}

// Import is an imported function.
type Import struct {
	Module, Name string
	Type         FuncType
}

func (im *Import) signature() FuncType { return im.Type }
func (im *Import) name() string        { return im.Module + "." + im.Name }

// Global is a module-defined global variable.
type Global struct {
	Name    string
	Type    ValType
	Mutable bool
	Init    int64   // initial value for integer globals
	InitF   float64 // initial value for float globals
	index   uint32
}

// Local is a function local (or parameter).
type Local struct {
	idx uint32
	typ ValType
}

// Type reports the local's value type.
func (l Local) Type() ValType { return l.typ }

// Func is a module-defined function.
type Func struct {
	Name   string
	Type   FuncType
	Body   []Expr
	locals []ValType
}

// NewFunc returns a new function with the given signature.
func NewFunc(name string, params []ValType, results ...ValType) *Func {
	return &Func{Name: name, Type: FuncType{Params: params, Results: results}}
}

func (f *Func) signature() FuncType { return f.Type }
func (f *Func) name() string        { return f.Name }

// Param returns the i'th parameter.
func (f *Func) Param(i int) Local { return Local{uint32(i), f.Type.Params[i]} }

// Local allocates a new local variable.
func (f *Func) Local(t ValType) Local {
	f.locals = append(f.locals, t)
	return Local{uint32(len(f.Type.Params) + len(f.locals) - 1), t}
}

// Add appends expressions to the function body.
func (f *Func) Add(body ...Expr) { f.Body = append(f.Body, body...) }

// Export maps an export name to a function.
type Export struct {
	Name string
	Func *Func
}

// Module is a WebAssembly module under construction.
type Module struct {
	Funcs   []*Func
	Imports []*Import
	Globals []*Global
	Exports []Export
	// ExportMemory, if non-empty, exports linear memory under this name.
	ExportMemory string
	// MinPages is the minimum memory size; it is raised to fit static data.
	MinPages uint32
	// MaxPages, if non-zero, is the maximum memory size in pages.
	MaxPages uint32
	// Customs are custom sections appended after all other sections.
	Customs []Custom
	// Names controls emission of the "name" custom section.
	Names bool

	data []byte
}

// Custom is a custom section.
type Custom struct {
	Name    string
	Payload []byte
}

// DataBase is the address at which static data begins. Address 0 is
// reserved so that a zero pointer never refers to a live object.
const DataBase = 16

// Static allocates n bytes of zeroed static memory aligned to align and
// returns its address.
func (m *Module) Static(n, align int) uint32 {
	if len(m.data) == 0 {
		m.data = make([]byte, DataBase)
	}
	for len(m.data)%align != 0 {
		m.data = append(m.data, 0)
	}
	addr := len(m.data)
	m.data = append(m.data, make([]byte, n)...)
	return uint32(addr)
}

// StaticBytes copies b into static memory and returns its address.
func (m *Module) StaticBytes(b []byte, align int) uint32 {
	addr := m.Static(len(b), align)
	copy(m.data[addr:], b)
	return addr
}

// PutU32 writes a little-endian uint32 into static memory.
func (m *Module) PutU32(addr uint32, v uint32) {
	m.data[addr] = byte(v)
	m.data[addr+1] = byte(v >> 8)
	m.data[addr+2] = byte(v >> 16)
	m.data[addr+3] = byte(v >> 24)
}

// PutU64 writes a little-endian uint64 into static memory.
func (m *Module) PutU64(addr uint32, v uint64) {
	m.PutU32(addr, uint32(v))
	m.PutU32(addr+4, uint32(v>>32))
}

// PutF64 writes a float64 into static memory.
func (m *Module) PutF64(addr uint32, v float64) { m.PutU64(addr, math.Float64bits(v)) }

// DataEnd returns the end address of static data.
func (m *Module) DataEnd() uint32 {
	if len(m.data) == 0 {
		return DataBase
	}
	return uint32(len(m.data))
}

// AddFunc registers a function and returns it.
func (m *Module) AddFunc(f *Func) *Func { m.Funcs = append(m.Funcs, f); return f }

// AddImport registers an imported function.
func (m *Module) AddImport(im *Import) *Import { m.Imports = append(m.Imports, im); return im }

// AddGlobal registers a global.
func (m *Module) AddGlobal(g *Global) *Global {
	g.index = uint32(len(m.Globals))
	m.Globals = append(m.Globals, g)
	return g
}

// Export exports f under name.
func (m *Module) Export(name string, f *Func) { m.Exports = append(m.Exports, Export{name, f}) }

// StaticSlice returns a view of n bytes of static memory at addr.
func (m *Module) StaticSlice(addr uint32, n int) []byte { return m.data[addr : int(addr)+n] }
