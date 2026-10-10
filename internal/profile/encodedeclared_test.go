package profile

import (
	"reflect"
	"testing"
)

func TestProfile_AWrittenProfileKeepsEveryDeclaration(t *testing.T) {
	no := false
	p := Profile{Name: "gw", BaseURL: "http://x/v1", Models: []Model{{
		ID: "m", ContextWindow: 10,
		Reasoning:         Reasoning{Kind: "effort", Levels: []string{"xhigh", "max"}, AlwaysOn: true},
		Decisions:         true,
		StructuredOutputs: &no,
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
