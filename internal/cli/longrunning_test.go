package cli

import (
	"context"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// A run with nobody in front of it answers a command that cannot finish with
// the redirect, even under --yes, and runs one that can.
func TestHeadless_AServerCommandIsRedirectedNotRun(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), headlessApproval{opts: printOpts{yes: true}, run: fakeRun(&ran)})
	call := func(command string) provider.ToolCall {
		return provider.ToolCall{ID: "c", Name: tools.ExecCommandName, Arguments: `{"command":"` + command + `"}`}
	}
	want := "error: " + tools.LongRunningError{}.Error()
	if got := resolve(call("npm run dev")); got != want {
		t.Errorf("npm run dev = %q, want %q", got, want)
	}
	if len(ran) != 0 {
		t.Fatalf("a redirected command ran: %v", ran)
	}
	resolve(call("go test ./..."))
	if len(ran) != 1 {
		t.Fatalf("an ordinary command did not run: %v", ran)
	}
}
