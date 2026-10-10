package run

import (
	"path/filepath"
	"testing"
)

// A reader of the live record while a writer saves it sees every record
// whole: the file is renamed into place, never truncated and refilled.
func TestSprintSave_IsAtomic(t *testing.T) {
	root := t.TempDir()
	sp := StartSprint("s", "build", 0, false)
	sp.Attempts = []string{"a-one", "b-two"}
	if err := sp.Save(root); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan int)
	go func() {
		torn := 0
		for {
			select {
			case <-stop:
				done <- torn
				return
			default:
			}
			if got, ok := Live(root); !ok || len(got.Attempts) != 2 {
				torn++
			}
		}
	}()
	for i := 0; i < 1000; i++ {
		if err := sp.Save(root); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if torn := <-done; torn > 0 {
		t.Fatalf("the live record was read half-written %d times", torn)
	}
	if left, _ := filepath.Glob(filepath.Join(Dir(root), "*.tmp")); len(left) > 0 {
		t.Fatalf("temporary files were left behind: %v", left)
	}
}
