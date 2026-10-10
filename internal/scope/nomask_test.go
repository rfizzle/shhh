package scope

import (
	"testing"

	"github.com/rfizzle/shhh/internal/scope/mask"
)

// With no mask registered nothing says what containment hides, so a
// directory is never cleared as ordinary: it is asked about.
func TestClassify_WithoutAMaskANamedDirectoryIsSensitive(t *testing.T) {
	prev := mask.Current()
	mask.Use(nil)
	defer mask.Use(prev)
	if class, reason := Classify(t.TempDir()); class != Sensitive || reason == "" {
		t.Fatalf("Classify with no mask = %v, %q; want Sensitive with a reason", class, reason)
	}
}
