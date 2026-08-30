package storage

import "testing"

func TestMCPTrustIsPerRootAndReplaceable(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, ok := db.MCPTrusted("/a", "gh"); ok {
		t.Fatal("trusted before anything was recorded")
	}
	if err := db.TrustMCP("/a", "gh", "fp1"); err != nil {
		t.Fatal(err)
	}
	if fp, ok := db.MCPTrusted("/a", "gh"); !ok || fp != "fp1" {
		t.Errorf("trusted = %q %v", fp, ok)
	}
	if _, ok := db.MCPTrusted("/b", "gh"); ok {
		t.Error("trust leaked across roots")
	}
	if err := db.TrustMCP("/a", "gh", "fp2"); err != nil {
		t.Fatal(err)
	}
	if fp, _ := db.MCPTrusted("/a", "gh"); fp != "fp2" {
		t.Errorf("re-trust did not replace: %q", fp)
	}
	had, err := db.DistrustMCP("/a", "gh")
	if err != nil || !had {
		t.Errorf("distrust = %v %v", had, err)
	}
	if had, _ := db.DistrustMCP("/a", "gh"); had {
		t.Error("distrust of nothing reported a row")
	}
	if _, ok := db.MCPTrusted("/a", "gh"); ok {
		t.Error("still trusted after distrust")
	}
}
