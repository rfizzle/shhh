package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
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
	if !strings.HasPrefix(note, "No secrets") || announce != "" {
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
	if note, _ = manage([]string{"forget", "SHHH_TEST_TOKEN"}); !strings.HasPrefix(note, "No secret") {
		t.Fatalf("second forget: %q", note)
	}
}
