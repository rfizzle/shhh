package cli

// What a client that is not shhh's own terminal gets when it drives the
// agent, asserted against the built binary for the same reason the unattended
// run's contract is: the protocol is a promise made to another process, and
// one checked a function call away from the command that serves it can be
// broken anywhere along the way with none of these noticing.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/rpc"
)

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// A socket that is already there is refused rather than replaced: it is
// either a server that is still running, whose clients would silently stop
// being served, or the remains of one that died, and unlinking on the
// person's behalf is how the first case becomes the second.
func TestServeOnSocket_RefusesAPathThatIsAlreadyThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taken")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := serveOnSocket(context.Background(), rpc.NewServer(nil), path)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("listening on a path that is taken answered %v", err)
	}
}
