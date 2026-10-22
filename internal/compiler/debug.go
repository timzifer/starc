package compiler

import (
	"encoding/json"

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
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
	Wasm string `json:"wasm,omitempty"`
}

// debugSection returns the content of the starc.debug section, which maps
// the module back to the source: the functions of the profile, in the
// order of its entries, and the source positions by position id.
func (c *compiler) debugSection(filename string) []byte {
	type section struct {
		Version   int             `json:"version"`
		File      string          `json:"file"`
		Functions []debugFunction `json:"functions"`
		Positions [][2]int        `json:"positions"`
	}
	s := section{Version: 1, File: filename, Functions: []debugFunction{}, Positions: [][2]int{{0, 0}}}
	for _, e := range c.r.ProfileEntries() {
		s.Functions = append(s.Functions, debugFunction{e.Name, string(e.Kind), e.Line, e.Col, e.Wasm})
	}
	s.Positions = append(s.Positions, c.r.Positions()...)
	b, _ := json.Marshal(s)
	return b
}
