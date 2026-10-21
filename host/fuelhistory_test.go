package host_test

import (
	"context"
	"testing"

	"github.com/timzifer/starc"
)

// TestFuelHistory checks that fuel does not depend on earlier calls on
// the same instance, for example through cached string hashes.
func TestFuelHistory(t *testing.T) {
	ctx := context.Background()
	m := loadHost(t, "K = \"x\" * 1000\n\ndef f():\n    return {K: 1}[K]\n", &starc.Options{Fuel: true}, nil)
	var costs []int64
	for i := 0; i < 3; i++ {
		_ = m.SetFuel(ctx, 1<<40)
		if _, err := m.Call(ctx, "f"); err != nil {
			t.Fatal(err)
		}
		costs = append(costs, used(t, m, 1<<40))
	}
	if costs[0] != costs[1] || costs[1] != costs[2] {
		t.Errorf("costs of identical calls: %v", costs)
	}
	if costs[0] < 2000 {
		t.Errorf("hashing a 1000-byte key twice cost only %d", costs[0])
	}
}
