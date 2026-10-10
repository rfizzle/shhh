package profile

import (
	"reflect"
	"strings"
	"testing"
)

func TestProfile_AWrittenProfileKeepsEveryDeclaration(t *testing.T) {
	no := false
	p := Profile{Name: "gw", BaseURL: "http://x/v1", Models: []Model{{
		ID: "m", ContextWindow: 10,
		Reasoning:         Reasoning{Kind: "effort", Levels: []string{"xhigh", "max"}, AlwaysOn: true},
		Decisions:         true,
		StructuredOutputs: &no,
		PromptCache:       &no,
		Flows: map[string]map[string]any{
			"classifier": {"structured_outputs": false},
			"backlog":    {"structured_outputs": false},
		},
	}}}
	dir := t.TempDir()
	got, err := LoadFile(writeProfile(t, dir, "providers.toml", Encode([]Profile{p})))
	if err != nil {
		t.Fatal(err)
	}
	want := p.Models[0]
	if len(got) != 1 || len(got[0].Models) != 1 || !reflect.DeepEqual(got[0].Models[0], want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	again, err := LoadFile(writeProfile(t, dir, "again.toml", Encode(got)))
	if err != nil || !reflect.DeepEqual(again[0].Models[0], want) {
		t.Fatalf("second round: %+v, %v", again, err)
	}
}

// prompt_cache is written only as false, model-wide, and no flow may scope
// it: the cache belongs to the endpoint and not to any one call on it.
func TestProfile_PromptCacheRefusesTrue(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ line, want string }{
		{"prompt_cache = true", "prompt_cache can only be false"},
		{`prompt_cache = "false"`, "prompt_cache"},
		{"flows = { title = { prompt_cache = false } }", "caching is a property of the endpoint, not of a call"},
	} {
		_, err := LoadFile(writeProfile(t, dir, "gw.toml", "name = \"gw\"\nbase_url = \"http://x/v1\"\n[[models]]\nid = \"m\"\n"+tc.line+"\n"))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want %q", tc.line, err, tc.want)
		}
	}
	got, err := LoadFile(writeProfile(t, dir, "ok.toml", "name = \"gw\"\nbase_url = \"http://x/v1\"\n[[models]]\nid = \"m\"\nprompt_cache = false\n"))
	if err != nil || got[0].Models[0].PromptCache == nil || *got[0].Models[0].PromptCache {
		t.Fatalf("prompt_cache = false: %+v, %v", got, err)
	}
	if d, ok := got[0].Models[0].declared(); !ok || d.Model["prompt_cache"] != false {
		t.Errorf("the declaration is registered as the registry reads it: %+v", d)
	}
}
