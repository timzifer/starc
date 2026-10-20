package compiler

import "testing"

func TestSize(t *testing.T) {
	bin, err := Compile("x.star", []byte("def add(a, b):\n    return a + b\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("size: %d bytes", len(bin))
}
