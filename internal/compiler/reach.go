package compiler

import (
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

// methodPrefixes maps prelude name prefixes to the type whose methods
// they implement.
var methodPrefixes = []string{"_str_", "_list_", "_dict_", "_set_"}

// preludeShow returns the name of a prelude function in backtraces, as
// the built-in it implements, or "" for an internal helper.
func preludeShow(name string) string {
	for _, p := range methodPrefixes {
		if strings.HasPrefix(name, p) {
			return name[len(p):]
		}
	}
	if strings.HasPrefix(name, "_") || name == "lambda" {
		return ""
	}
	return name
}

// reach computes which prelude definitions and primitive methods the
// program can use, so that unused library code is not compiled.
type reach struct {
	c       *compiler
	defs    map[string]bool // included prelude defs
	queue   []string
	attrs   map[string]bool // attribute names used
	dynamic bool            // attributes accessed by name at run time
	// modDefs are the droppable defs of loaded modules that are used.
	modDefs map[*unit]map[string]bool
}

func (c *compiler) computeReach() *reach {
	rc := &reach{c: c, defs: map[string]bool{}, attrs: map[string]bool{}, modDefs: map[*unit]map[string]bool{}}
	rc.walk(c.user.file, false)
	for _, u := range c.modOrder {
		rc.walkModule(u)
	}
	for _, u := range append([]*unit{c.user}, c.modOrder...) {
		for _, s := range u.file.Stmts {
			if ld, ok := s.(*syntax.LoadStmt); ok {
				for _, from := range ld.From {
					rc.needModule(c.modules[ld.ModuleName()], from.Name)
				}
			}
		}
	}
	for _, s := range c.prelude.file.Stmts {
		if _, ok := s.(*syntax.DefStmt); !ok {
			rc.walk(s, true)
		}
	}
	for len(rc.queue) > 0 {
		name := rc.queue[0]
		rc.queue = rc.queue[1:]
		rc.walk(c.prelude.defs[name], true)
	}
	return rc
}

func (rc *reach) need(name string) {
	if _, ok := rc.c.prelude.defs[name]; ok && !rc.defs[name] {
		rc.defs[name] = true
		rc.queue = append(rc.queue, name)
	}
}

func (rc *reach) attr(name string) {
	if rc.attrs[name] {
		return
	}
	rc.attrs[name] = true
	for _, p := range methodPrefixes {
		rc.need(p + name)
	}
}

func (rc *reach) walk(n syntax.Node, prelude bool) { rc.walkIn(n, prelude, nil) }

// walkIn is walk for a node of the loaded module u (nil for the program
// or the prelude).
func (rc *reach) walkIn(n syntax.Node, prelude bool, u *unit) {
	syntax.Walk(n, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Ident:
			b, _ := n.Binding.(*resolve.Binding)
			if b == nil {
				break
			}
			if u != nil && b.Scope == resolve.Global {
				rc.needModule(u, n.Name)
			}
			if (prelude && b.Scope == resolve.Global) || (!prelude && b.Scope == resolve.Universal) {
				rc.need(n.Name)
			}
			if b.Scope == resolve.Universal && (n.Name == "getattr" || n.Name == "hasattr" || n.Name == "dir") {
				rc.dynamicAccess()
			}
		case *syntax.DotExpr:
			rc.attr(n.Name.Name)
		case *syntax.BinaryExpr:
			if n.Op == syntax.PERCENT {
				rc.need("_percent_format")
			}
		case *syntax.AssignStmt:
			if n.Op == syntax.PERCENT_EQ {
				rc.need("_percent_format")
			}
		}
		return true
	})
}

func (rc *reach) dynamicAccess() {
	if rc.dynamic {
		return
	}
	rc.dynamic = true
	for name := range rc.c.prelude.defs {
		for _, p := range methodPrefixes {
			if strings.HasPrefix(name, p) {
				rc.need(name)
			}
		}
	}
}

// usesMethod reports whether a method name may be looked up.
func (rc *reach) usesMethod(name string) bool { return rc.dynamic || rc.attrs[name] }

// A def of a loaded module that nothing uses is not compiled, like the
// unused definitions of the prelude, if dropping it cannot change what
// the module does: the default values of its parameters must be
// constants, since they are evaluated when the def runs.

// droppable reports whether the defs of name in u can be dropped.
func droppable(u *unit, name string) bool {
	found := false
	for _, s := range u.file.Stmts {
		d, ok := s.(*syntax.DefStmt)
		if !ok || d.Name.Name != name {
			continue
		}
		found = true
		for _, p := range d.Params {
			b, ok := p.(*syntax.BinaryExpr)
			if !ok {
				continue
			}
			switch y := b.Y.(type) {
			case *syntax.Literal:
			case *syntax.Ident:
				if bd, _ := y.Binding.(*resolve.Binding); bd == nil || bd.Scope != resolve.Universal {
					return false
				}
			default:
				return false
			}
		}
	}
	return found
}

// walkModule walks the code of the loaded module u that always runs:
// everything but its droppable defs.
func (rc *reach) walkModule(u *unit) {
	rc.modDefs[u] = map[string]bool{}
	for _, s := range u.file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && droppable(u, d.Name.Name) {
			continue
		}
		rc.walkIn(s, false, u)
	}
}

// needModule records that the global name of the loaded module u is used.
func (rc *reach) needModule(u *unit, name string) {
	if rc.modDefs[u][name] || !droppable(u, name) {
		return
	}
	rc.modDefs[u][name] = true
	for _, s := range u.file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && d.Name.Name == name {
			rc.walkIn(d, false, u)
		}
	}
}

// moduleDefUsed reports whether the def of name in the loaded module u
// must be compiled.
func (rc *reach) moduleDefUsed(u *unit, name string) bool {
	return rc.modDefs[u][name] || !droppable(u, name)
}
