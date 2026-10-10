package sandbox

import (
	"os"
	"testing"
)

// TestMain lets this test binary stand in for shhh in the two places a
// contained command runs shhh itself: as the network bridge — a bubblewrap
// wrap with a host list runs the program that built it inside the namespace
// — and as the command helper inside a sandbox container. Under test that
// program is this one.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case BridgeArg:
			os.Exit(RunBridge(os.Args[2:]))
		case ExecArg:
			os.Exit(RunExec(os.Args[2:]))
		}
	}
	os.Exit(m.Run())
}
