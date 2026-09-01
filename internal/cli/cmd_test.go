package cli

// The root generates nothing: what used to be `shhh <prompt>` is `shhh cmd`,
// and both ways of arriving at the root with a prompt in hand have to say so.
// A bare `shhh` on a terminal still prints the tree, which help_test.go
// covers.

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestBarePromptNamesTheCommandThatGenerates(t *testing.T) {
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"list open ports"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("`shhh \"list open ports\"` should be refused, not generated")
	}
	if !strings.Contains(err.Error(), `shhh cmd "list open ports"`) {
		t.Errorf("the refusal should spell the command that would have run it, got %q", err)
	}
}

// A prompt on stdin used to be the whole no-TTY contract, and a script that
// still pipes into the root gets a failure rather than the help page: help on
// stdout is what `echo … | shhh | sh` would then run.
func TestPipedPromptNamesTheCommandThatReadsIt(t *testing.T) {
	// fang resolves its width once per process, so a render here asks for
	// the same one every other help render in this package does.
	t.Setenv("__FANG_TEST_WIDTH", "80")
	t.Setenv("NO_COLOR", "1")
	// `go test` hands the process a stdin that is not a terminal, which is
	// the condition the root branches on.
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)
	if err := execute(context.Background(), cmd); err == nil {
		t.Fatal("a piped `shhh` should be refused, not answered with the help page")
	}
	if !strings.Contains(stderr.String(), "shhh cmd") {
		t.Errorf("the refusal should name `shhh cmd`, got:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("nothing may reach the pipe on stdout, got:\n%s", stdout.String())
	}
}
