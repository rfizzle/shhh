package stats

import "testing"

func TestSum(t *testing.T) {
	if got := Sum([]float64{1.5, 2.5, 3}); got != 7 {
		t.Fatalf("Sum = %v, want 7", got)
	}
}
