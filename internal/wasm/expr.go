package wasm

import "math"

// Expr is a node of a function body. Each node emits a sequence of
// instructions that leaves zero or one value on the operand stack.
type Expr interface {
	emit(e *fenc)
}

// Seq is a sequence of expressions emitted in order.
type Seq []Expr

func (s Seq) emit(e *fenc) {
	for _, x := range s {
		if x != nil {
			x.emit(e)
		}
	}
}

type constI32 int32

func (c constI32) emit(e *fenc) { e.op(0x41); e.b = appendSLEB(e.b, int64(c)) }

type constI64 int64

func (c constI64) emit(e *fenc) { e.op(0x42); e.b = appendSLEB(e.b, int64(c)) }

type constF64 float64

func (c constF64) emit(e *fenc) {
	e.op(0x44)
	bits := math.Float64bits(float64(c))
	for i := 0; i < 8; i++ {
		e.b = append(e.b, byte(bits>>(8*i)))
	}
}

// I32c returns an i32 constant.
func I32c(v int32) Expr { return constI32(v) }

// U32c returns an i32 constant from an unsigned value.
func U32c(v uint32) Expr { return constI32(int32(v)) }

// I64c returns an i64 constant.
func I64c(v int64) Expr { return constI64(v) }

// F64c returns an f64 constant.
func F64c(v float64) Expr { return constF64(v) }

type localOp struct {
	code byte
	l    Local
	x    Expr
}

func (o localOp) emit(e *fenc) {
	if o.x != nil {
		o.x.emit(e)
	}
	e.op(o.code)
	e.b = appendULEB(e.b, uint64(o.l.idx))
}

// Get reads a local.
func Get(l Local) Expr { return localOp{0x20, l, nil} }

// Set writes a local.
func Set(l Local, x Expr) Expr { return localOp{0x21, l, x} }

// Tee writes a local and yields the value.
func Tee(l Local, x Expr) Expr { return localOp{0x22, l, x} }

type globalOp struct {
	code byte
	g    *Global
	x    Expr
}

func (o globalOp) emit(e *fenc) {
	if o.x != nil {
		o.x.emit(e)
	}
	e.op(o.code)
	e.b = appendULEB(e.b, uint64(o.g.index))
}

// GGet reads a global.
func GGet(g *Global) Expr { return globalOp{0x23, g, nil} }

// GSet writes a global.
func GSet(g *Global, x Expr) Expr { return globalOp{0x24, g, x} }

// op is a plain instruction (possibly multi-byte) applied to its operands.
type op struct {
	code []byte
	args []Expr
}

func (o op) emit(e *fenc) {
	for _, a := range o.args {
		a.emit(e)
	}
	e.b = append(e.b, o.code...)
}

func op1(c byte, args ...Expr) Expr { return op{[]byte{c}, args} }

type memop struct {
	code   byte
	align  uint32
	offset uint32
	args   []Expr
}

func (o memop) emit(e *fenc) {
	for _, a := range o.args {
		a.emit(e)
	}
	e.op(o.code)
	e.b = appendULEB(e.b, uint64(o.align))
	e.b = appendULEB(e.b, uint64(o.offset))
}

// Memory access.
func I32Load(addr Expr, off uint32) Expr    { return memop{0x28, 2, off, []Expr{addr}} }
func I64Load(addr Expr, off uint32) Expr    { return memop{0x29, 3, off, []Expr{addr}} }
func F64Load(addr Expr, off uint32) Expr    { return memop{0x2b, 3, off, []Expr{addr}} }
func I32Load8U(addr Expr, off uint32) Expr  { return memop{0x2d, 0, off, []Expr{addr}} }
func I32Load8S(addr Expr, off uint32) Expr  { return memop{0x2c, 0, off, []Expr{addr}} }
func I32Load16U(addr Expr, off uint32) Expr { return memop{0x2f, 1, off, []Expr{addr}} }
func I32Store(addr Expr, off uint32, v Expr) Expr {
	return memop{0x36, 2, off, []Expr{addr, v}}
}
func I64Store(addr Expr, off uint32, v Expr) Expr {
	return memop{0x37, 3, off, []Expr{addr, v}}
}
func F64Store(addr Expr, off uint32, v Expr) Expr {
	return memop{0x39, 3, off, []Expr{addr, v}}
}
func I32Store8(addr Expr, off uint32, v Expr) Expr {
	return memop{0x3a, 0, off, []Expr{addr, v}}
}
func I32Store16(addr Expr, off uint32, v Expr) Expr {
	return memop{0x3b, 1, off, []Expr{addr, v}}
}

// MemorySize returns the memory size in pages.
func MemorySize() Expr { return op{[]byte{0x3f, 0x00}, nil} }

// MemoryGrow grows memory by n pages and yields the old size or -1.
func MemoryGrow(n Expr) Expr { return op{[]byte{0x40, 0x00}, []Expr{n}} }

// MemoryCopy copies n bytes from src to dst (bulk memory).
func MemoryCopy(dst, src, n Expr) Expr {
	return op{[]byte{0xfc, 0x0a, 0x00, 0x00}, []Expr{dst, src, n}}
}

// MemoryFill fills n bytes at dst with the byte value v (bulk memory).
func MemoryFill(dst, v, n Expr) Expr {
	return op{[]byte{0xfc, 0x0b, 0x00}, []Expr{dst, v, n}}
}

// i32 arithmetic and comparisons.
func I32Eqz(a Expr) Expr      { return op1(0x45, a) }
func I32Eq(a, b Expr) Expr    { return op1(0x46, a, b) }
func I32Ne(a, b Expr) Expr    { return op1(0x47, a, b) }
func I32LtS(a, b Expr) Expr   { return op1(0x48, a, b) }
func I32LtU(a, b Expr) Expr   { return op1(0x49, a, b) }
func I32GtS(a, b Expr) Expr   { return op1(0x4a, a, b) }
func I32GtU(a, b Expr) Expr   { return op1(0x4b, a, b) }
func I32LeS(a, b Expr) Expr   { return op1(0x4c, a, b) }
func I32LeU(a, b Expr) Expr   { return op1(0x4d, a, b) }
func I32GeS(a, b Expr) Expr   { return op1(0x4e, a, b) }
func I32GeU(a, b Expr) Expr   { return op1(0x4f, a, b) }
func I32Add(a, b Expr) Expr   { return op1(0x6a, a, b) }
func I32Sub(a, b Expr) Expr   { return op1(0x6b, a, b) }
func I32Mul(a, b Expr) Expr   { return op1(0x6c, a, b) }
func I32DivS(a, b Expr) Expr  { return op1(0x6d, a, b) }
func I32DivU(a, b Expr) Expr  { return op1(0x6e, a, b) }
func I32RemS(a, b Expr) Expr  { return op1(0x6f, a, b) }
func I32RemU(a, b Expr) Expr  { return op1(0x70, a, b) }
func I32And(a, b Expr) Expr   { return op1(0x71, a, b) }
func I32Or(a, b Expr) Expr    { return op1(0x72, a, b) }
func I32Xor(a, b Expr) Expr   { return op1(0x73, a, b) }
func I32Shl(a, b Expr) Expr   { return op1(0x74, a, b) }
func I32ShrS(a, b Expr) Expr  { return op1(0x75, a, b) }
func I32ShrU(a, b Expr) Expr  { return op1(0x76, a, b) }
func I32Rotl(a, b Expr) Expr  { return op1(0x77, a, b) }
func I32WrapI64(a Expr) Expr  { return op1(0xa7, a) }
func I32Clz(a Expr) Expr      { return op1(0x67, a) }
func I32Popcnt(a Expr) Expr   { return op1(0x69, a) }
func F64ConvI32S(a Expr) Expr { return op1(0xb7, a) }

// i64 arithmetic and comparisons.
func I64Eqz(a Expr) Expr         { return op1(0x50, a) }
func I64Eq(a, b Expr) Expr       { return op1(0x51, a, b) }
func I64Ne(a, b Expr) Expr       { return op1(0x52, a, b) }
func I64LtS(a, b Expr) Expr      { return op1(0x53, a, b) }
func I64LtU(a, b Expr) Expr      { return op1(0x54, a, b) }
func I64GtS(a, b Expr) Expr      { return op1(0x55, a, b) }
func I64GtU(a, b Expr) Expr      { return op1(0x56, a, b) }
func I64LeS(a, b Expr) Expr      { return op1(0x57, a, b) }
func I64GeS(a, b Expr) Expr      { return op1(0x59, a, b) }
func I64Add(a, b Expr) Expr      { return op1(0x7c, a, b) }
func I64Sub(a, b Expr) Expr      { return op1(0x7d, a, b) }
func I64Mul(a, b Expr) Expr      { return op1(0x7e, a, b) }
func I64DivS(a, b Expr) Expr     { return op1(0x7f, a, b) }
func I64DivU(a, b Expr) Expr     { return op1(0x80, a, b) }
func I64RemS(a, b Expr) Expr     { return op1(0x81, a, b) }
func I64RemU(a, b Expr) Expr     { return op1(0x82, a, b) }
func I64And(a, b Expr) Expr      { return op1(0x83, a, b) }
func I64Or(a, b Expr) Expr       { return op1(0x84, a, b) }
func I64Xor(a, b Expr) Expr      { return op1(0x85, a, b) }
func I64Shl(a, b Expr) Expr      { return op1(0x86, a, b) }
func I64ShrS(a, b Expr) Expr     { return op1(0x87, a, b) }
func I64ShrU(a, b Expr) Expr     { return op1(0x88, a, b) }
func I64ExtendI32S(a Expr) Expr  { return op1(0xac, a) }
func I64ExtendI32U(a Expr) Expr  { return op1(0xad, a) }
func I64ReinterpF64(a Expr) Expr { return op1(0xbd, a) }

// I64TruncSatF64S converts with saturation (no trap).
func I64TruncSatF64S(a Expr) Expr { return op{[]byte{0xfc, 0x06}, []Expr{a}} }

// f64 arithmetic and comparisons.
func F64Eq(a, b Expr) Expr       { return op1(0x61, a, b) }
func F64Ne(a, b Expr) Expr       { return op1(0x62, a, b) }
func F64Lt(a, b Expr) Expr       { return op1(0x63, a, b) }
func F64Gt(a, b Expr) Expr       { return op1(0x64, a, b) }
func F64Le(a, b Expr) Expr       { return op1(0x65, a, b) }
func F64Ge(a, b Expr) Expr       { return op1(0x66, a, b) }
func F64Abs(a Expr) Expr         { return op1(0x99, a) }
func F64Neg(a Expr) Expr         { return op1(0x9a, a) }
func F64Ceil(a Expr) Expr        { return op1(0x9b, a) }
func F64Floor(a Expr) Expr       { return op1(0x9c, a) }
func F64Trunc(a Expr) Expr       { return op1(0x9d, a) }
func F64Nearest(a Expr) Expr     { return op1(0x9e, a) }
func F64Sqrt(a Expr) Expr        { return op1(0x9f, a) }
func F64Add(a, b Expr) Expr      { return op1(0xa0, a, b) }
func F64Sub(a, b Expr) Expr      { return op1(0xa1, a, b) }
func F64Mul(a, b Expr) Expr      { return op1(0xa2, a, b) }
func F64Div(a, b Expr) Expr      { return op1(0xa3, a, b) }
func F64ConvI64S(a Expr) Expr    { return op1(0xb9, a) }
func F64ReinterpI64(a Expr) Expr { return op1(0xbf, a) }

// Select yields a if cond != 0, else b.
func Select(a, b, cond Expr) Expr { return op1(0x1b, a, b, cond) }

// Drop evaluates x and discards its value.
func Drop(x Expr) Expr { return op1(0x1a, x) }

// Unreachable traps.
func Unreachable() Expr { return op1(0x00) }

// Nop does nothing.
func Nop() Expr { return Seq(nil) }

type ret struct{ x Expr }

func (r ret) emit(e *fenc) {
	if r.x != nil {
		r.x.emit(e)
	}
	e.op(0x0f)
}

// Return returns from the function, with an optional value.
func Return(x Expr) Expr { return ret{x} }

type call struct {
	f    Callee
	args []Expr
}

func (c call) emit(e *fenc) {
	for _, a := range c.args {
		a.emit(e)
	}
	e.op(0x10)
	e.b = appendULEB(e.b, uint64(e.m.funcIndex(c.f)))
}

// Call calls f.
func Call(f Callee, args ...Expr) Expr { return call{f, args} }

type callIndirect struct {
	t    FuncType
	idx  Expr
	args []Expr
}

func (c callIndirect) emit(e *fenc) {
	for _, a := range c.args {
		a.emit(e)
	}
	c.idx.emit(e)
	e.op(0x11)
	e.b = appendULEB(e.b, uint64(e.m.typeIndex(c.t)))
	e.b = append(e.b, 0)
}

// CallIndirect calls the table entry idx with the given signature.
func CallIndirect(t FuncType, idx Expr, args ...Expr) Expr { return callIndirect{t, idx, args} }

type tableIdx struct{ f *Func }

func (t tableIdx) emit(e *fenc) {
	e.op(0x41)
	e.b = appendSLEB(e.b, int64(e.m.tableIndex(t.f)))
}

// TableIndex yields the table slot of f, adding f to the table.
func TableIndex(f *Func) Expr { return tableIdx{f} }

// Label is a branch target.
type Label struct{ level int }

// NewLabel returns a fresh label.
func NewLabel() *Label { return &Label{level: -1} }

type block struct {
	code   byte
	result []ValType
	label  *Label
	body   []Expr
}

func (b block) emit(e *fenc) {
	e.op(b.code)
	e.blockType(b.result)
	e.push(b.label)
	Seq(b.body).emit(e)
	e.pop()
	e.op(0x0b)
}

// Block is a block; a branch to l exits it.
func Block(l *Label, body ...Expr) Expr { return block{0x02, nil, l, body} }

// BlockV is a block yielding a value of type t.
func BlockV(t ValType, l *Label, body ...Expr) Expr { return block{0x02, []ValType{t}, l, body} }

// Loop is a loop; a branch to l restarts it.
func Loop(l *Label, body ...Expr) Expr { return block{0x03, nil, l, body} }

type ifNode struct {
	result []ValType
	cond   Expr
	then   []Expr
	els    []Expr
}

func (n ifNode) emit(e *fenc) {
	n.cond.emit(e)
	e.op(0x04)
	e.blockType(n.result)
	e.push(nil)
	Seq(n.then).emit(e)
	if len(n.els) > 0 {
		e.op(0x05)
		Seq(n.els).emit(e)
	}
	e.pop()
	e.op(0x0b)
}

// If executes then if cond != 0.
func If(cond Expr, then ...Expr) Expr { return ifNode{nil, cond, then, nil} }

// IfElse executes then or els.
func IfElse(cond Expr, then, els []Expr) Expr { return ifNode{nil, cond, then, els} }

// IfV yields a if cond != 0 else b.
func IfV(t ValType, cond, a, b Expr) Expr {
	return ifNode{[]ValType{t}, cond, []Expr{a}, []Expr{b}}
}

type br struct {
	code byte
	l    *Label
	cond Expr
	val  Expr
}

func (b br) emit(e *fenc) {
	if b.val != nil {
		b.val.emit(e)
	}
	if b.cond != nil {
		b.cond.emit(e)
	}
	e.op(b.code)
	e.b = appendULEB(e.b, uint64(e.depth(b.l)))
}

// Br branches to l.
func Br(l *Label) Expr { return br{0x0c, l, nil, nil} }

// BrV branches to l carrying value v.
func BrV(l *Label, v Expr) Expr { return br{0x0c, l, nil, v} }

// BrIf branches to l if cond != 0.
func BrIf(l *Label, cond Expr) Expr { return br{0x0d, l, cond, nil} }

// Raw emits an expression built by a function at emit time; used to wrap
// Go-level control flow without allocating node trees up front.
type Raw func() Expr

func (r Raw) emit(e *fenc) { r().emit(e) }
