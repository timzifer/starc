package compiler

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzPrograms generates whole programs from the fuzz input and checks
// them differentially. Unlike FuzzCompile, whose inputs the parser
// mostly rejects, every generated program compiles, so the fuzzer
// exercises the code generator: nested functions and closures, lambdas,
// comprehensions, loops, keyword arguments and mutation.
func FuzzPrograms(f *testing.F) {
	for _, seed := range []string{
		"",
		"\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10",
		"\x06\x02\x03\x01\x07\x05\x04\x01\x02\x05\x03\x07\x01\x06\x02\x04\x03\x05\x01\x02",
		strings.Repeat("\x05\x01\x02\x03", 12),
		strings.Repeat("\x01\x06\x03\x02\x07\x04\x05", 10),
		strings.Repeat("\xfe\x13\x37\x42\x99\x07", 16),
	} {
		f.Add([]byte(seed))
	}
	rt := newFuzzRuntime(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		// Half of the programs run with fuel metering, which must not
		// change the behavior.
		differential(t, rt, generate(data), len(data)%2 == 1)
	})
}

// genType is the static type of a generated expression. Static types
// keep most programs running to completion instead of stopping at the
// first type error.
type genType int

const (
	tInt genType = iota
	tFloat
	tStr
	tBool
	tList // list of int
	tDict // dict from string to int
	nTypes
)

type genVar struct {
	name string
	typ  genType
}

type genFunc struct {
	name     string
	params   []genVar
	defaults int // number of trailing parameters with a default value
	result   genType
}

// A progGen turns fuzz input into a Starlark program. Every choice
// consumes one byte; once the input is exhausted, every choice is 0,
// which always selects the simplest alternative, so generation ends.
//
// Programs terminate quickly: loops iterate over at most a few
// elements, functions call only functions defined before them (so there
// is no recursion), and ints are multiplied and shifted only by small
// constants. Strings and lists can still grow quickly, which the memory
// limits of differential catch.
type progGen struct {
	data   []byte
	b      strings.Builder
	indent int
	names  int
	stmts  int

	vars       []genVar  // variables in scope, innermost last
	funcs      []genFunc // functions in scope
	localStart int       // vars[localStart:] are local to the current function
	inFunc     bool
	loops      int // enclosing loops in the current function
}

func generate(data []byte) string {
	g := &progGen{data: data}
	n := 1 + g.choose(12)
	for i := 0; i < n; i++ {
		g.stmt(true)
	}
	// Print the final state, which also reveals wrong mutations.
	for _, v := range g.vars {
		g.line("print(%q, %s)", v.name, v.name)
	}
	return g.b.String()
}

func (g *progGen) choose(n int) int {
	if len(g.data) == 0 {
		return 0
	}
	c := int(g.data[0]) % n
	g.data = g.data[1:]
	return c
}

func (g *progGen) pick(alts ...string) string { return alts[g.choose(len(alts))] }

func (g *progGen) fresh(prefix string) string {
	g.names++
	return fmt.Sprintf("%s%d", prefix, g.names)
}

func (g *progGen) line(format string, args ...any) {
	g.b.WriteString(strings.Repeat("    ", g.indent))
	fmt.Fprintf(&g.b, format, args...)
	g.b.WriteByte('\n')
}

// block emits an indented block. Variables and functions defined in it
// go out of scope at its end, since they may not have been assigned.
func (g *progGen) block(top bool) {
	nv, nf := len(g.vars), len(g.funcs)
	g.indent++
	n := 1 + g.choose(3)
	for i := 0; i < n; i++ {
		g.stmt(top)
	}
	g.indent--
	g.vars, g.funcs = g.vars[:nv], g.funcs[:nf]
}

// stmt emits a statement. top reports whether it is directly in the
// module or in a function body, where def statements may appear.
func (g *progGen) stmt(top bool) {
	g.stmts++
	if g.stmts > 60 {
		g.line("pass")
		return
	}
	switch g.choose(12) {
	case 0:
		args := make([]string, 1+g.choose(3))
		for i := range args {
			args[i] = g.expr(g.anyType(), 0)
		}
		g.line("print(%s)", strings.Join(args, ", "))
	case 1, 2:
		t := g.anyType()
		e := g.expr(t, 0)
		v := genVar{g.fresh("v"), t}
		g.line("%s = %s", v.name, e)
		g.vars = append(g.vars, v)
	case 3:
		if v, ok := g.assignable(); ok {
			g.line("%s = %s", v.name, g.expr(v.typ, 0))
		} else {
			g.line("pass")
		}
	case 4:
		v, ok := g.assignable()
		switch {
		case ok && v.typ == tInt:
			g.line("%s %s %s", v.name, g.pick("+=", "-=", "//=", "%=", "&=", "|=", "^="), g.expr(tInt, 1))
		case ok && (v.typ == tFloat || v.typ == tStr || v.typ == tList):
			g.line("%s += %s", v.name, g.expr(v.typ, 1))
		case ok && v.typ == tDict:
			g.line("%s |= %s", v.name, g.expr(tDict, 1))
		default:
			g.line("pass")
		}
	case 5:
		g.line("if %s:", g.expr(tBool, 0))
		g.block(false)
		if g.choose(2) == 1 {
			g.line("elif %s:", g.expr(tBool, 0))
			g.block(false)
		}
		if g.choose(2) == 1 {
			g.line("else:")
			g.block(false)
		}
	case 6:
		if g.loops >= 2 {
			g.line("pass")
			return
		}
		g.loops++
		nv := len(g.vars)
		if g.choose(3) == 0 {
			x := genVar{g.fresh("x"), tInt}
			g.line("for %s in %s:", x.name, g.iterable())
			g.vars = append(g.vars, x)
		} else {
			i, x := genVar{g.fresh("i"), tInt}, genVar{g.fresh("x"), tInt}
			g.line("for %s, %s in enumerate(%s):", i.name, x.name, g.iterable())
			g.vars = append(g.vars, i, x)
		}
		g.block(false)
		if g.choose(2) == 1 {
			g.indent++
			g.line("%s", g.pick("pass", "continue", "break"))
			g.indent--
		}
		g.vars = g.vars[:nv]
		g.loops--
	case 7:
		if top && len(g.funcs) < 6 {
			g.def()
		} else {
			g.line("print(%s)", g.expr(g.anyType(), 0))
		}
	case 8:
		g.mutate()
	case 9:
		if g.loops > 0 && g.choose(2) == 1 {
			g.line("if %s:", g.expr(tBool, 1))
			g.indent++
			g.line("%s", g.pick("break", "continue"))
			g.indent--
		} else if c, ok := g.call(g.anyType(), 1); ok {
			g.line("%s", c)
		} else {
			g.line("pass")
		}
	case 10:
		a, b := genVar{g.fresh("v"), g.anyType()}, genVar{g.fresh("v"), g.anyType()}
		g.line("%s, %s = %s, %s", a.name, b.name, g.expr(a.typ, 0), g.expr(b.typ, 0))
		g.vars = append(g.vars, a, b)
	case 11:
		if g.inFunc && g.choose(2) == 1 {
			// An early return; the function's result type is unknown
			// here, so returning None checks only control flow.
			g.line("if %s:", g.expr(tBool, 1))
			g.indent++
			g.line("return None")
			g.indent--
		} else {
			g.line("print(%s)", g.expr(g.anyType(), 0))
		}
	}
}

// assignable returns a variable that may be assigned here: in a
// function, assigning a global would make it local everywhere in the
// function, so only locals qualify.
func (g *progGen) assignable() (genVar, bool) {
	vs := g.vars[g.localStart:]
	if len(vs) == 0 {
		return genVar{}, false
	}
	return vs[g.choose(len(vs))], true
}

// mutate emits a statement that mutates a list or dict.
func (g *progGen) mutate() {
	var l, d []genVar
	for _, v := range g.vars {
		switch v.typ {
		case tList:
			l = append(l, v)
		case tDict:
			d = append(d, v)
		}
	}
	if len(l) > 0 && (len(d) == 0 || g.choose(2) == 0) {
		v := l[g.choose(len(l))].name
		switch g.choose(7) {
		case 0:
			g.line("%s.append(%s)", v, g.expr(tInt, 1))
		case 1:
			g.line("%s.extend(%s)", v, g.expr(tList, 1))
		case 2:
			g.line("%s.insert(%s, %s)", v, g.expr(tInt, 2), g.expr(tInt, 1))
		case 3:
			g.line("%s[%s] = %s", v, g.expr(tInt, 2), g.expr(tInt, 1))
		case 4:
			g.line("%s.remove(%s)", v, g.expr(tInt, 2))
		case 5:
			g.line("%s.pop(%s)", v, g.pick("", "0", "-1", g.expr(tInt, 2)))
		case 6:
			g.line("%s.clear()", v)
		}
		return
	}
	if len(d) > 0 {
		v := d[g.choose(len(d))].name
		switch g.choose(6) {
		case 0:
			g.line("%s[%s] = %s", v, g.expr(tStr, 2), g.expr(tInt, 1))
		case 1:
			g.line("%s.update(%s)", v, g.expr(tDict, 1))
		case 2:
			g.line("%s.setdefault(%s, %s)", v, g.expr(tStr, 2), g.expr(tInt, 2))
		case 3:
			g.line("%s.pop(%s, None)", v, g.expr(tStr, 2))
		case 4:
			g.line("%s.popitem()", v)
		case 5:
			g.line("%s.clear()", v)
		}
		return
	}
	g.line("pass")
}

// def emits a function definition. Its body sees the variables in scope
// and its parameters, and may call only functions defined before it.
func (g *progGen) def() {
	fn := genFunc{name: g.fresh("f"), result: g.anyType()}
	params := make([]string, g.choose(4))
	for i := range params {
		p := genVar{g.fresh("p"), g.anyType()}
		fn.params = append(fn.params, p)
		params[i] = p.name
	}
	fn.defaults = g.choose(len(params) + 1)
	for i := len(params) - fn.defaults; i < len(params); i++ {
		params[i] += " = " + g.literal(fn.params[i].typ)
	}
	g.line("def %s(%s):", fn.name, strings.Join(params, ", "))

	nv, nf := len(g.vars), len(g.funcs)
	localStart, inFunc, loops := g.localStart, g.inFunc, g.loops
	g.localStart, g.inFunc, g.loops = len(g.vars), true, 0
	g.vars = append(g.vars, fn.params...)
	g.indent++
	n := g.choose(4)
	for i := 0; i < n; i++ {
		g.stmt(true)
	}
	g.line("return %s", g.expr(fn.result, 0))
	g.indent--
	g.vars, g.funcs = g.vars[:nv], g.funcs[:nf]
	g.localStart, g.inFunc, g.loops = localStart, inFunc, loops
	g.funcs = append(g.funcs, fn)
}

// call returns a call of a function with result type t, if there is one.
func (g *progGen) call(t genType, depth int) (string, bool) {
	var fs []genFunc
	for _, f := range g.funcs {
		if f.result == t {
			fs = append(fs, f)
		}
	}
	if len(fs) == 0 {
		return "", false
	}
	f := fs[g.choose(len(fs))]
	var args []string
	required := len(f.params) - f.defaults
	keywords := g.choose(2) == 1
	for i, p := range f.params {
		switch {
		case i < required && !keywords:
			args = append(args, g.expr(p.typ, depth))
		case i < required || g.choose(2) == 1:
			args = append(args, p.name+" = "+g.expr(p.typ, depth))
		}
	}
	return fmt.Sprintf("%s(%s)", f.name, strings.Join(args, ", ")), true
}

func (g *progGen) anyType() genType { return genType(g.choose(int(nTypes))) }

// iterable returns a list of ints with at most a few elements, which
// bounds the work of nested loops.
func (g *progGen) iterable() string {
	switch g.choose(3) {
	case 0:
		return fmt.Sprintf("range(%d)", g.choose(5))
	case 1:
		return g.literal(tList)
	default:
		return fmt.Sprintf("%s[:%d]", g.expr(tList, 2), 1+g.choose(4))
	}
}

const maxDepth = 4

// expr returns an expression of type t. Composite expressions are
// parenthesized, so precedence never matters.
func (g *progGen) expr(t genType, depth int) string {
	if depth >= maxDepth || g.choose(3) == 0 {
		return g.leaf(t)
	}
	depth++
	switch g.choose(8) {
	case 0:
		return fmt.Sprintf("(%s if %s else %s)", g.expr(t, depth), g.expr(tBool, depth), g.expr(t, depth))
	case 1:
		if c, ok := g.call(t, depth); ok {
			return c
		}
	case 2:
		// A lambda that is called at once; it may capture locals.
		p := genVar{g.fresh("a"), g.anyType()}
		arg := g.expr(p.typ, depth)
		g.vars = append(g.vars, p)
		body := g.expr(t, depth)
		g.vars = g.vars[:len(g.vars)-1]
		return fmt.Sprintf("(lambda %s: %s)(%s)", p.name, body, arg)
	}
	switch t {
	case tInt:
		return g.intExpr(depth)
	case tFloat:
		return g.floatExpr(depth)
	case tStr:
		return g.strExpr(depth)
	case tBool:
		return g.boolExpr(depth)
	case tList:
		return g.listExpr(depth)
	default:
		return g.dictExpr(depth)
	}
}

func (g *progGen) leaf(t genType) string {
	var vs []string
	for _, v := range g.vars {
		if v.typ == t {
			vs = append(vs, v.name)
		}
	}
	if len(vs) > 0 && g.choose(2) == 1 {
		return vs[g.choose(len(vs))]
	}
	return g.literal(t)
}

func (g *progGen) literal(t genType) string {
	switch t {
	case tInt:
		return g.pick("0", "1", "2", "3", "(-1)", "7", "10", "(-12)", "100", "255", "2147483647",
			"(-2147483648)", "4294967296", "4611686018427387904", "9223372036854775807", "(-9223372036854775807 - 1)")
	case tFloat:
		return g.pick("0.0", "1.5", "(-2.25)", "0.1", "1e300", "(-1e-300)", "3.0", `float("inf")`,
			`float("-inf")`, `float("nan")`, "(-0.0)", "1e16", "123.456", "5e-324")
	case tStr:
		return g.pick(`""`, `"a"`, `"ab"`, `" a b "`, `"A,b,,c"`, `"x\ty"`, `"10"`, `"-3"`,
			`"Hello World"`, `"a\nb\n"`, `"1.5"`, `"%"`, `"{}"`, `"aaa"`)
	case tBool:
		return g.pick("False", "True")
	case tList:
		elems := make([]string, g.choose(4))
		for i := range elems {
			elems[i] = g.literal(tInt)
		}
		return "[" + strings.Join(elems, ", ") + "]"
	default:
		elems := make([]string, g.choose(3))
		for i := range elems {
			elems[i] = g.literal(tStr) + ": " + g.literal(tInt)
		}
		return "{" + strings.Join(elems, ", ") + "}"
	}
}

func (g *progGen) smallInt() string { return g.pick("0", "1", "2", "3", "(-1)", "(-2)") }

func (g *progGen) intExpr(d int) string {
	switch g.choose(12) {
	case 0:
		return fmt.Sprintf("(%s %s %s)", g.expr(tInt, d), g.pick("+", "-", "//", "%", "&", "|", "^"), g.expr(tInt, d))
	case 1:
		// Multiplying and shifting only by small constants keeps ints
		// small where the interpreter goes on with big integers.
		return fmt.Sprintf("(%s * %s)", g.expr(tInt, d), g.smallInt())
	case 2:
		return fmt.Sprintf("(%s%s)", g.pick("-", "~", "+"), g.expr(tInt, d))
	case 3:
		return fmt.Sprintf("(%s %s %d)", g.expr(tInt, d), g.pick("<<", ">>"), g.choose(9)*g.choose(9))
	case 4:
		return fmt.Sprintf("len(%s)", g.expr(g.pickType(tStr, tList, tDict), d))
	case 5:
		if g.choose(2) == 0 {
			return fmt.Sprintf("hash(%s)", g.expr(tStr, d))
		}
		return fmt.Sprintf("%s(%s)", g.pick("abs", "int"), g.expr(tInt, d))
	case 6:
		return fmt.Sprintf("%s(%s, %s)", g.pick("min", "max"), g.expr(tInt, d), g.expr(tInt, d))
	case 7:
		return fmt.Sprintf("int(%s)", g.expr(g.pickType(tFloat, tStr, tBool), d))
	case 8:
		return fmt.Sprintf("%s.%s(%s)", g.expr(tStr, d), g.pick("find", "rfind", "count"), g.expr(tStr, d))
	case 9:
		l := g.expr(tList, d)
		switch g.choose(4) {
		case 0:
			return fmt.Sprintf("%s[%s]", l, g.expr(tInt, d))
		case 1:
			return fmt.Sprintf("%s(%s)", g.pick("min", "max"), l)
		case 2:
			return fmt.Sprintf("%s.index(%s)", l, g.expr(tInt, d))
		default:
			return fmt.Sprintf("%s.pop()", l)
		}
	case 10:
		dct := g.expr(tDict, d)
		if g.choose(2) == 0 {
			return fmt.Sprintf("%s[%s]", dct, g.expr(tStr, d))
		}
		return fmt.Sprintf("%s.get(%s, %s)", dct, g.expr(tStr, d), g.expr(tInt, d))
	default:
		return fmt.Sprintf("ord((%s + \"_\")[0])", g.expr(tStr, d))
	}
}

func (g *progGen) floatExpr(d int) string {
	switch g.choose(6) {
	case 0:
		return fmt.Sprintf("(%s %s %s)", g.expr(tFloat, d), g.pick("+", "-", "*", "/", "//", "%"), g.expr(tFloat, d))
	case 1:
		return fmt.Sprintf("(%s %s %s)", g.expr(tFloat, d), g.pick("+", "-", "*", "/", "//", "%"), g.expr(tInt, d))
	case 2:
		return fmt.Sprintf("(%s %s %s)", g.expr(tInt, d), g.pick("+", "-", "*", "/", "//", "%"), g.expr(tFloat, d))
	case 3:
		return fmt.Sprintf("float(%s)", g.expr(g.pickType(tInt, tFloat, tStr, tBool), d))
	case 4:
		return fmt.Sprintf("(%s%s)", g.pick("-", "+"), g.expr(tFloat, d))
	default:
		return fmt.Sprintf("%s(%s, %s)", g.pick("min", "max"), g.expr(tFloat, d), g.expr(tFloat, d))
	}
}

func (g *progGen) strExpr(d int) string {
	s := func() string { return g.expr(tStr, d) }
	switch g.choose(10) {
	case 0:
		return fmt.Sprintf("(%s + %s)", s(), s())
	case 1:
		return fmt.Sprintf("(%s * %s)", s(), g.smallInt())
	case 2:
		return fmt.Sprintf("%s(%s)", g.pick("str", "repr"), g.expr(g.anyType(), d))
	case 3:
		m := g.pick("upper", "lower", "title", "capitalize", "strip", "lstrip", "rstrip")
		return fmt.Sprintf("%s.%s()", s(), m)
	case 4:
		m := g.pick("replace", "removeprefix", "removesuffix", "strip", "lstrip", "rstrip")
		if m == "replace" {
			return fmt.Sprintf("%s.replace(%s, %s)", s(), g.literal(tStr), g.literal(tStr))
		}
		return fmt.Sprintf("%s.%s(%s)", s(), m, s())
	case 5:
		switch g.choose(4) {
		case 0:
			return fmt.Sprintf("%s[%s]", s(), g.expr(tInt, d))
		case 1:
			return fmt.Sprintf("%s[%s:%s]", s(), g.expr(tInt, d), g.expr(tInt, d))
		case 2:
			return fmt.Sprintf("%s[::%s]", s(), g.smallInt())
		default:
			return fmt.Sprintf("%s[%s::%s]", s(), g.expr(tInt, d), g.smallInt())
		}
	case 6:
		sep := g.literal(tStr)
		switch g.choose(5) {
		case 0:
			return fmt.Sprintf("%s.join(%s.split(%s))", sep, s(), g.pick(`","`, `" "`, `"a"`, ""))
		case 1:
			return fmt.Sprintf("%s.join(%s.elems())", sep, s())
		case 2:
			return fmt.Sprintf("%s.join(%s.splitlines())", sep, s())
		case 3:
			return fmt.Sprintf("%s.join(%s.keys())", sep, g.expr(tDict, d))
		default:
			return fmt.Sprintf("%s.join([%s, %s])", sep, s(), s())
		}
	case 7:
		return fmt.Sprintf("(%s %% (%s, %s, %s, %s))", g.pick(`"%s|%d|%r|%x"`, `"%r %o %s %X"`, `"%s%i%s%d"`),
			s(), g.expr(tInt, d), g.expr(g.anyType(), d), g.expr(tInt, d))
	case 8:
		return fmt.Sprintf("%s.format(%s, %s)", g.pick(`"{}-{!r}"`, `"{1}{0}{1}"`, `"{0!r}/{1!s}"`),
			g.expr(g.anyType(), d), g.expr(g.anyType(), d))
	default:
		sep := g.pick(`","`, `" "`, `"a"`)
		switch g.choose(3) {
		case 0:
			return fmt.Sprintf("%s.partition(%s)[%d]", s(), sep, g.choose(3))
		case 1:
			return fmt.Sprintf("%s.rpartition(%s)[%d]", s(), sep, g.choose(3))
		default:
			return fmt.Sprintf("%s.rsplit(%s, 1)[%d]", s(), sep, -g.choose(2))
		}
	}
}

func (g *progGen) boolExpr(d int) string {
	switch g.choose(8) {
	case 0:
		t := g.pickType(tInt, tFloat, tStr, tList)
		return fmt.Sprintf("(%s %s %s)", g.expr(t, d), g.pick("<", "<=", ">", ">=", "==", "!="), g.expr(t, d))
	case 1:
		t := g.pickType(tBool, tDict, tInt)
		u := g.pickType(t, tFloat, tStr)
		return fmt.Sprintf("(%s %s %s)", g.expr(t, d), g.pick("==", "!="), g.expr(u, d))
	case 2:
		return fmt.Sprintf("(%s %s %s)", g.expr(tInt, d), g.pick("<", ">=", "=="), g.expr(tFloat, d))
	case 3:
		return fmt.Sprintf("(%s %s %s)", g.expr(tBool, d), g.pick("and", "or"), g.expr(tBool, d))
	case 4:
		return fmt.Sprintf("(not %s)", g.expr(tBool, d))
	case 5:
		in := g.pick("in", "not in")
		switch g.choose(3) {
		case 0:
			return fmt.Sprintf("(%s %s %s)", g.expr(tStr, d), in, g.expr(tStr, d))
		case 1:
			return fmt.Sprintf("(%s %s %s)", g.expr(tInt, d), in, g.expr(tList, d))
		default:
			return fmt.Sprintf("(%s %s %s)", g.expr(tStr, d), in, g.expr(tDict, d))
		}
	case 6:
		m := g.pick("isdigit", "isalpha", "isalnum", "isupper", "islower", "isspace", "istitle")
		if g.choose(2) == 0 {
			return fmt.Sprintf("%s.%s()", g.expr(tStr, d), m)
		}
		return fmt.Sprintf("%s.%s(%s)", g.expr(tStr, d), g.pick("startswith", "endswith"), g.expr(tStr, d))
	default:
		return fmt.Sprintf("%s(%s)", g.pick("bool", "any", "all"), g.expr(tList, d))
	}
}

func (g *progGen) listExpr(d int) string {
	l := func() string { return g.expr(tList, d) }
	switch g.choose(9) {
	case 0:
		elems := make([]string, g.choose(4))
		for i := range elems {
			elems[i] = g.expr(tInt, d)
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case 1:
		return fmt.Sprintf("(%s + %s)", l(), l())
	case 2:
		return fmt.Sprintf("(%s * %s)", l(), g.smallInt())
	case 3:
		switch g.choose(3) {
		case 0:
			return fmt.Sprintf("sorted(%s)", l())
		case 1:
			return fmt.Sprintf("sorted(%s, reverse = %s)", l(), g.expr(tBool, d))
		default:
			return fmt.Sprintf("list(reversed(%s))", l())
		}
	case 4:
		switch g.choose(3) {
		case 0:
			return fmt.Sprintf("%s[%s:%s]", l(), g.expr(tInt, d), g.expr(tInt, d))
		case 1:
			return fmt.Sprintf("%s[::%s]", l(), g.smallInt())
		default:
			return fmt.Sprintf("%s[%s:%s:%s]", l(), g.expr(tInt, d), g.expr(tInt, d), g.smallInt())
		}
	case 5:
		src := l()
		x := genVar{g.fresh("c"), tInt}
		g.vars = append(g.vars, x)
		e, cond := g.expr(tInt, d), g.expr(tBool, d)
		g.vars = g.vars[:len(g.vars)-1]
		if g.choose(2) == 0 {
			return fmt.Sprintf("[%s for %s in %s]", e, x.name, src)
		}
		return fmt.Sprintf("[%s for %s in %s if %s]", e, x.name, src, cond)
	case 6:
		return fmt.Sprintf("list(range(%d, %d, %s))", g.choose(9)-4, g.choose(9)-4, g.pick("1", "2", "(-1)", "3"))
	case 7:
		switch g.choose(3) {
		case 0:
			return fmt.Sprintf("list(%s.values())", g.expr(tDict, d))
		case 1:
			return fmt.Sprintf("list(%s.%s())", g.expr(tStr, d), g.pick("elem_ords", "codepoint_ords"))
		default:
			return fmt.Sprintf("[len(%s), %s]", g.expr(tStr, d), g.expr(tInt, d))
		}
	default:
		return fmt.Sprintf("list(%s)", g.pick(g.expr(tList, d), fmt.Sprintf("range(%d)", g.choose(6))))
	}
}

func (g *progGen) dictExpr(d int) string {
	switch g.choose(5) {
	case 0:
		elems := make([]string, g.choose(3))
		for i := range elems {
			elems[i] = g.expr(tStr, d) + ": " + g.expr(tInt, d)
		}
		return "{" + strings.Join(elems, ", ") + "}"
	case 1:
		return fmt.Sprintf("(%s | %s)", g.expr(tDict, d), g.expr(tDict, d))
	case 2:
		return fmt.Sprintf("dict(%s)", g.expr(tDict, d))
	case 3:
		src := g.expr(tList, d)
		x := genVar{g.fresh("c"), tInt}
		g.vars = append(g.vars, x)
		k, v := g.expr(tStr, d), g.expr(tInt, d)
		g.vars = g.vars[:len(g.vars)-1]
		return fmt.Sprintf("{%s: %s for %s in %s}", k, v, x.name, src)
	default:
		return fmt.Sprintf("dict(%s = %s, %s = %s)", g.pick("a", "b"), g.expr(tInt, d), g.pick("c", "d"), g.expr(tInt, d))
	}
}

func (g *progGen) pickType(ts ...genType) genType { return ts[g.choose(len(ts))] }
