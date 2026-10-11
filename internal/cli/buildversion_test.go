package cli

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
)

func TestVersion_FromBuildInfo(t *testing.T) {
	for _, tc := range []struct{ name, linked, main, want string }{
		{"linked wins", "0.18.0", "v0.19.0", "0.18.0"},
		{"go install tag", "dev", "v0.19.0", "v0.19.0"},
		{"devel stays dev", "dev", "(devel)", "dev"},
		{"pseudo stays dev", "dev", "v0.19.1-0.20261001120000-abcdef123456", "dev"},
		{"empty stays dev", "", "", "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := readBuildInfo
			readBuildInfo = func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: tc.main}}, true
			}
			t.Cleanup(func() { readBuildInfo = old })
			if got := buildVersion(tc.linked); got != tc.want {
				t.Fatalf("buildVersion(%q) = %q, want %q", tc.linked, got, tc.want)
			}
		})
	}
}

func TestSandboxImage_FallsBackToTheTag(t *testing.T) {
	oldImage, oldTag := sandboxImage, moduleTag
	t.Cleanup(func() { sandboxImage, moduleTag = oldImage, oldTag })
	sandboxImage, moduleTag = "", "v0.19.0"
	var cfg config.Config
	if got, want := sandboxImageFor(cfg), "ghcr.io/rfizzle/shhh-sandbox:v0.19.0"; got != want {
		t.Fatalf("sandboxImageFor = %q, want %q", got, want)
	}
	if !imageFromVersionTag("") {
		t.Fatal("the tag fallback is not reported as the version tag's")
	}
	cfg.Sandbox.ContainerImage = "example.com/mine@sha256:" + strings.Repeat("b", 64)
	if got := sandboxImageFor(cfg); got != cfg.Sandbox.ContainerImage {
		t.Fatalf("configured image lost to the tag: %q", got)
	}
	if imageFromVersionTag(cfg.Sandbox.ContainerImage) {
		t.Fatal("a configured image is reported as the tag's")
	}
	moduleTag = ""
	cfg.Sandbox.ContainerImage = ""
	if got := sandboxImageFor(cfg); got != "" {
		t.Fatalf("no tag and no image should give none, got %q", got)
	}
}
