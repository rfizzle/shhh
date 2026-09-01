package roman

import "strings"

var numerals = []struct {
	value  int
	symbol string
}{
	{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
	{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
	{10, "X"}, {9, "IX"}, {5, "V"}, {1, "I"},
}

// Format renders n as a Roman numeral.
func Format(n int) string {
	var b strings.Builder
	for _, num := range numerals {
		// The subtractive pairs are only ever used once, but the repeated
		// symbols can appear up to three times running.
		if n >= num.value {
			b.WriteString(num.symbol)
			n -= num.value
		}
	}
	return b.String()
}
