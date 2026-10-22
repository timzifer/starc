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
	// dbgVars are the variables that a debug build stores at each
	// statement, in the order of the function's debug descriptor.
	dbgVars []Local
	dbgArea Local
	// pos is the position of the operation being compiled, maintained as
	// by go.starlark.net's compiler so that errors report the same
	// positions.
	pos syntax.Position
}

type loop struct {
	brk, cont *Label
	iterable  Local // the iterated value, for iter_end; -1 if none
	hasIter   bool
}

func (fc *fcomp) tmp() Local { return fc.f.Local(I32) }

// setPos sets the position of the next operations that can fail.
func (fc *fcomp) setPos(p syntax.Position) { fc.pos = p }

// here yields code that records the current position for the operation
// that follows. Positions within the prelude are not recorded: there,
// errors are reported at the call of the built-in.
func (fc *fcomp) here() Expr {
	if fc.u.prelude {
		return Nop()
	}
	return fc.c.r.SetPos(fc.c.r.Pos(int(fc.pos.Line), int(fc.pos.Col)))
}

// at sets the position to p and yields code that records it.
func (fc *fcomp) at(p syntax.Position) Expr {
	fc.setPos(p)
	return fc.here()
}

// then yields x followed by recording p, for the last operand of an
// operation at p: the position must be recorded after the operands have
// been evaluated, which may record positions of their own.
func (fc *fcomp) then(x Expr, p syntax.Position) Expr { return Seq{x, fc.at(p)} }

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
	if d := fc.debugVars(mod.Locals, nil); d != 0 {
		c.r.DebugToplevel(d)
	}
	if !u.prelude {
		f.Add(c.r.PushToplevel())
	}
	f.Add(fc.stmts(u.file.Stmts)...)
	if !u.prelude {
		f.Add(c.r.PopFrame())
	}
	return f
}

// function compiles a def or lambda and returns the code that creates
// the function object.
func (fc *fcomp) function(fn *resolve.Function, outer *fcomp) Expr {
	c, r := fc.c, fc.c.r
	c.nfunc++
	f := c.m.AddFunc(NewFunc(fc.u.name+"."+fn.Name, rt.CallType.Params, rt.CallType.Results...))
	inner := &fcomp{c: c, u: fc.u, f: f, self: f.Param(2)}

	// Parameters. Errors in the defaults are reported at the def.
	outer.setPos(fn.Pos)
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
	if fc.u.prelude {
		flags |= rt.SpecPrelude
	}
	spec := r.Spec(fn.Name, names, fn.NumKwonlyParams, flags)
	if !fc.u.prelude {
		r.ProfileFunction(spec, rt.ProfileEntry{Name: fn.Name, Kind: rt.ProfFunction,
			Line: int(fn.Pos.Line), Col: int(fn.Pos.Col), Wasm: f.Name})
	} else if show := preludeShow(fn.Name); show != "" {
		r.SpecShow(spec, show)
		r.ProfileFunction(spec, rt.ProfileEntry{Name: show, Kind: rt.ProfBuiltin})
	}
	nparams := len(names)
	if fn.HasVarargs {
		nparams++
	}
	if fn.HasKwargs {
		nparams++
	}

	// Prologue. Errors in binding the arguments are reported at the
	// start of the body.
	inner.setPos(c.entryPos(fn))
	f.Add(inner.here())
	inner.setPos(fn.Pos)
	if !recursion {
		inner.spec = spec
		c.specs = append(c.specs, spec)
		f.Add(r.Call("enter", U32c(spec)))
	}
	f.Add(r.Enter())
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
	if d := inner.debugVars(fn.Locals, fn.FreeVars); d != 0 {
		r.DebugFunction(spec, d)
	}
	f.Add(inner.block(fn.Body, rt.FuelCall)...)
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
	return append(code, r.LeaveCall(), Return(Get(t)))
}

// ---- statements ----

func (fc *fcomp) stmts(stmts []syntax.Stmt) []Expr { return fc.block(stmts, 0) }

// block compiles a statement list. With fuel metering, the statements
// and extra are charged together on entry.
func (fc *fcomp) block(stmts []syntax.Stmt, extra int64) []Expr {
	n := int64(0)
	for _, s := range stmts {
		if !fc.dropped(s) {
			n++
		}
	}
	out := []Expr{fc.c.r.ChargeN(n*rt.FuelStatement + extra)}
	for _, s := range stmts {
		if fc.c.opts.Debug && !fc.u.prelude {
			start, _ := s.Span()
			vars := make([]Expr, len(fc.dbgVars))
			for i, l := range fc.dbgVars {
				vars[i] = Get(l)
			}
			out = append(out, fc.c.r.DebugStatement(fc.c.r.Pos(int(start.Line), int(start.Col)), fc.dbgArea, vars))
		}
		out = append(out, fc.stmt(s))
	}
	return out
}

// dropped reports whether s is a prelude definition that the program does
// not use and that is therefore not compiled (nor charged, so that fuel
// does not depend on the size of the prelude).
func (fc *fcomp) dropped(s syntax.Stmt) bool {
	d, ok := s.(*syntax.DefStmt)
	return ok && fc.u.prelude && fc.c.reach != nil && !fc.c.reach.defs[d.Name.Name]
}

func (fc *fcomp) stmt(s syntax.Stmt) Expr {
	r := fc.c.r
	switch s := s.(type) {
	case *syntax.ExprStmt:
		return Drop(fc.expr(s.X))

	case *syntax.AssignStmt:
		if s.Op == syntax.EQ {
			return fc.assign(s.LHS, fc.expr(s.RHS), s.OpPos)
		}
		op := augmentedOps[s.Op]
		switch lhs := unparen(s.LHS).(type) {
		case *syntax.Ident:
			return fc.assign(lhs, fc.binop(op, fc.ident(lhs), fc.expr(s.RHS), true, s.OpPos), s.OpPos)
		case *syntax.IndexExpr:
			x, y := fc.tmp(), fc.tmp()
			code := Seq{Set(x, fc.expr(lhs.X)), Set(y, fc.expr(lhs.Y))}
			old := Seq{fc.at(lhs.Lbrack), r.Call("index", Get(x), Get(y))}
			v := fc.binop(op, old, fc.expr(s.RHS), true, s.OpPos)
			return append(code, r.Call("setindex", Get(x), Get(y), fc.then(v, lhs.Lbrack)))
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
			fc.at(s.For),
			Set(seq, r.Call("iter_begin", Get(x))),
			Set(n, r.Call("seq_len", Get(seq))),
			Set(i, I32c(0)),
		}
		vars := fc.assign(s.Vars, r.Call("seq_at", Get(seq), Get(i)), s.For)
		fc.loops = append(fc.loops, l)
		body := fc.block(s.Body, rt.FuelIteration)
		fc.loops = fc.loops[:len(fc.loops)-1]
		return Seq{
			head,
			Block(l.brk, Loop(top,
				BrIf(l.brk, I32GeS(Get(i), Get(n))),
				vars,
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
			r.ChargeN(rt.FuelIteration),
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
		if fc.dropped(s) {
			return Nop()
		}
		return fc.assign(s.Name, fc.function(s.Function.(*resolve.Function), fc), s.Def)

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

// assign yields code storing v into the target lhs; pos is the position
// of the assignment, where unpacking fails.
func (fc *fcomp) assign(lhs syntax.Expr, v Expr, pos syntax.Position) Expr {
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
		code := Seq{Set(s, r.Call("unpack", fc.then(v, pos), I32c(int32(len(elems)))))}
		for i, e := range elems {
			code = append(code, fc.assign(e, r.Call("seq_at", Get(s), I32c(int32(i))), pos))
		}
		return code
	case *syntax.IndexExpr:
		// The right-hand side is evaluated before the target operands.
		t := fc.tmp()
		return Seq{Set(t, v), r.Call("setindex", fc.expr(lhs.X), fc.then(fc.expr(lhs.Y), lhs.Lbrack), Get(t))}
	}
	start, _ := lhs.Span()
	fc.c.errorf(start, "invalid assignment target")
	return Drop(v)
}

// ---- expressions ----

func (fc *fcomp) ident(id *syntax.Ident) Expr {
	r := fc.c.r
	b := id.Binding.(*resolve.Binding)
	// A variable may be unbound: its position is recorded only then.
	checked := func(v Expr) Expr {
		t := fc.tmp()
		fc.setPos(id.NamePos)
		return IfV(I32, Tee(t, v), Get(t), Seq{fc.here(), r.Call("unbound", r.S(id.Name)), Unreachable()})
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

// binop yields x op y; pos is the position of the operator.
func (fc *fcomp) binop(op syntax.Token, x, y Expr, inplace bool, pos syntax.Position) Expr {
	r := fc.c.r
	y = fc.then(y, pos)
	if inplace && op == syntax.PLUS {
		return r.Call("inplace_add", x, y)
	}
	if inplace && op == syntax.PIPE {
		return r.Call("inplace_or", x, y)
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
			code = append(code, r.Call("dict_lit_set", Get(t), fc.expr(ent.Key), fc.then(fc.expr(ent.Value), ent.Colon)))
		}
		return append(code, Get(t))

	case *syntax.UnaryExpr:
		x := fc.expr(e.X)
		if e.Op == syntax.NOT {
			fc.setPos(e.OpPos) // not cannot fail
		} else {
			x = fc.then(x, e.OpPos)
		}
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
		}
		x, y := fc.expr(e.X), fc.expr(e.Y)
		if e.Op == syntax.PLUS && plusFolded(e) {
			// go.starlark.net adds the literals at compile time, so this
			// addition, which cannot fail, has no position.
			return r.Call(rt.BinOps[rt.OpAdd], x, y)
		}
		if _, ok := binopNames[e.Op]; ok {
			return fc.binop(e.Op, x, y, false, e.OpPos)
		}
		y = fc.then(y, e.OpPos)
		switch e.Op {
		case syntax.EQL:
			return fc.boolv(r.Call("eq", x, y))
		case syntax.NEQ:
			return fc.boolv(I32Eqz(r.Call("eq", x, y)))
		case syntax.LT:
			return fc.boolv(I32LtS(r.Call("cmp", x, y, r.S("<")), I32c(0)))
		case syntax.GT:
			return fc.boolv(I32GtS(r.Call("cmp", x, y, r.S(">")), I32c(0)))
		case syntax.LE:
			return fc.boolv(I32LeS(r.Call("cmp", x, y, r.S("<=")), I32c(0)))
		case syntax.GE:
			return fc.boolv(I32GeS(r.Call("cmp", x, y, r.S(">=")), I32c(0)))
		case syntax.IN:
			return fc.boolv(r.Call("contains", x, y))
		case syntax.NOT_IN:
			return fc.boolv(I32Eqz(r.Call("contains", x, y)))
		}

	case *syntax.CondExpr:
		return IfV(I32, r.Call("truth", fc.expr(e.Cond)), fc.expr(e.True), fc.expr(e.False))

	case *syntax.IndexExpr:
		return r.Call("index", fc.expr(e.X), fc.then(fc.expr(e.Y), e.Lbrack))

	case *syntax.SliceExpr:
		opt := func(x syntax.Expr) Expr {
			if x == nil {
				return I32c(0)
			}
			return fc.expr(x)
		}
		x, lo, hi, step := fc.expr(e.X), opt(e.Lo), opt(e.Hi), opt(e.Step)
		fc.setPos(fc.c.slicePos(e))
		return r.Call("slice", x, lo, hi, Seq{step, fc.here()})

	case *syntax.DotExpr:
		if n, ok := fc.c.hostRef(e); ok {
			return fc.c.hostExpr(n)
		}
		return r.Call("attr", fc.then(fc.expr(e.X), e.Dot), I32c(int32(r.MethodID(e.Name.Name))))

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
	npos, nkw := 0, 0
	var star, kwStar []syntax.Expr
	for _, a := range e.Args {
		if u, ok := a.(*syntax.UnaryExpr); ok && u.Op == syntax.STAR {
			star = append(star, u.X)
		} else if ok && u.Op == syntax.STARSTAR {
			kwStar = append(kwStar, u.X)
		} else if b, ok := a.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
			nkw++
		} else {
			npos++
		}
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
			fc.at(dot.Dot),
			Set(fnT, r.Call("method_resolve", Get(rv), I32c(int32(r.MethodID(dot.Name.Name))))))
		recv = Get(rv)
	} else {
		code = append(code, Set(fnT, fc.expr(e.Fn)))
	}

	// As in go.starlark.net, the positional and keyword arguments are
	// evaluated in order, then the operands of *args and **kwargs; these
	// are unpacked by the call itself, where their errors are reported.
	offset := 0
	if recv != nil {
		offset = 1
	}
	args, l := fc.tmp(), Local{}
	if len(star) == 0 {
		code = append(code, Set(args, r.Call("tuple_new", I32c(int32(npos+offset)))))
		if recv != nil {
			code = append(code, r.TupleSet(Get(args), 0, recv))
		}
	} else {
		l = fc.tmp()
		code = append(code, Set(l, r.Call("list_new", I32c(int32(npos+offset)))))
		if recv != nil {
			code = append(code, r.Call("list_append_raw", Get(l), recv))
		}
	}
	kw, d := I32c(0), Local{}
	if nkw > 0 || len(kwStar) > 0 {
		d = fc.tmp()
		code = append(code, Set(d, r.Call("dict_new")))
		kw = Get(d)
	}
	i := offset
	for _, a := range e.Args {
		if u, ok := a.(*syntax.UnaryExpr); ok && (u.Op == syntax.STAR || u.Op == syntax.STARSTAR) {
			continue
		}
		if b, ok := a.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
			code = append(code, r.Call("dict_set", Get(d), r.S(b.X.(*syntax.Ident).Name), fc.expr(b.Y)))
		} else if len(star) == 0 {
			code = append(code, r.TupleSet(Get(args), i, fc.expr(a)))
			i++
		} else {
			code = append(code, r.Call("list_append_raw", Get(l), fc.expr(a)))
		}
	}
	var unpack Seq
	for _, x := range star {
		t := fc.tmp()
		code = append(code, Set(t, fc.expr(x)))
		unpack = append(unpack, r.Call("list_extend", Get(l), Get(t)))
	}
	if len(star) > 0 {
		unpack = append(unpack, Set(args, r.Call("tuple_from", Get(l))))
	}
	for _, x := range kwStar {
		t := fc.tmp()
		code = append(code, Set(t, fc.expr(x)))
		unpack = append(unpack, r.Call("kwargs_merge", Get(d), Get(t)))
	}
	code = append(code, fc.at(e.Lparen), unpack)
	// For a method call, args[0] is the receiver, unless it is a module
	// or a struct.
	if recv != nil {
		return append(code, Raw(func() Expr {
			if r.HasModules() || fc.c.structsPossible() {
				return r.Call("call_method", Get(fnT), Get(args), kw)
			}
			return r.Call("call", Get(fnT), Get(args), kw)
		}))
	}
	callee := "call"
	if fc.u.prelude && r.Cfg.Backtrace {
		// A local of the prelude, such as key, holds a callable of the
		// program, whose frame is shown in backtraces.
		if id, ok := e.Fn.(*syntax.Ident); ok && id.Binding.(*resolve.Binding).Scope != resolve.Global &&
			id.Binding.(*resolve.Binding).Scope != resolve.Universal {
			callee = "call_cb"
		}
	}
	return append(code, r.Call(callee, Get(fnT), Get(args), kw))
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
				return r.Call("dict_set", Get(res), fc.expr(ent.Key), fc.then(fc.expr(ent.Value), ent.Colon))
			}
			return r.Call("list_append_raw", Get(res), fc.expr(e.Body))
		}
		switch cl := e.Clauses[i].(type) {
		case *syntax.IfClause:
			return If(r.Call("truth", fc.expr(cl.Cond)), gen(i+1))
		case *syntax.ForClause:
			x, seq, n, j := fc.tmp(), fc.tmp(), fc.tmp(), fc.tmp()
			brk, top := NewLabel(), NewLabel()
			head := Seq{
				Set(x, fc.expr(cl.X)),
				fc.at(cl.For),
				Set(seq, r.Call("iter_begin", Get(x))),
				Set(n, r.Call("seq_len", Get(seq))),
				Set(j, I32c(0)),
			}
			vars := fc.assign(cl.Vars, r.Call("seq_at", Get(seq), Get(j)), cl.For)
			return Seq{
				head,
				Block(brk, Loop(top,
					BrIf(brk, I32GeS(Get(j), Get(n))),
					r.ChargeN(rt.FuelIteration),
					vars,
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

// entryPos returns the position at which go.starlark.net reports errors
// in a call before the body of fn runs, such as in binding the arguments:
// that of the first instruction of the body, or of the def if that
// instruction has none.
func (c *compiler) entryPos(fn *resolve.Function) syntax.Position {
	for _, st := range fn.Body {
		s := &psim{c: c}
		entry := s.newBlock()
		s.cur = entry
		switch st := st.(type) {
		case *syntax.BranchStmt:
			if st.Token == syntax.PASS {
				continue
			}
		case *syntax.ExprStmt:
			if _, ok := st.X.(*syntax.Literal); ok {
				continue // go.starlark.net does not compile doc strings
			}
			s.expr(st.X)
		case *syntax.AssignStmt:
			switch lhs := unparen(st.LHS).(type) {
			case *syntax.IndexExpr:
				if st.Op != syntax.EQ {
					s.expr(lhs.X)
					break
				}
				s.expr(st.RHS)
			case *syntax.Ident:
				if st.Op != syntax.EQ {
					s.expr(lhs)
					break
				}
				s.expr(st.RHS)
			default:
				s.expr(st.RHS)
			}
		case *syntax.IfStmt:
			s.ifelse(st.Cond, s.newBlock(), s.newBlock())
		case *syntax.WhileStmt:
			s.ifelse(st.Cond, s.newBlock(), s.newBlock())
		case *syntax.ForStmt:
			s.expr(st.X)
		case *syntax.ReturnStmt:
			if st.Result != nil {
				s.expr(st.Result)
			}
		case *syntax.DefStmt:
			s.function(st.Function.(*resolve.Function))
		}
		if len(entry.insns) > 0 && entry.insns[0].pos.Line != 0 {
			return entry.insns[0].pos
		}
		break
	}
	return fn.Pos
}

// plusFolded reports whether go.starlark.net folds the addition e at
// compile time: it adds adjacent string, list or tuple literals of a chain
// a + b + ... (see addable in its compiler).
func plusFolded(e *syntax.BinaryExpr) bool {
	left := unparen(e.X)
	if l, ok := left.(*syntax.BinaryExpr); ok && l.Op == syntax.PLUS {
		left = unparen(l.Y)
	}
	a := addable(left)
	return a != 0 && a == addable(unparen(e.Y))
}

// addable classifies the literals that go.starlark.net folds.
func addable(e syntax.Expr) rune {
	switch e := e.(type) {
	case *syntax.Literal:
		if e.Token == syntax.STRING {
			return 's'
		}
	case *syntax.ListExpr:
		return 'l'
	case *syntax.TupleExpr:
		return 't'
	}
	return 0
}
