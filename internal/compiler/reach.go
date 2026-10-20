package compiler

import (
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

// methodPrefixes maps prelude name prefixes to the type whose methods
// they implement.
var methodPrefixes = []string{"_str_", "_list_", "_dict_"}

// reach computes which prelude definitions and primitive methods the
// program can use, so that unused library code is not compiled.
type reach struct {
	c       *compiler
	defs    map[string]bool // included prelude defs
	queue   []string
	attrs   map[string]bool // attribute names used
	dynamic bool            // attributes accessed by name at run time
}

func (c *compiler) computeReach() *reach {
	rc := &reach{c: c, defs: map[string]bool{}, attrs: map[string]bool{}}
	rc.walk(c.user.file, false)
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

func (rc *reach) walk(n syntax.Node, prelude bool) {
	syntax.Walk(n, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Ident:
			b, _ := n.Binding.(*resolve.Binding)
			if b == nil {
				break
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
