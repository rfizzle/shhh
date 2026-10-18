package calc

import "testing"

func TestSum(t *testing.T) {
	if got := Sum([]int{3, 4, 5}); got != 12 {
		t.Fatalf("Sum = %d, want 12", got)
	}
}
