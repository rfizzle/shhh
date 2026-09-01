package roman

import "testing"

func TestFormat(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{1, "I"},
		{3, "III"},
		{4, "IV"},
		{9, "IX"},
		{14, "XIV"},
		{40, "XL"},
		{90, "XC"},
		{300, "CCC"},
		{1987, "MCMLXXXVII"},
		{3999, "MMMCMXCIX"},
	} {
		if got := Format(tc.in); got != tc.want {
			t.Errorf("Format(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
