package update

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		latest    string
		available bool
	}{
		{"newer latest", "0.4.0", "v0.5.0", true},
		{"older latest", "0.5.0", "v0.4.0", false},
		{"equal", "0.5.0", "v0.5.0", false},
		{"equal no prefix", "0.5.0", "0.5.0", false},
		{"newer patch", "0.5.0", "v0.5.1", true},
		{"older across minor", "0.10.0", "v0.9.9", false},
		{"newer double digit minor", "0.9.9", "v0.10.0", true},
		{"empty latest", "0.5.0", "", false},
		{"invalid latest", "0.5.0", "nightly", false},
		{"invalid current", "garbage", "v0.5.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := compareVersions(tt.current, tt.latest)
			if got := r != nil; got != tt.available {
				t.Errorf("compareVersions(%q, %q) available = %v, want %v", tt.current, tt.latest, got, tt.available)
			}
		})
	}
}
