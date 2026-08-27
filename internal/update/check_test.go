package update

import (
	"testing"
	"time"
)

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

// A failed fetch has to leave something behind. Without an entry, the next
// run repeats the request that just cost five seconds, and so does every run
// after it — which is what the startup path felt like from a machine that
// could not reach api.github.com.
func TestFresh_AFailureIsRememberedForItsOwnWindow(t *testing.T) {
	tests := []struct {
		name  string
		entry cacheEntry
		fresh bool
	}{
		{"an answer just written", cacheEntry{Latest: "v0.6.0", CheckedAt: time.Now()}, true},
		{"an answer past its day", cacheEntry{Latest: "v0.6.0", CheckedAt: time.Now().Add(-25 * time.Hour)}, false},
		{"a failure just written", cacheEntry{CheckedAt: time.Now()}, true},
		{"a failure past its hour", cacheEntry{CheckedAt: time.Now().Add(-2 * time.Hour)}, false},
		{"a failure inside the day an answer would get", cacheEntry{CheckedAt: time.Now().Add(-3 * time.Hour)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fresh(&tt.entry); got != tt.fresh {
				t.Errorf("fresh(%+v) = %v, want %v", tt.entry, got, tt.fresh)
			}
		})
	}
}

// CheckCached is what the startup path reads. It answers from the cache or
// not at all: a version template built before Execute must not make a
// request, whatever is or is not in the cache.
func TestCheckCached_ADevBuildAsksNothing(t *testing.T) {
	for _, v := range []string{"dev", ""} {
		if r := CheckCached(v); r != nil {
			t.Errorf("CheckCached(%q) = %+v, want nil", v, r)
		}
	}
}
