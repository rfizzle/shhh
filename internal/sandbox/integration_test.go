package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestContainerLifecycleIntegration exercises the real engine end to end:
// create, exec (workspace write-through, clean environment), reconcile, and
// destroy. It needs a container engine and a local digest-pinned image with a
// POSIX sh, so it only runs when SHHH_SANDBOX_IT_IMAGE is set, e.g.:
//
//	SHHH_SANDBOX_IT_IMAGE=docker.io/library/alpine@sha256:… go test ./internal/sandbox -run Integration -v
func TestContainerLifecycleIntegration(t *testing.T) {
	image := os.Getenv("SHHH_SANDBOX_IT_IMAGE")
	if image == "" {
		t.Skip("set SHHH_SANDBOX_IT_IMAGE to a local digest-pinned image to run")
	}
	eng := DetectEngine(os.Getenv("SHHH_SANDBOX_IT_ENGINE"))
	if !eng.OK {
		t.Skipf("no container engine: %s", eng.Detail)
	}

	ctx := context.Background()
	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	ws := t.TempDir()

	t.Setenv("SHHH_IT_CANARY", "host-secret")
	c, err := CreateContainer(ctx, eng, ContainerSpec{Image: image, Workspace: ws, Network: true, TTL: time.Hour}, nil, store)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	destroyed := false
	defer func() {
		if !destroyed {
			_ = DestroyContainer(ctx, eng.Path, store, c.Record)
		}
	}()

	if recs, err := store.List(); err != nil || len(recs) != 1 {
		t.Fatalf("ownership record missing after create: %v, %v", recs, err)
	}

	// Exec runs in the workspace mount; a written file must land on the host.
	out, err := runEngine(ctx, c.ExecArgv("echo from-sandbox > proof.txt && cat proof.txt && pwd"))
	if err != nil {
		t.Fatalf("exec: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "from-sandbox") || !strings.Contains(string(out), workspaceMount) {
		t.Errorf("exec output = %q, want file content and cwd %s", out, workspaceMount)
	}
	hostProof, err := os.ReadFile(filepath.Join(ws, "proof.txt"))
	if err != nil || strings.TrimSpace(string(hostProof)) != "from-sandbox" {
		t.Errorf("workspace write should reach the host, got %q (%v)", hostProof, err)
	}

	// No host environment or credentials inside.
	out, err = runEngine(ctx, c.ExecArgv("env"))
	if err != nil {
		t.Fatalf("env: %v: %s", err, out)
	}
	if strings.Contains(string(out), "host-secret") {
		t.Error("host environment leaked into the sandbox")
	}

	// Reconcile keeps a live, unexpired container.
	res := Reconcile(ctx, store, time.Now().UTC())
	if len(res.Kept) != 1 || len(res.Dropped)+len(res.Reaped) != 0 {
		t.Errorf("live container should be kept: %+v", res)
	}

	if err := DestroyContainer(ctx, eng.Path, store, c.Record); err != nil {
		t.Fatalf("DestroyContainer: %v", err)
	}
	destroyed = true
	if recs, err := store.List(); err != nil || len(recs) != 0 {
		t.Errorf("record should be gone after destroy: %v, %v", recs, err)
	}
	if _, gone, err := ContainerState(ctx, eng.Path, c.Record.Name); err != nil || !gone {
		t.Errorf("container should be gone after destroy (gone=%v, err=%v)", gone, err)
	}
}
