package compiler

import (
	"fmt"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"

	"github.com/timzifer/starc/internal/rt"
	. "github.com/timzifer/starc/internal/wasm"
)

// hostNode is a predeclared host name: a host function, or a module
// whose members are host functions and modules.
type hostNode struct {
	name     string // dotted name
	fn       bool
	children map[string]*hostNode
	addr     uint32 // static object, once created
}

// hostTree validates the dotted host function names and arranges them
// as a tree of predeclared modules, keyed by the top-level names.
func hostTree(names []string) (map[string]*hostNode, error) {
	top := map[string]*hostNode{}
	for _, name := range names {
		parts, err := hostName(name)
		if err != nil {
			return nil, err
		}
		level := top
		for i, p := range parts {
			full := strings.Join(parts[:i+1], ".")
			leaf := i == len(parts)-1
			n := level[p]
			if n == nil {
				n = &hostNode{name: full, fn: leaf}
				if !leaf {
					n.children = map[string]*hostNode{}
				}
				level[p] = n
			} else if n.fn != leaf {
				return nil, fmt.Errorf("host function %q: %q is both a function and a module", name, full)
			}
			level = n.children
		}
	}
	return top, nil
}

// hostName splits a host function name into its identifiers.
func hostName(name string) ([]string, error) {
	bad := func(why string) ([]string, error) {
		return nil, fmt.Errorf("invalid host function name %q: %s", name, why)
	}
	x, err := (&syntax.FileOptions{}).ParseExpr("<host>", name, 0)
	if err != nil {
		return bad("want identifiers separated by dots")
	}
	var parts []string
	for {
		if d, ok := x.(*syntax.DotExpr); ok {
			parts = append(parts, d.Name.Name)
			x = d.X
			continue
		}
		id, ok := x.(*syntax.Ident)
		if !ok {
			return bad("want identifiers separated by dots")
		}
		parts = append(parts, id.Name)
		break
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	if strings.Join(parts, ".") != name {
		return bad("want identifiers separated by dots")
	}
	switch top := parts[0]; {
	case top == "None" || top == "True" || top == "False":
		return bad(top + " cannot be redefined")
	case strings.HasPrefix(top, "starc_"):
		return bad("the prefix starc_ is reserved")
	}
	return parts, nil
}

// hostValue returns the static object for a host name, creating it and,
// for a module, all of its members on first use.
func (c *compiler) hostValue(n *hostNode) uint32 {
	if n.addr != 0 {
		return n.addr
	}
	if n.fn {
		n.addr = c.r.HostBuiltin(n.name)
		return n.addr
	}
	members := make([]rt.Member, 0, len(n.children))
	for _, name := range sortedKeys(n.children) {
		members = append(members, rt.Member{Name: name, Value: c.hostValue(n.children[name])})
	}
	n.addr = c.r.Module(n.name, members)
	return n.addr
}

// hostRef reports whether e statically denotes a predeclared host
// module or a member of one, and resolves it. A missing member is a
// compile error, reported here; n is nil then.
func (c *compiler) hostRef(e syntax.Expr) (n *hostNode, ok bool) {
	switch e := unparen(e).(type) {
	case *syntax.Ident:
		b, _ := e.Binding.(*resolve.Binding)
		if b == nil || b.Scope != resolve.Universal {
			return nil, false
		}
		n, ok = c.host[e.Name]
		return n, ok
	case *syntax.DotExpr:
		m, ok := c.hostRef(e.X)
		if !ok || (m != nil && m.fn) {
			return nil, false
		}
		if m == nil {
			return nil, true
		}
		if n = m.children[e.Name.Name]; n == nil {
			c.errorf(e.Name.NamePos, "module %s has no .%s field or method", m.name, e.Name.Name)
		}
		return n, true
	}
	return nil, false
}

// hostExpr yields the value of a resolved host reference.
func (c *compiler) hostExpr(n *hostNode) Expr {
	if n == nil {
		return c.r.NoneV()
	}
	return U32c(c.hostValue(n))
}
