// Package starc compiles Starlark programs ahead of time into standalone
// WebAssembly modules.
//
// The generated module contains native WebAssembly code for the program
// plus the parts of a small runtime library (memory management, values,
// operators, built-in functions) that the program uses. No interpreter
// is embedded.
//
// Compile a program from bytes, a reader (such as an *os.File), or a path:
//
//	wasm, err := starc.Compile("prog.star", src, nil)
//	wasm, err := starc.CompileReader("prog.star", f, nil)
//	wasm, err := starc.CompileFile("prog.star", &starc.Options{ExecModel: starc.Command})
//
// By default, modules follow the WASI reactor model: the host calls
// _initialize once, which runs the program's top-level statements and
// freezes all global values, and may then call any exported top-level
// function. Arguments and results are exchanged through linear memory
// using the encoding of package [github.com/timzifer/starc/abi]. Package
// [github.com/timzifer/starc/host] runs modules with wazero.
//
// See docs/abi.md for the module interface and docs/language.md for the
// supported language subset.
package starc
