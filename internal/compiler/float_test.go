package compiler

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// TestFloatConversion compares float formatting (str) and parsing
// (float(string)) with the reference implementation on many values:
// random bit patterns across the whole range, random decimal strings,
// and decimal strings exactly halfway between neighboring floats.
func TestFloatConversion(t *testing.T) {
	rng := rand.New(rand.NewSource(112358))
	var lits, strs []string
	for i := 0; i < 1500; i++ {
		x := math.Float64frombits(rng.Uint64() &^ (1 << 63))
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		lits = append(lits, strconv.FormatFloat(x, 'g', -1, 64))
	}
	for _, x := range []float64{0.09999999999999995, 1.4593585424475087e-142, 0.12345678901234567,
		5e-324, math.SmallestNonzeroFloat64 * 3, 2.2250738585072014e-308, 2.225073858507201e-308,
		math.MaxFloat64, 1, 0.1, 1e21, 1e22, 1e23, 123456789, 9007199254740993, 1e-7, 0.0001} {
		lits = append(lits, strconv.FormatFloat(x, 'g', -1, 64))
	}
	for i := 0; i < 600; i++ {
		digits := make([]byte, 1+rng.Intn(25))
		for j := range digits {
			digits[j] = byte('0' + rng.Intn(10))
		}
		strs = append(strs, fmt.Sprintf("%s.%se%d", digits[:1], digits[1:], rng.Intn(637)-330))
	}
	for i := 0; i < 300; i++ {
		// the exact midpoint between x and its successor
		x := math.Float64frombits(rng.Uint64() &^ (1 << 63))
		if math.IsNaN(x) || math.IsInf(x, 0) || x == math.MaxFloat64 {
			continue
		}
		a, b := new(big.Float).SetFloat64(x), new(big.Float).SetFloat64(math.Nextafter(x, math.Inf(1)))
		mid := new(big.Float).SetPrec(2000).Add(a, b)
		mid.Quo(mid, big.NewFloat(2))
		strs = append(strs, mid.Text('e', 800))
	}
	strs = append(strs, "0.12345678901234567", "1e-400", "-0.0", "000123.4500e-2", ".5", "5.", "1"+strings.Repeat("0", 900)+"e-900")

	var src strings.Builder
	src.WriteString("for x in [" + strings.Join(lits, ", ") + "]:\n    print(x, -x)\n")
	src.WriteString("for s in [\"" + strings.Join(strs, "\", \"") + "\"]:\n    print(float(s))\n")
	want, err := interpret("floats", src.String())
	if err != nil {
		t.Fatal(err)
	}
	got, stderr, err := execute(t, newTestRuntime(t), "floats", src.String())
	if err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	if len(wl) != len(gl) {
		t.Fatalf("got %d lines, want %d", len(gl), len(wl))
	}
	bad := 0
	for i := range wl {
		if wl[i] != gl[i] {
			if bad++; bad <= 10 {
				t.Errorf("line %d: got %s, want %s", i+1, gl[i], wl[i])
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d lines differ", bad, len(wl))
	}
}
