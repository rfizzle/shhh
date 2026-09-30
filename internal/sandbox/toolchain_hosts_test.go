package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/rfizzle/shhh/internal/project"
)

// A checkout's toolchain declaration reads its hosts with a copy of this
// package's grammar, because this package imports that one. The copy has to
// give the proxy's answers, entry for entry: a registry the declaration
// accepted and the host list then refused would be an install that can
// never reach where it was told to fetch from.
func TestTheToolchainReadsHostsAsTheProxyDoes(t *testing.T) {
	for _, h := range []string{
		"proxy.golang.org", " Registry.NPMjs.org. ", "10.0.0.2", "[::1]", "pypi_mirror.local",
		"https://proxy.golang.org", "proxy.golang.org:443", "proxy.golang.org/path", "*.npmjs.org",
		"-bad.example", "bad-.example", "a..b", "under score.example", "ünicode.example",
	} {
		root := t.TempDir()
		path := filepath.Join(root, filepath.FromSlash(project.ToolchainFile))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("hosts = ["+strconv.Quote(h)+"]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tc, _, declErr := project.LoadToolchain(project.Trust{Root: root, Granted: true})
		proxy, proxyErr := ParseHosts([]string{h})
		if (declErr == nil) != (proxyErr == nil) {
			t.Errorf("%q: declaration says %v, proxy says %v", h, declErr, proxyErr)
			continue
		}
		if declErr == nil && !slices.Equal(tc.Hosts, proxy) {
			t.Errorf("%q: declaration reads %v, proxy reads %v", h, tc.Hosts, proxy)
		}
	}
}
