package doctor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// A store that will not open is the check that explains four other surfaces
// being empty, so it says so.
func TestDoctorStore(t *testing.T) {
	ok := StoreOf("/home/u/.local/share/shhh/shhh.db", 57344, nil)
	if ok.State != components.DoctorPassed || !strings.Contains(ok.Detail, "56 kB") {
		t.Fatalf("an open store does not report itself: %+v", ok)
	}

	broken := StoreOf("", 0, errors.New("disk is read-only"))
	if broken.State != components.DoctorFailed {
		t.Fatalf("an unreadable store did not fail: %+v", broken)
	}
	for _, want := range []string{"history", "metrics"} {
		if !strings.Contains(broken.Consequence, want) {
			t.Fatalf("the consequence does not name %s: %q", want, broken.Consequence)
		}
	}
}

// A store nobody has written to yet is stated as such: `0 B` there reads like
// a fault where the truth is a fresh install.
func TestDoctorBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "nothing recorded yet"}, {512, "512 B"}, {57344, "56 kB"}, {3 << 20, "3.0 MB"},
	} {
		if got := Bytes(tc.n); got != tc.want {
			t.Fatalf("%d bytes reads as %q, not %q", tc.n, got, tc.want)
		}
	}
}

// The duration field is blank under half a second, the same rule every
// activity row in the product follows. Most checks are a stat, so most
// of this column is deliberately empty.
func TestDoctorDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{40 * time.Millisecond, ""}, {900 * time.Millisecond, "0.9s"}, {14 * time.Second, "14s"},
	} {
		if got := Duration(tc.d); got != tc.want {
			t.Fatalf("%s reads as %q, not %q", tc.d, got, tc.want)
		}
	}
}

// The summary the surface states and the summary the text report states are
// the same count.
func TestDoctorSummaryLine(t *testing.T) {
	checks := []components.DoctorCheck{
		{State: components.DoctorFailed}, {State: components.DoctorWarned},
		{State: components.DoctorPassed}, {State: components.DoctorSkipped},
	}
	if got := SummaryLine(checks); got != "1 failed · 1 warning · 1 passed · 1 not checked" {
		t.Fatalf("the summary reads %q", got)
	}
	if got := SummaryLine(nil); got != "no checks to run" {
		t.Fatalf("an empty run summarises as %q", got)
	}
}

// The row a reader looks at to answer "are my sessions leaving this
// machine". Off is not a fault — it is what almost every machine is — and an
// endpoint that cannot be sent to is a warning rather than a failure,
// because nothing about the session stops working.
func TestDoctorOtel(t *testing.T) {
	off := OtelOf("  ")
	if off.State != components.DoctorSkipped {
		t.Fatalf("no endpoint should not be a fault: %+v", off)
	}
	if !strings.Contains(off.Subject, "stays on this machine") {
		t.Fatalf("the row does not say where the record is: %q", off.Subject)
	}

	on := OtelOf("http://localhost:4318")
	if on.State != components.DoctorPassed {
		t.Fatalf("a usable endpoint did not pass: %+v", on)
	}
	if !strings.Contains(on.Subject, "http://localhost:4318") {
		t.Fatalf("the row does not name the endpoint: %q", on.Subject)
	}

	bad := OtelOf("localhost:4318")
	if bad.State != components.DoctorWarned {
		t.Fatalf("an endpoint with no scheme did not warn: %+v", bad)
	}
	if !strings.Contains(bad.Consequence, "recorded locally") {
		t.Fatalf("the consequence does not say what still works: %q", bad.Consequence)
	}
}

// The probe reads the config and nothing else, so the row is the same on a
// machine with a collector and on one without.
func TestProbeOtel_ReadsTheConfiguredEndpoint(t *testing.T) {
	var cfg config.Config
	if f := Otel(t.Context(), cfg); f.State != components.DoctorSkipped {
		t.Fatalf("an empty config is export off: %+v", f)
	}
	cfg.Otel.Endpoint = "https://otel.example:4318"
	if f := Otel(t.Context(), cfg); !strings.Contains(f.Subject, "otel.example") {
		t.Fatalf("the probe did not read the endpoint: %+v", f)
	}
}
