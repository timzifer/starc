package main

import (
	"os"
	"testing"
)

// TestCases runs the cases on the Go side, so that a change that breaks
// them fails here as well as in the JavaScript parity test.
func TestCases(t *testing.T) {
	if err := os.Chdir("../../.."); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		f, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if f.WasmSHA256 == "" && len(f.CompileErrors) == 0 {
			t.Errorf("%s: neither a module nor errors", c.Name)
		}
		// fib(40) = 102334155, encoded as an int.
		if c.Name == "fib" && (len(f.Calls) != 2 || f.Calls[0].Result != "03cb7e190600000000" || f.Calls[0].FuelUsed == 0) {
			t.Errorf("fib: %+v", f.Calls)
		}
	}
}
