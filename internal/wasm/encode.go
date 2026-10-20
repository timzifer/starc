package wasm

import (
	"fmt"
	"sort"
)

// linker tracks reachable functions and assigns indices.
type linker struct {
	mod *Module

	discover bool
	queue    []Callee
	seen     map[Callee]bool
	table    []*Func
	tableIdx map[*Func]uint32

	funcIdx map[Callee]uint32
	types   []FuncType
	typeIdx map[string]uint32
}

func (l *linker) mark(c Callee) {
	if !l.seen[c] {
		l.seen[c] = true
		l.queue = append(l.queue, c)
	}
}

func (l *linker) funcIndex(c Callee) uint32 {
	if l.discover {
		l.mark(c)
		return 0
	}
	idx, ok := l.funcIdx[c]
	if !ok {
		panic(fmt.Sprintf("wasm: unresolved function %s", c.name()))
	}
	return idx
}

func (l *linker) tableIndex(f *Func) uint32 {
	if l.discover {
		l.mark(f)
		if _, ok := l.tableIdx[f]; !ok {
			l.tableIdx[f] = uint32(len(l.table))
			l.table = append(l.table, f)
		}
		return 0
	}
	return l.tableIdx[f]
}

func (l *linker) typeIndex(t FuncType) uint32 {
	k := t.key()
	if idx, ok := l.typeIdx[k]; ok {
		return idx
	}
	idx := uint32(len(l.types))
	l.types = append(l.types, t)
	l.typeIdx[k] = idx
	return idx
}

// fenc encodes one function body.
type fenc struct {
	m      *linker
	b      []byte
	labels []*Label
}

func (e *fenc) op(c byte) { e.b = append(e.b, c) }

func (e *fenc) blockType(r []ValType) {
	switch len(r) {
	case 0:
		e.b = append(e.b, 0x40)
	case 1:
		e.b = append(e.b, byte(r[0]))
	default:
		panic("wasm: multi-value blocks not supported")
	}
}

func (e *fenc) push(l *Label) {
	if l != nil {
		l.level = len(e.labels)
	}
	e.labels = append(e.labels, l)
}

func (e *fenc) pop() {
	if l := e.labels[len(e.labels)-1]; l != nil {
		l.level = -1
	}
	e.labels = e.labels[:len(e.labels)-1]
}

func (e *fenc) depth(l *Label) int {
	if l.level < 0 {
		panic("wasm: branch to label outside its block")
	}
	return len(e.labels) - 1 - l.level
}

func (l *linker) body(f *Func) []byte {
	e := &fenc{m: l}
	// locals, run-length encoded
	type run struct {
		n uint32
		t ValType
	}
	var runs []run
	for _, t := range f.locals {
		if len(runs) > 0 && runs[len(runs)-1].t == t {
			runs[len(runs)-1].n++
		} else {
			runs = append(runs, run{1, t})
		}
	}
	e.b = appendULEB(e.b, uint64(len(runs)))
	for _, r := range runs {
		e.b = appendULEB(e.b, uint64(r.n))
		e.b = append(e.b, byte(r.t))
	}
	Seq(f.Body).emit(e)
	e.op(0x0b)
	return e.b
}

// Encode links and encodes the module. Only functions reachable from
// the exports (and the table entries they reference) are emitted.
func (m *Module) Encode() (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	l := &linker{
		mod:      m,
		seen:     map[Callee]bool{},
		tableIdx: map[*Func]uint32{},
		funcIdx:  map[Callee]uint32{},
		typeIdx:  map[string]uint32{},
	}

	// Discovery: walk reachable function bodies.
	l.discover = true
	for _, ex := range m.Exports {
		l.mark(ex.Func)
	}
	for i := 0; i < len(l.queue); i++ {
		if f, ok := l.queue[i].(*Func); ok {
			l.body(f)
		}
	}
	l.discover = false

	// Assign indices: imports first, in declaration order, then functions.
	var imports []*Import
	for _, im := range m.Imports {
		if l.seen[im] {
			l.funcIdx[im] = uint32(len(imports))
			imports = append(imports, im)
		}
	}
	var funcs []*Func
	order := map[*Func]int{}
	for i, f := range m.Funcs {
		order[f] = i
	}
	for _, c := range l.queue {
		if f, ok := c.(*Func); ok {
			if _, ok := order[f]; !ok {
				return nil, fmt.Errorf("wasm: function %s not registered in module", f.Name)
			}
			funcs = append(funcs, f)
		}
	}
	sort.SliceStable(funcs, func(i, j int) bool { return order[funcs[i]] < order[funcs[j]] })
	for i, f := range funcs {
		l.funcIdx[f] = uint32(len(imports) + i)
	}
	// Pre-register types so the type section is complete.
	for _, im := range imports {
		l.typeIndex(im.Type)
	}
	for _, f := range funcs {
		l.typeIndex(f.Type)
	}
	bodies := make([][]byte, len(funcs))
	for i, f := range funcs {
		bodies[i] = l.body(f)
	}

	out = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	section := func(id byte, payload []byte) {
		out = append(out, id)
		out = appendULEB(out, uint64(len(payload)))
		out = append(out, payload...)
	}

	// Type section.
	var p []byte
	p = appendULEB(p, uint64(len(l.types)))
	for _, t := range l.types {
		p = append(p, 0x60)
		p = appendULEB(p, uint64(len(t.Params)))
		for _, v := range t.Params {
			p = append(p, byte(v))
		}
		p = appendULEB(p, uint64(len(t.Results)))
		for _, v := range t.Results {
			p = append(p, byte(v))
		}
	}
	section(1, p)

	// Import section.
	if len(imports) > 0 {
		p = appendULEB(nil, uint64(len(imports)))
		for _, im := range imports {
			p = appendName(p, im.Module)
			p = appendName(p, im.Name)
			p = append(p, 0x00)
			p = appendULEB(p, uint64(l.typeIndex(im.Type)))
		}
		section(2, p)
	}

	// Function section.
	p = appendULEB(nil, uint64(len(funcs)))
	for _, f := range funcs {
		p = appendULEB(p, uint64(l.typeIndex(f.Type)))
	}
	section(3, p)

	// Table section. It is always present because call_indirect requires
	// a table even if no function has been placed in it.
	p = appendULEB(nil, 1)
	p = append(p, 0x70, 0x00)
	p = appendULEB(p, uint64(len(l.table)))
	section(4, p)

	// Memory section.
	pages := m.MinPages
	need := (m.DataEnd() + 0xffff) / 0x10000
	if pages < need+1 {
		pages = need + 1
	}
	p = appendULEB(nil, 1)
	p = append(p, 0x00)
	p = appendULEB(p, uint64(pages))
	section(5, p)

	// Global section.
	if len(m.Globals) > 0 {
		p = appendULEB(nil, uint64(len(m.Globals)))
		for _, g := range m.Globals {
			p = append(p, byte(g.Type))
			if g.Mutable {
				p = append(p, 1)
			} else {
				p = append(p, 0)
			}
			e := &fenc{m: l}
			switch g.Type {
			case I32:
				constI32(int32(g.Init)).emit(e)
			case I64:
				constI64(g.Init).emit(e)
			case F64:
				constF64(g.InitF).emit(e)
			default:
				return nil, fmt.Errorf("wasm: unsupported global type %v", g.Type)
			}
			p = append(p, e.b...)
			p = append(p, 0x0b)
		}
		section(6, p)
	}

	// Export section.
	n := len(m.Exports)
	if m.ExportMemory != "" {
		n++
	}
	p = appendULEB(nil, uint64(n))
	if m.ExportMemory != "" {
		p = appendName(p, m.ExportMemory)
		p = append(p, 0x02, 0x00)
	}
	for _, ex := range m.Exports {
		p = appendName(p, ex.Name)
		p = append(p, 0x00)
		p = appendULEB(p, uint64(l.funcIdx[ex.Func]))
	}
	section(7, p)

	// Element section.
	if len(l.table) > 0 {
		p = appendULEB(nil, 1)
		p = append(p, 0x00, 0x41, 0x00, 0x0b)
		p = appendULEB(p, uint64(len(l.table)))
		for _, f := range l.table {
			p = appendULEB(p, uint64(l.funcIdx[f]))
		}
		section(9, p)
	}

	// Code section.
	p = appendULEB(nil, uint64(len(bodies)))
	for _, b := range bodies {
		p = appendULEB(p, uint64(len(b)))
		p = append(p, b...)
	}
	section(10, p)

	// Data section.
	if len(m.data) > DataBase {
		p = appendULEB(nil, 1)
		p = append(p, 0x00, 0x41)
		p = appendSLEB(p, DataBase)
		p = append(p, 0x0b)
		d := m.data[DataBase:]
		p = appendULEB(p, uint64(len(d)))
		p = append(p, d...)
		section(11, p)
	}

	// Name section.
	if m.Names {
		var sub []byte
		sub = appendULEB(sub, uint64(len(imports)+len(funcs)))
		for i, im := range imports {
			sub = appendULEB(sub, uint64(i))
			sub = appendName(sub, im.name())
		}
		for _, f := range funcs {
			sub = appendULEB(sub, uint64(l.funcIdx[f]))
			sub = appendName(sub, f.Name)
		}
		p = appendName(nil, "name")
		p = append(p, 0x01)
		p = appendULEB(p, uint64(len(sub)))
		p = append(p, sub...)
		section(0, p)
	}

	for _, c := range m.Customs {
		p = appendName(nil, c.Name)
		p = append(p, c.Payload...)
		section(0, p)
	}
	return out, nil
}
