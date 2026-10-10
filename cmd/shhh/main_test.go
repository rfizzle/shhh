package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/sandbox"
)

// runMainEnv makes this test binary the shhh binary: TestMain runs main in
// place of the tests, with the arguments it was given.
const runMainEnv = "SHHH_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The helper argument is answered before the keymap or the command tree is
// read: inside a container neither exists, and a helper that read them would
// say so on stderr — which is the command's stderr — before every command a
// session runs there. A keymap file that does not load is planted where the
// keymap is read, so a helper that got that far would say so.
func TestHelperArgIsAnsweredBeforeTheRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	for _, path := range config.KeymapPaths() {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("this is not toml ="), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(os.Args[0], sandbox.ExecArg, "--version")
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the helper's --version failed: %v\n%s", err, stderr.String())
	}
	if got, want := stdout.String(), "shhh-sandbox-exec "+sandbox.HelperVersion+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("the helper read something before answering:\n%s", stderr.String())
	}

	// The control: the same planted file is read, and refused, by the
	// program the helper is not.
	cmd = exec.Command(os.Args[0], "--version")
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	stderr.Reset()
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if !bytes.Contains(stderr.Bytes(), []byte("keybindings refused")) {
		t.Errorf("the planted keymap was not read by the program itself, so the test proves nothing:\n%s", stderr.String())
	}
}
