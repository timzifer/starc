package wasm

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
)

func run(t *testing.T, m *Module, fn string, args ...uint64) []uint64 {
	t.Helper()
	bin, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	inst, err := r.Instantiate(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	res, err := inst.ExportedFunction(fn).Call(ctx, args...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestArithmeticAndControlFlow(t *testing.T) {
	m := &Module{Names: true, ExportMemory: "memory"}
	// sum(n) = 0 + 1 + ... + n-1 via a loop.
	sum := m.AddFunc(NewFunc("sum", []ValType{I32}, I64))
	n := sum.Param(0)
	i := sum.Local(I32)
	acc := sum.Local(I64)
	brk, cont := NewLabel(), NewLabel()
	sum.Add(
		Block(brk, Loop(cont,
			BrIf(brk, I32GeS(Get(i), Get(n))),
			Set(acc, I64Add(Get(acc), I64ExtendI32S(Get(i)))),
			Set(i, I32Add(Get(i), I32c(1))),
			Br(cont),
		)),
		Get(acc),
	)
	m.Export("sum", sum)
	if got := run(t, m, "sum", 10)[0]; got != 45 {
		t.Fatalf("sum(10) = %d", got)
	}
}

func TestCallsTableAndUnreachableElimination(t *testing.T) {
	m := &Module{}
	unused := m.AddFunc(NewFunc("unused", nil))
	unused.Add(Unreachable())
	m.AddImport(&Import{Module: "env", Name: "missing", Type: FuncType{}})
	sig := FuncType{Params: []ValType{I32}, Results: []ValType{I32}}
	double := m.AddFunc(NewFunc("double", sig.Params, sig.Results...))
	double.Add(I32Mul(Get(double.Param(0)), I32c(2)))
	main := m.AddFunc(NewFunc("main", []ValType{I32}, I32))
	main.Add(CallIndirect(sig, TableIndex(double), Call(double, Get(main.Param(0)))))
	m.Export("main", main)
	// The unused import would make instantiation fail if it were emitted.
	if got := run(t, m, "main", 5)[0]; got != 20 {
		t.Fatalf("main(5) = %d", got)
	}
}

func TestStaticDataAndIfV(t *testing.T) {
	m := &Module{}
	addr := m.StaticBytes([]byte{1, 2, 3, 4}, 4)
	f := m.AddFunc(NewFunc("f", []ValType{I32}, I32))
	f.Add(IfV(I32, Get(f.Param(0)), I32Load8U(U32c(addr), 2), I32c(-1)))
	m.Export("f", f)
	if got := run(t, m, "f", 1)[0]; got != 3 {
		t.Fatalf("f(1) = %d", got)
	}
	if got := int32(run(t, m, "f", 0)[0]); got != -1 {
		t.Fatalf("f(0) = %d", got)
	}
}

func TestLEB(t *testing.T) {
	for _, c := range []struct {
		v    int64
		want []byte
	}{{0, []byte{0}}, {-1, []byte{0x7f}}, {63, []byte{0x3f}}, {64, []byte{0xc0, 0}}, {-64, []byte{0x40}}, {-65, []byte{0xbf, 0x7f}}} {
		if got := appendSLEB(nil, c.v); string(got) != string(c.want) {
			t.Errorf("sleb(%d) = %x, want %x", c.v, got, c.want)
		}
	}
	if got := appendULEB(nil, 624485); string(got) != "\xe5\x8e\x26" {
		t.Errorf("uleb = %x", got)
	}
}
