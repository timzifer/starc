package compiler

import (
	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

// The positions of most operations are those that their own setPos calls
// record in go.starlark.net's compiler. The slice operation is the
// exception: go.starlark.net records the bracket before compiling the
// operands, and the SLICE instruction itself has no position. It inherits
// the position of the closest preceding instruction that has one, in the
// layout of go.starlark.net's control-flow graph, which depends on the
// branches within the operands. slicePos replays that compilation.

// slicePos returns the position at which go.starlark.net reports an error
// of the slice operation e.
func (c *compiler) slicePos(e *syntax.SliceExpr) syntax.Position {
	s := &psim{c: c}
	entry := s.newBlock()
	s.cur = entry
	s.expr(e)
	s.cur.insns = append(s.cur.insns, pinsn{mark: true})

	// Linearize as go.starlark.net does: each block is followed by the
	// target of its jump, then by that of its conditional jump; empty
	// blocks are skipped.
	var order []*pblock
	var visit func(b *pblock)
	visit = func(b *pblock) {
		if b.visited {
			return
		}
		b.visited = true
		order = append(order, b)
		if b.jmp != nil {
			for len(b.jmp.insns) == 0 {
				b.jmp = b.jmp.jmp
			}
			visit(b.jmp)
		}
		if b.cjmp != nil {
			for len(b.cjmp.insns) == 0 {
				b.cjmp = b.cjmp.jmp
			}
			visit(b.cjmp)
		}
	}
	visit(entry)
	last := e.Lbrack
	for _, b := range order {
		for _, in := range b.insns {
			if in.mark {
				return last
			}
			if in.pos.Line != 0 {
				last = in.pos
			}
		}
	}
	return last
}

// goUniversal reports whether go.starlark.net resolves id as a universal
// name, whose lookup records no position. Host names and struct are
// predeclared names for embedders of go.starlark.net.
func (c *compiler) goUniversal(id *syntax.Ident) bool {
	if id.Binding.(*resolve.Binding).Scope != resolve.Universal {
		return false
	}
	_, host := c.host[id.Name]
	return !host && id.Name != "struct"
}

type pinsn struct {
	pos  syntax.Position
	mark bool // the slice operation
}

type pblock struct {
	insns     []pinsn
	jmp, cjmp *pblock
	visited   bool
}

// psim replays the code generation of go.starlark.net for expressions,
// keeping only the positions of the instructions and the control flow.
type psim struct {
	c   *compiler
	cur *pblock
	pos syntax.Position
}

func (s *psim) newBlock() *pblock { return &pblock{} }

func (s *psim) setPos(p syntax.Position) { s.pos = p }

// emit appends an instruction, which takes the pending position.
func (s *psim) emit() {
	s.cur.insns = append(s.cur.insns, pinsn{pos: s.pos})
	s.pos = syntax.Position{}
}

func (s *psim) jump(b *pblock) {
	s.cur.jmp = b
	s.cur = nil
}

func (s *psim) condjump(t, f *pblock) {
	s.emit()
	s.cur.cjmp = t
	s.jump(f)
}

func (s *psim) expr(e syntax.Expr) {
	switch e := e.(type) {
	case *syntax.ParenExpr:
		s.expr(e.X)
	case *syntax.Ident:
		if !s.c.goUniversal(e) {
			s.setPos(e.NamePos)
		}
		s.emit()
	case *syntax.ListExpr:
		s.exprs(e.List)
		s.emit()
	case *syntax.TupleExpr:
		s.exprs(e.List)
		s.emit()
	case *syntax.CondExpr:
		t, f, done := s.newBlock(), s.newBlock(), s.newBlock()
		s.ifelse(e.Cond, t, f)
		s.cur = t
		s.expr(e.True)
		s.jump(done)
		s.cur = f
		s.expr(e.False)
		s.jump(done)
		s.cur = done
	case *syntax.IndexExpr:
		s.expr(e.X)
		s.expr(e.Y)
		s.setPos(e.Lbrack)
		s.emit()
	case *syntax.SliceExpr:
		s.setPos(e.Lbrack)
		for _, x := range []syntax.Expr{e.X, e.Lo, e.Hi, e.Step} {
			if x != nil {
				s.expr(x)
			} else {
				s.emit()
			}
		}
		s.emit()
	case *syntax.Comprehension:
		s.emit()
		s.comprehension(e, 0)
	case *syntax.DictExpr:
		s.emit()
		for _, x := range e.List {
			ent := x.(*syntax.DictEntry)
			s.emit()
			s.expr(ent.Key)
			s.expr(ent.Value)
			s.setPos(ent.Colon)
			s.emit()
		}
	case *syntax.UnaryExpr:
		s.expr(e.X)
		s.setPos(e.OpPos)
		s.emit()
	case *syntax.BinaryExpr:
		switch e.Op {
		case syntax.OR, syntax.AND:
			done, y := s.newBlock(), s.newBlock()
			s.expr(e.X)
			s.emit()
			if e.Op == syntax.OR {
				s.condjump(done, y)
			} else {
				s.condjump(y, done)
			}
			s.cur = y
			s.emit()
			s.expr(e.Y)
			s.jump(done)
			s.cur = done
		case syntax.PLUS:
			s.plus(e)
		default:
			s.expr(e.X)
			s.expr(e.Y)
			s.setPos(e.OpPos)
			s.emit()
		}
	case *syntax.DotExpr:
		s.expr(e.X)
		s.setPos(e.Dot)
		s.emit()
	case *syntax.CallExpr:
		s.expr(e.Fn)
		var star []syntax.Expr
		for _, a := range e.Args {
			if b, ok := a.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
				s.emit()
				s.expr(b.Y)
				continue
			}
			if u, ok := a.(*syntax.UnaryExpr); ok && (u.Op == syntax.STAR || u.Op == syntax.STARSTAR) {
				star = append(star, u.X)
				continue
			}
			s.expr(a)
		}
		// *args before **kwargs, as the parser requires.
		s.exprs(star)
		s.setPos(e.Lparen)
		s.emit()
	case *syntax.LambdaExpr:
		s.function(e.Function.(*resolve.Function))
	default: // literals
		s.emit()
	}
}

// function replays the creation of a function: its defaults, markers of
// keyword-only parameters without defaults, and free variables.
func (s *psim) function(fn *resolve.Function) {
	s.setPos(fn.Pos)
	star := false
	for _, p := range fn.Params {
		switch p := p.(type) {
		case *syntax.BinaryExpr:
			s.expr(p.Y)
		case *syntax.UnaryExpr:
			star = true
		case *syntax.Ident:
			if star {
				s.emit()
			}
		}
	}
	for range fn.FreeVars {
		s.emit()
	}
	s.emit()
	s.emit()
}

func (s *psim) exprs(es []syntax.Expr) {
	for _, x := range es {
		s.expr(x)
	}
}

func (s *psim) ifelse(cond syntax.Expr, t, f *pblock) {
	switch cond := cond.(type) {
	case *syntax.UnaryExpr:
		if cond.Op == syntax.NOT {
			s.ifelse(cond.X, f, t)
			return
		}
	case *syntax.BinaryExpr:
		switch cond.Op {
		case syntax.AND:
			s.expr(cond.X)
			y := s.newBlock()
			s.condjump(y, f)
			s.cur = y
			s.ifelse(cond.Y, t, f)
			return
		case syntax.OR:
			s.expr(cond.X)
			y := s.newBlock()
			s.condjump(t, y)
			s.cur = y
			s.ifelse(cond.Y, t, f)
			return
		case syntax.NOT_IN:
			in := *cond
			in.Op = syntax.IN
			s.expr(&in)
			s.condjump(f, t)
			return
		}
	}
	s.expr(cond)
	s.condjump(t, f)
}

// plus replays the compilation of a chain of additions, in which adjacent
// literals are added at compile time.
func (s *psim) plus(e *syntax.BinaryExpr) {
	type summand struct {
		x       syntax.Expr
		plusPos syntax.Position
	}
	var args []summand
	for plus := e; ; {
		args = append(args, summand{unparen(plus.Y), plus.OpPos})
		left := unparen(plus.X)
		x, ok := left.(*syntax.BinaryExpr)
		if !ok || x.Op != syntax.PLUS {
			args = append(args, summand{x: left})
			break
		}
		plus = x
	}
	for i, j := 0, len(args)-1; i < j; i, j = i+1, j-1 {
		args[i], args[j] = args[j], args[i]
	}
	out := args[:0]
	for i := 0; i < len(args); {
		j := i + 1
		if code := addable(args[i].x); code != 0 {
			for j < len(args) && addable(args[j].x) == code {
				j++
			}
			if j > i+1 {
				// Only the elements of the folded literals remain.
				var elems []syntax.Expr
				for _, a := range args[i:j] {
					switch x := a.x.(type) {
					case *syntax.ListExpr:
						elems = append(elems, x.List...)
					case *syntax.TupleExpr:
						elems = append(elems, x.List...)
					}
				}
				args[i].x = &syntax.ListExpr{List: elems}
			}
		}
		out = append(out, args[i])
		i = j
	}
	s.expr(out[0].x)
	for _, a := range out[1:] {
		s.expr(a.x)
		s.setPos(a.plusPos)
		s.emit()
	}
}

func (s *psim) comprehension(e *syntax.Comprehension, i int) {
	if i == len(e.Clauses) {
		s.emit()
		if e.Curly {
			ent := e.Body.(*syntax.DictEntry)
			s.expr(ent.Key)
			s.expr(ent.Value)
			s.setPos(ent.Colon)
		} else {
			s.expr(e.Body)
		}
		s.emit()
		return
	}
	switch cl := e.Clauses[i].(type) {
	case *syntax.IfClause:
		t, done := s.newBlock(), s.newBlock()
		s.ifelse(cl.Cond, t, done)
		s.cur = t
		s.comprehension(e, i+1)
		s.jump(done)
		s.cur = done
	case *syntax.ForClause:
		head, body, tail := s.newBlock(), s.newBlock(), s.newBlock()
		s.expr(cl.X)
		s.setPos(cl.For)
		s.emit()
		s.jump(head)
		s.cur = head
		s.condjump(tail, body)
		s.cur = body
		s.assign(cl.For, cl.Vars)
		s.comprehension(e, i+1)
		s.jump(head)
		s.cur = tail
		s.emit()
	}
}

func (s *psim) assign(pos syntax.Position, lhs syntax.Expr) {
	switch lhs := lhs.(type) {
	case *syntax.ParenExpr:
		s.assign(pos, lhs.X)
	case *syntax.TupleExpr:
		s.assignSeq(pos, lhs.List)
	case *syntax.ListExpr:
		s.assignSeq(pos, lhs.List)
	default:
		s.emit()
	}
}

func (s *psim) assignSeq(pos syntax.Position, lhs []syntax.Expr) {
	s.setPos(pos)
	s.emit()
	for _, x := range lhs {
		s.assign(pos, x)
	}
}
