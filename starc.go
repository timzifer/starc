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
// filename is used in error messages only.
func Compile(filename string, src []byte, opts *Options) ([]byte, error) {
	if opts == nil {
		opts = &Options{}
	}
	bin, err := compiler.Compile(filename, src, compiler.Options{
		Command: opts.ExecModel == Command,
		Exports: opts.Exports,
		Names:   !opts.NoNames,
		Dialect: syntax.FileOptions{
			While:           opts.Dialect.While,
			TopLevelControl: opts.Dialect.TopLevelControl,
			GlobalReassign:  opts.Dialect.GlobalReassign,
			Recursion:       opts.Dialect.Recursion,
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
