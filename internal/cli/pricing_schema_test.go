package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/provider"
)

// A profile's structured_outputs = false travels profile model, pricing
// overlay, installCapabilities, and so decides what SchemaFor answers.
func TestSchemaFor_AProfileDeclarationReachesTheAnswer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.toml")
	body := `
name     = "gw"
base_url = "https://gw.example"
api      = "anthropic-messages"

[[models]]
id                 = "claude-sonnet-5-5"
structured_outputs = false

[[models]]
id = "claude-opus-5-5"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	table := pricing.Snapshot()
	table.Overlay(profile.Pricing(loaded))
	installCapabilities(table)
	t.Cleanup(func() { provider.SetCapabilityLookup(nil) })

	opts := provider.CompletionOpts{ResponseSchema: &provider.ResponseSchema{Name: "v", Schema: []byte(`{"type":"object"}`)}}
	if opts.SchemaFor("claude-sonnet-5-5") != nil {
		t.Error("the declared model should be sent no schema")
	}
	if opts.SchemaFor("claude-opus-5-5") == nil {
		t.Error("a model that declared nothing keeps the floor")
	}
}

func TestProfile_StructuredOutputsTrueIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.toml")
	body := "name = \"gw\"\nbase_url = \"https://gw.example\"\n[[models]]\nid = \"m\"\nstructured_outputs = true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.LoadFile(path); err == nil {
		t.Fatal("structured_outputs = true should be refused")
	}
}
