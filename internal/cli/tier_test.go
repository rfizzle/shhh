//go:build !contract

package cli

// The hermetic tier drives nothing: the built binary is a loopback contract's
// fixture (print_integration_test.go), and a suite that never starts a
// listener has no use for it. See docs/capabilities/testing.md.
func prepareContractTier(string) {}
