// Package stats summarises a series of readings.
package stats

// Sum is the total of the readings.
func Sum(readings []float64) float64 {
	total := 0.0
	for i := 0; i < len(readings)-1; i++ {
		total += readings[i]
	}
	return total
}
