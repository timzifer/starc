package compiler

import (
	"encoding/json"

	"go.starlark.net/resolve"

	. "github.com/timzifer/starc/internal/wasm"
)

// profileExports exports the counters of the fuel profile.
func (c *compiler) profileExports() {
	m, r := c.m, c.r
	count := m.AddFunc(NewFunc(ExportProfile, nil, I32))
	count.Add(r.Call("prof_count"))
	m.Export(ExportProfile, count)
	for _, e := range []struct{ export, fn string }{{ExportProfFuel, "prof_fuel"}, {ExportProfCalls, "prof_calls"}} {
		f := m.AddFunc(NewFunc(e.export, []ValType{I32}, I64))
		f.Add(r.Call(e.fn, Get(f.Param(0))))
		m.Export(e.export, f)
	}
	reset := m.AddFunc(NewFunc(ExportProfReset, nil))
	reset.Add(r.Call("prof_reset"))
	m.Export(ExportProfReset, reset)
}

// debugFunction describes an entry of the profile in the debug section.
type debugFunction struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	File int    `json:"file,omitempty"` // index into the section's files
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
	Wasm string `json:"wasm,omitempty"`
}

// debugSection returns the content of the starc.debug section, which maps
// the module back to the source: the functions of the profile, in the
// order of its entries, the source positions by position id and, in a
// debug build, the position ids of the statements.
func (c *compiler) debugSection(filename string) []byte {
	type section struct {
		Version    int             `json:"version"`
		File       string          `json:"file"`
		Files      []string        `json:"files"`
		Functions  []debugFunction `json:"functions"`
		Positions  [][3]int        `json:"positions"`
		Statements []uint32        `json:"statements,omitempty"`
	}
	s := section{Version: 1, File: filename, Functions: []debugFunction{}, Positions: [][3]int{{0, 0, 0}}}
	for _, e := range c.r.ProfileEntries() {
		file := 0
		if e.File != "" {
			file = c.r.FileIndex(e.File)
		}
		s.Functions = append(s.Functions, debugFunction{e.Name, string(e.Kind), file, e.Line, e.Col, e.Wasm})
	}
	s.Files = c.r.Files()
	s.Positions = append(s.Positions, c.r.Positions()...)
	s.Statements = c.r.DebugStatements()
	b, _ := json.Marshal(s)
	return b
}

// debugVars sets up the variables that a debug build stores at each
// statement of the function being compiled: its local and free
// variables. It returns their descriptor, or 0 if there are no hooks.
func (fc *fcomp) debugVars(locals, free []*resolve.Binding) uint32 {
	if !fc.c.opts.Debug || fc.u.prelude {
		return 0
	}
	var names []string
	var cells []bool
	for i, b := range locals {
		names = append(names, b.First.Name)
		cells = append(cells, b.Scope == resolve.Cell)
		fc.dbgVars = append(fc.dbgVars, fc.locals[i])
	}
	for i, b := range free {
		names = append(names, b.First.Name)
		cells = append(cells, true)
		fc.dbgVars = append(fc.dbgVars, fc.free[i])
	}
	fc.dbgArea = fc.tmp()
	return fc.c.r.DebugDescriptor(names, cells)
}

// Exports of debug builds, which the host calls during a stop (see
// rt/debug.go). Strings are packed (ptr | len<<32) pairs; values are
// handles that starc_dbg_value and starc_dbg_type format.
var debugExportList = []struct {
	name, fn string
	params   []ValType
	results  []ValType
}{
	{"starc_dbg_mode", "dbg_set_mode", []ValType{I32}, nil},
	{"starc_dbg_break", "dbg_set_break", []ValType{I32, I32}, nil},
	{"starc_dbg_frames", "dbg_frames", nil, []ValType{I32}},
	{"starc_dbg_frame", "dbg_frame_name", []ValType{I32}, []ValType{I64}},
	{"starc_dbg_frame_kind", "dbg_frame_kind", []ValType{I32}, []ValType{I32}},
	{"starc_dbg_frame_pos", "dbg_frame_pos", []ValType{I32}, []ValType{I64}},
	{"starc_dbg_locals", "dbg_locals", []ValType{I32}, []ValType{I32}},
	{"starc_dbg_local_name", "dbg_local_name", []ValType{I32, I32}, []ValType{I64}},
	{"starc_dbg_local", "dbg_local", []ValType{I32, I32}, []ValType{I32}},
	{"starc_dbg_value", "dbg_value", []ValType{I32}, []ValType{I64}},
	{"starc_dbg_type", "dbg_type", []ValType{I32}, []ValType{I64}},
	{"starc_dbg_error", "dbg_error", nil, []ValType{I64}},
}

// Exports for the globals of the program.
const (
	ExportDbgGlobals    = "starc_dbg_globals"
	ExportDbgGlobalName = "starc_dbg_global_name"
	ExportDbgGlobal     = "starc_dbg_global"
)

func (c *compiler) debugExports() {
	m, r := c.m, c.r
	for _, e := range debugExportList {
		f := m.AddFunc(NewFunc(e.name, e.params, e.results...))
		args := make([]Expr, len(e.params))
		for i := range e.params {
			args[i] = Get(f.Param(i))
		}
		f.Add(r.Call(e.fn, args...))
		m.Export(e.name, f)
	}
	globals := c.user.file.Module.(*resolve.Module).Globals
	count := m.AddFunc(NewFunc(ExportDbgGlobals, nil, I32))
	count.Add(I32c(int32(len(globals))))
	m.Export(ExportDbgGlobals, count)
	name := m.AddFunc(NewFunc(ExportDbgGlobalName, []ValType{I32}, I64))
	value := m.AddFunc(NewFunc(ExportDbgGlobal, []ValType{I32}, I32))
	for i, b := range globals {
		name.Add(If(I32Eq(Get(name.Param(0)), I32c(int32(i))), Return(I64c(r.StrPacked(b.First.Name)))))
		value.Add(If(I32Eq(Get(value.Param(0)), I32c(int32(i))), Return(GGet(c.user.globals[i]))))
	}
	name.Add(I64c(0))
	value.Add(I32c(0))
	m.Export(ExportDbgGlobalName, name)
	m.Export(ExportDbgGlobal, value)
}

// debugPrefix starts the names of the exports of debug builds, which are
// reserved.
const debugPrefix = "starc_dbg_"
