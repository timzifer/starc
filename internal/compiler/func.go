package compiler

import (
	"go.starlark.net/resolve"
	"go.starlark.net/syntax"

	"github.com/timzifer/starc/internal/rt"
	. "github.com/timzifer/starc/internal/wasm"
)

// fcomp compiles one function body (or a unit's top-level code).
type fcomp struct {
	c      *compiler
	u      *unit
	f      *Func
	locals []Local // by binding index
	free   []Local // cells of free variables
	self   Local
	loops  []*loop
	spec   uint32 // non-zero if a recursion guard is active
}

type loop struct {
	brk, cont *Label
	iterable  Local // the iterated value, for iter_end; -1 if none
	hasIter   bool
}

func (fc *fcomp) tmp() Local { return fc.f.Local(I32) }

// compileToplevel compiles the top-level statements of a unit.
func (c *compiler) compileToplevel(u *unit) *Func {
	f := c.m.AddFunc(NewFunc(u.name+".toplevel", nil))
	fc := &fcomp{c: c, u: u, f: f}
	mod := u.file.Module.(*resolve.Module)
	for _, b := range mod.Locals {
		l := f.Local(I32)
		fc.locals = append(fc.locals, l)
		if b.Scope == resolve.Cell {
			f.Add(Set(l, c.r.Call("cell", I32c(0))))
		}
	}
	f.Add(fc.stmts(u.file.Stmts)...)
	return f
}

// function compiles a def or lambda and returns the code that creates
// the function object.
func (fc *fcomp) function(fn *resolve.Function, outer *fcomp) Expr {
	c, r := fc.c, fc.c.r
	c.nfunc++
	f := c.m.AddFunc(NewFunc(fc.u.name+"."+fn.Name, rt.CallType.Params, rt.CallType.Results...))
	inner := &fcomp{c: c, u: fc.u, f: f, self: f.Param(2)}

	// Parameters.
	var names []string
	var defaults []Expr // by named-parameter index, nil if required
	flags := 0
	for _, p := range fn.Params {
		switch p := p.(type) {
		case *syntax.Ident:
			names = append(names, p.Name)
			defaults = append(defaults, nil)
		case *syntax.BinaryExpr:
			names = append(names, p.X.(*syntax.Ident).Name)
			defaults = append(defaults, outer.expr(p.Y))
		}
	}
	if fn.HasVarargs {
		flags |= rt.SpecVarargs
	}
	if fn.HasKwargs {
		flags |= rt.SpecKwargs
	}
	recursion := fc.u.opts.Recursion
	if recursion {
		flags |= rt.SpecRecursive
	}
	spec := r.Spec(fn.Name, names, fn.NumKwonlyParams, flags)
	nparams := len(names)
	if fn.HasVarargs {
		nparams++
	}
	if fn.HasKwargs {
		nparams++
	}

	// Prologue.
	if !recursion {
		inner.spec = spec
		c.specs = append(c.specs, spec)
		f.Add(r.Call("enter", U32c(spec)))
	}
	slots := f.Local(I32)
	f.Add(Set(slots, r.Call("bind", Get(f.Param(2)), Get(f.Param(0)), Get(f.Param(1)))))
	for i, b := range fn.Locals {
		l := f.Local(I32)
		inner.locals = append(inner.locals, l)
		if i < nparams {
			f.Add(Set(l, r.Slot(Get(slots), i)))
			if b.Scope == resolve.Cell {
				f.Add(Set(l, r.Call("cell", Get(l))))
			}
		} else if b.Scope == resolve.Cell {
			f.Add(Set(l, r.Call("cell", I32c(0))))
		}
	}
	for i := range fn.FreeVars {
		l := f.Local(I32)
		inner.free = append(inner.free, l)
		f.Add(Set(l, r.FreeVar(Get(f.Param(2)), i)))
	}
	f.Add(inner.stmts(fn.Body)...)
	f.Add(inner.ret(r.NoneV()))

	// Closure construction in the enclosing function.
	defs := I32c(0)
	anyDefault := false
	for _, d := range defaults {
		if d != nil {
			anyDefault = true
		}
	}
	code := Seq{}
	if anyDefault {
		t := fc.tmp()
		code = append(code, Set(t, r.Call("tuple_new", I32c(int32(len(names))))))
		for i, d := range defaults {
			if d != nil {
				code = append(code, r.TupleSet(Get(t), i, d))
			}
		}
		defs = Get(t)
	}
	freev := I32c(0)
	if len(fn.FreeVars) > 0 {
		t := fc.tmp()
		code = append(code, Set(t, r.Call("tuple_new", I32c(int32(len(fn.FreeVars))))))
		for i, fv := range fn.FreeVars {
			var cell Expr
			switch fv.Scope {
			case resolve.Cell:
				cell = Get(fc.locals[fv.Index])
			case resolve.Free:
				cell = Get(fc.free[fv.Index])
			default:
				panic("compiler: unexpected free variable scope " + fv.Scope.String())
			}
			code = append(code, r.TupleSet(Get(t), i, cell))
		}
		freev = Get(t)
	}
	code = append(code, r.Call("func_new", TableIndex(f), U32c(spec), defs, freev))
	return code
}

// ret yields code returning v from the current function, releasing
// iterators of enclosing loops and the recursion guard.
func (fc *fcomp) ret(v Expr) Expr {
	r := fc.c.r
	t := fc.tmp()
	code := Seq{Set(t, v)}
	for i := len(fc.loops) - 1; i >= 0; i-- {
		if l := fc.loops[i]; l.hasIter {
			code = append(code, r.Call("iter_end", Get(l.iterable)))
		}
	}
	if fc.spec != 0 {
		code = append(code, r.Leave(fc.spec))
	}
	return append(code, Return(Get(t)))
}

// ---- statements ----

func (fc *fcomp) stmts(stmts []syntax.Stmt) []Expr {
	var out []Expr
	for _, s := range stmts {
		out = append(out, fc.stmt(s))
	}
	return out
}

func (fc *fcomp) stmt(s syntax.Stmt) Expr {
	r := fc.c.r
	switch s := s.(type) {
	case *syntax.ExprStmt:
		return Drop(fc.expr(s.X))

	case *syntax.AssignStmt:
		if s.Op == syntax.EQ {
			return fc.assign(s.LHS, fc.expr(s.RHS))
		}
		op := augmentedOps[s.Op]
		switch lhs := unparen(s.LHS).(type) {
		case *syntax.Ident:
			return fc.assign(lhs, fc.binop(op, fc.ident(lhs), fc.expr(s.RHS), true))
		case *syntax.IndexExpr:
			x, y := fc.tmp(), fc.tmp()
			return Seq{
				Set(x, fc.expr(lhs.X)),
				Set(y, fc.expr(lhs.Y)),
				r.Call("setindex", Get(x), Get(y), fc.binop(op, r.Call("index", Get(x), Get(y)), fc.expr(s.RHS), true)),
			}
		default:
			fc.c.errorf(s.OpPos, "invalid augmented assignment target")
			return Nop()
		}

	case *syntax.IfStmt:
		return IfElse(r.Call("truth", fc.expr(s.Cond)), fc.stmts(s.True), fc.stmts(s.False))

	case *syntax.ForStmt:
		x, seq, n, i := fc.tmp(), fc.tmp(), fc.tmp(), fc.tmp()
		l := &loop{brk: NewLabel(), cont: NewLabel(), iterable: x, hasIter: true}
		top := NewLabel()
		head := Seq{
			Set(x, fc.expr(s.X)),
			Set(seq, r.Call("iter_begin", Get(x))),
			Set(n, r.Call("seq_len", Get(seq))),
			Set(i, I32c(0)),
		}
		fc.loops = append(fc.loops, l)
		body := fc.stmts(s.Body)
		fc.loops = fc.loops[:len(fc.loops)-1]
		return Seq{
			head,
			Block(l.brk, Loop(top,
				BrIf(l.brk, I32GeS(Get(i), Get(n))),
				fc.assign(s.Vars, r.Call("seq_at", Get(seq), Get(i))),
				Set(i, I32Add(Get(i), I32c(1))),
				Block(l.cont, body...),
				Br(top),
			)),
			r.Call("iter_end", Get(x)),
		}

	case *syntax.WhileStmt:
		l := &loop{brk: NewLabel(), cont: NewLabel()}
		top := NewLabel()
		fc.loops = append(fc.loops, l)
		body := fc.stmts(s.Body)
		fc.loops = fc.loops[:len(fc.loops)-1]
		return Block(l.brk, Loop(top,
			BrIf(l.brk, I32Eqz(r.Call("truth", fc.expr(s.Cond)))),
			Block(l.cont, body...),
			Br(top),
		))

	case *syntax.BranchStmt:
		switch s.Token {
		case syntax.PASS:
			return Nop()
		case syntax.BREAK:
			return Br(fc.loops[len(fc.loops)-1].brk)
		case syntax.CONTINUE:
			return Br(fc.loops[len(fc.loops)-1].cont)
		}

	case *syntax.ReturnStmt:
		if s.Result == nil {
			return fc.ret(r.NoneV())
		}
		return fc.ret(fc.expr(s.Result))

	case *syntax.DefStmt:
		if fc.u.prelude && fc.c.reach != nil && !fc.c.reach.defs[s.Name.Name] {
			return Nop()
		}
		return fc.assign(s.Name, fc.function(s.Function.(*resolve.Function), fc))

	case *syntax.LoadStmt:
		fc.c.errorf(s.Load, "load statements are not supported")
		return Nop()
	}
	start, _ := s.Span()
	fc.c.errorf(start, "unsupported statement %T", s)
	return Nop()
}

var augmentedOps = map[syntax.Token]syntax.Token{
	syntax.PLUS_EQ: syntax.PLUS, syntax.MINUS_EQ: syntax.MINUS, syntax.STAR_EQ: syntax.STAR,
	syntax.SLASH_EQ: syntax.SLASH, syntax.SLASHSLASH_EQ: syntax.SLASHSLASH, syntax.PERCENT_EQ: syntax.PERCENT,
	syntax.AMP_EQ: syntax.AMP, syntax.PIPE_EQ: syntax.PIPE, syntax.CIRCUMFLEX_EQ: syntax.CIRCUMFLEX,
	syntax.LTLT_EQ: syntax.LTLT, syntax.GTGT_EQ: syntax.GTGT,
}

func unparen(e syntax.Expr) syntax.Expr {
	for {
		p, ok := e.(*syntax.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// assign yields code storing v into the target lhs.
func (fc *fcomp) assign(lhs syntax.Expr, v Expr) Expr {
	r := fc.c.r
	switch lhs := unparen(lhs).(type) {
	case *syntax.Ident:
		b := lhs.Binding.(*resolve.Binding)
		switch b.Scope {
		case resolve.Local:
			return Set(fc.locals[b.Index], v)
		case resolve.Cell:
			return r.CellSet(Get(fc.locals[b.Index]), v)
		case resolve.Global:
			return GSet(fc.u.globals[b.Index], v)
		}
		fc.c.errorf(lhs.NamePos, "cannot assign to %s variable %s", b.Scope, lhs.Name)
		return Drop(v)
	case *syntax.TupleExpr, *syntax.ListExpr:
		var elems []syntax.Expr
		if t, ok := lhs.(*syntax.TupleExpr); ok {
			elems = t.List
		} else {
			elems = lhs.(*syntax.ListExpr).List
		}
		s := fc.tmp()
		code := Seq{Set(s, r.Call("unpack", v, I32c(int32(len(elems)))))}
		for i, e := range elems {
			code = append(code, fc.assign(e, r.Call("seq_at", Get(s), I32c(int32(i)))))
		}
		return code
	case *syntax.IndexExpr:
		// The right-hand side is evaluated before the target operands.
		t := fc.tmp()
		return Seq{Set(t, v), r.Call("setindex", fc.expr(lhs.X), fc.expr(lhs.Y), Get(t))}
	}
	start, _ := lhs.Span()
	fc.c.errorf(start, "invalid assignment target")
	return Drop(v)
}

// ---- expressions ----

func (fc *fcomp) ident(id *syntax.Ident) Expr {
	r := fc.c.r
	b := id.Binding.(*resolve.Binding)
	checked := func(v Expr) Expr {
		t := fc.tmp()
		return IfV(I32, Tee(t, v), Get(t), Seq{r.Call("unbound", r.S(id.Name)), Unreachable()})
	}
	switch b.Scope {
	case resolve.Local:
		return checked(Get(fc.locals[b.Index]))
	case resolve.Cell:
		return checked(r.CellGet(Get(fc.locals[b.Index])))
	case resolve.Free:
		return checked(r.CellGet(Get(fc.free[b.Index])))
	case resolve.Global:
		return checked(GGet(fc.u.globals[b.Index]))
	case resolve.Universal:
		switch id.Name {
		case "None":
			return r.NoneV()
		case "True":
			return r.TrueV()
		case "False":
			return r.FalseV()
		}
		if !fc.u.prelude {
			if n, ok := fc.c.host[id.Name]; ok {
				return fc.c.hostExpr(n)
			}
			if g, ok := fc.c.prelude.byName[id.Name]; ok {
				return GGet(g)
			}
		}
		return U32c(r.BuiltinObj(id.Name))
	}
	fc.c.errorf(id.NamePos, "unsupported reference to %s", id.Name)
	return r.NoneV()
}

var binopNames = map[syntax.Token]string{
	syntax.PLUS: rt.OpAdd, syntax.MINUS: rt.OpSub, syntax.STAR: rt.OpMul, syntax.SLASH: rt.OpDiv,
	syntax.SLASHSLASH: rt.OpFloorDiv, syntax.PERCENT: rt.OpMod, syntax.AMP: rt.OpAnd, syntax.PIPE: rt.OpOr,
	syntax.CIRCUMFLEX: rt.OpXor, syntax.LTLT: rt.OpShl, syntax.GTGT: rt.OpShr,
}

func (fc *fcomp) binop(op syntax.Token, x, y Expr, inplace bool) Expr {
	r := fc.c.r
	if inplace && op == syntax.PLUS {
		return r.Call("inplace_add", x, y)
	}
	return r.Call(rt.BinOps[binopNames[op]], x, y)
}

func (fc *fcomp) expr(e syntax.Expr) Expr {
	r := fc.c.r
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return fc.expr(e.X)

	case *syntax.Ident:
		return fc.ident(e)

	case *syntax.Literal:
		switch v := e.Value.(type) {
		case string:
			if e.Token == syntax.STRING {
				return r.S(v)
			}
		case int64:
			return U32c(r.Int(v))
		case float64:
			return U32c(r.Float(v))
		}
		fc.c.errorf(e.TokenPos, "unsupported literal %s", e.Raw)
		return r.NoneV()

	case *syntax.ListExpr:
		t := fc.tmp()
		code := Seq{Set(t, r.Call("list_new", I32c(int32(len(e.List)))))}
		for _, x := range e.List {
			code = append(code, r.Call("list_append_raw", Get(t), fc.expr(x)))
		}
		return append(code, Get(t))

	case *syntax.TupleExpr:
		return fc.tuple(e.List)

	case *syntax.DictExpr:
		t := fc.tmp()
		code := Seq{Set(t, r.Call("dict_new"))}
		for _, x := range e.List {
			ent := x.(*syntax.DictEntry)
			code = append(code, r.Call("dict_lit_set", Get(t), fc.expr(ent.Key), fc.expr(ent.Value)))
		}
		return append(code, Get(t))

	case *syntax.UnaryExpr:
		x := fc.expr(e.X)
		switch e.Op {
		case syntax.MINUS:
			return r.Call("neg", x)
		case syntax.PLUS:
			return r.Call("pos", x)
		case syntax.TILDE:
			return r.Call("invert", x)
		case syntax.NOT:
			return r.Call("not", x)
		}

	case *syntax.BinaryExpr:
		switch e.Op {
		case syntax.AND, syntax.OR:
			t := fc.tmp()
			x := Set(t, fc.expr(e.X))
			if e.Op == syntax.AND {
				return Seq{x, IfV(I32, r.Call("truth", Get(t)), fc.expr(e.Y), Get(t))}
			}
			return Seq{x, IfV(I32, r.Call("truth", Get(t)), Get(t), fc.expr(e.Y))}
		case syntax.EQL:
			return fc.boolv(r.Call("eq", fc.expr(e.X), fc.expr(e.Y)))
		case syntax.NEQ:
			return fc.boolv(I32Eqz(r.Call("eq", fc.expr(e.X), fc.expr(e.Y))))
		case syntax.LT:
			return fc.boolv(I32LtS(r.Call("cmp", fc.expr(e.X), fc.expr(e.Y), r.S("<")), I32c(0)))
		case syntax.GT:
			return fc.boolv(I32GtS(r.Call("cmp", fc.expr(e.X), fc.expr(e.Y), r.S(">")), I32c(0)))
		case syntax.LE:
			return fc.boolv(I32LeS(r.Call("cmp", fc.expr(e.X), fc.expr(e.Y), r.S("<=")), I32c(0)))
		case syntax.GE:
			return fc.boolv(I32GeS(r.Call("cmp", fc.expr(e.X), fc.expr(e.Y), r.S(">=")), I32c(0)))
		case syntax.IN:
			return fc.boolv(r.Call("contains", fc.expr(e.X), fc.expr(e.Y)))
		case syntax.NOT_IN:
			return fc.boolv(I32Eqz(r.Call("contains", fc.expr(e.X), fc.expr(e.Y))))
		}
		if _, ok := binopNames[e.Op]; ok {
			return fc.binop(e.Op, fc.expr(e.X), fc.expr(e.Y), false)
		}

	case *syntax.CondExpr:
		return IfV(I32, r.Call("truth", fc.expr(e.Cond)), fc.expr(e.True), fc.expr(e.False))

	case *syntax.IndexExpr:
		return r.Call("index", fc.expr(e.X), fc.expr(e.Y))

	case *syntax.SliceExpr:
		opt := func(x syntax.Expr) Expr {
			if x == nil {
				return I32c(0)
			}
			return fc.expr(x)
		}
		return r.Call("slice", fc.expr(e.X), opt(e.Lo), opt(e.Hi), opt(e.Step))

	case *syntax.DotExpr:
		if n, ok := fc.c.hostRef(e); ok {
			return fc.c.hostExpr(n)
		}
		return r.Call("attr", fc.expr(e.X), I32c(int32(r.MethodID(e.Name.Name))))

	case *syntax.CallExpr:
		return fc.call(e)

	case *syntax.LambdaExpr:
		return fc.function(e.Function.(*resolve.Function), fc)

	case *syntax.Comprehension:
		return fc.comprehension(e)
	}
	start, _ := e.Span()
	fc.c.errorf(start, "unsupported expression %T", e)
	return r.NoneV()
}

func (fc *fcomp) boolv(cond Expr) Expr {
	r := fc.c.r
	return Select(r.TrueV(), r.FalseV(), cond)
}

func (fc *fcomp) tuple(elems []syntax.Expr) Expr {
	r := fc.c.r
	t := fc.tmp()
	code := Seq{Set(t, r.Call("tuple_new", I32c(int32(len(elems)))))}
	for i, x := range elems {
		code = append(code, r.TupleSet(Get(t), i, fc.expr(x)))
	}
	return append(code, Get(t))
}

func (fc *fcomp) call(e *syntax.CallExpr) Expr {
	r := fc.c.r
	var pos []syntax.Expr
	var star []syntax.Expr // positional items: nil entry means "plain"
	var kwNames []string
	var kwVals []syntax.Expr
	var kwStar []syntax.Expr
	hasStar := false
	for _, a := range e.Args {
		switch a := a.(type) {
		case *syntax.UnaryExpr:
			if a.Op == syntax.STAR {
				hasStar = true
				pos = append(pos, a.X)
				star = append(star, a.X)
				continue
			}
			if a.Op == syntax.STARSTAR {
				kwStar = append(kwStar, a.X)
				continue
			}
		case *syntax.BinaryExpr:
			if a.Op == syntax.EQ {
				kwNames = append(kwNames, a.X.(*syntax.Ident).Name)
				kwVals = append(kwVals, a.Y)
				continue
			}
		}
		pos = append(pos, a)
		star = append(star, nil)
	}

	// The callee is evaluated first. For a method call, that is the
	// receiver and the method lookup, which fails before any argument is
	// evaluated if there is no such method.
	var code Seq
	fnT := fc.tmp()
	var recv Expr
	if n, ok := fc.c.hostRef(e.Fn); ok {
		code = append(code, Set(fnT, fc.c.hostExpr(n)))
	} else if dot, ok := e.Fn.(*syntax.DotExpr); ok {
		rv := fc.tmp()
		code = append(code,
			Set(rv, fc.expr(dot.X)),
			Set(fnT, r.Call("method_resolve", Get(rv), I32c(int32(r.MethodID(dot.Name.Name))))))
		recv = Get(rv)
	} else {
		code = append(code, Set(fnT, fc.expr(e.Fn)))
	}

	// Positional arguments.
	args := fc.tmp()
	offset := 0
	if recv != nil {
		offset = 1
	}
	if !hasStar {
		code = append(code, Set(args, r.Call("tuple_new", I32c(int32(len(pos)+offset)))))
		if recv != nil {
			code = append(code, r.TupleSet(Get(args), 0, recv))
		}
		for i, x := range pos {
			code = append(code, r.TupleSet(Get(args), i+offset, fc.expr(x)))
		}
	} else {
		l := fc.tmp()
		code = append(code, Set(l, r.Call("list_new", I32c(int32(len(pos)+offset)))))
		if recv != nil {
			code = append(code, r.Call("list_append_raw", Get(l), recv))
		}
		for i, x := range pos {
			if star[i] != nil {
				code = append(code, r.Call("list_extend", Get(l), fc.expr(x)))
			} else {
				code = append(code, r.Call("list_append_raw", Get(l), fc.expr(x)))
			}
		}
		code = append(code, Set(args, r.Call("tuple_from", Get(l))))
	}

	// Keyword arguments.
	kw := I32c(0)
	if len(kwNames) > 0 || len(kwStar) > 0 {
		d := fc.tmp()
		code = append(code, Set(d, r.Call("dict_new")))
		for i, name := range kwNames {
			code = append(code, r.Call("dict_set", Get(d), r.S(name), fc.expr(kwVals[i])))
		}
		for _, x := range kwStar {
			code = append(code, r.Call("kwargs_merge", Get(d), fc.expr(x)))
		}
		kw = Get(d)
	}
	// For a method call, args[0] is the receiver, unless it is a module.
	if recv != nil {
		return append(code, Raw(func() Expr {
			if r.HasModules() {
				return r.Call("call_method", Get(fnT), Get(args), kw)
			}
			return r.Call("call", Get(fnT), Get(args), kw)
		}))
	}
	return append(code, r.Call("call", Get(fnT), Get(args), kw))
}

func (fc *fcomp) comprehension(e *syntax.Comprehension) Expr {
	r := fc.c.r
	res := fc.tmp()
	var code Seq
	if e.Curly {
		code = append(code, Set(res, r.Call("dict_new")))
	} else {
		code = append(code, Set(res, r.Call("list_new", I32c(0))))
	}
	var gen func(i int) Expr
	gen = func(i int) Expr {
		if i == len(e.Clauses) {
			if e.Curly {
				ent := e.Body.(*syntax.DictEntry)
				return r.Call("dict_set", Get(res), fc.expr(ent.Key), fc.expr(ent.Value))
			}
			return r.Call("list_append_raw", Get(res), fc.expr(e.Body))
		}
		switch cl := e.Clauses[i].(type) {
		case *syntax.IfClause:
			return If(r.Call("truth", fc.expr(cl.Cond)), gen(i+1))
		case *syntax.ForClause:
			x, seq, n, j := fc.tmp(), fc.tmp(), fc.tmp(), fc.tmp()
			brk, top := NewLabel(), NewLabel()
			return Seq{
				Set(x, fc.expr(cl.X)),
				Set(seq, r.Call("iter_begin", Get(x))),
				Set(n, r.Call("seq_len", Get(seq))),
				Set(j, I32c(0)),
				Block(brk, Loop(top,
					BrIf(brk, I32GeS(Get(j), Get(n))),
					fc.assign(cl.Vars, r.Call("seq_at", Get(seq), Get(j))),
					Set(j, I32Add(Get(j), I32c(1))),
					gen(i+1),
					Br(top),
				)),
				r.Call("iter_end", Get(x)),
			}
		}
		panic("compiler: bad comprehension clause")
	}
	code = append(code, gen(0), Get(res))
	return code
}
