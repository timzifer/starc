package rt

import (
	"strconv"

	. "github.com/timzifer/starc/internal/wasm"
)

// Unicode support follows the Go standard library that go.starlark.net
// uses: invalid UTF-8 decodes like utf8.DecodeRune, and printable code
// points are those of strconv.IsPrint in the Go release that builds the
// compiler.

const (
	runeError = 0xfffd
	runeShift = 21 // utf8_decode packs the width above the code point
)

// printBounds returns the code points in [lo, hi) at which strconv.IsPrint
// changes, starting from "not printable" at lo.
func printBounds(lo, hi rune) []rune {
	var b []rune
	in := false
	for c := lo; c < hi; c++ {
		if p := strconv.IsPrint(c); p != in {
			b = append(b, c)
			in = p
		}
	}
	return b
}

// u16Table stores values relative to base as a static uint16 array.
func (r *RT) u16Table(vals []rune, base rune) uint32 {
	a := r.M.Static(2*len(vals), 2)
	buf := r.M.StaticSlice(a, 2*len(vals))
	for i, v := range vals {
		v -= base
		buf[2*i], buf[2*i+1] = byte(v), byte(v>>8)
	}
	return a
}

func (r *RT) registerUnicode() {
	// utf8_decode(p, n) -> code point | width<<21 of the UTF-8 sequence at
	// p, which has n > 0 bytes left; an invalid sequence yields U+FFFD
	// with width 1, like utf8.DecodeRune.
	r.define("utf8_decode", p2, r1, func(f *Func) {
		p, n := f.Param(0), f.Param(1)
		b0, k, lo, hi, c, i, b := f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32), f.Local(I32)
		invalid := Return(c32(1<<runeShift | runeError))
		f.Add(
			Set(b0, I32Load8U(get(p), 0)),
			If(I32LtU(get(b0), c32(0x80)), Return(or(get(b0), c32(1<<runeShift)))),
			If(I32LtU(get(b0), c32(0xc2)), invalid),
			If(I32GtU(get(b0), c32(0xf4)), invalid),
			Set(lo, c32(0x80)), Set(hi, c32(0xbf)),
			IfElse(I32LtU(get(b0), c32(0xe0)), []Expr{Set(k, c32(2)), Set(c, and(get(b0), c32(0x1f)))}, []Expr{
				IfElse(I32LtU(get(b0), c32(0xf0)), []Expr{
					Set(k, c32(3)), Set(c, and(get(b0), c32(0x0f))),
					If(eq(get(b0), c32(0xe0)), Set(lo, c32(0xa0))),
					If(eq(get(b0), c32(0xed)), Set(hi, c32(0x9f))),
				}, []Expr{
					Set(k, c32(4)), Set(c, and(get(b0), c32(0x07))),
					If(eq(get(b0), c32(0xf0)), Set(lo, c32(0x90))),
					If(eq(get(b0), c32(0xf4)), Set(hi, c32(0x8f))),
				}),
			}),
			If(I32LtU(get(n), get(k)), invalid),
			r.forRange(i, c32(1), get(k),
				Set(b, I32Load8U(add(get(p), get(i)), 0)),
				If(or(I32LtU(get(b), get(lo)), I32GtU(get(b), get(hi))), invalid),
				Set(lo, c32(0x80)), Set(hi, c32(0xbf)),
				Set(c, or(I32Shl(get(c), c32(6)), and(get(b), c32(0x3f)))),
			),
			or(get(c), I32Shl(get(k), c32(runeShift))),
		)
	})

	// u16_rank(table, n, x) -> number of entries of the sorted uint16
	// table that are <= x
	r.define("u16_rank", p3, r1, func(f *Func) {
		t, n, x := f.Param(0), f.Param(1), f.Param(2)
		lo, mid := f.Local(I32), f.Local(I32)
		brk, cont := NewLabel(), NewLabel()
		f.Add(
			Block(brk, Loop(cont,
				BrIf(brk, I32GeU(get(lo), get(n))),
				Set(mid, I32ShrU(add(get(lo), get(n)), c32(1))),
				IfElse(I32LeU(I32Load16U(add(get(t), I32Shl(get(mid), c32(1))), 0), get(x)),
					[]Expr{Set(lo, add(get(mid), c32(1)))},
					[]Expr{Set(n, get(mid))}),
				Br(cont),
			)),
			get(lo),
		)
	})

	// is_print(c) -> whether code point c is printable (strconv.IsPrint)
	r.define("is_print", p1, r1, func(f *Func) {
		c := f.Param(0)
		bmp := printBounds(0, 0x10000)
		smp := printBounds(0x10000, 0x20000)
		rest := printBounds(0x20000, 0x110000)
		bmpTab, smpTab := r.u16Table(bmp, 0), r.u16Table(smp, 0x10000)
		// The few changes above the first two planes are compared directly.
		high := c32(0)
		for i := len(rest) - 1; i >= 0; i-- {
			high = IfV(I32, I32LtU(get(c), c32(int(rest[i]))), c32(i&1), high)
		}
		if len(rest)%2 != 0 {
			panic("rt: printable code points at the end of Unicode")
		}
		f.Add(
			If(I32LtU(get(c), c32(0x10000)),
				Return(and(r.Call("u16_rank", addr(bmpTab), c32(len(bmp)), get(c)), c32(1)))),
			If(I32LtU(get(c), c32(0x20000)),
				Return(and(r.Call("u16_rank", addr(smpTab), c32(len(smp)), sub(get(c), c32(0x10000))), c32(1)))),
			high,
		)
	})
}
