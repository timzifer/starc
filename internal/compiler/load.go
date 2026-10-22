package compiler

import (
	"errors"
	"slices"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"

	. "github.com/timzifer/starc/internal/wasm"
)

// Loader returns the source of the module that a load statement names,
// as written in the statement. The module name is also its file name in
// positions.
type Loader func(module string) ([]byte, error)

// Loaded modules are compiled into the same WebAssembly module as the
// program. Each is a unit with its own globals; its top-level code runs
// when a load statement first reaches it, after which its globals are
// frozen, as go.starlark.net's ExecFile does. The names that a load
// statement binds are local to the loading file.

// loadModules resolves the load statements of u and, recursively, of the
// modules they load; stack lists the modules being loaded. Unknown
// modules, cycles and names that a module does not define are errors at
// the load statement.
func (c *compiler) loadModules(u *unit, stack []string) {
	for _, stmt := range u.file.Stmts {
		ld, ok := stmt.(*syntax.LoadStmt)
		if !ok {
			continue
		}
		name := ld.ModuleName()
		if slices.Contains(stack, name) {
			cycle := append(stack[slices.Index(stack, name):], name)
			c.errorf(ld.Load, "cannot load %s: cycle in load graph (%s)", name, strings.Join(cycle, " -> "))
			continue
		}
		m := c.modules[name]
		if m == nil {
			if m = c.loadModule(name, ld.Load); m == nil {
				continue
			}
			c.loadModules(m, append(stack, name))
			// Post-order: a module comes after those it loads.
			c.modOrder = append(c.modOrder, m)
		}
		for _, from := range ld.From {
			if _, ok := m.byName[from.Name]; !ok && from.Name != "" && from.Name[0] != '_' {
				c.errorf(from.NamePos, "load: name %s not found in module %s", from.Name, name)
			}
		}
	}
}

// loadModule parses and resolves the module name, loaded at pos; it
// returns nil after reporting an error.
func (c *compiler) loadModule(name string, pos syntax.Position) *unit {
	if c.opts.Loader == nil {
		c.errorf(pos, "cannot load %s: no loader (see Options.Loader)", name)
		return nil
	}
	src, err := c.opts.Loader(name)
	if err != nil {
		c.errorf(pos, "cannot load %s: %v", name, err)
		return nil
	}
	dialect := c.opts.Dialect
	file, err := dialect.Parse(name, src, 0)
	if err != nil {
		var se syntax.Error
		if errors.As(err, &se) {
			c.errs = append(c.errs, Error{se.Pos, se.Msg})
		} else {
			c.errorf(pos, "cannot load %s: %v", name, err)
		}
		return nil
	}
	if err := resolve.File(file, func(string) bool { return false }, c.universe); err != nil {
		var el resolve.ErrorList
		if !errors.As(err, &el) {
			c.errorf(pos, "cannot load %s: %v", name, err)
			return nil
		}
		for _, e := range el {
			c.errs = append(c.errs, Error{e.Pos, e.Msg})
		}
		return nil
	}
	u := c.newUnit("load:"+name, file, &dialect, false)
	u.module = name
	c.checkSupported(file)
	u.initFlag = c.m.AddGlobal(&Global{Name: u.name + ".loaded", Type: I32, Mutable: true})
	u.initFn = c.m.AddFunc(NewFunc(u.name+".init", nil))
	c.modules[name] = u
	return u
}

// compileModules compiles the top-level code of the loaded modules into
// their init functions, which run it once and then freeze the globals.
func (c *compiler) compileModules() {
	r := c.r
	for _, u := range c.modOrder {
		top := c.compileToplevel(u)
		u.initFn.Add(If(GGet(u.initFlag), Return(nil)), GSet(u.initFlag, I32c(1)), Call(top))
		for _, g := range u.globals {
			u.initFn.Add(r.Call("freeze", GGet(g)))
		}
	}
}

// load compiles a load statement: it runs the module's top-level code if
// it has not run yet and binds the loaded names. An error in the module
// appears at the load statement, as in go.starlark.net, where it is
// reported as "cannot load".
func (fc *fcomp) load(s *syntax.LoadStmt) Expr {
	r := fc.c.r
	name := s.ModuleName()
	m := fc.c.modules[name]
	code := Seq{fc.at(s.Load), r.LoadEnter(m.dbgDesc), Call(m.initFn), r.PopFrame()}
	for i, from := range s.From {
		v := fc.tmp()
		code = append(code,
			Set(v, GGet(m.byName[from.Name])),
			// A global that the module did not assign is not exported.
			If(I32Eqz(Get(v)), Seq{fc.here(), r.Fail("load: name " + from.Name + " not found in module " + name)}),
			fc.assign(s.To[i], Get(v), s.Load),
		)
	}
	return code
}

// metaFiles returns the source files for starc.meta if the program
// consists of several files.
func (c *compiler) metaFiles() []string {
	if c.opts.NoBacktrace || len(c.r.Files()) < 2 {
		return nil
	}
	return c.r.Files()
}
