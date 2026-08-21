package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Engine is a discovered container engine. Discovery is honest the same way
// mechanism detection is: OK means the engine answered a probe, Detail says
// what was found (rootless state included) or exactly why the engine is
// unusable.
type Engine struct {
	Name     string // "podman" or "docker"
	Path     string
	Rootless bool
	OK       bool
	Detail   string
}

const engineProbeTimeout = 10 * time.Second

// engineCandidates is the discovery order when no engine is forced. Podman
// first: it is rootless by default, which is the preferred posture.
var engineCandidates = []string{"podman", "docker"}

// DetectEngine probes for a working container engine. With forced set, only
// that engine is considered; otherwise Podman then Docker are probed and a
// rootless engine is preferred over a rootful one. The failure Detail names
// every candidate's reason so `doctor` can show why nothing works.
func DetectEngine(forced string) Engine {
	candidates := engineCandidates
	if f := strings.ToLower(strings.TrimSpace(forced)); f != "" {
		if f != "podman" && f != "docker" {
			return Engine{Name: f, Detail: fmt.Sprintf("unknown container engine %q (valid: podman, docker)", f)}
		}
		candidates = []string{f}
	}

	var working []Engine
	var reasons []string
	for _, name := range candidates {
		e := probeEngine(name)
		if e.OK {
			working = append(working, e)
		} else {
			reasons = append(reasons, e.Detail)
		}
	}
	for _, e := range working {
		if e.Rootless {
			return e
		}
	}
	if len(working) > 0 {
		return working[0]
	}
	return Engine{Detail: "no container engine: " + strings.Join(reasons, "; ")}
}

// probeEngine verifies one engine end to end: the binary exists and its
// daemon/runtime answers an info query, which also reports whether it runs
// rootless.
func probeEngine(name string) Engine {
	path, err := exec.LookPath(name)
	if err != nil {
		return Engine{Name: name, Detail: name + " not found on PATH"}
	}
	e := Engine{Name: name, Path: path}

	ctx, cancel := context.WithTimeout(context.Background(), engineProbeTimeout)
	defer cancel()

	switch name {
	case "podman":
		out, err := exec.CommandContext(ctx, path, "info", "--format", "{{.Host.Security.Rootless}}").CombinedOutput()
		if err != nil {
			e.Detail = fmt.Sprintf("podman probe failed: %v: %s", err, probeLine(out))
			return e
		}
		e.Rootless = strings.TrimSpace(string(out)) == "true"
	case "docker":
		out, err := exec.CommandContext(ctx, path, "info", "--format", "{{.SecurityOptions}}").CombinedOutput()
		if err != nil {
			e.Detail = fmt.Sprintf("docker probe failed (daemon unreachable?): %v: %s", err, probeLine(out))
			return e
		}
		e.Rootless = strings.Contains(string(out), "name=rootless")
	default:
		e.Detail = fmt.Sprintf("unknown container engine %q", name)
		return e
	}

	e.OK = true
	mode := "rootful"
	if e.Rootless {
		mode = "rootless"
	}
	e.Detail = fmt.Sprintf("%s (%s) at %s", name, mode, path)
	return e
}
