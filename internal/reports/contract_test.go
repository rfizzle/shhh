package reports

import (
	"os"
	"testing"
)

func requireLoopbackContract(t *testing.T) {
	t.Helper()
	if os.Getenv("SHHH_TEST_CONTRACT") != "1" {
		t.Skip("report serving contract; run make test-contract on a listener-capable host")
	}
}
