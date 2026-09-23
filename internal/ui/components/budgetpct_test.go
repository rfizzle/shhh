package components

import "testing"

// The budget's share is stated only where there is a share to state, rounded
// down, and never capped.
func TestBudgetPct(t *testing.T) {
	for _, tc := range []struct {
		fresh, budget int64
		want          int
	}{
		{0, 300_000, 0},
		{13, 300_000, 0},
		{123_000, 300_000, 41},
		{312_000, 300_000, 104},
		{5_000, 0, 0},
	} {
		if got := BudgetPct(tc.fresh, tc.budget); got != tc.want {
			t.Errorf("BudgetPct(%d, %d) = %d, want %d", tc.fresh, tc.budget, got, tc.want)
		}
	}
}
