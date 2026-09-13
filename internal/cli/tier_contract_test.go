//go:build contract

package cli

// The contract tier drives the built binary against a loopback provider, so
// the binary is built once, before the home moves (logs_test.go).
func prepareContractTier(dir string) { buildShhhBinary(dir) }
