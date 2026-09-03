package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/secret"
)

func TestLoadSecrets_ConfigSkipsUnsetFlagRefuses(t *testing.T) {
	t.Setenv("SHHH_TEST_SET", "abc")
	var warn bytes.Buffer
	cfg := config.Config{Secrets: config.SecretsConfig{Env: []string{"SHHH_TEST_SET", "SHHH_TEST_UNSET"}}}
	v, err := loadSecrets(cfg, []string{"INLINE=xyz"}, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(v.Names(), ","); got != "INLINE,SHHH_TEST_SET" {
		t.Fatalf("Names = %q", got)
	}
	if !strings.Contains(warn.String(), "SHHH_TEST_UNSET") {
		t.Fatalf("expected a warning for the unset name, got %q", warn.String())
	}
	if _, err := loadSecrets(config.Config{}, []string{"SHHH_TEST_UNSET"}, &warn); err == nil {
		t.Fatal("--secret naming an unset variable must fail")
	}
}

func TestSecretsManager(t *testing.T) {
	t.Setenv("SHHH_TEST_TOKEN", "tok-tok-tok")
	v := secret.New()
	manage := secretsManager(v)

	note, announce := manage(nil)
	if !strings.Contains(note, "⊘ no secrets in this session") || announce != "" {
		t.Fatalf("empty listing: %q / %q", note, announce)
	}
	note, announce = manage([]string{"set", "SHHH_TEST_TOKEN"})
	if !strings.Contains(note, "$SHHH_TEST_TOKEN") || !strings.Contains(announce, "[secret:SHHH_TEST_TOKEN]") {
		t.Fatalf("set: %q / %q", note, announce)
	}
	if strings.Contains(note, "tok-tok-tok") || strings.Contains(announce, "tok-tok-tok") {
		t.Fatal("neither text may carry the value")
	}
	note, _ = manage([]string{"list"})
	if !strings.Contains(note, "$SHHH_TEST_TOKEN") || strings.Contains(note, "tok-tok") {
		t.Fatalf("list: %q", note)
	}
	if note, _ = manage([]string{"set", "NOPE_NOT_SET"}); !strings.HasPrefix(note, "Error:") {
		t.Fatalf("unset: %q", note)
	}
	if _, announce = manage([]string{"forget", "SHHH_TEST_TOKEN"}); announce == "" || v.Len() != 0 {
		t.Fatal("forget must remove and announce")
	}
	if note, _ = manage([]string{"forget", "SHHH_TEST_TOKEN"}); !strings.Contains(note, "✗ no secret named SHHH_TEST_TOKEN") {
		t.Fatalf("second forget: %q", note)
	}
}

// The gate is built while the toolset is, which is before the session opens
// its secrets, so it cannot be handed the vault — it takes the scrub the
// reducer is given a moment later and reads it when a check's output is
// kept. Wired without that, a check that prints one of the session's
// variables leaves the value in a file for a week and in the excerpt on
// screen, neither of which the executor chain ever sees.
func TestOpenQualityGate_TakesTheScrubOpenedAfterIt(t *testing.T) {
	const value = "sk-live-0f1e2d3c4b5a6978"
	t.Setenv("GATE_TEST_KEY", value)
	store, err := evidence.Open(filepath.Join(t.TempDir(), "evidence"), evidence.NewSessionID())
	if err != nil {
		t.Fatal(err)
	}
	red := evidence.NewReducer(store)
	gate := openQualityGate(config.Config{}, red, nil)
	if gate == nil {
		t.Fatal("a gate is expected wherever the working directory resolves")
	}

	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".shhh"), 0o700); err != nil {
		t.Fatal(err)
	}
	suite := `{"suites": {"default": {"checks": [{"name": "leaky", "exe": "sh",
		"args": ["-c", "printf 'key=%s\\n' \"$GATE_TEST_KEY\"; exit 1"]}]}}}`
	if err := os.WriteFile(filepath.Join(ws, ".shhh", "quality.json"), []byte(suite), 0o600); err != nil {
		t.Fatal(err)
	}
	// The suite is in a temp tree rather than the one the test process runs
	// in, and containment is the session's business and not this wiring's.
	gate.Workspace, gate.Wrap = ws, nil

	v := secret.New()
	if err := v.Add("GATE_TEST_KEY", value); err != nil {
		t.Fatal(err)
	}
	red.SetScrub(v.Scrub)

	res, err := gate.Run(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	out := res.Format(res.Fingerprint)
	if strings.Contains(out, value) {
		t.Fatalf("the gate result handed the value back:\n%s", out)
	}
	if !strings.Contains(out, secret.Placeholder("GATE_TEST_KEY")) {
		t.Fatalf("the excerpt must name the secret it held:\n%s", out)
	}
}
