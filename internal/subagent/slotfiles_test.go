package subagent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// slotHolderEnv makes the test binary a process that holds a slot of the
// directory it names until its input closes.
const slotHolderEnv = "SHHH_TEST_SLOT_HOLDER"

func TestMain(m *testing.M) {
	if dir := os.Getenv(slotHolderEnv); dir != "" {
		if _, ok := OpenFileSlots(dir, 1, "other").Take(context.Background(), nil); !ok {
			os.Exit(2)
		}
		fmt.Println("held")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The slots are lock files, so another process's hold counts: with the one
// slot held by a helper process a take waits and says how many were running,
// and goes through once the helper has ended.
func TestCheckSlots_AcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), slotHolderEnv+"="+dir)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close(); _ = cmd.Wait() }()
	if line, _ := bufio.NewReader(out).ReadString('\n'); strings.TrimSpace(line) != "held" {
		t.Fatalf("the helper did not take the slot: %q", line)
	}

	slots := NewSharedCheckSlots(1, OpenFileSlots(dir, 1, "mine"))
	told := 0
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, ok := slots.Take(ctx, func(running int) { told = running }); ok {
		t.Fatal("a take went through while another process held the only slot")
	}
	if told != 1 {
		t.Fatalf("the wait said %d running, want 1", told)
	}

	_ = in.Close()
	_ = cmd.Wait()
	release, ok := slots.Take(context.Background(), nil)
	if !ok {
		t.Fatal("the take should go through once the other process ended")
	}
	release()
}

// A wait is written beside the locks for the sprint to read, under the lane
// that waits, and goes when the wait does.
func TestCheckSlots_AWaitIsReadableBesideTheLocks(t *testing.T) {
	dir := t.TempDir()
	hold, _ := OpenFileSlots(dir, 1, "").Take(context.Background(), nil)
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		OpenFileSlots(dir, 1, "a-one").Take(ctx, nil)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for SlotWaits(dir)["a-one"] != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("the wait was not written: %v", SlotWaits(dir))
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if w := SlotWaits(dir); len(w) != 0 {
		t.Fatalf("a wait that ended left its marker: %v", w)
	}
}
