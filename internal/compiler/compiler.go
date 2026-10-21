// Package compiler translates resolved Starlark syntax trees into a
// WebAssembly module that links against the runtime of package rt.
package compiler

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"

	"github.com/timzifer/starc/internal/rt"
	. "github.com/timzifer/starc/internal/wasm"
)

//go:embed prelude.star
var preludeSrc string

// Options configures compilation.
type Options struct {
	// Command selects the WASI command model (_start) instead of the
	// reactor model (_initialize plus exported functions).
	Command bool
	// Exports lists the top-level functions to export. If nil, all
	// top-level functions whose names do not begin with "_" are exported.
	Exports []string
	// Names emits the "name" custom section with function names.
	Names bool
	// Dialect selects optional language features.
	Dialect syntax.FileOptions
	// Host lists host functions by dotted name, such as "ctx.entity".
	// Each becomes an import from module starc_host; the leading names
	// are predeclared modules.
	Host []string
	// NoWASI removes all WASI imports; print goes to the host.
	NoWASI bool
	// HostCost is the fuel charged per call of a host function, in
	// addition to the cost of a call.
	HostCost map[string]int64
	// Fuel enables fuel metering.
	Fuel bool
	// FiniteFloats makes NaN and infinite floats errors.
	FiniteFloats bool
	// MaxMemoryPages is the maximum memory size in 64 KiB pages; 0 means
	// rt.MaxMemoryPages.
	MaxMemoryPages uint32
	// MaxCallDepth limits the depth of calls; 0 means rt.DefaultMaxCallDepth.
	MaxCallDepth int
	// MaxInputBytes and MaxResultBytes limit the encoded arguments and
	// result of an exported call; 0 means no limit.
	MaxInputBytes, MaxResultBytes uint32
}

// Error is a compilation error at a source position.
type Error struct {
	Pos syntax.Position
	Msg string
}

func (e Error) Error() string { return e.Pos.String() + ": " + e.Msg }

// ErrorList is a list of compilation errors.
type ErrorList []Error

func (l ErrorList) Error() string {
	if len(l) == 1 {
		return l[0].Error()
	}
	return fmt.Sprintf("%s (and %d more errors)", l[0].Error(), len(l)-1)
}

// Reserved export names.
const (
	ExportAlloc     = "starc_alloc"
	ExportError     = "starc_error"
	ExportErrorCode = "starc_error_code"
	ExportFuelSet   = "starc_fuel_set"
	ExportFuel      = "starc_fuel"
	MetaSection     = "starc.meta"
)

// unit is one compiled source file (the prelude or the user program).
type unit struct {
	name    string
	file    *syntax.File
	globals []*Global // by resolve.Module.Globals index
	byName  map[string]*Global
	defs    map[string]*syntax.DefStmt
	opts    *syntax.FileOptions
	prelude bool
}

type compiler struct {
	opts    Options
	m       *Module
	r       *rt.RT
	errs    ErrorList
	prelude *unit
	user    *unit
	specs   []uint32 // specs with recursion guards
	reach   *reach
	nfunc   int
	host    map[string]*hostNode // predeclared host names
}

// Compile compiles a Starlark file to a WebAssembly binary.
func Compile(filename string, src []byte, opts Options) (out []byte, err error) {
	c := &compiler{opts: opts}
	if opts.Command && opts.NoWASI {
		return nil, errors.New("a command module requires WASI")
	}
	host, err := hostTree(opts.Host)
	if err != nil {
		return nil, err
	}
	c.host = host
	defer func() {
		if r := recover(); r != nil {
			if el, ok := r.(ErrorList); ok {
				out, err = nil, el
				return
			}
			panic(r)
		}
	}()

	// Parse and resolve the prelude.
	popts := &syntax.FileOptions{Set: true, While: true, TopLevelControl: true, GlobalReassign: true, Recursion: true}
	pfile, err := popts.Parse("<prelude>", preludeSrc, 0)
	if err != nil {
		return nil, fmt.Errorf("internal error: prelude: %w", err)
	}
	m := &Module{ExportMemory: "memory", Names: opts.Names}
	m.MaxPages = opts.MaxMemoryPages
	if m.MaxPages == 0 {
		m.MaxPages = rt.MaxMemoryPages
	}
	r := rt.New(m, rt.Config{Command: opts.Command, NoWASI: opts.NoWASI, Fuel: opts.Fuel, FiniteFloats: opts.FiniteFloats,
		MaxCallDepth: opts.MaxCallDepth, MaxInputBytes: opts.MaxInputBytes, MaxResultBytes: opts.MaxResultBytes})
	c.m, c.r = m, r

	preludeUniverse := func(name string) bool {
		return name == "None" || name == "True" || name == "False" || r.HasBuiltin(name)
	}
	if err := resolve.File(pfile, func(string) bool { return false }, preludeUniverse); err != nil {
		return nil, fmt.Errorf("internal error: prelude: %w", err)
	}
	c.prelude = c.newUnit("prelude", pfile, popts, true)

	// Parse and resolve the user file.
	dialect := opts.Dialect
	file, err := dialect.Parse(filename, src, 0)
	if err != nil {
		var se syntax.Error
		if errors.As(err, &se) {
			return nil, ErrorList{{se.Pos, se.Msg}}
		}
		return nil, err
	}
	universe := func(name string) bool {
		if name == "None" || name == "True" || name == "False" {
			return true
		}
		if _, ok := c.host[name]; ok {
			return true
		}
		if name == "set" && !opts.Dialect.Set {
			return false
		}
		if strings.HasPrefix(name, "_") {
			return false
		}
		if _, ok := c.prelude.defs[name]; ok {
			return true
		}
		return r.HasBuiltin(name)
	}
	if err := resolve.File(file, func(string) bool { return false }, universe); err != nil {
		var el resolve.ErrorList
		if errors.As(err, &el) {
			var out ErrorList
			for _, e := range el {
				out = append(out, Error{e.Pos, e.Msg})
			}
			return nil, out
		}
		return nil, err
	}
	c.user = c.newUnit("main", file, &dialect, false)
	c.checkSupported(file)
	if len(c.errs) > 0 {
		return nil, c.errs
	}

	c.reach = c.computeReach()

	// Hooks into the prelude used by the runtime.
	r.Hooks["percent_format"] = func() Expr { return c.preludeGlobal("_percent_format") }

	// Methods: primitive methods are static builtin objects; prelude
	// methods are stored after the prelude has been initialized.
	for _, ms := range r.PrimitiveMethods() {
		if !c.reach.usesMethod(ms.Name) {
			continue
		}
		r.SetMethod(ms.Tag, ms.Name, U32c(r.BuiltinObj(ms.Builtin)))
	}

	// Initialization function.
	initF := m.AddFunc(NewFunc("init", nil))
	initBody := []Expr{}

	pinit := c.compileToplevel(c.prelude)
	initBody = append(initBody, Call(pinit))
	for _, name := range sortedKeys(c.prelude.defs) {
		if !c.reach.defs[name] {
			continue
		}
		for prefix, tag := range map[string]int{"_str_": rt.TString, "_list_": rt.TList, "_dict_": rt.TDict, "_set_": rt.TSet} {
			if strings.HasPrefix(name, prefix) {
				initBody = append(initBody, r.StoreMethod(tag, name[len(prefix):], GGet(c.prelude.byName[name])))
			}
		}
	}
	uinit := c.compileToplevel(c.user)
	initBody = append(initBody, Call(uinit))
	// Freeze all globals and set the arena mark.
	for _, u := range []*unit{c.prelude, c.user} {
		for _, g := range u.globals {
			initBody = append(initBody, r.Call("freeze", GGet(g)))
		}
	}
	initBody = append(initBody, GSet(r.HeapMark, GGet(r.Heap)))
	if len(c.errs) > 0 {
		return nil, c.errs
	}

	// Exports.
	exports, err := c.exportList()
	if err != nil {
		return nil, err
	}
	var meta []exportMeta
	if opts.Command {
		m.Export("_start", initF)
	} else {
		m.Export("_initialize", initF)
		alloc := m.AddFunc(NewFunc("starc_alloc", []ValType{I32}, I32))
		alloc.Add(r.Call("abi_alloc", Get(alloc.Param(0))))
		m.Export(ExportAlloc, alloc)
		errF := m.AddFunc(NewFunc("starc_error", nil, I64))
		errF.Add(r.Call("abi_error"))
		m.Export(ExportError, errF)
		codeF := m.AddFunc(NewFunc(ExportErrorCode, nil, I32))
		codeF.Add(r.Call("error_code"))
		m.Export(ExportErrorCode, codeF)
		for _, name := range exports {
			m.Export(name, c.exportWrapper(name))
			meta = append(meta, c.exportMeta(name))
		}
	}

	if opts.Fuel {
		setF := m.AddFunc(NewFunc(ExportFuelSet, []ValType{I64}))
		setF.Add(r.Call("fuel_set", Get(setF.Param(0))))
		m.Export(ExportFuelSet, setF)
		getF := m.AddFunc(NewFunc(ExportFuel, nil, I64))
		getF.Add(r.Call("fuel_get"))
		m.Export(ExportFuel, getF)
	}

	r.LayoutMethods()
	static := r.Finish()
	initF.Add(Call(static))
	initF.Add(initBody...)

	hostFuncs := append([]string{}, r.HostFuncs()...)
	sort.Strings(hostFuncs)
	payload, _ := json.Marshal(struct {
		Version int           `json:"version"`
		Model   string        `json:"model"`
		Exports []exportMeta  `json:"exports"`
		Host    []string      `json:"host,omitempty"`
		NoWASI  bool          `json:"no_wasi,omitempty"`
		Fuel    *rt.FuelTable `json:"fuel,omitempty"`
		Limits  rt.Limits     `json:"limits"`
		Finite  bool          `json:"finite_floats,omitempty"`
	}{1, map[bool]string{true: "command", false: "reactor"}[opts.Command], meta, hostFuncs, opts.NoWASI, c.fuelTable(),
		rt.Limits{MemoryPages: int(m.MaxPages), CallDepth: r.Cfg.MaxCallDepth,
			InputBytes: int(opts.MaxInputBytes), ResultBytes: int(opts.MaxResultBytes)}, opts.FiniteFloats})
	m.Customs = append(m.Customs, Custom{Name: MetaSection, Payload: payload})

	return m.Encode()
}

// fuelTable returns the cost table for starc.meta, or nil without
// metering. It lists the declared costs of the imported host functions.
func (c *compiler) fuelTable() *rt.FuelTable {
	if !c.opts.Fuel {
		return nil
	}
	var host map[string]int64
	for _, name := range c.r.HostFuncs() {
		if cost := c.opts.HostCost[name]; cost != 0 {
			if host == nil {
				host = map[string]int64{}
			}
			host[name] = cost
		}
	}
	t := rt.Table(host)
	return &t
}

type exportMeta struct {
	Name   string   `json:"name"`
	Params []string `json:"params"`
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *compiler) errorf(pos syntax.Position, format string, args ...any) {
	c.errs = append(c.errs, Error{pos, fmt.Sprintf(format, args...)})
}

func (c *compiler) newUnit(name string, file *syntax.File, opts *syntax.FileOptions, prelude bool) *unit {
	u := &unit{name: name, file: file, byName: map[string]*Global{}, defs: map[string]*syntax.DefStmt{},
		opts: opts, prelude: prelude}
	mod := file.Module.(*resolve.Module)
	for _, b := range mod.Globals {
		g := c.m.AddGlobal(&Global{Name: name + "." + b.First.Name, Type: I32, Mutable: true})
		u.globals = append(u.globals, g)
		u.byName[b.First.Name] = g
	}
	for _, stmt := range file.Stmts {
		if d, ok := stmt.(*syntax.DefStmt); ok {
			u.defs[d.Name.Name] = d
		}
	}
	return u
}

func (c *compiler) preludeGlobal(name string) Expr {
	g, ok := c.prelude.byName[name]
	if !ok {
		panic("compiler: prelude lacks " + name)
	}
	return GGet(g)
}

// checkSupported reports constructs that the compiler does not support.
func (c *compiler) checkSupported(file *syntax.File) {
	syntax.Walk(file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.LoadStmt:
			c.errorf(n.Load, "load statements are not supported")
		case *syntax.Literal:
			switch n.Token {
			case syntax.BYTES:
				c.errorf(n.TokenPos, "bytes literals are not supported")
			case syntax.INT:
				if _, ok := n.Value.(int64); !ok {
					c.errorf(n.TokenPos, "integer literal %s exceeds 64 bits (big integers are not supported)", n.Raw)
				}
			}
		}
		return true
	})
}

func (c *compiler) exportList() ([]string, error) {
	reserved := map[string]bool{"memory": true, "_initialize": true, "_start": true, ExportAlloc: true, ExportError: true,
		ExportErrorCode: true, ExportFuelSet: true, ExportFuel: true}
	if c.opts.Exports == nil {
		var names []string
		for _, stmt := range c.user.file.Stmts {
			if d, ok := stmt.(*syntax.DefStmt); ok && !strings.HasPrefix(d.Name.Name, "_") {
				if reserved[d.Name.Name] {
					return nil, ErrorList{{d.Name.NamePos, fmt.Sprintf("function name %q collides with a reserved export", d.Name.Name)}}
				}
				names = append(names, d.Name.Name)
			}
		}
		return dedupe(names), nil
	}
	for _, name := range c.opts.Exports {
		if reserved[name] {
			return nil, fmt.Errorf("export %q collides with a reserved export", name)
		}
		if _, ok := c.user.defs[name]; !ok {
			return nil, fmt.Errorf("export %q: no top-level function with that name", name)
		}
	}
	return dedupe(c.opts.Exports), nil
}

func dedupe(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func (c *compiler) exportMeta(name string) exportMeta {
	d := c.user.defs[name]
	em := exportMeta{Name: name, Params: []string{}}
	for _, p := range d.Params {
		switch p := p.(type) {
		case *syntax.Ident:
			em.Params = append(em.Params, p.Name)
		case *syntax.BinaryExpr:
			em.Params = append(em.Params, p.X.(*syntax.Ident).Name)
		case *syntax.UnaryExpr:
			if id, ok := p.X.(*syntax.Ident); ok {
				prefix := "*"
				if p.Op == syntax.STARSTAR {
					prefix = "**"
				}
				em.Params = append(em.Params, prefix+id.Name)
			}
		}
	}
	return em
}

// exportWrapper generates name(ptr, len) -> i64 which decodes the
// arguments, calls the Starlark function and encodes its result.
func (c *compiler) exportWrapper(name string) *Func {
	r := c.r
	f := c.m.AddFunc(NewFunc("export."+name, []ValType{I32, I32}, I64))
	ptr, n := f.Param(0), f.Param(1)
	fn, kw, args := f.Local(I32), f.Local(I32), f.Local(I32)
	f.Add(If(I32Eqz(Get(ptr)), r.Call("arena_reset")))
	for _, s := range c.specs {
		f.Add(r.Leave(s))
	}
	f.Add(
		GSet(r.ErrObj, I32c(0)),
		GSet(r.ErrCode, I32c(0)),
		GSet(r.Depth, I32c(0)),
		Set(fn, GGet(c.user.byName[name])),
		Set(kw, r.Call("alloc", I32c(4))),
		Set(args, r.Call("abi_args", Get(ptr), Get(n), Get(kw))),
		r.Call("abi_result", r.Call("call", Get(fn), Get(args), I32Load(Get(kw), 0))),
	)
	return f
}
