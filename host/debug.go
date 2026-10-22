package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// Debugger stops a module built with Options.Debug at statements of the
// program, so that the host can inspect the call stack and the
// variables (see docs/debug.md). Stops do not change what the program
// does or the fuel it uses.
type Debugger struct {
	// Step stops at every statement.
	Step bool
	// Lines stops at the statements that start on these lines.
	Lines []int
	// Errors also stops at a run-time error, before the call ends, so
	// that the stack and variables at the error can be inspected; Stop.Err
	// is then the message.
	Errors bool
	// Stop is called at each stop, on the goroutine that runs the
	// module. The Stop is valid only during the call.
	Stop func(ctx context.Context, s *Stop)
}

// Stop is a module stopped at a statement or at an error.
type Stop struct {
	// Pos is the position of the statement, or of the failing operation.
	Pos Position
	// Err is the message of the error at a stop at an error, and empty at
	// a statement.
	Err    string
	mod    api.Module
	dbg    *DebugInfo
	errors bool
}

// DebugFrame is a frame of the call stack at a stop.
type DebugFrame struct {
	Name string
	// Kind is "function" for a function of the program or its top level,
	// "builtin" or "prelude" (a built-in implemented in Starlark).
	Kind string
	// Pos is where the frame is: the call of the next frame, or the
	// statement of the stop for the innermost one. It is the zero
	// Position for built-ins.
	Pos Position
	// Locals are the local variables of a function of the program, as
	// at the start of the frame's current statement.
	Locals []Variable
}

// Variable is a variable at a stop.
type Variable struct {
	Name string
	// Bound reports whether the variable has a value; Type and Value
	// are empty otherwise.
	Bound bool
	Type  string
	// Value is the value formatted as by repr.
	Value string
}

// Stack returns the call stack, outermost first.
func (s *Stop) Stack(ctx context.Context) ([]DebugFrame, error) {
	n, err := s.call32(ctx, "starc_dbg_frames")
	if err != nil {
		return nil, err
	}
	frames := make([]DebugFrame, 0, n)
	prev := int32(0)
	for i := uint64(0); i < uint64(n); i++ {
		kind, err := s.call32(ctx, "starc_dbg_frame_kind", i)
		if err != nil {
			return nil, err
		}
		// As in backtraces, internal prelude helpers (kind 3) and what
		// the prelude calls for itself are hidden.
		hidden := kind == 3 || (kind == 1 && prev >= 2)
		prev = kind
		if hidden {
			continue
		}
		var fr DebugFrame
		if fr.Name, err = s.str(ctx, "starc_dbg_frame", i); err != nil {
			return nil, err
		}
		fr.Kind = [...]string{"function", "builtin", "prelude"}[min(max(kind, 0), 2)]
		if kind == 0 {
			res, err := s.call(ctx, "starc_dbg_frame_pos", i)
			if err != nil {
				return nil, err
			}
			fr.Pos = decodePos(s.dbg.Files, s.dbg.File, res)
		}
		nl, err := s.call32(ctx, "starc_dbg_locals", i)
		if err != nil {
			return nil, err
		}
		for j := uint64(0); j < uint64(nl); j++ {
			name, err := s.str(ctx, "starc_dbg_local_name", i, j)
			if err != nil {
				return nil, err
			}
			v, err := s.call32(ctx, "starc_dbg_local", i, j)
			if err != nil {
				return nil, err
			}
			fr.Locals = append(fr.Locals, s.variable(ctx, name, v))
		}
		frames = append(frames, fr)
	}
	return frames, nil
}

// Globals returns the global variables of the program.
func (s *Stop) Globals(ctx context.Context) ([]Variable, error) {
	n, err := s.call32(ctx, "starc_dbg_globals")
	if err != nil {
		return nil, err
	}
	var out []Variable
	for i := uint64(0); i < uint64(n); i++ {
		name, err := s.str(ctx, "starc_dbg_global_name", i)
		if err != nil {
			return nil, err
		}
		v, err := s.call32(ctx, "starc_dbg_global", i)
		if err != nil {
			return nil, err
		}
		out = append(out, s.variable(ctx, name, v))
	}
	return out, nil
}

// SetStep turns stopping at every statement on or off; with it off, the
// module stops at the breakpoints only.
func (s *Stop) SetStep(ctx context.Context, on bool) error {
	_, err := s.call(ctx, "starc_dbg_mode", uint64(debugMode(on, s.errors)))
	return err
}

// variable formats the value with handle v. A value that cannot be
// formatted, such as a too deeply nested one, is shown as an error.
func (s *Stop) variable(ctx context.Context, name string, v int32) Variable {
	out := Variable{Name: name}
	if v == 0 {
		return out
	}
	out.Bound = true
	var err error
	if out.Type, err = s.str(ctx, "starc_dbg_type", uint64(uint32(v))); err == nil {
		out.Value, err = s.str(ctx, "starc_dbg_value", uint64(uint32(v)))
	}
	if err != nil {
		if msg, e := s.str(ctx, "starc_dbg_error"); e == nil && msg != "" {
			err = errors.New(msg)
		}
		out.Value = fmt.Sprintf("<error: %v>", err)
	}
	return out
}

func (s *Stop) call(ctx context.Context, name string, args ...uint64) (uint64, error) {
	f := s.mod.ExportedFunction(name)
	if f == nil {
		return 0, fmt.Errorf("host: module lacks %s (not a debug build?)", name)
	}
	res, err := f.Call(ctx, args...)
	if err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

func (s *Stop) call32(ctx context.Context, name string, args ...uint64) (int32, error) {
	res, err := s.call(ctx, name, args...)
	return api.DecodeI32(res), err
}

// str calls an export that returns a packed string.
func (s *Stop) str(ctx context.Context, name string, args ...uint64) (string, error) {
	res, err := s.call(ctx, name, args...)
	if err != nil || res == 0 {
		return "", err
	}
	b, ok := s.mod.Memory().Read(uint32(res), uint32(res>>32))
	if !ok {
		return "", fmt.Errorf("host: %s: string out of range", name)
	}
	return string(b), nil
}

// debugMode returns the mode of a debug build (see rt.DbgMode).
func debugMode(step, errors bool) int {
	mode := 1 // rt.DbgBreak
	if step {
		mode = 2 // rt.DbgStep
	}
	if errors {
		mode |= 4 // rt.DbgErrors
	}
	return mode
}

// errorStop marks the argument of starc_dbg at an error (rt.DbgErrorStop).
const errorStop = 1 << 31

// stopFunc implements the import starc_dbg(pos).
func stopFunc(d *Debugger, info *DebugInfo) api.GoModuleFunc {
	return func(ctx context.Context, mod api.Module, stack []uint64) {
		if d == nil || d.Stop == nil || info == nil {
			return
		}
		s := &Stop{mod: mod, dbg: info, errors: d.Errors}
		arg := api.DecodeU32(stack[0])
		if arg&errorStop != 0 {
			arg &^= errorStop
			s.Err, _ = s.str(ctx, "starc_dbg_error")
		}
		if id := int(arg); id < len(info.Positions) {
			p := info.Positions[id]
			s.Pos = Position{Filename: fileName(info.Files, info.File, p[2]), Line: p[0], Col: p[1]}
		}
		d.Stop(ctx, s)
	}
}

// startDebugger sets the mode and the breakpoints of a debug build.
func (m *Module) startDebugger(ctx context.Context, d *Debugger) error {
	if d == nil {
		return nil
	}
	mode, brk := m.mod.ExportedFunction("starc_dbg_mode"), m.mod.ExportedFunction("starc_dbg_break")
	if mode == nil || brk == nil || m.debug == nil {
		return fmt.Errorf("host: Config.Debug requires a module built with Options.Debug")
	}
	lines := map[int]bool{}
	for _, l := range d.Lines {
		lines[l] = true
	}
	for _, id := range m.debug.Statements {
		if int(id) < len(m.debug.Positions) && lines[m.debug.Positions[id][0]] {
			if _, err := brk.Call(ctx, uint64(id), 1); err != nil {
				return err
			}
		}
	}
	_, err := mode.Call(ctx, uint64(debugMode(d.Step, d.Errors)))
	return err
}
