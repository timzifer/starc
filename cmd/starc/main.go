// Command starc compiles Starlark programs to WebAssembly.
//
// Usage:
//
//	starc [build] [flags] file.star     compile to file.wasm
//	starc run [flags] file.star|file.wasm [function [args...]]
//	starc version
//
// Run "starc help" for details.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/abi"
	"github.com/timzifer/starc/host"
)

// version is set by the release build.
var version = ""

const usage = `starc compiles Starlark programs into standalone WebAssembly modules.

Usage:
  starc [build] [flags] <file.star | ->
  starc run [flags] <file.star | file.wasm> [function [json-args...]]
  starc version

Build flags:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "build":
			return build(args[1:], stdin, stdout, stderr)
		case "run":
			return runCmd(args[1:], stdin, stdout, stderr)
		case "version", "-version", "--version":
			fmt.Fprintln(stdout, "starc", versionString())
			return 0
		case "help", "-h", "-help", "--help":
			fs, _ := buildFlags(stderr)
			fs.SetOutput(stdout)
			fmt.Fprint(stdout, usage)
			fs.PrintDefaults()
			return 0
		}
	}
	return build(args, stdin, stdout, stderr)
}

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := moduleVersion(bi.Main.Version); v != "" {
			return v
		}
	}
	return starc.Version
}

// moduleVersion turns the main module version recorded by the Go
// toolchain (for example by "go install ...@v0.1.0") into the form the
// release build uses. It returns "" when no version was recorded.
func moduleVersion(v string) string {
	if v == "" || v == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

type buildOpts struct {
	out     string
	command bool
	exports string
	noNames bool
	host    string
	noWASI  bool
	fuel    bool
	finite  bool
	budget  int64
	limits  struct {
		pages, input, result uint
		depth                int
	}
	dialect starc.Dialect
}

func buildFlags(stderr io.Writer) (*flag.FlagSet, *buildOpts) {
	o := &buildOpts{}
	fs := flag.NewFlagSet("starc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.out, "o", "", "output file (default: input name with .wasm; \"-\" for stdout)")
	fs.BoolVar(&o.command, "command", false, "build a WASI command (_start) instead of a reactor with exports")
	fs.StringVar(&o.exports, "export", "", "comma-separated functions to export (default: all public top-level functions)")
	fs.BoolVar(&o.noNames, "no-names", false, "omit the debug name section")
	fs.StringVar(&o.host, "host", "", "comma-separated host functions to import, such as ctx.entity,now")
	fs.BoolVar(&o.noWASI, "no-wasi", false, "import nothing but host functions (print goes to the host)")
	fs.BoolVar(&o.fuel, "fuel", false, "compile deterministic fuel metering into the module (run: report the fuel used)")
	fs.Int64Var(&o.budget, "budget", 0, "run: fuel budget for the top-level code and for the call (implies -fuel; 0 = unlimited)")
	fs.UintVar(&o.limits.pages, "max-pages", 0, "maximum memory size in 64 KiB pages (default 65535)")
	fs.IntVar(&o.limits.depth, "max-depth", 0, "maximum call depth (default 500)")
	fs.UintVar(&o.limits.input, "max-input", 0, "maximum size of the encoded arguments of a call in bytes (default unlimited)")
	fs.UintVar(&o.limits.result, "max-result", 0, "maximum size of the encoded result of a call in bytes (default unlimited)")
	fs.BoolVar(&o.finite, "finite-floats", false, "make NaN and infinite floats errors")
	fs.BoolVar(&o.dialect.While, "while", false, "allow while loops")
	fs.BoolVar(&o.dialect.TopLevelControl, "toplevel-control", false, "allow if/for/while at top level")
	fs.BoolVar(&o.dialect.GlobalReassign, "global-reassign", false, "allow reassignment of top-level names")
	fs.BoolVar(&o.dialect.Recursion, "recursion", false, "allow recursive functions")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	return fs, o
}

func (o *buildOpts) options() *starc.Options {
	opts := &starc.Options{NoNames: o.noNames, NoWASI: o.noWASI, Fuel: o.fuel || o.budget > 0, FiniteFloats: o.finite, Dialect: o.dialect,
		MaxMemoryPages: uint32(min(o.limits.pages, 1<<32-1)), MaxCallDepth: o.limits.depth,
		MaxInputBytes: uint32(min(o.limits.input, 1<<32-1)), MaxResultBytes: uint32(min(o.limits.result, 1<<32-1))}
	if o.command {
		opts.ExecModel = starc.Command
	}
	opts.Exports = splitList(o.exports)
	for _, h := range splitList(o.host) {
		opts.Host = append(opts.Host, starc.HostFunc{Name: h})
	}
	return opts
}

// splitList splits a comma-separated flag value; it returns nil if
// there are no elements.
func splitList(s string) []string {
	var out []string
	for _, e := range strings.Split(s, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// compileInput compiles the named input ("-" for stdin).
func compileInput(name string, stdin io.Reader, opts *starc.Options) ([]byte, error) {
	if name == "-" {
		return starc.CompileReader("<stdin>", stdin, opts)
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return starc.CompileReader(name, f, opts)
}

func report(stderr io.Writer, err error) int {
	var el starc.ErrorList
	if errors.As(err, &el) {
		for _, e := range el {
			fmt.Fprintln(stderr, e)
		}
		return 1
	}
	fmt.Fprintln(stderr, "starc:", err)
	return 1
}

func build(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs, o := buildFlags(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	in := fs.Arg(0)
	wasm, err := compileInput(in, stdin, o.options())
	if err != nil {
		return report(stderr, err)
	}
	out := o.out
	if out == "" {
		if in == "-" {
			out = "-"
		} else {
			out = strings.TrimSuffix(in, filepath.Ext(in)) + ".wasm"
		}
	}
	if out == "-" {
		if _, err := stdout.Write(wasm); err != nil {
			return report(stderr, err)
		}
		return 0
	}
	if err := os.WriteFile(out, wasm, 0o644); err != nil { //nolint:gosec // output artifact
		return report(stderr, err)
	}
	return 0
}

func runCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs, o := buildFlags(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}
	in := fs.Arg(0)
	var wasm []byte
	var err error
	if strings.HasSuffix(in, ".wasm") {
		wasm, err = os.ReadFile(in)
	} else {
		opts := o.options()
		if fs.NArg() == 1 && !o.command && !o.noWASI {
			// Without a function to call, run the program as a command so
			// that errors are reported on stderr with a non-zero status.
			opts.ExecModel = starc.Command
		}
		wasm, err = compileInput(in, stdin, opts)
	}
	if err != nil {
		return report(stderr, err)
	}
	ctx := context.Background()
	if o.budget < 0 {
		fmt.Fprintln(stderr, "starc: negative -budget")
		return 2
	}
	budget := o.budget
	if budget == 0 {
		budget = math.MaxInt64
	}
	m, err := host.Load(ctx, wasm, &host.Config{Stdout: stdout, Stderr: stderr, InitFuel: o.budget})
	if err != nil {
		var exit *host.ExitError
		if errors.As(err, &exit) {
			return int(exit.Code)
		}
		return report(stderr, err)
	}
	defer m.Close(ctx)
	// reportFuel prints the fuel used since the budget was set.
	reportFuel := func() {
		if rest, err := m.Fuel(ctx); err == nil && (o.fuel || o.budget > 0) {
			fmt.Fprintf(stderr, "fuel used: %d\n", budget-rest)
		}
	}
	if fs.NArg() == 1 {
		reportFuel()
		return 0
	}
	if m.Meta().Fuel != nil {
		if err := m.SetFuel(ctx, budget); err != nil {
			return report(stderr, err)
		}
	}
	callArgs := make([]any, 0, fs.NArg()-2)
	for _, a := range fs.Args()[2:] {
		v, err := parseJSONArg(a)
		if err != nil {
			fmt.Fprintf(stderr, "starc: argument %q: %v\n", a, err)
			return 2
		}
		callArgs = append(callArgs, v)
	}
	res, err := m.Call(ctx, fs.Arg(1), callArgs...)
	reportFuel()
	if err != nil {
		return report(stderr, err)
	}
	b, err := json.Marshal(toJSON(res))
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// parseJSONArg parses a command-line argument as JSON; integral numbers
// become ints. A value that is not valid JSON is passed as a string.
func parseJSONArg(s string) (any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return s, nil //nolint:nilerr // plain strings need no quoting
	}
	return fromJSON(v)
}

func fromJSON(v any) (any, error) {
	switch v := v.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i, nil
		}
		return v.Float64()
	case []any:
		for i := range v {
			x, err := fromJSON(v[i])
			if err != nil {
				return nil, err
			}
			v[i] = x
		}
		return v, nil
	case map[string]any:
		for k := range v {
			x, err := fromJSON(v[k])
			if err != nil {
				return nil, err
			}
			v[k] = x
		}
		return v, nil
	}
	return v, nil
}

// toJSON converts a decoded Starlark value into a JSON-encodable value.
func toJSON(v any) any {
	switch v := v.(type) {
	case abi.Tuple:
		return toJSON([]any(v))
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = toJSON(x)
		}
		return out
	case *abi.Dict:
		obj := make(map[string]any, v.Len())
		pairs := make([]any, 0, v.Len())
		allStrings := true
		for _, e := range v.Entries {
			k, ok := e.Key.(string)
			if !ok {
				allStrings = false
			}
			obj[k] = toJSON(e.Value)
			pairs = append(pairs, []any{toJSON(e.Key), toJSON(e.Value)})
		}
		if allStrings {
			return obj
		}
		return pairs
	}
	return v
}
