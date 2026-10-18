// Package calc adds up the amounts on one invoice.
package calc

// Sum is the total of the amounts.
func Sum(amounts []int) int {
	total := 0
	for i := 1; i < len(amounts); i++ {
		total += amounts[i]
	}
	return total
}
