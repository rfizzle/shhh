package calltier

import (
	"strings"
	"testing"
)

func TestNew_ATierlessToolFailsToRegister(t *testing.T) {
	_, err := New(map[string]Declaration{"new_tool": {Kind: Run}})
	if err == nil || !strings.Contains(err.Error(), "new_tool declares no tier") {
		t.Fatalf("a tool with no tier registered: %v", err)
	}
}

func TestNew_AKindlessToolFailsToRegister(t *testing.T) {
	if _, err := New(map[string]Declaration{"x": {Tier: Write}}); err == nil {
		t.Fatal("a tool with no kind registered")
	}
}

func TestNew_ADuplicateFailsToRegister(t *testing.T) {
	d := map[string]Declaration{"x": {Tier: Write, Kind: Edit}}
	if _, err := New(d, d); err == nil {
		t.Fatal("a name declared twice registered")
	}
}

func TestNew_MergesEverySource(t *testing.T) {
	r, err := New(
		map[string]Declaration{"a": {Tier: Write, Kind: Edit}},
		map[string]Declaration{"b": {Tier: Command, Kind: Fetch}},
	)
	if err != nil || len(r) != 2 {
		t.Fatalf("got %v, %v", r, err)
	}
}
