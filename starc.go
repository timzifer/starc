package starc

import (
	"errors"
	"fmt"
	"io"
	"os"

	"go.starlark.net/syntax"

	"github.com/timzifer/starc/internal/compiler"
)

// Version is the version of starc. It is set at build time for releases.
var Version = "dev"

// ExecModel selects how the generated module is driven by its host.
type ExecModel int

const (
	// Reactor produces a WASI reactor: the host calls _initialize once to
	// run the top-level code and may then call exported functions.
	Reactor ExecModel = iota
	// Command produces a WASI command: _start runs the top-level code,
	// errors are written to stderr and terminate the process.
	Command
)

func (m ExecModel) String() string {
	switch m {
	case Reactor:
		return "reactor"
	case Command:
		return "command"
	}
	return fmt.Sprintf("ExecModel(%d)", int(m))
}

// Dialect enables optional language features, mirroring the options of
// the reference implementation.
type Dialect struct {
	While           bool // allow while loops
	TopLevelControl bool // allow if/for/while at top level
	GlobalReassign  bool // allow reassignment of top-level names
	Recursion       bool // allow recursive function calls
	Set             bool // provide the set type and the set builtin
}

// Options configures compilation. The zero value is a reactor module
// exporting all public top-level functions in standard Starlark.
type Options struct {
	ExecModel ExecModel
	// Exports lists the top-level functions to export. If nil, every
	// top-level def whose name does not begin with "_" is exported.
	// Ignored for command modules.
	Exports []string
	// NoNames omits the "name" custom section (debug function names).
	NoNames bool
	Dialect Dialect
	// Host declares functions that the embedder provides. They are
	// imported from the module "starc_host" and appear to the program as
	// predeclared builtins, or as members of predeclared modules for
	// dotted names. Package host supplies them through Config.Funcs.
	Host []HostFunc
	// NoWASI builds a module that imports nothing outside "starc_host":
	// print calls the host import starc_print, and errors are reported
	// through starc_error only. It cannot be combined with Command.
	NoWASI bool
	// Fuel compiles deterministic fuel metering into the module: every
	// statement, call, loop iteration and unit of library work costs fuel
	// according to a versioned cost table (see docs/fuel.md). The host
	// sets the budget with starc_fuel_set; when it is exhausted, the
	// program fails with "fuel exhausted" (starc_error_code 2).
	Fuel bool
	// MaxMemoryPages is the maximum size of linear memory in 64 KiB pages,
	// at most 65535. 0 means 65535 (one page short of 4 GiB). Allocating
	// beyond it fails with "out of memory" (starc_error_code 3).
	MaxMemoryPages uint32
	// MaxCallDepth is the maximum depth of calls of Starlark functions,
	// together with recursion into nested values (repr, hash, encoding, …).
	// 0 means 500, which every common engine supports; the maximum is 2^20.
	// Exceeding it fails with "maximum call depth exceeded" (code 4). Much
	// larger values can overflow the stack of some engines, such as
	// wazero's interpreter.
	MaxCallDepth int
	// MaxInputBytes and MaxResultBytes limit the encoded arguments and the
	// encoded result of an exported call (code 5); 0 means no limit.
	MaxInputBytes, MaxResultBytes uint32
	// FiniteFloats makes every operation that would create NaN or an
	// infinity fail with "non-finite float value", including float("nan"),
	// overflowing arithmetic and such values passed in by the host. Without
	// it, NaN is always canonicalized to a single bit pattern (see
	// docs/language.md).
	FiniteFloats bool
	// Struct provides the builtin struct(**kwargs), as embedders of
	// go.starlark.net usually predeclare it (starlarkstruct.Make). Struct
	// values from the host are available without it.
	Struct bool
	// NoBacktrace omits the source positions and the call stack of
	// run-time errors (see docs/abi.md), which cost a store before every
	// operation that can fail and a few instructions per call. Errors
	// then carry only their message.
	NoBacktrace bool
	// Profile records the fuel that each function uses (see docs/debug.md),
	// which host.Module.Profile reads. It requires Fuel and backtraces;
	// a profiled module uses exactly the same fuel as one without.
	Profile bool
	// Debug compiles hooks at the statements of the program, at which
	// host.Config.Debug can stop it to inspect the call stack and the
	// variables (see docs/debug.md). It requires backtraces. A debug
	// build uses the same fuel and gives the same results as a release
	// build, but is larger and slower.
	Debug bool
}

// maxCallDepth bounds Options.MaxCallDepth; no engine supports deeper
// call stacks anyway.
const maxCallDepth = 1 << 20

// HostFunc declares a function provided by the host.
type HostFunc struct {
	// Name is the function's name, optionally qualified by modules, for
	// example "now" or "ctx.entity". The program calls it as ctx.entity(...);
	// ctx is then a predeclared module value.
	Name string
	// Cost is the fuel charged for each call, in addition to the cost of
	// a call and of passing the arguments and the result. The host can
	// charge more during the call (see host.ChargeFuel).
	Cost int64
}

// Position is a source location.
type Position struct {
	Filename  string
	Line, Col int
}

func (p Position) String() string {
	if p.Line == 0 {
		return p.Filename
	}
	if p.Col == 0 {
		return fmt.Sprintf("%s:%d", p.Filename, p.Line)
	}
	return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Col)
}

// Error is a compile-time error in the Starlark source.
type Error struct {
	Pos Position
	Msg string
}

func (e Error) Error() string { return e.Pos.String() + ": " + e.Msg }

// ErrorList is returned when compilation fails because of errors in the
// source. It is never empty.
type ErrorList []Error

func (l ErrorList) Error() string {
	if len(l) == 1 {
		return l[0].Error()
	}
	return fmt.Sprintf("%s (and %d more errors)", l[0].Error(), len(l)-1)
}

// Compile compiles Starlark source code into a WebAssembly module. The
// filename appears in error messages and, unless Options.NoBacktrace is
// set, in the module, for the positions of run-time errors.
func Compile(filename string, src []byte, opts *Options) ([]byte, error) {
	if opts == nil {
		opts = &Options{}
	}
	if opts.MaxMemoryPages > 65535 {
		return nil, fmt.Errorf("MaxMemoryPages %d exceeds 65535", opts.MaxMemoryPages)
	}
	if opts.MaxCallDepth < 0 || opts.MaxCallDepth > maxCallDepth {
		return nil, fmt.Errorf("MaxCallDepth %d out of range [0, %d]", opts.MaxCallDepth, maxCallDepth)
	}
	var host []string
	costs := map[string]int64{}
	for _, h := range opts.Host {
		if h.Cost < 0 {
			return nil, fmt.Errorf("host function %q: negative cost %d", h.Name, h.Cost)
		}
		if c, dup := costs[h.Name]; dup && c != h.Cost {
			return nil, fmt.Errorf("host function %q declared with costs %d and %d", h.Name, c, h.Cost)
		}
		host = append(host, h.Name)
		costs[h.Name] = h.Cost
	}
	bin, err := compiler.Compile(filename, src, compiler.Options{
		Command:  opts.ExecModel == Command,
		Exports:  opts.Exports,
		Names:    !opts.NoNames,
		Host:     host,
		HostCost: costs,
		NoWASI:   opts.NoWASI,
		Fuel:     opts.Fuel,

		MaxMemoryPages: opts.MaxMemoryPages,
		MaxCallDepth:   opts.MaxCallDepth,
		MaxInputBytes:  opts.MaxInputBytes,
		MaxResultBytes: opts.MaxResultBytes,
		FiniteFloats:   opts.FiniteFloats,
		Struct:         opts.Struct,
		NoBacktrace:    opts.NoBacktrace,
		Profile:        opts.Profile,
		Debug:          opts.Debug,
		Dialect: syntax.FileOptions{
			While:           opts.Dialect.While,
			TopLevelControl: opts.Dialect.TopLevelControl,
			GlobalReassign:  opts.Dialect.GlobalReassign,
			Recursion:       opts.Dialect.Recursion,
			Set:             opts.Dialect.Set,
		},
	})
	var el compiler.ErrorList
	if errors.As(err, &el) {
		out := make(ErrorList, len(el))
		for i, e := range el {
			out[i] = Error{Position{e.Pos.Filename(), int(e.Pos.Line), int(e.Pos.Col)}, e.Msg}
		}
		return nil, out
	}
	return bin, err
}

// CompileReader compiles the Starlark source read from r, for example
// an *os.File.
func CompileReader(filename string, r io.Reader, opts *Options) ([]byte, error) {
	src, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Compile(filename, src, opts)
}

// CompileFile compiles the Starlark file at path.
func CompileFile(path string, opts *Options) ([]byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Compile(path, src, opts)
}
