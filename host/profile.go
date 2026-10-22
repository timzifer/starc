package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// DebugInfo is the content of the starc.debug custom section, which
// modules built with a profile contain. It maps the module back to the
// source.
type DebugInfo struct {
	Version int    `json:"version"`
	File    string `json:"file"`
	// Files are the source files by index, the main file first.
	Files []string `json:"files"`
	// Functions are the entries of the profile, in order.
	Functions []DebugFunction `json:"functions"`
	// Positions are the source positions (line, col, file index) by
	// position id; entry 0 is unknown.
	Positions [][3]int `json:"positions"`
	// Statements are the position ids of the statements of a debug
	// build, where it can stop.
	Statements []uint32 `json:"statements,omitempty"`
}

// DebugFunction describes a function of the profile.
type DebugFunction struct {
	Name string `json:"name"`
	// Kind is "toplevel", "function" (of the program), "builtin" or
	// "host".
	Kind string `json:"kind"`
	// File (an index into Files), Line and Col locate the def of a
	// function of the program.
	File int `json:"file,omitempty"`
	Line int `json:"line,omitempty"`
	Col  int `json:"col,omitempty"`
	// Wasm names the WebAssembly function that implements a function of
	// the program, as in the name section.
	Wasm string `json:"wasm,omitempty"`
}

// ProfileEntry is the fuel that a function used, not counting the
// functions it called.
type ProfileEntry struct {
	Name string
	Kind string // as in DebugFunction
	// Pos is the def of a function of the program; it is the zero
	// Position for other kinds.
	Pos   Position
	Fuel  int64
	Calls int64
}

// ErrNoProfile reports a module built without Options.Profile.
var ErrNoProfile = errors.New("host: module was built without a profile")

const debugSection = "starc.debug"

func debugOf(cm wazero.CompiledModule) (*DebugInfo, error) {
	for _, cs := range cm.CustomSections() {
		if cs.Name() == debugSection {
			var d DebugInfo
			if err := json.Unmarshal(cs.Data(), &d); err != nil {
				return nil, fmt.Errorf("host: invalid %s section: %w", debugSection, err)
			}
			return &d, nil
		}
	}
	return nil, nil
}

// DebugInfo returns the content of the module's starc.debug section, or
// nil if it has none.
func (m *Module) DebugInfo() *DebugInfo { return m.debug }

// Profile returns the fuel used by each function since the module was
// loaded or the profile was reset, including the top-level code, in the
// order of DebugInfo().Functions. The fuel of all entries adds up to the
// fuel that the top-level code and the calls used, except for the
// encoding of arguments by the host.
func (m *Module) Profile(ctx context.Context) ([]ProfileEntry, error) {
	if m.profFuel == nil || m.debug == nil {
		return nil, ErrNoProfile
	}
	out := make([]ProfileEntry, len(m.debug.Functions))
	for i, f := range m.debug.Functions {
		e := ProfileEntry{Name: f.Name, Kind: f.Kind}
		if f.Line != 0 {
			e.Pos = Position{Filename: fileName(m.debug.Files, m.debug.File, f.File), Line: f.Line, Col: f.Col}
		}
		res, err := m.profFuel.Call(ctx, uint64(i))
		if err != nil {
			return nil, err
		}
		e.Fuel = int64(res[0])
		if res, err = m.profCalls.Call(ctx, uint64(i)); err != nil {
			return nil, err
		}
		e.Calls = int64(res[0])
		out[i] = e
	}
	return out, nil
}

// ResetProfile clears the profile.
func (m *Module) ResetProfile(ctx context.Context) error {
	if m.profReset == nil {
		return ErrNoProfile
	}
	_, err := m.profReset.Call(ctx)
	return err
}

func (m *Module) initProfile(mod api.Module) {
	m.profFuel = mod.ExportedFunction("starc_profile_fuel")
	m.profCalls = mod.ExportedFunction("starc_profile_calls")
	m.profReset = mod.ExportedFunction("starc_profile_reset")
}

// colBits is the number of bits of the column in a packed position; the
// index of the source file (in Meta.Files or DebugInfo.Files) is above.
const colBits = 20

// decodePos decodes a packed position: the line in the low 32 bits, then
// the column and the index of the file.
func decodePos(files []string, main string, v uint64) Position {
	hi := uint32(v >> 32)
	return Position{Filename: fileName(files, main, int(hi>>colBits)), Line: int(uint32(v)), Col: int(hi & (1<<colBits - 1))}
}

// fileName returns file i of files, or main if there is no such file.
func fileName(files []string, main string, i int) string {
	if i < len(files) {
		return files[i]
	}
	return main
}
