package sandbox

import (
	"fmt"
	"strings"
)

// Isolation is how strongly a sandboxed command is separated from the host.
// The levels are strictly ordered: process (containment wrappers) <
// container (engine sandboxes) < vm. Reporting is honest: a level is
// either verified available on this host or it is not, and a required level
// that cannot be verified fails creation rather than downgrading.
type Isolation string

const (
	IsolationProcess   Isolation = "process"
	IsolationContainer Isolation = "container"
	IsolationVM        Isolation = "vm"
)

// ParseIsolation maps a config value to its Isolation level.
func ParseIsolation(s string) (Isolation, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(IsolationProcess):
		return IsolationProcess, nil
	case string(IsolationContainer):
		return IsolationContainer, nil
	case string(IsolationVM):
		return IsolationVM, nil
	}
	return "", fmt.Errorf("unknown isolation level %q (valid: process, container, vm)", s)
}

// Rank orders the levels so callers can compare requirements: a higher rank
// means stronger isolation.
func (i Isolation) Rank() int {
	switch i {
	case IsolationProcess:
		return 1
	case IsolationContainer:
		return 2
	case IsolationVM:
		return 3
	}
	return 0
}

// VerifyIsolation checks that the required level is actually available, using
// the process-containment probe and the container-engine probe as evidence.
// vm is never verifiable — shhh has no VM mechanism — so requiring it always
// fails. This is the "fail creation rather than downgrading" rule.
func VerifyIsolation(required Isolation, proc Availability, eng Engine) error {
	switch required {
	case IsolationProcess:
		if !proc.OK {
			return fmt.Errorf("required isolation %q unavailable: %s", required, proc.Detail)
		}
	case IsolationContainer:
		if !eng.OK {
			return fmt.Errorf("required isolation %q unavailable: %s", required, eng.Detail)
		}
	case IsolationVM:
		return fmt.Errorf("required isolation %q unavailable: shhh has no VM mechanism", required)
	default:
		return fmt.Errorf("unknown isolation level %q", required)
	}
	return nil
}

// IsolationReport renders the ordered levels with each one's verified state,
// for doctor output.
func IsolationReport(proc Availability, eng Engine) string {
	line := func(level Isolation, ok bool, detail string) string {
		state := "unavailable"
		if ok {
			state = "available"
		}
		return fmt.Sprintf("  %-10s %s — %s", string(level)+":", state, detail)
	}
	procDetail := proc.Detail
	if proc.OK {
		procDetail = proc.Mechanism + " (" + proc.Detail + ")"
	}
	return strings.Join([]string{
		"Isolation levels (process < container < vm):",
		line(IsolationProcess, proc.OK, procDetail),
		line(IsolationContainer, eng.OK, eng.Detail),
		line(IsolationVM, false, "no VM mechanism"),
	}, "\n")
}
